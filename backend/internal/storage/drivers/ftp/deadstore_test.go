package ftp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// Issue #73: the FTP driver bounded only the TCP connect (15 s). Once
// connected nothing had a limit: a server that stopped answering left the
// liveness probe (NOOP) in connectLocked waiting forever — while holding the
// driver's lock, so every other operation on that storage waited behind it,
// forever too. A server that accepted the connection and never greeted did
// the same, and so did an upload or a download the server stopped moving.
//
// These tests point a real driver at a small FTP stand-in (fakeFTP) that is
// dead, hung, or slow but moving — the S3 driver's dead-store tests, for FTP.

var deadStoreSettings = map[string]any{
	"max_attempts":      6,
	"attempt_timeout_s": 1,
	"total_timeout_s":   3,
}

const deadStoreCeiling = 3*time.Second + 1500*time.Millisecond

// deadStoreCap: a call that has not returned by then is reported as hanging,
// instead of hanging the package until go test's own timeout.
const deadStoreCap = 20 * time.Second

var errDidNotReturn = errors.New("the call did not return")

func timeCall(ctx context.Context, run func(ctx context.Context) error) (time.Duration, error) {
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	select {
	case err := <-done:
		return time.Since(start), err
	case <-time.After(deadStoreCap):
		return time.Since(start), errDidNotReturn
	}
}

func ftpDriver(t *testing.T, addr string, overrides map[string]any) *Driver {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"host": host, "port": port, "user": "u", "password": "p", "root": "/fx"}
	for k, v := range deadStoreSettings {
		cfg[k] = v
	}
	for k, v := range overrides {
		cfg[k] = v
	}
	d := &Driver{}
	if err := d.Init(context.Background(), cfg); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Close takes the driver's lock; before #73 a hung server held it for
	// good, and an unbounded Close here hung the whole package.
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { _ = d.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Close waited 5 s for the driver's lock: something still holds it")
		}
	})
	return d
}

func assertUnavailable(t *testing.T, err error, took, ceiling time.Duration, words ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("a dead server answered success")
	}
	if took > ceiling {
		t.Errorf("took %.1fs, ceiling %.1fs", took.Seconds(), ceiling.Seconds())
	}
	if !errors.Is(err, storage.ErrUnavailable) {
		t.Errorf("not storage.ErrUnavailable: %v", err)
	}
	for _, w := range append([]string{"is unavailable"}, words...) {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not say %q: %v", w, err)
		}
	}
}

// ── the stand-in ──────────────────────────────────────────────────────

// fakeFTP speaks just enough FTP for the driver: login, EPSV, LIST, RETR,
// STOR, NOOP, MKD, DELE, RNFR/RNTO, QUIT. Its knobs make it hang or crawl.
type fakeFTP struct {
	l       net.Listener
	release chan struct{} // closed at cleanup: frees every hung handler

	silent atomic.Bool  // accept, never greet: a hung server process
	hung   atomic.Bool  // stop answering commands, on every session
	busy   atomic.Int32 // turn away this many sessions with 421
	login  string       // the answer to PASS

	// tlsConf, when set, makes it an explicit-FTPS server (AUTH TLS, PROT P);
	// plainData counts data connections that did not speak TLS under PROT P.
	tlsConf   *tls.Config
	plainData atomic.Int32

	storRate, retrRate int          // bytes per second; 0 = at once
	storStall          atomic.Int64 // stop reading an upload after this many bytes
	retrStall          atomic.Int64 // stop sending a download after this many bytes

	mu    sync.Mutex
	files map[string][]byte
	conns []net.Conn

	sessions atomic.Int32
}

func newFakeFTP(t *testing.T) *fakeFTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeFTP{l: l, release: make(chan struct{}), login: "230 logged in", files: map[string][]byte{}}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		close(s.release)
		_ = l.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	return s
}

func (s *fakeFTP) addr() string { return s.l.Addr().String() }

func (s *fakeFTP) track(c net.Conn) {
	s.mu.Lock()
	s.conns = append(s.conns, c)
	s.mu.Unlock()
}

func (s *fakeFTP) serve(c net.Conn) {
	defer c.Close()
	if s.silent.Load() {
		<-s.release
		return
	}
	s.sessions.Add(1)
	r := bufio.NewReader(c)
	reply := func(format string, a ...any) { _, _ = fmt.Fprintf(c, format+"\r\n", a...) }
	if s.busy.Add(-1) >= 0 {
		reply("421 Too many users, try again later")
		return
	}
	reply("220 fake ftp ready")
	var data net.Listener
	protP := false
	accept := func() net.Conn {
		if data == nil {
			return nil
		}
		dc, err := data.Accept()
		_ = data.Close()
		data = nil
		if err != nil {
			return nil
		}
		s.track(dc)
		return dc
	}
	// secure speaks TLS on a data connection under PROT P — after the 150
	// reply, because the client's handshake starts at its first read or write.
	secure := func(dc net.Conn) net.Conn {
		if dc == nil || !protP {
			return dc
		}
		tc := tls.Server(dc, s.tlsConf)
		_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tc.Handshake(); err != nil {
			s.plainData.Add(1)
			_ = dc.Close()
			return nil
		}
		_ = tc.SetDeadline(time.Time{})
		return tc
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if s.hung.Load() {
			<-s.release
			return
		}
		cmd, arg, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		switch strings.ToUpper(cmd) {
		case "USER":
			reply("331 password please")
		case "PASS":
			reply("%s", s.login)
		case "FEAT":
			reply("211-Features:\r\n UTF8\r\n211 End")
		case "TYPE", "OPTS", "PBSZ", "NOOP":
			reply("200 ok")
		case "AUTH":
			if s.tlsConf == nil {
				reply("502 no TLS here")
				continue
			}
			reply("234 go ahead")
			tc := tls.Server(c, s.tlsConf)
			c, r = tc, bufio.NewReader(tc)
		case "PROT":
			protP = s.tlsConf != nil && strings.EqualFold(arg, "P")
			reply("200 ok")
		case "EPSV":
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				reply("425 no data port")
				continue
			}
			data = l
			reply("229 Entering Extended Passive Mode (|||%d|)", l.Addr().(*net.TCPAddr).Port)
		case "LIST":
			dc := accept()
			reply("150 here it comes")
			if dc = secure(dc); dc != nil {
				_, _ = io.WriteString(dc, "-rw-r--r-- 1 u g 3 Jan 01 00:00 a.txt\r\ndrwxr-xr-x 2 u g 4096 Jan 01 00:00 sub\r\n")
				_ = dc.Close()
			}
			reply("226 done")
		case "STOR":
			dc := accept()
			reply("150 go ahead")
			if dc = secure(dc); dc != nil {
				got := s.readUpload(dc)
				s.mu.Lock()
				s.files[arg] = got
				s.mu.Unlock()
				_ = dc.Close()
			}
			reply("226 stored")
		case "RETR":
			s.mu.Lock()
			body, ok := s.files[arg]
			s.mu.Unlock()
			if !ok {
				_ = accept()
				reply("550 no such file")
				continue
			}
			dc := accept()
			reply("150 sending")
			if dc = secure(dc); dc != nil {
				s.sendDownload(dc, body)
				_ = dc.Close()
			}
			reply("226 sent")
		case "MKD":
			reply("257 created")
		case "DELE", "RMD", "RNTO", "CWD":
			reply("250 ok")
		case "RNFR", "REST":
			reply("350 go on")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func (s *fakeFTP) readUpload(dc net.Conn) []byte {
	var buf bytes.Buffer
	chunk := make([]byte, 64<<10)
	for {
		if n := s.storStall.Load(); n > 0 && int64(buf.Len()) >= n {
			<-s.release
			return buf.Bytes()
		}
		n, err := dc.Read(chunk)
		buf.Write(chunk[:n])
		if err != nil {
			return buf.Bytes()
		}
		if s.storRate > 0 {
			time.Sleep(time.Duration(n) * time.Second / time.Duration(s.storRate))
		}
	}
}

func (s *fakeFTP) sendDownload(dc net.Conn, body []byte) {
	const chunk = 64 << 10
	for off := 0; off < len(body); off += chunk {
		if n := s.retrStall.Load(); n > 0 && int64(off) >= n {
			<-s.release
			return
		}
		if _, err := dc.Write(body[off:min(off+chunk, len(body))]); err != nil {
			return
		}
		if s.retrRate > 0 {
			time.Sleep(time.Duration(chunk) * time.Second / time.Duration(s.retrRate))
		}
	}
}

// ── dead servers ──────────────────────────────────────────────────────

func refusedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

type ftpCall struct {
	name string
	run  func(ctx context.Context, d *Driver) error
}

var ftpCalls = []ftpCall{
	{"list", func(ctx context.Context, d *Driver) error {
		_, err := d.List(ctx, "/")
		return err
	}},
	{"upload", func(ctx context.Context, d *Driver) error {
		body := bytes.Repeat([]byte("x"), 64<<10)
		return d.Write(ctx, "/drop/report.pdf", bytes.NewReader(body), int64(len(body)))
	}},
}

func TestDeadStore_AnswersWithinTheCeiling(t *testing.T) {
	endpoints := []struct {
		name  string
		addr  func(t *testing.T) string
		words []string
	}{
		{"connection refused", refusedAddr, []string{"the connection was refused"}},
		{"never greets", func(t *testing.T) string {
			s := newFakeFTP(t)
			s.silent.Store(true)
			return s.addr()
		}, []string{"the server sent nothing for 1s"}},
		{"black-hole ip", func(*testing.T) string { return "192.0.2.1:21" }, nil},
		{"dns nxdomain", func(*testing.T) string { return "filex-dead-ftp.invalid:21" }, []string{"does not resolve", "(1 attempt in"}},
	}
	for _, ep := range endpoints {
		for _, c := range ftpCalls {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := ftpDriver(t, ep.addr(t), nil)
				took, err := timeCall(context.Background(), func(ctx context.Context) error { return c.run(ctx, d) })
				t.Logf("%s %s: %.1fs err=%v", ep.name, c.name, took.Seconds(), err)
				assertUnavailable(t, err, took, deadStoreCeiling, ep.words...)
			})
		}
	}
}

// ⚠ The bug itself: a server that worked, then stopped answering. The next
// operation's liveness probe (NOOP) used to wait forever holding the driver's
// lock, and every operation behind it waited too. Now the probe gives up
// after the attempt timeout, a fresh connection is tried within the budget,
// and each caller hears "unavailable"; a caller that gives up while waiting
// for the lock leaves at once.
func TestDeadStore_HungServerDoesNotHoldTheStorage(t *testing.T) {
	s := newFakeFTP(t)
	d := ftpDriver(t, s.addr(), nil)
	if _, err := d.List(context.Background(), "/"); err != nil {
		t.Fatalf("the server was fine at first: %v", err)
	}
	s.hung.Store(true)
	s.silent.Store(true) // a hung process does not greet new connections either

	var wg sync.WaitGroup
	results := make([]struct {
		took time.Duration
		err  error
	}, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].took, results[i].err = timeCall(context.Background(), func(ctx context.Context) error {
				_, err := d.List(ctx, "/")
				return err
			})
		}()
		time.Sleep(50 * time.Millisecond)
	}

	// A third caller that gives up after 300 ms while the others hold the lock.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	took, err := timeCall(ctx, func(ctx context.Context) error { _, err := d.Stat(ctx, "/a.txt"); return err })
	t.Logf("a caller that gave up: %.1fs err=%v", took.Seconds(), err)
	if err == nil || took > 1500*time.Millisecond {
		t.Errorf("a caller whose context ended waited %.1fs for the lock (err=%v)", took.Seconds(), err)
	}

	wg.Wait()
	for i, r := range results {
		t.Logf("caller %d: %.1fs err=%v", i, r.took.Seconds(), r.err)
		// The first waits the probe and the reconnects; the second waits for
		// the first, then does the same.
		assertUnavailable(t, r.err, r.took, time.Duration(i+1)*deadStoreCeiling+time.Second, "the server sent nothing for 1s")
	}
}

// A refusal is the server ANSWERING: a wrong password is not retried and is
// not "unavailable".
func TestDeadStore_RefusedLoginIsNotRetried(t *testing.T) {
	s := newFakeFTP(t)
	s.login = "530 Login incorrect."
	d := ftpDriver(t, s.addr(), nil)
	took, err := timeCall(context.Background(), func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	if err == nil || !strings.Contains(err.Error(), "530") {
		t.Fatalf("want the server's 530, got %v", err)
	}
	if errors.Is(err, storage.ErrUnavailable) {
		t.Errorf("a wrong password reported as the server being down: %v", err)
	}
	if n := s.sessions.Load(); n != 1 {
		t.Errorf("a refused login was tried %d times", n)
	}
	if took > time.Second {
		t.Errorf("a refusal took %.1fs", took.Seconds())
	}
}

// A connection that could not be made is tried again even for an upload —
// nothing of it had been sent. Here the server turns away two sessions ("421
// too many users") and takes the third.
func TestDeadStore_BusyServerIsRiddenOut(t *testing.T) {
	s := newFakeFTP(t)
	s.busy.Store(2)
	d := ftpDriver(t, s.addr(), nil)
	body := []byte("hello")
	if err := d.Write(context.Background(), "/b.txt", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("an upload to a server busy for two connections was not retried: %v", err)
	}
	if n := s.sessions.Load(); n != 3 {
		t.Errorf("%d sessions, want 3", n)
	}
	s.mu.Lock()
	got := string(s.files["/fx/b.txt"])
	s.mu.Unlock()
	if got != "hello" {
		t.Errorf("stored %q", got)
	}
}

// ── moving and stalled transfers ─────────────────────────────────────

// ⚠ The limits are on silence, not length: 4 MB each way at 512 KB/s is
// eight attempt timeouts, and the download's reader also walks away for 2.5 of
// them in the middle.
func TestDeadStore_MovingTransfersAreNeverCut(t *testing.T) {
	const size = 4 << 20
	payload := bytes.Repeat([]byte("m"), size)
	s := newFakeFTP(t)
	s.storRate, s.retrRate = 512<<10, 512<<10
	d := ftpDriver(t, s.addr(), map[string]any{"max_attempts": 1})

	took, err := timeCall(context.Background(), func(ctx context.Context) error {
		return d.Write(ctx, "/big.bin", bytes.NewReader(payload), size)
	})
	if err != nil {
		t.Fatalf("a moving upload was cut after %.1fs: %v", took.Seconds(), err)
	}
	if took < 5*time.Second {
		t.Fatalf("the upload took only %.1fs — it proves nothing", took.Seconds())
	}
	s.mu.Lock()
	stored := len(s.files["/fx/big.bin"])
	s.mu.Unlock()
	if stored != size {
		t.Fatalf("the server stored %d bytes", stored)
	}

	start := time.Now()
	rc, err := d.Read(context.Background(), "/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	half := make([]byte, size/2)
	if _, err := io.ReadFull(rc, half); err != nil {
		t.Fatalf("first half: %v", err)
	}
	time.Sleep(2500 * time.Millisecond)
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("the download was cut after %.1fs: %v", time.Since(start).Seconds(), err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sha256.Sum256(append(half, rest...)) != sha256.Sum256(payload) {
		t.Fatal("the download arrived altered")
	}
}

// A server that took the start of an upload and then stopped reading. (Not
// parallel: it lowers stall.SendStallFloor from a minute to the attempt
// timeout.)
func TestDeadStore_StalledUploadIsCut(t *testing.T) {
	stall.SendStallFloor = 0
	t.Cleanup(func() { stall.SendStallFloor = time.Minute })
	s := newFakeFTP(t)
	s.storStall.Store(1 << 20)
	d := ftpDriver(t, s.addr(), nil)
	body := bytes.Repeat([]byte("s"), 96<<20)
	took, err := timeCall(context.Background(), func(ctx context.Context) error {
		return d.Write(ctx, "/stuck.bin", bytes.NewReader(body), int64(len(body)))
	})
	t.Logf("stalled upload: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, deadStoreCeiling, "the store stopped taking the upload for 1s")
}

// A server that sent the start of a download and then went quiet: the reader
// hears it within the attempt timeout, and the storage is usable again.
func TestDeadStore_StalledDownloadIsCut(t *testing.T) {
	s := newFakeFTP(t)
	s.files["/fx/half.bin"] = bytes.Repeat([]byte("h"), 4<<20)
	s.retrStall.Store(1 << 20)
	d := ftpDriver(t, s.addr(), nil)
	rc, err := d.Read(context.Background(), "/half.bin")
	if err != nil {
		t.Fatal(err)
	}
	took, err := timeCall(context.Background(), func(context.Context) error {
		_, err := io.ReadAll(rc)
		return err
	})
	t.Logf("stalled download: %.1fs err=%v", took.Seconds(), err)
	var se *stall.Error
	if err == nil || !errors.As(err, &se) || took > 2*time.Second {
		t.Fatalf("a stalled download: %.1fs err=%v, want a stall within the attempt timeout", took.Seconds(), err)
	}
	took, _ = timeCall(context.Background(), func(context.Context) error { return rc.Close() })
	if took > 2*time.Second {
		t.Errorf("closing the stalled download took %.1fs", took.Seconds())
	}
	s.retrStall.Store(0)
	if _, err := d.List(context.Background(), "/"); err != nil {
		t.Errorf("the storage did not recover after the stalled download: %v", err)
	}
}

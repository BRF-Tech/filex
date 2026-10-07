package sftp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// Issue #75 (the rest of #73): the SFTP driver bounded only the TCP connect
// and the SSH handshake (10 s). Once connected nothing had a limit: a server
// that stopped answering left every operation waiting until the operating
// system gave up on the connection, and the dead session was handed out
// again for good, because nothing ever dropped it. An upload or a download the
// server stopped moving waited forever.
//
// These tests point a real driver at a real SSH server with the SFTP subsystem
// (golang.org/x/crypto/ssh + pkg/sftp's in-memory request server) that is
// dead, hung, or slow but moving - the FTP driver's dead-store tests, for SFTP.

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

func sftpDriver(t *testing.T, addr string, overrides map[string]any) *Driver {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	// root "/": the fake server's in-memory filesystem starts empty, and a
	// storage whose root folder does not exist answers List with "not found"
	// (the driver does not create its root), which is not the stall under test.
	cfg := map[string]any{
		"host": host, "port": port, "user": "u", "password": "p", "root": "/",
		"insecure_skip_host_key": true,
	}
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
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { _ = d.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Close waited 5 s: a silent server holds the shutdown")
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

// fakeSFTP is an SSH server with the SFTP subsystem over an in-memory file
// system. Its knobs make it hang or crawl.
type fakeSFTP struct {
	l        net.Listener
	release  chan struct{} // closed at cleanup: frees every hung handler
	handlers sftp.Handlers // one file system for every session
	password string

	silent atomic.Bool // accept, never send the SSH banner: a hung process
	hung   atomic.Bool // the SFTP subsystem stops answering, on every session

	// storRate: bytes per second an upload is read at; 0 = at once. A download
	// has no rate: SFTP sends what the reader asks for, at its pace (#159).
	storRate  int
	storStall atomic.Int64 // stop reading after this many bytes on a session
	retrStall atomic.Int64 // stop sending after this many bytes on a session

	mu    sync.Mutex
	conns []net.Conn

	sessions atomic.Int32 // SSH sessions that completed the handshake
	logins   atomic.Int32 // password attempts
}

func newFakeSFTP(t *testing.T) *fakeSFTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSFTP{l: l, release: make(chan struct{}), handlers: sftp.InMemHandler(), password: "p"}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.logins.Add(1)
			if string(pw) == s.password {
				return nil, nil
			}
			return nil, errors.New("password rejected")
		},
	}
	cfg.AddHostKey(signer)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.track(c)
			go s.serve(c, cfg)
		}
	}()
	t.Cleanup(func() {
		close(s.release)
		_ = l.Close()
		s.kill()
	})
	return s
}

func (s *fakeSFTP) addr() string { return s.l.Addr().String() }

func (s *fakeSFTP) track(c net.Conn) {
	s.mu.Lock()
	s.conns = append(s.conns, c)
	s.mu.Unlock()
}

// kill drops every connection, as a server restart or a NAT timeout does.
func (s *fakeSFTP) kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.conns = nil
}

func (s *fakeSFTP) serve(c net.Conn, cfg *ssh.ServerConfig) {
	if s.silent.Load() {
		<-s.release
		_ = c.Close()
		return
	}
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	s.sessions.Add(1)
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range creqs {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
				if ok {
					go func() {
						rs := sftp.NewRequestServer(&knob{Channel: ch, s: s}, s.handlers)
						_ = rs.Serve()
						_ = rs.Close()
					}()
				}
			}
		}()
	}
	_ = sc.Close()
}

// knob is the SFTP channel as the server sees it, with the stand-in's knobs.
type knob struct {
	ssh.Channel
	s       *fakeSFTP
	read    int64 // only the request server's reader touches it
	written int64 // writes are serialised by the request server
}

func (k *knob) Read(p []byte) (int, error) {
	s := k.s
	if s.hung.Load() {
		<-s.release
		return 0, io.EOF
	}
	if n := s.storStall.Load(); n > 0 && k.read >= n {
		<-s.release
		return 0, io.EOF
	}
	if s.storRate > 0 && len(p) > 16<<10 {
		p = p[:16<<10]
	}
	n, err := k.Channel.Read(p)
	k.read += int64(n)
	if s.storRate > 0 && n > 0 {
		time.Sleep(time.Duration(n) * time.Second / time.Duration(s.storRate))
	}
	return n, err
}

func (k *knob) Write(p []byte) (int, error) {
	s := k.s
	if s.hung.Load() {
		<-s.release
		return 0, io.EOF
	}
	written := 0
	for len(p) > 0 {
		if n := s.retrStall.Load(); n > 0 && k.written >= n {
			<-s.release
			return written, io.EOF
		}
		n, err := k.Channel.Write(p)
		written += n
		k.written += int64(n)
		p = p[n:]
		if err != nil {
			return written, err
		}
	}
	return written, nil
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

type sftpCall struct {
	name string
	run  func(ctx context.Context, d *Driver) error
}

var sftpCalls = []sftpCall{
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
			s := newFakeSFTP(t)
			s.silent.Store(true)
			return s.addr()
		}, []string{"the server sent nothing for 1s"}},
		{"black-hole ip", func(*testing.T) string { return "192.0.2.1:22" }, nil},
		{"dns nxdomain", func(*testing.T) string { return "filex-dead-sftp.invalid:22" }, []string{"does not resolve", "(1 attempt in"}},
	}
	for _, ep := range endpoints {
		for _, c := range sftpCalls {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := sftpDriver(t, ep.addr(t), nil)
				took, err := timeCall(context.Background(), func(ctx context.Context) error { return c.run(ctx, d) })
				t.Logf("%s %s: %.1fs err=%v", ep.name, c.name, took.Seconds(), err)
				assertUnavailable(t, err, took, deadStoreCeiling, ep.words...)
			})
		}
	}
}

// ⚠ The bug itself: a server that worked, then stopped answering. Every
// operation on the session waited until the operating system gave up on the
// connection. Now each call's watch cuts it after the attempt timeout, a fresh
// connection is tried within the budget, and each caller hears "unavailable";
// a caller that gives up leaves at once.
func TestDeadStore_HungServerDoesNotHoldTheStorage(t *testing.T) {
	s := newFakeSFTP(t)
	d := sftpDriver(t, s.addr(), nil)
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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	took, err := timeCall(ctx, func(ctx context.Context) error { _, err := d.Stat(ctx, "/a.txt"); return err })
	t.Logf("a caller that gave up: %.1fs err=%v", took.Seconds(), err)
	if err == nil || took > 1500*time.Millisecond {
		t.Errorf("a caller whose context ended waited %.1fs (err=%v)", took.Seconds(), err)
	}

	wg.Wait()
	for i, r := range results {
		t.Logf("caller %d: %.1fs err=%v", i, r.took.Seconds(), r.err)
		assertUnavailable(t, r.err, r.took, time.Duration(i+1)*deadStoreCeiling+time.Second)
	}
}

// A session whose connection died is not handed out again: before, the
// driver kept the first client it made, and after a server restart (or a NAT
// dropping an idle connection) every operation failed until filex restarted.
func TestDeadStore_ADeadSessionIsNotHandedOutAgain(t *testing.T) {
	s := newFakeSFTP(t)
	d := sftpDriver(t, s.addr(), nil)
	if _, err := d.List(context.Background(), "/"); err != nil {
		t.Fatalf("the server was fine at first: %v", err)
	}
	s.kill()
	time.Sleep(100 * time.Millisecond)
	took, err := timeCall(context.Background(), func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	if err != nil {
		t.Fatalf("after the connection dropped the storage did not reconnect (%.1fs): %v", took.Seconds(), err)
	}
	if n := s.sessions.Load(); n != 2 {
		t.Errorf("%d SSH sessions, want 2 (the first, and one after it dropped)", n)
	}
}

// An idle session is not a silent server: the watch only runs while a call
// waits, so a session left alone for longer than the attempt timeout is used
// again, not cut.
func TestDeadStore_AnIdleSessionIsKept(t *testing.T) {
	s := newFakeSFTP(t)
	d := sftpDriver(t, s.addr(), nil)
	ctx := context.Background()
	if _, err := d.List(ctx, "/"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := d.List(ctx, "/"); err != nil {
		t.Fatalf("after an idle pause: %v", err)
	}
	if n := s.sessions.Load(); n != 1 {
		t.Errorf("an idle session was cut: %d sessions, want 1", n)
	}
}

// A refusal is the server ANSWERING: a wrong password is not retried and is
// not "unavailable".
func TestDeadStore_RefusedLoginIsNotRetried(t *testing.T) {
	s := newFakeSFTP(t)
	s.password = "something else"
	d := sftpDriver(t, s.addr(), nil)
	took, err := timeCall(context.Background(), func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("want the server's refusal, got %v", err)
	}
	if errors.Is(err, storage.ErrUnavailable) {
		t.Errorf("a wrong password reported as the server being down: %v", err)
	}
	if n := s.logins.Load(); n != 1 {
		t.Errorf("a refused login was tried %d times", n)
	}
	if took > time.Second {
		t.Errorf("a refusal took %.1fs", took.Seconds())
	}
}

// ── moving and stalled transfers ─────────────────────────────────────

// ⚠ The limits are on silence, not length: 4 MB up at 512 KB/s is eight
// attempt timeouts, and the download, read a piece at a time by a reader who
// dawdles between pieces and once walks away for 2.5 attempt timeouts, keeps
// moving for longer than the whole budget.
//
// ⚠ The download waits on nothing but its reader (#159, the S3 driver's
// SlowDownloadAndSlowReaderAreNeverCut). SFTP sends only what is asked for:
// the next megabyte goes out when the driver's read-ahead asks for it, inside
// the reader's Read, and the server answers at once. So a Read waits only for
// a server that is already sending, nothing is in flight while the reader is
// away, and all the time the download takes is spent by the reader BETWEEN
// Reads, where a slower machine only makes the claim stronger. It used to be
// sent by the clock (512 KB/s): the shape that cut the S3 copy of this test on
// a loaded GitHub runner.
func TestDeadStore_MovingTransfersAreNeverCut(t *testing.T) {
	const (
		piece  = 128 << 10
		pieces = 32
		size   = piece * pieces
		// After each piece the reader dawdles: 31 x 125 ms, about 4 s, so the
		// download keeps moving for longer than the whole 3 s budget.
		dawdle = 125 * time.Millisecond
		// Once, halfway, it walks away for 2.5 attempt timeouts. Halfway is a
		// read-ahead boundary (1 MB), so the next piece is a Read on the network.
		away = 2500 * time.Millisecond
	)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i / piece) // a piece lost, repeated or swapped shows
	}
	s := newFakeSFTP(t)
	s.storRate = 512 << 10
	d := sftpDriver(t, s.addr(), map[string]any{"max_attempts": 1})

	took, err := timeCall(context.Background(), func(ctx context.Context) error {
		return d.Write(ctx, "/big.bin", bytes.NewReader(payload), size)
	})
	if err != nil {
		t.Fatalf("a moving upload was cut after %.1fs: %v", took.Seconds(), err)
	}
	if took < 5*time.Second {
		t.Fatalf("the upload took only %.1fs - it proves nothing", took.Seconds())
	}

	start := time.Now()
	rc, err := d.Read(context.Background(), "/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 0, size)
	buf := make([]byte, piece)
	for i := range pieces {
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatalf("piece %d of %d: the download was cut after %.1fs: %v", i+1, pieces, time.Since(start).Seconds(), err)
		}
		got = append(got, buf...)
		if i == pieces/2-1 {
			time.Sleep(away) // no Read waits: the stall limit must not run
		} else {
			time.Sleep(dawdle)
		}
	}
	if rest, err := io.ReadAll(rc); err != nil || len(rest) != 0 {
		t.Fatalf("after the last piece: %d more bytes, err=%v", len(rest), err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if took := time.Since(start); took < 5*time.Second {
		t.Fatalf("the download took only %.1fs - it did not outlast the budget", took.Seconds())
	}
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatal("the download arrived altered")
	}
}

// A server that took the start of an upload and then stopped reading. (Not
// parallel: it lowers stall.SendStallFloor from a minute to the attempt
// timeout.)
func TestDeadStore_StalledUploadIsCut(t *testing.T) {
	stall.SendStallFloor = 0
	t.Cleanup(func() { stall.SendStallFloor = time.Minute })
	s := newFakeSFTP(t)
	d := sftpDriver(t, s.addr(), nil)
	if _, err := d.List(context.Background(), "/"); err != nil {
		t.Fatal(err)
	}
	s.storStall.Store(1 << 20)
	body := bytes.Repeat([]byte("s"), 32<<20)
	took, err := timeCall(context.Background(), func(ctx context.Context) error {
		return d.Write(ctx, "/stuck.bin", bytes.NewReader(body), int64(len(body)))
	})
	t.Logf("stalled upload: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, deadStoreCeiling, "the store stopped taking the upload for 1s")
}

// A server that sent the start of a download and then went quiet: the reader
// hears it within the attempt timeout, and the storage is usable again.
func TestDeadStore_StalledDownloadIsCut(t *testing.T) {
	s := newFakeSFTP(t)
	d := sftpDriver(t, s.addr(), nil)
	body := bytes.Repeat([]byte("h"), 4<<20)
	if err := d.Write(context.Background(), "/half.bin", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	s.retrStall.Store(1 << 20)
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

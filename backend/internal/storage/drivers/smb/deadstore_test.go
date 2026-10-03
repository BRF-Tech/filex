package smb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// Issue #75 (the rest of #73): the SMB driver bounded only the TCP connect
// (dial_timeout_s). After it every request ran on the caller's context, which
// for a sync pass or a queued job never ends: a server that stopped answering
// - or accepted the connection and never answered the negotiation - held the
// operation for good, and a dead session was handed out again forever.
//
// The first half needs no SMB server: a refused port, a black hole, a name that
// does not resolve, a server that never answers the negotiation. The second
// half (a server that worked and then went quiet, transfers that stall or
// crawl) needs a real one: the live tests below run through a TCP proxy in
// front of Samba that can go silent, stop moving or crawl
// (FILEX_SMB_TEST, see smb_live_test.go).

var deadStoreSettings = map[string]any{
	"max_attempts":      6,
	"attempt_timeout_s": 1,
	"total_timeout_s":   3,
}

const deadStoreCeiling = 3*time.Second + 1500*time.Millisecond

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

func smbDriver(t *testing.T, addr string, overrides map[string]any) *Driver {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"host": host, "port": port, "share": "data", "user": "u", "password": "p"}
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

// silentAddr accepts connections and never answers a byte: a hung server
// process, or a firewall that lets the SYN through and drops the rest.
func silentAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				_, _ = io.Copy(io.Discard, c)
				<-release
			}()
		}
	}()
	t.Cleanup(func() {
		close(release)
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return l.Addr().String()
}

type smbCall struct {
	name string
	run  func(ctx context.Context, d *Driver) error
}

var smbCalls = []smbCall{
	{"list", func(ctx context.Context, d *Driver) error {
		_, err := d.List(ctx, "")
		return err
	}},
	{"upload", func(ctx context.Context, d *Driver) error {
		body := bytes.Repeat([]byte("x"), 64<<10)
		return d.Write(ctx, "drop/report.pdf", bytes.NewReader(body), int64(len(body)))
	}},
}

func TestDeadStore_AnswersWithinTheCeiling(t *testing.T) {
	endpoints := []struct {
		name  string
		addr  func(t *testing.T) string
		words []string
	}{
		{"connection refused", refusedAddr, []string{"the connection was refused"}},
		{"never answers the negotiation", silentAddr, []string{"the server sent nothing for 1s"}},
		{"black-hole ip", func(*testing.T) string { return "192.0.2.1:445" }, nil},
		{"dns nxdomain", func(*testing.T) string { return "filex-dead-smb.invalid:445" }, []string{"does not resolve", "(1 attempt in"}},
	}
	for _, ep := range endpoints {
		for _, c := range smbCalls {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := smbDriver(t, ep.addr(t), nil)
				took, err := timeCall(context.Background(), func(ctx context.Context) error { return c.run(ctx, d) })
				t.Logf("%s %s: %.1fs err=%v", ep.name, c.name, took.Seconds(), err)
				assertUnavailable(t, err, took, deadStoreCeiling, ep.words...)
			})
		}
	}
}

// A caller whose context ends while the server says nothing leaves at once.
func TestDeadStore_ACallerWhoGivesUpLeaves(t *testing.T) {
	d := smbDriver(t, silentAddr(t), map[string]any{"attempt_timeout_s": 30, "total_timeout_s": 60})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	took, err := timeCall(ctx, func(ctx context.Context) error { _, err := d.List(ctx, ""); return err })
	if err == nil || took > 1500*time.Millisecond {
		t.Errorf("a caller whose context ended waited %.1fs (err=%v)", took.Seconds(), err)
	}
}

// `dial_timeout_s`, the connect timeout this driver had before, is the attempt
// timeout's old name: a storage saved with it keeps its value - and it now
// bounds the negotiation too, which before it never did.
func TestDeadStore_DialTimeoutIsTheAttemptTimeoutsOldName(t *testing.T) {
	host, port, _ := net.SplitHostPort(silentAddr(t))
	d := mustInit(t, map[string]any{
		"host": host, "port": port, "share": "data", "user": "u",
		"dial_timeout_s": 1, "max_attempts": 1, "total_timeout_s": 1,
	})
	t.Cleanup(func() { _ = d.Close() })
	took, err := timeCall(context.Background(), func(ctx context.Context) error { _, err := d.List(ctx, ""); return err })
	t.Logf("dial_timeout_s 1, silent server: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, 2500*time.Millisecond, "the server sent nothing for 1s")
}

// ── live: a real server behind a proxy that can go quiet ─────────────

// stallProxy forwards one TCP port to the live server, and can be told to
// stop answering, to stop moving one direction after so many bytes, or to
// crawl.
type stallProxy struct {
	l        net.Listener
	upstream string
	release  chan struct{}

	silent    atomic.Bool  // new and open connections: nothing more either way
	upStall   atomic.Int64 // stop forwarding to the server after this many bytes
	downStall atomic.Int64 // stop forwarding to the client after this many bytes
	rate      int          // bytes per second, both ways; 0 = at once
	dials     atomic.Int32

	mu    sync.Mutex
	conns []net.Conn
}

func newStallProxy(t *testing.T, upstream string) *stallProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{l: l, upstream: upstream, release: make(chan struct{})}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			p.dials.Add(1)
			go p.serve(c)
		}
	}()
	t.Cleanup(func() {
		close(p.release)
		_ = l.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range p.conns {
			_ = c.Close()
		}
	})
	return p
}

func (p *stallProxy) addr() string { return p.l.Addr().String() }

func (p *stallProxy) serve(c net.Conn) {
	if p.silent.Load() {
		<-p.release
		_ = c.Close()
		return
	}
	up, err := net.Dial("tcp", p.upstream)
	if err != nil {
		_ = c.Close()
		return
	}
	p.mu.Lock()
	p.conns = append(p.conns, c, up)
	p.mu.Unlock()
	go p.pipe(up, c, &p.upStall)
	p.pipe(c, up, &p.downStall)
}

// pipe copies from src to dst until the proxy is told to stop.
func (p *stallProxy) pipe(dst, src net.Conn, stallAfter *atomic.Int64) {
	buf := make([]byte, 16<<10)
	var moved int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if p.silent.Load() {
				<-p.release
				return
			}
			if limit := stallAfter.Load(); limit > 0 && moved+int64(n) > limit {
				<-p.release
				return
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
			moved += int64(n)
			if p.rate > 0 {
				time.Sleep(time.Duration(n) * time.Second / time.Duration(p.rate))
			}
		}
		if err != nil {
			_ = dst.Close()
			return
		}
	}
}

// proxiedDriver is liveDriver through a stallProxy, with the dead-store limits.
func proxiedDriver(t *testing.T) (*Driver, *stallProxy) {
	t.Helper()
	addr := os.Getenv("FILEX_SMB_TEST")
	if addr == "" {
		t.Skip("set FILEX_SMB_TEST=host:port (see smb_live_test.go) to run the live SMB tests")
	}
	p := newStallProxy(t, addr)
	d := smbDriver(t, p.addr(), map[string]any{
		"share":    envOr("FILEX_SMB_SHARE", "data"),
		"user":     envOr("FILEX_SMB_USER", "filex"),
		"password": envOr("FILEX_SMB_PASS", "filexpass"),
		"root":     "filex-stall",
	})
	t.Cleanup(func() { _ = d.Delete(context.Background(), "") })
	return d, p
}

// ⚠ The bug itself: a server that worked, then stopped answering. The call
// on the open session is cut after the attempt timeout, a fresh connection is
// tried within the budget, and the caller hears "unavailable"; when the
// server answers again the storage works without a restart.
func TestLive_HungServerIsCutAndTheStorageRecovers(t *testing.T) {
	d, p := proxiedDriver(t)
	ctx := context.Background()
	if err := d.Mkdir(ctx, "x"); err != nil {
		t.Fatalf("the server was fine at first: %v", err)
	}
	p.silent.Store(true)
	took, err := timeCall(ctx, func(ctx context.Context) error { _, err := d.List(ctx, ""); return err })
	t.Logf("hung server: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, deadStoreCeiling, "the server sent nothing for 1s")

	p.silent.Store(false)
	if _, err := d.List(ctx, ""); err != nil {
		t.Fatalf("the storage did not recover when the server answered again: %v", err)
	}
}

// ⚠ The limits are on silence, not length: 4 MB each way at 512 KB/s is
// eight attempt timeouts, and the download's reader walks away for 2.5 of them.
func TestLive_MovingTransfersAreNeverCut(t *testing.T) {
	d, p := proxiedDriver(t)
	p.rate = 512 << 10
	const size = 4 << 20
	payload := bytes.Repeat([]byte("m"), size)
	ctx := context.Background()
	took, err := timeCall(ctx, func(ctx context.Context) error {
		return d.Write(ctx, "big.bin", bytes.NewReader(payload), size)
	})
	if err != nil {
		t.Fatalf("a moving upload was cut after %.1fs: %v", took.Seconds(), err)
	}
	if took < 5*time.Second {
		t.Fatalf("the upload took only %.1fs - it proves nothing", took.Seconds())
	}
	rc, err := d.Read(ctx, "big.bin")
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
		t.Fatalf("the download was cut: %v", err)
	}
	_ = rc.Close()
	if sha256.Sum256(append(half, rest...)) != sha256.Sum256(payload) {
		t.Fatal("the download arrived altered")
	}
}

// A server that took the start of an upload and then stopped reading. (Not
// parallel: it lowers stall.SendStallFloor.)
func TestLive_StalledUploadIsCut(t *testing.T) {
	stall.SendStallFloor = 0
	t.Cleanup(func() { stall.SendStallFloor = time.Minute })
	d, p := proxiedDriver(t)
	ctx := context.Background()
	if err := d.Mkdir(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	p.upStall.Store(1 << 20)
	body := bytes.Repeat([]byte("s"), 32<<20)
	took, err := timeCall(ctx, func(ctx context.Context) error {
		return d.Write(ctx, "stuck.bin", bytes.NewReader(body), int64(len(body)))
	})
	t.Logf("stalled upload: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, deadStoreCeiling, "the store stopped taking the upload for 1s")
}

// A server that sent the start of a download and then went quiet.
func TestLive_StalledDownloadIsCut(t *testing.T) {
	d, p := proxiedDriver(t)
	ctx := context.Background()
	body := bytes.Repeat([]byte("h"), 4<<20)
	if err := d.Write(ctx, "half.bin", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	p.downStall.Store(1 << 20)
	rc, err := d.Read(ctx, "half.bin")
	if err != nil {
		t.Fatal(err)
	}
	took, err := timeCall(ctx, func(context.Context) error {
		_, err := io.ReadAll(rc)
		return err
	})
	var se *stall.Error
	if err == nil || !errors.As(err, &se) || took > 2*time.Second {
		t.Fatalf("a stalled download: %.1fs err=%v, want a stall within the attempt timeout", took.Seconds(), err)
	}
	_ = rc.Close()
	p.downStall.Store(0)
	if _, err := d.List(ctx, ""); err != nil {
		t.Errorf("the storage did not recover after the stalled download: %v", err)
	}
}

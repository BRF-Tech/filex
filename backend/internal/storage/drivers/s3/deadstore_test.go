package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// Issue #44: a drop into an S3 folder while Hetzner's object storage was down
// answered 503 — correctly — but only after 85.6 s ("PutObject, exceeded
// maximum number of attempts, 6"). The visitor stared at a spinner for a
// minute and a half, and a proxy in front of filex may give up first.
//
// These tests point a real driver (Init, the real SDK client, real sockets)
// at endpoints that are dead in each way a store dies, and time the answer
// for the two calls a person waits on: listing and uploading. Measured before
// the fix, with the same settings (which the driver did not have yet, so it
// used the SDK's defaults): refused 26-27 s, black-hole address 142-147 s,
// silent socket did not return at all (cut by the test at 150 s), NXDOMAIN
// 0.1 s.

// deadStoreSettings are the knobs under test, tight so the tests run in
// seconds: an attempt may wait 1 s for a sign of life, and no new attempt
// starts unless it could finish within 3 s of the first.
var deadStoreSettings = map[string]any{
	"max_attempts":      6,
	"attempt_timeout_s": 1,
	"total_timeout_s":   3,
}

// deadStoreCeiling is how long a dead store may take to answer under
// deadStoreSettings: the total, plus scheduling slack.
const deadStoreCeiling = 3*time.Second + 1500*time.Millisecond

// deadStoreCap stops a hanging call from hanging the run; a call that
// reaches it has failed by a wide margin.
const deadStoreCap = 150 * time.Second

func deadStoreDriver(t *testing.T, endpoint string) *Driver {
	t.Helper()
	return deadStoreDriverWith(t, endpoint, nil)
}

func deadStoreDriverWith(t *testing.T, endpoint string, overrides map[string]any) *Driver {
	t.Helper()
	cfg := map[string]any{
		"bucket":     "b",
		"prefix":     "fx",
		"endpoint":   endpoint,
		"access_key": "fake",
		"secret_key": "fake",
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
	return d
}

// refusedEndpoint is a port nothing listens on: every connect is refused at
// once, so the whole cost is the retry schedule.
func refusedEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "http://" + addr
}

// silentEndpoint accepts connections and never says a word: the request goes
// out, the answer never comes. A hung store behind a live socket.
func silentEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	return "http://" + l.Addr().String()
}

// blackholeEndpoint is TEST-NET-1 (RFC 5737): routed nowhere, so the SYN goes
// out and nothing comes back — a host that fell off the network. (From WSL
// the Windows host answers "refused" after ~20 s; elsewhere it is silence.)
const blackholeEndpoint = "http://192.0.2.1:9000"

// nxdomainEndpoint can never resolve (RFC 6761 reserves .invalid).
const nxdomainEndpoint = "http://filex-dead-store.invalid:9000"

type deadCall struct {
	name string
	run  func(ctx context.Context, d *Driver) error
}

var deadCalls = []deadCall{
	{"list", func(ctx context.Context, d *Driver) error {
		_, err := d.List(ctx, "/")
		return err
	}},
	{"upload", func(ctx context.Context, d *Driver) error {
		body := bytes.Repeat([]byte("x"), 64<<10)
		return d.Write(ctx, "/drop/report.pdf", bytes.NewReader(body), int64(len(body)))
	}},
}

func timeCall(t *testing.T, run func(ctx context.Context) error) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadStoreCap)
	defer cancel()
	start := time.Now()
	err := run(ctx)
	return time.Since(start), err
}

// assertUnavailable: the error must say the STORE is down — in words, and as
// storage.ErrUnavailable for the code that answers 503.
func assertUnavailable(t *testing.T, err error, took time.Duration, words ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("a dead store answered success")
	}
	if took > deadStoreCeiling {
		t.Errorf("took %.1fs, ceiling %.1fs", took.Seconds(), deadStoreCeiling.Seconds())
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

func TestDeadStore_AnswersWithinTheCeiling(t *testing.T) {
	endpoints := []struct {
		name     string
		endpoint func(t *testing.T) string
		words    []string
	}{
		{"connection refused", refusedEndpoint, []string{"the connection was refused"}},
		{"silent socket", silentEndpoint, []string{"no answer within", "of sending the request"}},
		{"black-hole ip", func(*testing.T) string { return blackholeEndpoint }, nil},
		{"dns nxdomain", func(*testing.T) string { return nxdomainEndpoint }, []string{"does not resolve", "(1 attempt in"}},
	}
	for _, ep := range endpoints {
		for _, c := range deadCalls {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := deadStoreDriver(t, ep.endpoint(t))
				took, err := timeCall(t, func(ctx context.Context) error { return c.run(ctx, d) })
				t.Logf("%s %s: %.1fs err=%v", ep.name, c.name, took.Seconds(), err)
				assertUnavailable(t, err, took, ep.words...)
			})
		}
	}
}

// A DNS server that does not answer: the lookup is part of connecting, so the
// attempt timeout bounds it too. (Not parallel: it swaps the resolver.)
func TestDeadStore_SilentDNSServer(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	testResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", pc.LocalAddr().String())
	}}
	t.Cleanup(func() { testResolver = nil })

	for _, c := range deadCalls {
		d := deadStoreDriver(t, "http://store.filex-dead-dns.example:9000")
		took, err := timeCall(t, func(ctx context.Context) error { return c.run(ctx, d) })
		t.Logf("silent dns %s: %.1fs err=%v", c.name, took.Seconds(), err)
		assertUnavailable(t, err, took, "the name lookup got no answer within 1s")
	}
}

// fakeStore is a plain-HTTP S3 stand-in. handle decides each request; it is
// told how many requests came before it.
type fakeStore struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newFakeStore(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int32)) *fakeStore {
	t.Helper()
	fs := &fakeStore{}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, fs.hits.Add(1))
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func answerOK(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, listXML("fx/", "fx/a.txt"))
	case r.Header.Get("X-Amz-Copy-Source") != "":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><CopyObjectResult><ETag>"e"</ETag><LastModified>2026-09-25T00:00:00.000Z</LastModified></CopyObjectResult>`)
	default:
		w.Header().Set("ETag", `"e"`)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}
}

func answerS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

// A refusal is the store ANSWERING: asking again cannot change it, so it is
// neither retried nor reported as the store being down.
func TestDeadStore_RefusalIsNotRetried(t *testing.T) {
	for _, c := range deadCalls {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
				_, _ = io.Copy(io.Discard, r.Body)
				answerS3Error(w, http.StatusForbidden, "AccessDenied")
			})
			d := deadStoreDriver(t, fs.srv.URL)
			took, err := timeCall(t, func(ctx context.Context) error { return c.run(ctx, d) })
			if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
				t.Fatalf("want the store's AccessDenied, got %v", err)
			}
			if n := fs.hits.Load(); n != 1 {
				t.Errorf("403 was sent %d times, want once", n)
			}
			if errors.Is(err, storage.ErrUnavailable) {
				t.Errorf("a 403 reported as the store being down: %v", err)
			}
			if took > time.Second {
				t.Errorf("a refusal took %.1fs", took.Seconds())
			}
		})
	}
}

// An endpoint whose certificate does not verify is a misconfiguration: the
// SDK counts every failed send as a retryable connection error, so without
// refusedByTLS it would spend the whole budget on it.
func TestDeadStore_BadCertificateIsNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { answerOK(w, r) }))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			hits.Add(1)
		}
	}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the rejected handshake is the point
	srv.StartTLS()
	t.Cleanup(srv.Close)
	d := deadStoreDriver(t, srv.URL)
	took, err := timeCall(t, func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	if err == nil || !strings.Contains(err.Error(), "x509") {
		t.Fatalf("want a certificate error, got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("an untrusted certificate was tried %d times, want once", n)
	}
	if errors.Is(err, storage.ErrUnavailable) {
		t.Errorf("a bad certificate reported as the store being down: %v", err)
	}
	if took > time.Second {
		t.Errorf("took %.1fs", took.Seconds())
	}
}

// A short wobble is what the retries are for: two 503s, then the answer.
func TestDeadStore_Transient503IsRiddenOut(t *testing.T) {
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n <= 2 {
			_, _ = io.Copy(io.Discard, r.Body)
			answerS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable")
			return
		}
		answerOK(w, r)
	})
	d := deadStoreDriver(t, fs.srv.URL)
	objs, err := d.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("a two-503 wobble was not ridden out: %v", err)
	}
	if len(objs) != 1 || fs.hits.Load() != 3 {
		t.Fatalf("objs=%v hits=%d", objs, fs.hits.Load())
	}
}

// A store that keeps answering 503 is down, and is given up on in time.
func TestDeadStore_Persistent503GivesUpInTime(t *testing.T) {
	for _, c := range deadCalls {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
				_, _ = io.Copy(io.Discard, r.Body)
				answerS3Error(w, http.StatusServiceUnavailable, "ServiceUnavailable")
			})
			d := deadStoreDriver(t, fs.srv.URL)
			took, err := timeCall(t, func(ctx context.Context) error { return c.run(ctx, d) })
			t.Logf("503 %s: %.1fs, %d requests, err=%v", c.name, took.Seconds(), fs.hits.Load(), err)
			assertUnavailable(t, err, took, "the store answered 503 Service Unavailable")
			if fs.hits.Load() < 2 {
				t.Errorf("a 503 was not retried (%d requests)", fs.hits.Load())
			}
		})
	}
}

// slowRead reads r at about rate bytes per second.
func slowRead(r io.Reader, rate int) (int64, error) {
	const chunk = 64 << 10
	buf := make([]byte, chunk)
	var n int64
	tick := time.Duration(chunk) * time.Second / time.Duration(rate)
	for {
		k, err := io.ReadFull(r, buf)
		n += int64(k)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		time.Sleep(tick)
	}
}

// ⚠ The limits are on SILENCE, not on length. An upload that keeps moving for
// twice the whole budget — six attempt timeouts — must arrive whole.
func TestDeadStore_MovingUploadIsNeverCut(t *testing.T) {
	const size = 48 << 20
	var got atomic.Int64
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		n, err := slowRead(r.Body, 8<<20)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		got.Store(n)
		w.Header().Set("ETag", `"e"`)
		w.WriteHeader(http.StatusOK)
	})
	d := deadStoreDriver(t, fs.srv.URL)
	body := bytes.Repeat([]byte("y"), size)
	took, err := timeCall(t, func(ctx context.Context) error {
		return d.Write(ctx, "/big.bin", bytes.NewReader(body), size)
	})
	if err != nil {
		t.Fatalf("a moving upload was cut after %.1fs: %v", took.Seconds(), err)
	}
	if took < 5*time.Second {
		t.Fatalf("the upload took only %.1fs — it did not outlast the budget, so it proves nothing", took.Seconds())
	}
	if got.Load() != size || fs.hits.Load() != 1 {
		t.Fatalf("store got %d bytes in %d requests", got.Load(), fs.hits.Load())
	}
}

// …and the same for a download, including a reader who stops reading for a
// while: the stall timer runs only while a Read waits on the network.
func TestDeadStore_SlowDownloadAndSlowReaderAreNeverCut(t *testing.T) {
	const size = 32 << 20
	payload := bytes.Repeat([]byte("z"), size)
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.WriteHeader(http.StatusOK)
		const chunk = 256 << 10
		for off := 0; off < size; off += chunk {
			if _, err := w.Write(payload[off : off+chunk]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond) // ~6 MB/s
		}
	})
	d := deadStoreDriver(t, fs.srv.URL)
	start := time.Now()
	rc, err := d.Read(context.Background(), "/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	half := make([]byte, size/2)
	if _, err := io.ReadFull(rc, half); err != nil {
		t.Fatalf("first half: %v", err)
	}
	time.Sleep(2500 * time.Millisecond) // the reader walks away for 2.5 attempt timeouts
	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("the download was cut after %.1fs: %v", time.Since(start).Seconds(), err)
	}
	if took := time.Since(start); took < 5*time.Second {
		t.Fatalf("the download took only %.1fs — it did not outlast the budget", took.Seconds())
	}
	if sha256.Sum256(append(half, rest...)) != sha256.Sum256(payload) {
		t.Fatal("the download arrived altered")
	}
}

// A long listing that keeps arriving is an answer, not silence: the answer
// watch stops when the store's headers arrive, and the body only has to keep
// moving. This one takes about three attempt timeouts to arrive.
func TestDeadStore_SlowListingIsNeverCut(t *testing.T) {
	keys := make([]string, 0, 60)
	for i := range 60 {
		keys = append(keys, fmt.Sprintf("fx/file-%02d.txt", i))
	}
	full := listXML("fx/", keys...)
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		const pieces = 15
		step := (len(full) + pieces - 1) / pieces
		for off := 0; off < len(full); off += step {
			_, _ = io.WriteString(w, full[off:min(off+step, len(full))])
			w.(http.Flusher).Flush()
			time.Sleep(200 * time.Millisecond)
		}
	})
	d := deadStoreDriver(t, fs.srv.URL)
	start := time.Now()
	objs, err := d.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("a listing that kept arriving was cut after %.1fs: %v", time.Since(start).Seconds(), err)
	}
	if took := time.Since(start); took < 2500*time.Millisecond {
		t.Fatalf("the listing arrived in %.1fs — faster than three attempt timeouts, so it proves nothing", took.Seconds())
	}
	if len(objs) != 60 {
		t.Fatalf("got %d objects", len(objs))
	}
}

// A store that took the start of an upload and then stopped reading used to
// leave the upload blocked in Write for as long as TCP kept the peer alive.
// (Not parallel: it lowers stall.SendStallFloor from a minute to the attempt
// timeout, so the run takes seconds.)
func TestDeadStore_StalledUploadIsCut(t *testing.T) {
	stall.SendStallFloor = 0
	t.Cleanup(func() { stall.SendStallFloor = time.Minute })
	release := make(chan struct{})
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		<-release
	})
	t.Cleanup(func() { close(release) }) // after newFakeStore: runs before srv.Close, which waits for the handler
	d := deadStoreDriver(t, fs.srv.URL)
	body := bytes.Repeat([]byte("s"), 96<<20)
	took, err := timeCall(t, func(ctx context.Context) error {
		return d.Write(ctx, "/stuck.bin", bytes.NewReader(body), int64(len(body)))
	})
	t.Logf("stalled upload: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, "the store stopped taking the upload for 1s")
}

// ⚠ A store that takes a whole upload and then never answers. Over 2 MB the
// SDK sends "Expect: 100-continue", and the store's interim "100 Continue"
// used to stop the answer watch before the body was even sent: this upload
// was waited for forever. The wait is the attempt timeout plus the send tail
// (2.5 MB at 256 KB/s = 10 s).
func TestDeadStore_SilentAfterTheWholeUploadIsCut(t *testing.T) {
	const size = 5 << 19
	release := make(chan struct{})
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		_, _ = io.Copy(io.Discard, r.Body) // reading the body sends "100 Continue"
		<-release
	})
	t.Cleanup(func() { close(release) }) // after newFakeStore: runs before srv.Close, which waits for the handler
	d := deadStoreDriver(t, fs.srv.URL)
	body := bytes.Repeat([]byte("c"), size)
	took, err := timeCall(t, func(ctx context.Context) error {
		return d.Write(ctx, "/whole.bin", bytes.NewReader(body), size)
	})
	t.Logf("silent after the upload: %.1fs err=%v", took.Seconds(), err)
	want := time.Second + stall.SendTail(size)
	if err == nil || !errors.Is(err, storage.ErrUnavailable) || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("want the store reported as not answering, got %v", err)
	}
	if took < want-200*time.Millisecond || took > want+1500*time.Millisecond {
		t.Errorf("took %.1fs, want about %.1fs (one attempt: the attempt timeout plus the send tail)", took.Seconds(), want.Seconds())
	}
}

// A store that sent the start of a listing and then went quiet.
func TestDeadStore_StalledAnswerIsCut(t *testing.T) {
	release := make(chan struct{})
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		full := listXML("fx/", "fx/a.txt", "fx/b.txt")
		_, _ = io.WriteString(w, full[:len(full)/2])
		w.(http.Flusher).Flush()
		<-release
	})
	t.Cleanup(func() { close(release) }) // after newFakeStore: runs before srv.Close, which waits for the handler
	d := deadStoreDriver(t, fs.srv.URL)
	took, err := timeCall(t, func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	t.Logf("stalled answer: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, "the answer stopped arriving for 1s")
}

// ⚠ Server-side work is not silence: MinIO answers CopyObject (every rename)
// only after it has copied the object. The copy is waited for; the same wait
// on an ordinary request is not.
func TestDeadStore_CopyWaitsForTheStore(t *testing.T) {
	const work = 2500 * time.Millisecond
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		switch {
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/slow-head.bin"):
			time.Sleep(work)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "3")
			w.WriteHeader(http.StatusOK)
		case r.Header.Get("X-Amz-Copy-Source") != "":
			time.Sleep(work)
			answerOK(w, r)
		default:
			answerOK(w, r)
		}
	})
	d := deadStoreDriver(t, fs.srv.URL)
	if err := d.Copy(context.Background(), "/a.bin", "/b.bin"); err != nil {
		t.Fatalf("a copy that took the store %s was cut: %v", work, err)
	}
	took, err := timeCall(t, func(ctx context.Context) error { _, err := d.Stat(ctx, "/slow-head.bin"); return err })
	assertUnavailable(t, err, took, "no answer within 1s of sending the request")

	// ⚠ On a POOLED connection the transport replays a silent HEAD on a fresh
	// connection by itself. The replay must be cut at once, not waited for a
	// second time: one attempt costs one attempt timeout, which is what the
	// budget counts on.
	one := deadStoreDriverWith(t, fs.srv.URL, map[string]any{"max_attempts": 1})
	if _, err := one.Stat(context.Background(), "/a.bin"); err != nil {
		t.Fatal(err) // leaves a connection in the pool
	}
	took, err = timeCall(t, func(ctx context.Context) error { _, err := one.Stat(ctx, "/slow-head.bin"); return err })
	if err == nil || took > 1600*time.Millisecond {
		t.Errorf("one attempt on a pooled connection took %.1fs (err=%v); the transport's replay was waited for again", took.Seconds(), err)
	}
}

// A store that reads an upload slowly (an HDD-backed MinIO) is the hard case
// for "never cut a moving transfer", twice over:
//   - its last megabytes are still in the sockets' buffers when the request
//     counts as sent — measured 2.3-3.4 MB, i.e. 1.65 s at 2 MB/s and 4.65 s at
//     512 KB/s — so the answer wait must allow for that tail (stall.SendTail);
//   - the kernel hands a blocked writer room only in bursts, so the send sits
//     still for seconds at a time (stall.SendStallFloor).
//
// At 512 KB/s with a 1 s attempt timeout, either one missing cuts this upload.
func TestDeadStore_SlowStoreFinishingTheUploadIsNotCut(t *testing.T) {
	const size = 4 << 20
	var got atomic.Int64
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		n, err := slowRead(r.Body, 512<<10)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		got.Store(n)
		w.Header().Set("ETag", `"e"`)
		w.WriteHeader(http.StatusOK)
	})
	d := deadStoreDriverWith(t, fs.srv.URL, map[string]any{"max_attempts": 1})
	body := bytes.Repeat([]byte("t"), size)
	took, err := timeCall(t, func(ctx context.Context) error {
		return d.Write(ctx, "/tail.bin", bytes.NewReader(body), size)
	})
	if err != nil {
		t.Fatalf("an upload the store was still reading was cut after %.1fs: %v", took.Seconds(), err)
	}
	if got.Load() != size {
		t.Fatalf("store got %d bytes", got.Load())
	}
}

// An attempt that moved for longer than the whole budget and then broke is a
// transfer that lost its connection, not a store that is down: it gets a fresh
// budget, so it is retried. (The upload takes about 8 s, the budget is 3 s.)
func TestDeadStore_LongUploadThatBreaksIsRetried(t *testing.T) {
	fs := newFakeStore(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n == 1 {
			_, _ = slowRead(r.Body, 8<<20)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close() // dropped just before answering
			}
			return
		}
		answerOK(w, r)
	})
	d := deadStoreDriver(t, fs.srv.URL)
	body := bytes.Repeat([]byte("r"), 64<<20)
	if err := d.Write(context.Background(), "/long.bin", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("a long upload that broke at the end was not retried: %v", err)
	}
	if fs.hits.Load() != 2 {
		t.Fatalf("%d requests, want 2", fs.hits.Load())
	}
}

func TestPolicyFrom(t *testing.T) {
	def := defaultPolicy()
	if def.MaxAttempts != 6 || def.AttemptTimeout != 10*time.Second || def.TotalTimeout != 15*time.Second {
		t.Fatalf("defaults changed: %+v", def)
	}
	cases := []struct {
		name string
		cfg  map[string]any
		want policy
	}{
		{"json numbers", map[string]any{"max_attempts": float64(3), "attempt_timeout_s": float64(4), "total_timeout_s": float64(30)},
			policy{stall.Policy{MaxAttempts: 3, AttemptTimeout: 4 * time.Second, TotalTimeout: 30 * time.Second}}},
		{"form strings", map[string]any{"max_attempts": "2", "attempt_timeout_s": " 5 ", "total_timeout_s": "12"},
			policy{stall.Policy{MaxAttempts: 2, AttemptTimeout: 5 * time.Second, TotalTimeout: 12 * time.Second}}},
		{"empty, zero and junk mean the default", map[string]any{"max_attempts": "", "attempt_timeout_s": 0, "total_timeout_s": "soon"},
			def},
		{"negative means the default", map[string]any{"max_attempts": -1, "attempt_timeout_s": -5, "total_timeout_s": -1},
			def},
		{"past the bound is brought down", map[string]any{"max_attempts": 99, "attempt_timeout_s": 100000, "total_timeout_s": 100000},
			policy{stall.Policy{MaxAttempts: stall.MaxAttemptsLimit, AttemptTimeout: stall.AttemptTimeoutLimitS * time.Second, TotalTimeout: stall.TotalTimeoutLimitS * time.Second}}},
	}
	for _, c := range cases {
		if got := policyFrom(c.cfg); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

// The form shows the descriptor's defaults and bounds; the driver applies
// the policy's. They are the same constants — this keeps it that way.
func TestDescriptorShowsThePolicy(t *testing.T) {
	d, ok := storage.DescriptorFor("s3")
	if !ok {
		t.Fatal("no s3 descriptor")
	}
	def := defaultPolicy()
	want := map[string]struct{ def, max int }{
		"max_attempts":      {def.MaxAttempts, stall.MaxAttemptsLimit},
		"attempt_timeout_s": {int(def.AttemptTimeout / time.Second), stall.AttemptTimeoutLimitS},
		"total_timeout_s":   {int(def.TotalTimeout / time.Second), stall.TotalTimeoutLimitS},
	}
	for key, w := range want {
		f, ok := d.Field(key)
		if !ok {
			t.Errorf("%s is not in the descriptor, so no form offers it", key)
			continue
		}
		if f.Type != storage.FieldInt || !f.Advanced || f.Default != w.def ||
			f.Min == nil || *f.Min != 1 || f.Max == nil || *f.Max != w.max {
			t.Errorf("%s: type=%s advanced=%v default=%v min=%v max=%v, want int/advanced/%d/1/%d",
				key, f.Type, f.Advanced, f.Default, f.Min, f.Max, w.def, w.max)
		}
	}
}

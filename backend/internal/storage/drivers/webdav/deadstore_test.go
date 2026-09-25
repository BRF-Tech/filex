package webdav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
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

// Issue #73: the WebDAV driver's http.Client{Timeout: 60 * time.Second}
// bounded the WHOLE request, body included, so every upload and download that
// took longer than a minute was cut — a big file, a slow line — while a store
// that was down took up to that minute to say so, and not in words
// (storage.ErrUnavailable was never returned).
//
// These tests point a real driver at a local WebDAV stand-in that is dead,
// silent, or slow but moving, the same way the S3 driver's dead-store tests do
// (drivers/s3/deadstore_test.go).

// deadStoreSettings are tight so the tests run in seconds: an attempt may wait
// 1 s for a sign of life, and no new attempt starts unless it could finish
// within 3 s of the first.
var deadStoreSettings = map[string]any{
	"max_attempts":      6,
	"attempt_timeout_s": 1,
	"total_timeout_s":   3,
}

// deadStoreCeiling is how long a dead store may take to answer under
// deadStoreSettings: the total, plus scheduling slack.
const deadStoreCeiling = 3*time.Second + 1500*time.Millisecond

// deadStoreCap stops a hanging call from hanging the run.
const deadStoreCap = 150 * time.Second

func davDriver(t *testing.T, base string, overrides map[string]any) *Driver {
	t.Helper()
	cfg := map[string]any{"url": base + "/dav/", "user": "u", "password": "p", "root": "fx"}
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

// silentEndpoint accepts connections and never says a word.
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

// blackholeEndpoint is TEST-NET-1 (RFC 5737): the SYN goes out, nothing comes
// back. nxdomainEndpoint can never resolve (RFC 6761).
const (
	blackholeEndpoint = "http://192.0.2.1:9000"
	nxdomainEndpoint  = "http://filex-dead-dav.invalid:9000"
)

type davCall struct {
	name string
	run  func(ctx context.Context, d *Driver) error
}

var davCalls = []davCall{
	{"list", func(ctx context.Context, d *Driver) error {
		_, err := d.List(ctx, "/")
		return err
	}},
	{"upload", func(ctx context.Context, d *Driver) error {
		body := bytes.Repeat([]byte("x"), 64<<10)
		return d.Write(ctx, "/drop/report.pdf", bytes.NewReader(body), int64(len(body)))
	}},
}

func timeCall(run func(ctx context.Context) error) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadStoreCap)
	defer cancel()
	start := time.Now()
	err := run(ctx)
	return time.Since(start), err
}

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
		for _, c := range davCalls {
			t.Run(ep.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				d := davDriver(t, ep.endpoint(t), nil)
				took, err := timeCall(func(ctx context.Context) error { return c.run(ctx, d) })
				t.Logf("%s %s: %.1fs err=%v", ep.name, c.name, took.Seconds(), err)
				assertUnavailable(t, err, took, ep.words...)
			})
		}
	}
}

// fakeDAV is a WebDAV stand-in. handle decides each request; it is told how
// many requests came before it.
type fakeDAV struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newFakeDAV(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int32)) *fakeDAV {
	t.Helper()
	fs := &fakeDAV{}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, fs.hits.Add(1))
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func listXML(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	b.WriteString(`<D:response><D:href>/dav/fx/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:propstat></D:response>`)
	for _, n := range names {
		fmt.Fprintf(&b, `<D:response><D:href>/dav/fx/%s</D:href><D:propstat><D:prop><D:getcontentlength>3</D:getcontentlength><D:resourcetype/></D:prop></D:propstat></D:response>`, n)
	}
	b.WriteString(`</D:multistatus>`)
	return b.String()
}

func answerOK(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	switch r.Method {
	case "PROPFIND":
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, listXML("a.txt"))
	case http.MethodPut, "MKCOL", "COPY", "MOVE":
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusOK)
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

// slowWrite writes payload at about rate bytes per second.
func slowWrite(w http.ResponseWriter, payload []byte, rate int) {
	const chunk = 64 << 10
	tick := time.Duration(chunk) * time.Second / time.Duration(rate)
	for off := 0; off < len(payload); off += chunk {
		if _, err := w.Write(payload[off:min(off+chunk, len(payload))]); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		time.Sleep(tick)
	}
}

// ⚠ The bug itself: a transfer that keeps moving for longer than the minute
// the old client allowed. 75 s at 512 KB/s (a slow line) each way, both at
// once. The attempt timeout here is 1 s, so the transfer also outlasts it 75
// times over: what is bounded is silence, not length.
func TestDeadStore_MovingTransfersOutlastTheOldMinute(t *testing.T) {
	if testing.Short() {
		t.Skip("75 s: moves a file for longer than the old 60 s limit")
	}
	const rate = 512 << 10
	const size = 75 * rate
	payload := bytes.Repeat([]byte("m"), size)
	var got atomic.Int64
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		switch r.Method {
		case http.MethodPut:
			n, err := slowRead(r.Body, rate)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			got.Store(n)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			w.Header().Set("Content-Length", fmt.Sprint(size))
			w.WriteHeader(http.StatusOK)
			slowWrite(w, payload, rate)
		default:
			answerOK(w, r)
		}
	})
	d := davDriver(t, fs.srv.URL, nil)

	t.Run("upload", func(t *testing.T) {
		t.Parallel()
		took, err := timeCall(func(ctx context.Context) error {
			return d.Write(ctx, "/big.bin", bytes.NewReader(payload), size)
		})
		t.Logf("upload: %.1fs err=%v", took.Seconds(), err)
		if err != nil {
			t.Fatalf("a moving upload was cut after %.1fs: %v", took.Seconds(), err)
		}
		if took < 61*time.Second {
			t.Fatalf("the upload took only %.1fs — it did not outlast the old minute, so it proves nothing", took.Seconds())
		}
		if got.Load() != size {
			t.Fatalf("store got %d bytes", got.Load())
		}
	})
	t.Run("download", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		rc, err := d.Read(context.Background(), "/big.bin")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		took := time.Since(start)
		t.Logf("download: %.1fs err=%v", took.Seconds(), err)
		if err != nil {
			t.Fatalf("a moving download was cut after %.1fs: %v", took.Seconds(), err)
		}
		if took < 61*time.Second {
			t.Fatalf("the download took only %.1fs — it did not outlast the old minute", took.Seconds())
		}
		if sha256.Sum256(body) != sha256.Sum256(payload) {
			t.Fatal("the download arrived altered")
		}
	})
}

// The same claim in seconds, against a store that reads slowly (lesson: a
// fast stand-in hides the sockets' buffers): 4 MB at 512 KB/s is 8 attempt
// timeouts, and the last megabytes are still buffered when the request counts
// as sent.
func TestDeadStore_SlowStoreFinishingTheUploadIsNotCut(t *testing.T) {
	const size = 4 << 20
	var got atomic.Int64
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		if r.Method != http.MethodPut {
			answerOK(w, r)
			return
		}
		n, err := slowRead(r.Body, 512<<10)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		got.Store(n)
		w.WriteHeader(http.StatusCreated)
	})
	d := davDriver(t, fs.srv.URL, map[string]any{"max_attempts": 1})
	body := bytes.Repeat([]byte("t"), size)
	took, err := timeCall(func(ctx context.Context) error {
		return d.Write(ctx, "/tail.bin", bytes.NewReader(body), size)
	})
	if err != nil {
		t.Fatalf("an upload the store was still reading was cut after %.1fs: %v", took.Seconds(), err)
	}
	if got.Load() != size {
		t.Fatalf("store got %d bytes", got.Load())
	}
}

// A download that keeps arriving, read by a caller who walks away for a
// while: the stall timer runs only while a Read waits on the network.
func TestDeadStore_SlowDownloadAndSlowReaderAreNeverCut(t *testing.T) {
	const size = 8 << 20
	payload := bytes.Repeat([]byte("z"), size)
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.WriteHeader(http.StatusOK)
		slowWrite(w, payload, 2<<20)
	})
	d := davDriver(t, fs.srv.URL, nil)
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
	time.Sleep(2500 * time.Millisecond)
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

// A refusal is the store ANSWERING: never retried, never "unavailable".
func TestDeadStore_RefusalIsNotRetried(t *testing.T) {
	for _, c := range davCalls {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusUnauthorized)
			})
			d := davDriver(t, fs.srv.URL, nil)
			took, err := timeCall(func(ctx context.Context) error { return c.run(ctx, d) })
			if err == nil || !strings.Contains(err.Error(), "401") {
				t.Fatalf("want the store's 401, got %v", err)
			}
			if n := fs.hits.Load(); n != 1 {
				t.Errorf("401 was sent %d times, want once", n)
			}
			if errors.Is(err, storage.ErrUnavailable) {
				t.Errorf("a 401 reported as the store being down: %v", err)
			}
			if took > time.Second {
				t.Errorf("a refusal took %.1fs", took.Seconds())
			}
		})
	}
}

// A short wobble is what the retries are for: two 503s, then the answer.
func TestDeadStore_Transient503IsRiddenOut(t *testing.T) {
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n <= 2 {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		answerOK(w, r)
	})
	d := davDriver(t, fs.srv.URL, nil)
	objs, err := d.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("a two-503 wobble was not ridden out: %v", err)
	}
	if len(objs) != 1 || fs.hits.Load() != 3 {
		t.Fatalf("objs=%v hits=%d", objs, fs.hits.Load())
	}
}

// A 503 is the server saying it did not take the request, so even a MOVE —
// which is not sent again after silence, it may have happened — is.
func TestDeadStore_Transient503OnAMoveIsRiddenOut(t *testing.T) {
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		answerOK(w, r)
	})
	d := davDriver(t, fs.srv.URL, nil)
	if err := d.Move(context.Background(), "/a.txt", "/b.txt"); err != nil {
		t.Fatalf("a move answered 503 twice was not tried again: %v", err)
	}
	if n := fs.hits.Load(); n != 3 {
		t.Fatalf("%d requests, want 3", n)
	}
}

// What reached the server and got no answer: a listing changes nothing and is
// asked again; a move may have happened and is not sent twice.
func TestDeadStore_SilenceRetriesOnlyWhatChangesNothing(t *testing.T) {
	serverWorkTimeout = 1500 * time.Millisecond // not parallel: a move's answer wait, lowered
	t.Cleanup(func() { serverWorkTimeout = 10 * time.Minute })
	release := make(chan struct{})
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, n int32) {
		if n == 1 || r.Method == "MOVE" {
			<-release
			return
		}
		answerOK(w, r)
	})
	t.Cleanup(func() { close(release) }) // after newFakeDAV: runs before srv.Close, which waits for the handler
	d := davDriver(t, fs.srv.URL, nil)
	objs, err := d.List(context.Background(), "/")
	if err != nil || len(objs) != 1 {
		t.Fatalf("a listing whose first answer never came was not asked again: %v %v", objs, err)
	}
	before := fs.hits.Load()
	took, err := timeCall(func(ctx context.Context) error { return d.Move(ctx, "/a.txt", "/b.txt") })
	if err == nil {
		t.Fatal("a move that got no answer reported success")
	}
	if n := fs.hits.Load() - before; n != 1 {
		t.Errorf("a move that got no answer was sent %d times: it may have happened", n)
	}
	t.Logf("silent move: %.1fs err=%v", took.Seconds(), err)
}

// A store that keeps answering 503 is down, and is given up on in time. An
// upload whose body went out is not sent again (it could not be: the body is
// a stream), so it is one request.
func TestDeadStore_Persistent503GivesUpInTime(t *testing.T) {
	for _, c := range davCalls {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusServiceUnavailable)
			})
			d := davDriver(t, fs.srv.URL, nil)
			took, err := timeCall(func(ctx context.Context) error { return c.run(ctx, d) })
			t.Logf("503 %s: %.1fs, %d requests, err=%v", c.name, took.Seconds(), fs.hits.Load(), err)
			assertUnavailable(t, err, took, "the store answered 503 Service Unavailable")
			if c.name == "list" && fs.hits.Load() < 2 {
				t.Errorf("a 503 listing was not retried (%d requests)", fs.hits.Load())
			}
		})
	}
}

// A store that took the start of an upload and then stopped reading. (Not
// parallel: it lowers stall.SendStallFloor from a minute to the attempt
// timeout, so the run takes seconds.)
func TestDeadStore_StalledUploadIsCut(t *testing.T) {
	stall.SendStallFloor = 0
	t.Cleanup(func() { stall.SendStallFloor = time.Minute })
	release := make(chan struct{})
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		<-release
	})
	t.Cleanup(func() { close(release) }) // after newFakeDAV: runs before srv.Close, which waits for the handler
	d := davDriver(t, fs.srv.URL, nil)
	body := bytes.Repeat([]byte("s"), 96<<20)
	took, err := timeCall(func(ctx context.Context) error {
		return d.Write(ctx, "/stuck.bin", bytes.NewReader(body), int64(len(body)))
	})
	t.Logf("stalled upload: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, "the store stopped taking the upload for 1s")
}

// A store that takes a whole upload and then never answers: the wait is the
// attempt timeout plus the send tail (2.5 MB at 256 KB/s = 10 s).
func TestDeadStore_SilentAfterTheWholeUploadIsCut(t *testing.T) {
	const size = 5 << 19
	release := make(chan struct{})
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
	})
	t.Cleanup(func() { close(release) })
	d := davDriver(t, fs.srv.URL, nil)
	body := bytes.Repeat([]byte("c"), size)
	took, err := timeCall(func(ctx context.Context) error {
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
	if n := fs.hits.Load(); n != 1 {
		t.Errorf("an upload whose body went out was sent %d times", n)
	}
}

// A store that sent the start of a listing and then went quiet.
func TestDeadStore_StalledAnswerIsCut(t *testing.T) {
	release := make(chan struct{})
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		full := listXML("a.txt", "b.txt")
		_, _ = io.WriteString(w, full[:len(full)/2])
		w.(http.Flusher).Flush()
		<-release
	})
	t.Cleanup(func() { close(release) })
	d := davDriver(t, fs.srv.URL, map[string]any{"max_attempts": 1})
	took, err := timeCall(func(ctx context.Context) error { _, err := d.List(ctx, "/"); return err })
	t.Logf("stalled answer: %.1fs err=%v", took.Seconds(), err)
	assertUnavailable(t, err, took, "the answer stopped arriving for 1s")
}

// ⚠ Server-side work is not silence: a WebDAV server answers COPY and MOVE
// (every rename) only when it has copied or moved the tree. The copy is waited
// for; the same wait on a listing is not.
func TestDeadStore_CopyWaitsForTheServer(t *testing.T) {
	const work = 2500 * time.Millisecond
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		switch {
		case r.Method == "COPY" || r.Method == "MOVE":
			time.Sleep(work)
			answerOK(w, r)
		case r.Method == "PROPFIND" && strings.HasSuffix(r.URL.Path, "/slow"):
			time.Sleep(work)
			answerOK(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/slow.bin"):
			time.Sleep(work)
			w.WriteHeader(http.StatusOK)
		default:
			answerOK(w, r)
		}
	})
	d := davDriver(t, fs.srv.URL, nil)
	if err := d.Copy(context.Background(), "/a.bin", "/b.bin"); err != nil {
		t.Fatalf("a copy that took the server %s was cut: %v", work, err)
	}
	if err := d.Move(context.Background(), "/a.bin", "/c.bin"); err != nil {
		t.Fatalf("a move that took the server %s was cut: %v", work, err)
	}
	took, err := timeCall(func(ctx context.Context) error { _, err := d.List(ctx, "/slow"); return err })
	assertUnavailable(t, err, took, "no answer within 1s of sending the request")

	// ⚠ On a POOLED connection the transport replays a silent GET on a fresh
	// connection by itself. The replay must be cut at once: one attempt costs
	// one attempt timeout.
	one := davDriver(t, fs.srv.URL, map[string]any{"max_attempts": 1})
	if _, err := one.Stat(context.Background(), "/a.bin"); err != nil {
		t.Fatal(err) // leaves a connection in the pool
	}
	took, err = timeCall(func(ctx context.Context) error {
		rc, err := one.Read(ctx, "/slow.bin")
		if err == nil {
			rc.Close()
		}
		return err
	})
	if err == nil || took > 1600*time.Millisecond {
		t.Errorf("one attempt on a pooled connection took %.1fs (err=%v); the transport's replay was waited for again", took.Seconds(), err)
	}
}

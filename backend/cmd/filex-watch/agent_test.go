package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestSender(t *testing.T, f *fakeFilex) (*sender, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := testConfig(t, srv.URL)
	cl, err := newClient(cfg)
	require.NoError(t, err)
	return &sender{cfg: cfg, cl: cl, q: newQueue(cfg.QueueMax), log: quietLog(), info: agentInfo(cfg)}, srv
}

// The first report after a start asks for a resync; the next carries the
// burst under the next number.
func TestSender_StartsWithAResyncThenSendsBursts(t *testing.T) {
	f := &fakeFilex{}
	s, _ := newTestSender(t, f)
	ctx := context.Background()

	s.q.markResync(t0)
	s.step(ctx, t0.Add(2*time.Second))
	s.q.addFolder("/a", t0.Add(3*time.Second))
	s.q.addFolder("/b", t0.Add(3*time.Second))
	s.step(ctx, t0.Add(3500*time.Millisecond)) // not quiet yet: nothing sent
	s.step(ctx, t0.Add(4*time.Second))

	got := f.seen()
	require.Len(t, got, 2)
	assert.True(t, got[0].Resync)
	assert.EqualValues(t, 1, got[0].Seq)
	assert.Equal(t, []string{"/a", "/b"}, got[1].Folders)
	assert.EqualValues(t, 2, got[1].Seq)
	assert.False(t, got[1].Resync)
	assert.Equal(t, 1, got[0].Protocol)
	assert.Equal(t, version, got[0].Agent.Version)
}

// A report filex did not answer is sent again under the SAME number, after a
// pause, and - because filex may have restarted and lost what it held - as a
// resync.
func TestSender_ATransportFailureIsRetriedAsAResyncUnderTheSameNumber(t *testing.T) {
	f := &fakeFilex{answers: []func(http.ResponseWriter){status(503, "", ""), nil}}
	f.answers[1] = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}
	s, _ := newTestSender(t, f)
	ctx := context.Background()

	s.q.addFolder("/a", t0)
	s.step(ctx, t0.Add(2*time.Second))
	require.False(t, s.retryAt.IsZero(), "a pause before the next attempt")
	s.step(ctx, t0.Add(2*time.Second+100*time.Millisecond)) // inside the pause: nothing
	require.Len(t, f.seen(), 1)
	s.step(ctx, s.retryAt)

	got := f.seen()
	require.Len(t, got, 2)
	assert.Equal(t, got[0].Seq, got[1].Seq, "the same report, the same number")
	assert.Equal(t, []string{"/a"}, got[1].Folders)
	assert.False(t, got[0].Resync)
	assert.True(t, got[1].Resync, "filex may have lost what it held")
	assert.Nil(t, s.inflight)
	assert.Zero(t, s.backoff)
}

// A report filex refused as such (400) is dropped - sending it again cannot
// help - and the next asks for a resync instead.
func TestSender_ARefusedReportBecomesAResync(t *testing.T) {
	f := &fakeFilex{answers: []func(http.ResponseWriter){status(400, "watch_report_invalid", "bad"), nil}}
	f.answers[1] = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}
	s, _ := newTestSender(t, f)
	ctx := context.Background()

	s.q.addFolder("/a", t0)
	s.step(ctx, t0.Add(2*time.Second))
	assert.Nil(t, s.inflight, "dropped")
	s.step(ctx, t0.Add(10*time.Second))

	got := f.seen()
	require.Len(t, got, 2)
	assert.True(t, got[1].Resync)
	assert.Empty(t, got[1].Folders)
	assert.Equal(t, got[0].Seq+1, got[1].Seq)
}

// With nothing to say, a heartbeat every `heartbeat`.
func TestSender_Heartbeat(t *testing.T) {
	f := &fakeFilex{}
	s, _ := newTestSender(t, f)
	ctx := context.Background()
	s.lastSent = t0
	s.step(ctx, t0.Add(30*time.Second))
	assert.Empty(t, f.seen())
	s.step(ctx, t0.Add(61*time.Second))
	got := f.seen()
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Folders)
	assert.False(t, got[0].Resync)
}

// filex keeps the folders it has taken in memory until it hands them to its
// catalogue: an answer from a NEW filex process (another instance) means the
// old one may have died with some, and the next report asks for one resync.
// The same instance asks for nothing, and the first answer an agent hears is
// only remembered (its first report was a resync already).
func TestSender_AFilexRestartAsksForOneResync(t *testing.T) {
	f := &fakeFilex{instance: "A"}
	s, _ := newTestSender(t, f)
	ctx := context.Background()
	t1 := t0
	beat := func() {
		t1 = t1.Add(61 * time.Second)
		s.step(ctx, t1)
	}

	s.q.addFolder("/a", t1)
	t1 = t1.Add(2 * time.Second)
	s.step(ctx, t1)
	assert.Equal(t, "A", s.instance, "the first answer is remembered")
	beat()
	assert.True(t, s.q.take(1).empty(), "the same filex: nothing to ask")

	f.mu.Lock()
	f.instance = "B"
	f.mu.Unlock()
	beat()
	assert.Equal(t, "B", s.instance)
	t1 = t1.Add(2 * time.Second)
	s.step(ctx, t1)

	got := f.seen()
	require.Len(t, got, 4)
	assert.False(t, got[1].Resync)
	assert.False(t, got[2].Resync, "the report the new filex answered was not a resync")
	assert.True(t, got[3].Resync, "the next one is")

	beat()
	got = f.seen()
	require.Len(t, got, 5)
	assert.False(t, got[4].Resync, "once")
}

// `check` sends one report and says what filex answered.
func TestRun_Check(t *testing.T) {
	f := &fakeFilex{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	root := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, []byte(testKey), 0o600))
	var out, errOut bytes.Buffer
	code := run([]string{"check", "--server", srv.URL, "--storage", "7", "--root", root, "--key-file", keyFile},
		envOf(nil), &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.Contains(t, out.String(), `"medya"`)
	require.Len(t, f.seen(), 1)
	assert.False(t, strings.Contains(out.String(), testKey), "the key is never printed")
}

func TestRun_Version(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 0, run([]string{"version"}, envOf(nil), &out, io.Discard))
	assert.Equal(t, "filex-watch "+version+"\n", out.String())
}

// A bad configuration stops at start with what to set, and exit code 2.
func TestRun_BadConfig(t *testing.T) {
	var errOut bytes.Buffer
	assert.Equal(t, 2, run([]string{"--storage", "1"}, envOf(nil), io.Discard, &errOut))
	assert.Contains(t, errOut.String(), "key")
}

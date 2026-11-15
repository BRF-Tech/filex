package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// fakeFilex is the door as the agent meets it: it records every report and
// answers what the test tells it to.
type fakeFilex struct {
	mu      sync.Mutex
	reports []watchproto.Report
	auth    []string
	// answer, when set, writes the next answers (one per call, the last one
	// repeating); nil: 202 accepted.
	answers []func(w http.ResponseWriter)
	// instance is the filex process the default answer names.
	instance string
}

func (f *fakeFilex) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	var rep watchproto.Report
	_ = json.NewDecoder(r.Body).Decode(&rep)
	f.reports = append(f.reports, rep)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	instance := f.instance
	var answer func(http.ResponseWriter)
	if len(f.answers) > 0 {
		answer = f.answers[0]
		if len(f.answers) > 1 {
			f.answers = f.answers[1:]
		}
	}
	f.mu.Unlock()
	if r.URL.Path != watchproto.EventsPath(7) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if answer != nil {
		answer(w)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(watchproto.Answer{Accepted: true, Seq: rep.Seq, Queued: len(rep.Folders), Storage: "medya", Instance: instance})
}

func (f *fakeFilex) seen() []watchproto.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]watchproto.Report(nil), f.reports...)
}

func status(code int, errCode, msg string, header ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(code)
		if errCode != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode, "message": msg})
		}
	}
}

func testConfig(t *testing.T, server string) *Config {
	t.Helper()
	cfg := defaults()
	cfg.Server, cfg.Storage, cfg.Root, cfg.key = server, 7, t.TempDir(), testKey
	return &cfg
}

func TestClient_SendsTheKeyAndReadsTheAnswer(t *testing.T) {
	f := &fakeFilex{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cl, err := newClient(testConfig(t, srv.URL))
	require.NoError(t, err)
	ans, err := cl.send(context.Background(), watchproto.Report{Protocol: 1, Seq: 3, Folders: []string{"/a"}})
	require.NoError(t, err)
	assert.True(t, ans.Accepted)
	assert.EqualValues(t, 3, ans.Seq)
	assert.Equal(t, "medya", ans.Storage)
	assert.Equal(t, "Bearer "+testKey, f.auth[0])
}

// Each refusal is classified the way the agent acts on it.
func TestClient_ClassifiesRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		answer               func(http.ResponseWriter)
		transport, badReport bool
		retryAfter           time.Duration
		says                 string
	}{
		"a revoked key":    {answer: status(401, "watch_key_unknown", "This filex-watch key is not valid"), says: "watch_key_unknown"},
		"another storage":  {answer: status(403, "watch_key_other_storage", "belongs to storage 9")},
		"too many":         {answer: status(429, "watch_rate_limited", "wait", "Retry-After", "60"), retryAfter: time.Minute},
		"filex restarting": {answer: status(502, "", ""), transport: true},
		"a bad report":     {answer: status(400, "watch_report_invalid", "could not be read"), badReport: true},
		"too large":        {answer: status(413, "watch_report_too_large", "larger"), badReport: true},
		"switched off":     {answer: status(409, "watch_storage_off", "switched off")},
	} {
		f := &fakeFilex{answers: []func(http.ResponseWriter){tc.answer}}
		srv := httptest.NewServer(f)
		cl, err := newClient(testConfig(t, srv.URL))
		require.NoError(t, err)
		_, err = cl.send(context.Background(), watchproto.Report{Protocol: 1})
		srv.Close()
		require.Error(t, err, name)
		se := asSendError(err)
		assert.Equal(t, tc.transport, se.transport(), name)
		assert.Equal(t, tc.badReport, se.badReport(), name)
		assert.Equal(t, tc.retryAfter, se.RetryAfter, name)
		if tc.says != "" {
			assert.Contains(t, se.Error(), tc.says, name)
		}
	}
}

// No answer at all is a transport failure: filex may have restarted.
func TestClient_NoAnswerIsATransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	cl, err := newClient(testConfig(t, url))
	require.NoError(t, err)
	_, err = cl.send(context.Background(), watchproto.Report{Protocol: 1})
	require.Error(t, err)
	assert.True(t, asSendError(err).transport())
}

func TestBackoff_DoublesToItsCeilingAndJitters(t *testing.T) {
	d := time.Duration(0)
	var seen []time.Duration
	for i := 0; i < 12; i++ {
		d = nextBackoff(d)
		seen = append(seen, d)
	}
	assert.Equal(t, time.Second, seen[0])
	assert.Equal(t, 2*time.Second, seen[1])
	assert.Equal(t, 4*time.Second, seen[2])
	assert.Equal(t, backoffMax, seen[11], "never past five minutes")
	for i := 0; i < 200; i++ {
		j := jitter(10 * time.Second)
		assert.GreaterOrEqual(t, j, 8*time.Second)
		assert.LessOrEqual(t, j, 12*time.Second)
	}
}

// A field longer than a report may carry is cut on a character boundary.
func TestClip(t *testing.T) {
	long := strings.Repeat("ş", watchproto.MaxAgentField)
	c := clip(long)
	assert.LessOrEqual(t, len(c), watchproto.MaxAgentField)
	assert.True(t, utf8.ValidString(c))
	assert.Equal(t, "nas01", clip("nas01"))
}

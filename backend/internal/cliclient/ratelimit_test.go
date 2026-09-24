package cliclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimiter_ZeroMeansUnlimited(t *testing.T) {
	assert.Nil(t, NewRateLimiter(0))
	assert.Nil(t, NewRateLimiter(-5))
	var l *RateLimiter
	require.NoError(t, l.WaitN(context.Background(), 1<<30), "a nil limiter never waits")
}

// The bucket starts full (one second's worth), so moving 3 s worth of bytes
// takes about 2 s — measured, with generous bounds for a busy CI box.
func TestRateLimiter_PacesAStream(t *testing.T) {
	const rate = 256 << 10 // 256 KiB/s
	l := NewRateLimiter(rate)
	src := bytes.NewReader(make([]byte, 3*rate))

	start := time.Now()
	n, err := io.Copy(io.Discard, l.Reader(context.Background(), src))
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, int64(3*rate), n)
	assert.GreaterOrEqual(t, elapsed, 1800*time.Millisecond, "3 s of bytes at the limit, 1 s of burst")
	assert.Less(t, elapsed, 4*time.Second)
}

// One limiter for all four parallel transfers: together they stay under it.
func TestRateLimiter_IsSharedAcrossConcurrentStreams(t *testing.T) {
	const rate = 256 << 10
	l := NewRateLimiter(rate)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(io.Discard, l.Reader(context.Background(), bytes.NewReader(make([]byte, rate/2))))
		}()
	}
	wg.Wait()
	// 2 s worth of bytes in total, 1 s of burst: about 1 s, never the 0 s four
	// separate limiters would allow.
	assert.GreaterOrEqual(t, time.Since(start), 800*time.Millisecond)
}

func TestRateLimiter_StopsWhenTheContextEnds(t *testing.T) {
	l := NewRateLimiter(1024)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := io.Copy(io.Discard, l.Reader(ctx, bytes.NewReader(make([]byte, 1<<20))))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDownload_HonoursTheDownloadLimit(t *testing.T) {
	const rate = 512 << 10
	body := bytes.Repeat([]byte("x"), 2*rate)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.bin", time.Unix(1_700_000_000, 0), bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	api := New(Conn{URL: srv.URL, Token: "tok"})
	api.DownLimit = NewRateLimiter(rate)

	start := time.Now()
	var buf bytes.Buffer
	n, err := api.DownloadSized(context.Background(), "docs://f.bin", &buf, int64(len(body)))
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), n)
	assert.GreaterOrEqual(t, time.Since(start), 800*time.Millisecond)
}

func TestUpload_HonoursTheUploadLimit(t *testing.T) {
	const rate = 512 << 10
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")
	api.UpLimit = NewRateLimiter(rate)
	api.StagedThreshold = -1 // the multipart path; the staged path is paced by putChunk

	p := filepath.Join(t.TempDir(), "big.bin")
	require.NoError(t, os.WriteFile(p, bytes.Repeat([]byte("y"), 2*rate), 0o644))

	start := time.Now()
	_, _, err := api.Upload(context.Background(), p, "docs://inbox/")
	require.NoError(t, err)
	assert.Equal(t, 2*rate, len(fs.uploadBytes))
	assert.GreaterOrEqual(t, time.Since(start), 800*time.Millisecond)
}

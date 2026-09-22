package cliclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preparingJSON is the body v0.20–v0.42 sent for a big file on a slow backend,
// verbatim in shape: what 45 files on a real deployment ended up containing.
const preparingJSON = `{"name":"poster v02.psd","percent":0,"ready":false,"size":151983227,"state":"preparing"}`

// downloadServer answers action=download the way a real filex does: an
// unranged request for a "big" file gets the 202 "preparing" answer (what every
// server up to v0.42.2 sent to anything that was not a browser), a Range
// request goes through http.ServeContent and gets 206.
type downloadServer struct {
	body       []byte
	always202  bool   // answer 202 even to a Range request (no real server does)
	ignoreRng  bool   // answer 200 + the whole body whatever the Range says
	contentRng string // when set, a 206 carrying THIS Content-Range instead
	sawRange   []string
	status416  bool // answer 416 bytes */0 (an empty object asked for bytes=0-)
}

func (d *downloadServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "download" {
			http.Error(w, "unexpected action", http.StatusBadRequest)
			return
		}
		rng := r.Header.Get("Range")
		d.sawRange = append(d.sawRange, rng)
		switch {
		case d.status416:
			w.Header().Set("Content-Range", "bytes */0")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		case d.always202 || rng == "":
			w.Header().Set("Retry-After", "2")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(preparingJSON))
		case d.contentRng != "":
			w.Header().Set("Content-Range", d.contentRng)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(d.body)
		case d.ignoreRng:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(d.body)
		default:
			http.ServeContent(w, r, "poster.psd", time.Unix(1_700_000_000, 0), bytes.NewReader(d.body))
		}
	})
}

func newDownloadClient(t *testing.T, d *downloadServer) *Client {
	t.Helper()
	srv := httptest.NewServer(d.handler(t))
	t.Cleanup(srv.Close)
	return New(Conn{URL: srv.URL, Token: "tok"})
}

// ⚠ The data-loss bug this file exists for: a 202 used to be "2xx, so the
// file", its JSON was written to disk, and the sync engine later uploaded that
// JSON over the real file on the server.
func TestDownload_AsksForTheWholeRangeSoNoServerAnswersPreparing(t *testing.T) {
	d := &downloadServer{body: bytes.Repeat([]byte("8BPS"), 4096)}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	n, err := api.Download(context.Background(), "docs://poster.psd", &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(len(d.body)), n)
	assert.Equal(t, d.body, buf.Bytes())
	require.Len(t, d.sawRange, 1)
	assert.Equal(t, "bytes=0-", d.sawRange[0], "every download must ask for bytes=0- — the one request no server answers with 202")
}

func TestDownload_A202IsNeverWrittenAsTheFile(t *testing.T) {
	d := &downloadServer{body: []byte("real bytes"), always202: true}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	n, err := api.Download(context.Background(), "docs://poster.psd", &buf)
	require.Error(t, err)
	assert.Zero(t, n)
	assert.Zero(t, buf.Len(), "not one byte of a 202 may reach the writer")
	var ae *APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusAccepted, ae.Status)
	assert.Contains(t, err.Error(), "preparing")
}

func TestDownload_AServerThatIgnoresRangeStillDelivers(t *testing.T) {
	d := &downloadServer{body: []byte("whole object"), ignoreRng: true}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	_, err := api.Download(context.Background(), "docs://poster.psd", &buf)
	require.NoError(t, err)
	assert.Equal(t, "whole object", buf.String())
}

func TestDownload_APartialRangeIsRefused(t *testing.T) {
	d := &downloadServer{body: []byte("0123456789"), contentRng: "bytes 0-9/100"}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	_, err := api.Download(context.Background(), "docs://poster.psd", &buf)
	require.Error(t, err)
	assert.Zero(t, buf.Len(), "a window that is not the whole file must not be written as the file")
}

func TestDownload_ARangeNotStartingAtZeroIsRefused(t *testing.T) {
	d := &downloadServer{body: []byte("0123456789"), contentRng: "bytes 5-14/15"}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	_, err := api.Download(context.Background(), "docs://poster.psd", &buf)
	require.Error(t, err)
	assert.Zero(t, buf.Len())
}

func TestDownload_AnEmptyObjectIsAnEmptyFile(t *testing.T) {
	d := &downloadServer{status416: true}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	n, err := api.DownloadSized(context.Background(), "docs://empty.txt", &buf, 0)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestDownload_A416ForANonEmptyFileIsAnError(t *testing.T) {
	d := &downloadServer{status416: true}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	_, err := api.DownloadSized(context.Background(), "docs://big.bin", &buf, 5)
	require.Error(t, err)
}

// The belt to the braces above: even a body the server calls the file is
// refused when its length is not the one the listing promised. On the
// deployment that found the bug, this single check would have saved all 45.
func TestDownloadSized_ADeclaredLengthMismatchWritesNothing(t *testing.T) {
	d := &downloadServer{body: []byte(preparingJSON), ignoreRng: true}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	_, err := api.DownloadSized(context.Background(), "docs://poster.psd", &buf, 151983227)
	require.Error(t, err)
	var sm *SizeMismatchError
	require.True(t, errors.As(err, &sm), "want a *SizeMismatchError, got %T: %v", err, err)
	assert.Equal(t, int64(151983227), sm.Want)
	assert.Equal(t, int64(len(preparingJSON)), sm.Got)
	assert.Zero(t, buf.Len(), "a declared length that disagrees with the listing must stop the download before the first byte")
}

func TestDownloadSized_AShortBodyIsAnError(t *testing.T) {
	// Chunked (no Content-Length), so the mismatch only shows once the
	// body ends. The caller's temp file is thrown away on this error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("abc"))
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	api := New(Conn{URL: srv.URL, Token: "tok"})

	var buf bytes.Buffer
	_, err := api.DownloadSized(context.Background(), "docs://f.bin", &buf, 10)
	require.Error(t, err)
	var sm *SizeMismatchError
	require.True(t, errors.As(err, &sm), "got %T: %v", err, err)
	assert.Equal(t, int64(3), sm.Got)
}

func TestDownloadSized_TheRightSizePasses(t *testing.T) {
	d := &downloadServer{body: []byte(strings.Repeat("x", 1234))}
	api := newDownloadClient(t, d)

	var buf bytes.Buffer
	n, err := api.DownloadSized(context.Background(), "docs://f.bin", &buf, 1234)
	require.NoError(t, err)
	assert.Equal(t, int64(1234), n)
}

func TestSizeMismatchErrorReadsLikeASentence(t *testing.T) {
	err := &SizeMismatchError{Remote: "docs://a.bin", Want: 10, Got: 3}
	assert.Equal(t, fmt.Sprintf("docs://a.bin: the server listed %d bytes but sent %d; nothing was written", 10, 3), err.Error())
}

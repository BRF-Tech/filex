package handlers_test

// A big file on a slow backend: say "preparing", then serve it at local-disk
// speed.
//
// The requirement, verbatim (translated from Turkish): "if the fs is slow and
// the file is big, we need to do something like telling the user we're building
// a cache just for them and starting the download once the cache is ready."
//
// These drive the REAL route (`/api/files/manager?action=download`) over a
// real local-FS storage flagged `slow: true`, and they assert bytes and status
// codes — not that a package was called. §5 trap 7 of the handover is about
// exactly the alternative: two tests passed green this month while the product
// was broken, because they asked the layer next to the one that mattered.
//
// This file deliberately uses only symbols that predate the change
// (api.Deps, api.BuildRouter, config.Load, the local driver, the store), so it
// compiles against the pre-change tree and can be run there as RED EVIDENCE:
// there, the first request answers 200 with the whole body and the second
// re-reads the whole object from the backend.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// cacheTestMinSize is what the fixture sets FILEX_CACHE_MIN_SIZE to. The
// production default is 64 MiB; a test that wrote 64 MiB per case to prove a
// threshold would be measuring the disk, so the threshold itself is moved and
// the file is sized around it.
const cacheTestMinSize = 1 << 20 // 1 MiB

// slowFixture is a full router whose one storage is flagged slow, with the
// cache threshold lowered so a 2 MiB file counts as "big".
type slowFixture struct {
	*stagedFixture
	reads *atomic.Int64 // whole-object reads that reached the driver
	dir   string        // the cache directory
}

func newSlowFixture(t *testing.T) *slowFixture {
	t.Helper()

	// Config comes from the environment through config.Load, which is what a
	// deployment uses — and, not incidentally, the only way to say
	// "FILEX_CACHE_MIN_SIZE" using symbols that exist before this change.
	dataDir := t.TempDir()
	t.Setenv("FILEX_DATA_DIR", dataDir)
	t.Setenv("FILEX_CACHE_MIN_SIZE", fmt.Sprint(cacheTestMinSize))
	loaded, err := config.Load("")
	require.NoError(t, err)

	reads := &atomic.Int64{}
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		loaded.PublicURL = d.Cfg.PublicURL
		loaded.CORS = d.Cfg.CORS
		d.Cfg = loaded

		inner := d.StorageResolver
		d.StorageResolver = func(id int64) (storage.Driver, error) {
			drv, err := inner(id)
			if err != nil {
				return nil, err
			}
			return &countingReadDriver{Driver: drv, reads: reads}, nil
		}
	})

	// Flag the storage slow, the way an operator does: a key in the storage's
	// own config.
	st := f.storage
	st.ConfigJSON = json.RawMessage(`{"root":"` + jsonEscape(f.rootDir) + `","slow":true}`)
	require.NoError(t, f.store.UpdateStorage(context.Background(), st))

	return &slowFixture{stagedFixture: f, reads: reads, dir: filepath.Join(dataDir, "cache")}
}

// countingReadDriver counts whole-object reads that actually reached the
// backend. It is the difference between "the cache exists" and "the cache is
// used": the second download of a prepared file must not touch the driver.
type countingReadDriver struct {
	storage.Driver
	reads *atomic.Int64
}

func (d *countingReadDriver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	d.reads.Add(1)
	return d.Driver.Read(ctx, p)
}

func (d *countingReadDriver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	rr, ok := d.Driver.(storage.RangeReader)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	d.reads.Add(1)
	return rr.ReadRange(ctx, p, off, length)
}

// seed writes a file straight onto the storage root and catalogues it, which
// is the state a synced backend is in.
func (f *slowFixture) seed(t *testing.T, name string, body []byte) *model.Node {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, name), body, 0o644))
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID,
		Name:      name,
		Path:      "/" + name,
		PathHash:  pathkey.Hash(f.storage.ID, name),
		Type:      model.NodeTypeFile,
		Size:      int64(len(body)),
		Mime:      "application/octet-stream",
	})
	require.NoError(t, err)
	return n
}

// download drives the real route. hdr lets a test be a browser or an XHR.
func (f *slowFixture) download(t *testing.T, name string, hdr map[string]string, query string) *http.Response {
	t.Helper()
	url := f.srv.URL + "/api/files/manager?action=download&path=" +
		"main%3A%2F%2F" + name
	if query != "" {
		url += "&" + query
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func bodyBytes(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return b
}

// waitReady polls the status endpoint the way the wait page does.
func (f *slowFixture) waitReady(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp := f.download(t, name, map[string]string{"Accept": "application/json"}, "cache=status")
		require.Equal(t, http.StatusOK, resp.StatusCode, "the status poll always answers 200 JSON")
		j := decodeJSON(t, resp)
		if ready, _ := j["ready"].(bool); ready {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the prepared copy never became ready")
}

// bigBody is a recognisable 2 MiB body: an off-by-N window is a visibly wrong
// slice rather than plausible-looking bytes.
func bigBody() []byte {
	b := make([]byte, 2<<20)
	for i := range b {
		b[i] = byte('a' + (i % 26))
	}
	return b
}

// ── the sequence the owner asked for ────────────────────────────────────────

// TestSlowBigDownload_202ThenReadyThenBytes is the whole feature in one test:
// the first request is told a copy is being prepared (with a percentage), the
// poll reports progress, and the download that follows returns the file —
// byte for byte.
func TestSlowBigDownload_202ThenReadyThenBytes(t *testing.T) {
	f := newSlowFixture(t)
	body := bigBody()
	f.seed(t, "big.bin", body)

	first := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusAccepted, first.StatusCode,
		"a big file on a slow storage must be announced, not dribbled")
	j := decodeJSON(t, first)
	require.Equal(t, "preparing", j["state"])
	require.Equal(t, false, j["ready"])
	pct, ok := j["percent"].(float64)
	require.True(t, ok, "the answer must carry a percentage: %v", j)
	require.GreaterOrEqual(t, pct, float64(0))
	require.LessOrEqual(t, pct, float64(99), "100 belongs to a file that is actually on disk")
	require.Equal(t, float64(len(body)), j["size"])
	require.Equal(t, "2", first.Header.Get("Retry-After"))

	f.waitReady(t, "big.bin")

	second := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusOK, second.StatusCode)
	require.Equal(t, body, bodyBytes(t, second), "the prepared copy must be the file, exactly")
}

// TestPreparedCopyIsServedWithoutTouchingTheBackend — the point of preparing.
// Once the copy exists, ten downloads cost zero backend reads.
func TestPreparedCopyIsServedWithoutTouchingTheBackend(t *testing.T) {
	f := newSlowFixture(t)
	body := bigBody()
	f.seed(t, "big.bin", body)

	f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "").Body.Close()
	f.waitReady(t, "big.bin")

	before := f.reads.Load()
	for i := 0; i < 10; i++ {
		resp := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, body, bodyBytes(t, resp))
	}
	require.Equal(t, before, f.reads.Load(),
		"a prepared file must be served from local disk, not re-read from the backend")
}

// TestRangedReadFromThePreparedCopyIsByteIdentical — seeking must produce the
// same bytes the driver would have. Windows at the start, the middle and the
// end, plus the suffix form a media player sends.
func TestRangedReadFromThePreparedCopyIsByteIdentical(t *testing.T) {
	f := newSlowFixture(t)
	body := bigBody()
	f.seed(t, "big.bin", body)

	f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "").Body.Close()
	f.waitReady(t, "big.bin")

	cases := []struct {
		hdr        string
		start, end int
	}{
		{"bytes=0-99", 0, 100},
		{"bytes=1000-1999", 1000, 2000},
		{fmt.Sprintf("bytes=%d-", len(body)-100), len(body) - 100, len(body)},
		{"bytes=-100", len(body) - 100, len(body)},
	}
	for _, tc := range cases {
		resp := f.download(t, "big.bin", map[string]string{"Range": tc.hdr}, "")
		require.Equal(t, http.StatusPartialContent, resp.StatusCode, "range %s", tc.hdr)
		require.Equal(t, body[tc.start:tc.end], bodyBytes(t, resp), "range %s", tc.hdr)
	}
}

// TestBrowserGetsAWaitPage — the explorer opens downloads with window.open, so
// the response IS the page the user is looking at. JSON would be a wall of
// text; a progress page is the answer, and it must carry the poll URL.
func TestBrowserGetsAWaitPage(t *testing.T) {
	f := newSlowFixture(t)
	f.seed(t, "big.bin", bigBody())

	resp := f.download(t, "big.bin", map[string]string{
		"Accept":          "text/html,application/xhtml+xml",
		"Accept-Language": "en",
	}, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	page := string(bodyBytes(t, resp))
	require.Contains(t, page, "Preparing your download")
	require.Contains(t, page, "cache=status", "the page must know where to poll")
	require.Contains(t, page, "big.bin")
}

// TestWaitPageSpeaksTurkishWhenAsked — every user-visible string on this
// project's surfaces exists in both languages, with the right characters.
func TestWaitPageSpeaksTurkishWhenAsked(t *testing.T) {
	f := newSlowFixture(t)
	f.seed(t, "big.bin", bigBody())

	resp := f.download(t, "big.bin", map[string]string{
		"Accept":          "text/html",
		"Accept-Language": "tr",
	}, "")
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Contains(t, string(bodyBytes(t, resp)), "İndirme hazırlanıyor")
}

// ── the cases that must NOT be made worse ───────────────────────────────────

// TestSmallFileOnSlowStorageIsServedImmediately — the size gate. A small file
// on the slowest NAS in the world is one round trip; "preparing… 0 %" would be
// a worse answer than the file.
func TestSmallFileOnSlowStorageIsServedImmediately(t *testing.T) {
	f := newSlowFixture(t)
	small := []byte("small enough to just send")
	f.seed(t, "small.txt", small)

	resp := f.download(t, "small.txt", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, small, bodyBytes(t, resp))
	require.Empty(t, cacheFiles(t, f.dir), "a small file must not occupy the cache at all")
}

// TestBigFileOnAStorageNobodyCalledSlowIsServedImmediately — the storage gate.
// Without the flag (and with nothing measured) the behaviour is exactly what
// it was before this feature existed.
func TestBigFileOnAStorageNobodyCalledSlowIsServedImmediately(t *testing.T) {
	f := newSlowFixture(t)
	st := f.storage
	st.ConfigJSON = json.RawMessage(`{"root":"` + jsonEscape(f.rootDir) + `"}`) // no slow flag
	require.NoError(t, f.store.UpdateStorage(context.Background(), st))

	body := bigBody()
	f.seed(t, "big.bin", body)

	resp := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, body, bodyBytes(t, resp))
	require.Empty(t, cacheFiles(t, f.dir))
}

// TestPreviewNeverWaits — a preview is somebody scrubbing a video or opening a
// PDF page. Making them wait for a whole prefetch before the first frame is
// worse than the ranged read they would otherwise have got.
func TestPreviewNeverWaits(t *testing.T) {
	f := newSlowFixture(t)
	body := bigBody()
	f.seed(t, "big.bin", body)

	url := f.srv.URL + "/api/files/manager?action=preview&path=main%3A%2F%2Fbig.bin"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode, "a preview must never be answered 202")
	require.Equal(t, body, bodyBytes(t, resp))
}

// TestRangeRequestIsNeverAnswered202 — a Range is a resume or a seek from a
// client already committed to a body. 202 is not an answer it can use.
func TestRangeRequestIsNeverAnswered202(t *testing.T) {
	f := newSlowFixture(t)
	body := bigBody()
	f.seed(t, "big.bin", body)

	resp := f.download(t, "big.bin", map[string]string{"Range": "bytes=0-99"}, "")
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	require.Equal(t, body[:100], bodyBytes(t, resp))
}

// TestStagedNodeIsNeverCached — a node whose bytes are still in filex's own
// staging area is a category error for this cache: the staging copy is already
// on local disk (so a "prepared local copy" buys nothing), and its identity
// changes when the transfer completes, so an entry keyed on the staged ETag
// would be orphaned the moment it became correct.
func TestStagedNodeIsNeverCached(t *testing.T) {
	f := newSlowFixture(t)
	n := f.seed(t, "big.bin", bigBody())

	require.NoError(t, f.store.SetNodeTransferState(context.Background(), n.ID, model.TransferStateStaged))

	resp := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	defer resp.Body.Close()
	require.NotEqual(t, http.StatusAccepted, resp.StatusCode,
		"a staged node must never be answered with a cache-preparing response")
	require.Empty(t, cacheFiles(t, f.dir), "and it must never produce a cache entry")
}

// TestChangedFileIsNotServedFromTheOldCopy — the invalidation rule, at the
// surface. The copy is keyed on the file's identity, so a file replaced on the
// backend gets a different key: the next download prepares the NEW content
// instead of handing out the old one forever.
//
// The local driver reports no ETag at all, so this is also the test that the
// size+mtime fallback works — and that is not a corner case, it is the driver
// an NFS/SMB mount uses.
func TestChangedFileIsNotServedFromTheOldCopy(t *testing.T) {
	f := newSlowFixture(t)
	first := bigBody()
	f.seed(t, "big.bin", first)

	f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "").Body.Close()
	f.waitReady(t, "big.bin")
	served := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, first, bodyBytes(t, served))

	// Replaced on the backend, out of band — a NAS is exactly this.
	second := append(bigBody(), []byte("and then some more")...)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootDir, "big.bin"), second, 0o644))

	again := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusAccepted, again.StatusCode,
		"the changed file must be prepared afresh, not answered from the old copy")
	again.Body.Close()
	f.waitReady(t, "big.bin")

	final := f.download(t, "big.bin", map[string]string{"Accept": "application/json"}, "")
	require.Equal(t, http.StatusOK, final.StatusCode)
	require.Equal(t, second, bodyBytes(t, final), "the new content, not the cached old one")
}

// cacheFiles lists the prepared copies on disk (ignoring temp files).
func cacheFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

var _ = bytes.Equal

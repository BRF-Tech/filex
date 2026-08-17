package handlers_test

/* Range downloads, measured in bytes served.

   Before this, `?action=download` answered every request with the whole
   object: no seeking in a video, no resume after a dropped connection, and
   a retry paid for the file twice. The old path also had no way to be
   wrong about a byte window — these tests exist because the new one does,
   so each case asserts the exact slice, not just the status code. */

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// rangeBody is 1000 bytes of a repeating pattern: an off-by-N window is
// visibly the wrong slice rather than plausible-looking bytes.
const rangeBody = 1000

func seedRangeFile(t *testing.T) (*handlers.Manager, db.Store, *local.Driver, *model.Storage, string, string) {
	t.Helper()
	mh, store, drv, st, dir := newMutateFixture(t)
	body := strings.Repeat("0123456789", rangeBody/10)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip.bin"), []byte(body), 0o644))
	return mh, store, drv, st, dir, body
}

// getRange drives the GET dispatcher with an optional Range header.
func getRange(t *testing.T, mh *handlers.Manager, path, rangeHdr string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"action": []string{"download"}, "path": []string{path}}
	req := httptest.NewRequest("GET", "/api/files/manager?"+q.Encode(), nil)
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	rec := httptest.NewRecorder()
	mh.List(rec, req)
	return rec
}

// ---------- the four range shapes a client actually sends ----------

// TestDownload_ClosedRange — what a video player asks for when you drag the
// scrubber: an exact window, answered with 206 and exactly those bytes.
func TestDownload_ClosedRange(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=100-199")

	require.Equal(t, 206, rec.Code)
	require.Equal(t, body[100:200], rec.Body.String())
	require.Len(t, rec.Body.String(), 100)
	require.Equal(t, "bytes 100-199/1000", rec.Header().Get("Content-Range"))
	require.Equal(t, "100", rec.Header().Get("Content-Length"))
	require.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
}

// TestDownload_OpenEndedRange — what a resumed download sends: "carry on
// from where the connection died".
func TestDownload_OpenEndedRange(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=900-")

	require.Equal(t, 206, rec.Code)
	require.Equal(t, body[900:], rec.Body.String())
	require.Equal(t, "bytes 900-999/1000", rec.Header().Get("Content-Range"))
}

// TestDownload_SuffixRange — "the last 100 bytes", which media probes use
// to find a trailing index (MP4 moov, ZIP central directory).
func TestDownload_SuffixRange(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=-100")

	require.Equal(t, 206, rec.Code)
	require.Equal(t, body[900:], rec.Body.String())
	require.Equal(t, "bytes 900-999/1000", rec.Header().Get("Content-Range"))
}

// TestDownload_UnsatisfiableRange — a window entirely past the end is 416
// with the real size, not a 200 full body and not a truncated 206.
func TestDownload_UnsatisfiableRange(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=5000-6000")

	require.Equal(t, 416, rec.Code)
	require.Equal(t, "bytes */1000", rec.Header().Get("Content-Range"))
	require.NotContains(t, rec.Body.String(), body[:20])
}

// TestDownload_MultiRange — two windows in one request (PDF viewers do
// this). ServeContent seeks per part, which is the case a seeker that
// cheated on backward seeks would get wrong.
func TestDownload_MultiRange(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=0-9,100-109")

	require.Equal(t, 206, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "multipart/byteranges")
	require.Contains(t, rec.Body.String(), "bytes 0-9/1000")
	require.Contains(t, rec.Body.String(), "bytes 100-109/1000")
	require.Contains(t, rec.Body.String(), body[:10])
}

// ---------- and the request that carries no Range at all ----------

// TestDownload_NoRangeIsUnchanged — the plain download must behave exactly
// as it did before ranges existed: 200, whole body, attachment filename,
// cache header. Plus Accept-Ranges, so a client knows it may resume.
func TestDownload_NoRangeIsUnchanged(t *testing.T) {
	mh, _, _, _, _, body := seedRangeFile(t)

	rec := getRange(t, mh, "main://clip.bin", "")

	require.Equal(t, 200, rec.Code)
	require.Equal(t, body, rec.Body.String())
	require.Equal(t, "1000", rec.Header().Get("Content-Length"))
	require.Equal(t, `attachment; filename="clip.bin"`, rec.Header().Get("Content-Disposition"))
	require.Equal(t, "private, max-age=60", rec.Header().Get("Cache-Control"))
	require.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
}

// TestPreview_KeepsInlineHeaders — preview is the same code path with a
// different disposition; the MIME override and nosniff must survive.
func TestPreview_KeepsInlineHeaders(t *testing.T) {
	mh, _, _, _, dir, _ := seedRangeFile(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("# hi"), 0o644))

	q := url.Values{"action": []string{"preview"}, "path": []string{"main://note.md"}}
	req := httptest.NewRequest("GET", "/api/files/manager?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	mh.List(rec, req)

	require.Equal(t, 200, rec.Code)
	require.Equal(t, "# hi", rec.Body.String())
	require.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Empty(t, rec.Header().Get("Content-Disposition"))
}

// ---------- the backend must not pay for the seek ----------

// countingDriver records how the handler actually talks to the backend:
// whole-object Reads vs ranged reads and at which offsets.
type countingDriver struct {
	*local.Driver
	reads int
	offs  []int64
}

func (d *countingDriver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	d.reads++
	return d.Driver.Read(ctx, p)
}

func (d *countingDriver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	d.offs = append(d.offs, off)
	return d.Driver.ReadRange(ctx, p, off, length)
}

func newCountingFixture(t *testing.T) (*handlers.Manager, *countingDriver, string) {
	t.Helper()
	_, store, drv, st, dir := newMutateFixture(t)
	body := strings.Repeat("0123456789", rangeBody/10)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip.bin"), []byte(body), 0o644))

	cd := &countingDriver{Driver: drv}
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return cd, nil
	}
	return handlers.NewManager(store, resolver), cd, body
}

// TestDownload_RangeNeverRefetchesFromZero is the performance claim as an
// assertion. http.ServeContent seeks to the end to size the body before it
// serves anything; a seeker that answered by reading would pull the whole
// object on every single request — slower than the io.Copy it replaced.
// So: one ranged open, at the requested offset, and no whole-object Read.
func TestDownload_RangeNeverRefetchesFromZero(t *testing.T) {
	mh, cd, body := newCountingFixture(t)

	rec := getRange(t, mh, "main://clip.bin", "bytes=600-699")

	require.Equal(t, 206, rec.Code)
	require.Equal(t, body[600:700], rec.Body.String())
	require.Equal(t, []int64{600}, cd.offs, "the backend must be opened once, at the offset asked for")
	require.Zero(t, cd.reads, "a range request must never fall back to a whole-object read")
}

// TestDownload_NoRangeOpensExactlyOneStream — the plain download keeps its
// single open (and therefore its clean 500 on a storage error), rather than
// opening one for the size probe and another for the body.
func TestDownload_NoRangeOpensExactlyOneStream(t *testing.T) {
	mh, cd, body := newCountingFixture(t)

	rec := getRange(t, mh, "main://clip.bin", "")

	require.Equal(t, 200, rec.Code)
	require.Equal(t, body, rec.Body.String())
	require.Equal(t, 1, cd.reads)
	require.Empty(t, cd.offs)
}

// TestDownload_EmptyFile — the size-0 guard. http.ServeContent trusts the
// seeker's size, so a zero keeps the whole-object path rather than risk
// answering "empty" for a driver whose Stat does not fill Size in.
func TestDownload_EmptyFile(t *testing.T) {
	mh, _, _, _, dir, _ := seedRangeFile(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.bin"), nil, 0o644))

	rec := getRange(t, mh, "main://empty.bin", "")

	require.Equal(t, 200, rec.Code)
	require.Empty(t, rec.Body.String())
}

// ---------- drivers that cannot range ----------

// unrangedDriver hides ReadRange: embedding the storage.Driver interface
// exposes only the base methods, which is exactly the shape of a backend
// that cannot start a transfer at an offset.
type unrangedDriver struct {
	storage.Driver
}

// TestDownload_UnrangedDriverFallsBackToWholeBody — no faking: a driver
// that cannot seek answers the whole object with 200 (what it always did)
// and says so, instead of promising a resume it cannot honour.
func TestDownload_UnrangedDriverFallsBackToWholeBody(t *testing.T) {
	_, store, drv, st, dir := newMutateFixture(t)
	body := strings.Repeat("0123456789", rangeBody/10)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip.bin"), []byte(body), 0o644))

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return unrangedDriver{Driver: drv}, nil
	}
	mh := handlers.NewManager(store, resolver)

	rec := getRange(t, mh, "main://clip.bin", "bytes=100-199")

	require.Equal(t, 200, rec.Code, "an unranged driver must answer the whole body, not a wrong window")
	require.Equal(t, body, rec.Body.String())
	require.Equal(t, "none", rec.Header().Get("Accept-Ranges"))
}

// ---------- the share cap must not learn to leak ----------

// shareGetRange is limitGet with a Range header — the request a media
// player or a resuming downloader sends at a public link.
func shareGetRange(h *handlers.Share, token, rangeHdr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)
	return rec
}

// TestShareDownload_RangeStillCostsExactlyOne pins the decision taken with
// this change: public share links (/s/, /s/{token}/f/, /d/) do NOT go
// through the ranged path. They reserve a slot off the link's cap before a
// byte leaves (v0.18.0, the day a "3 downloads" link served four), and one
// HTTP request is one download. Serving ranges there would make a player's
// twenty seeks cost twenty downloads, and a rule like "only count offset 0"
// would let `Range: bytes=1-` fetch the file forever for free.
//
// So: a Range header at a share link changes nothing — whole body, 200,
// one download spent.
func TestShareDownload_RangeStillCostsExactlyOne(t *testing.T) {
	h, store, sh := newLimitFixture(t, 1)

	rec := shareGetRange(h, sh.Token, "bytes=0-4")

	require.Equal(t, 200, rec.Code, "the share path answers 200, not 206")
	require.Equal(t, "capped payload\n", rec.Body.String(), "a share link serves the whole file")
	require.Empty(t, rec.Header().Get("Content-Range"))

	cur, err := store.GetShareByID(context.Background(), sh.ID)
	require.NoError(t, err)
	require.Equal(t, 1, cur.DownloadCount)

	second := shareGetRange(h, sh.Token, "bytes=5-9")
	require.NotEqual(t, 200, second.Code, "the cap must still be spent after a ranged request")
}

// TestShareDownload_RangeCannotOutrunTheCap — the same rule at the size
// where a leak would be worth exploiting: five ranged requests against a
// three-download link still hand out three files.
func TestShareDownload_RangeCannotOutrunTheCap(t *testing.T) {
	h, store, sh := newLimitFixture(t, 3)

	served := 0
	for i := 0; i < 5; i++ {
		rec := shareGetRange(h, sh.Token, fmt.Sprintf("bytes=%d-", i))
		if rec.Code == 200 && strings.Contains(rec.Body.String(), "capped payload") {
			served++
		}
	}
	require.Equal(t, 3, served, "a 3-download link served %d ranged requests", served)

	cur, err := store.GetShareByID(context.Background(), sh.ID)
	require.NoError(t, err)
	require.Equal(t, 3, cur.DownloadCount)
}

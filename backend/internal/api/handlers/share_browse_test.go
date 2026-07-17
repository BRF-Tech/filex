package handlers_test

/* wiring:d2 — folder-share browse page + /s/{token}/f/* sub-file endpoint:
   layout auto-selection (≥60% visual → gallery), subdir navigation,
   containment, PIN enforcement, thumb rules and download counting. */

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// tiny 1x1 GIF so image files carry real bytes.
const browseTestGif = "GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"

func newBrowseFixture(t *testing.T, files map[string]string) (*handlers.Share, db.Store, *model.Share) {
	t.Helper()
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "album"))
	dirs := map[string]bool{}
	for p, content := range files {
		full := "album/" + p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			d := "album/" + p[:i]
			if !dirs[d] {
				require.NoError(t, drv.Mkdir(ctx, d))
				dirs[d] = true
			}
		}
		require.NoError(t, drv.Write(ctx, full, strings.NewReader(content), int64(len(content))))
	}

	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "album", Path: "album",
		PathHash: mutTestPathHash(st.ID, "album"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)

	return handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir())), store, sh
}

func browseGet(h *handlers.Share, token, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/s/"+token+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)
	return rec
}

func browseGetFile(h *handlers.Share, token, rel, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/s/"+token+"/f/"+rel+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	rctx.URLParams.Add("*", rel)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleBrowseFile(rec, req)
	return rec
}

// A visual-heavy folder (3 of 4 files image/video = 75%) renders the
// gallery grid; entries carry /f/ links and image tiles inline thumbs.
func TestShareBrowse_GalleryLayout(t *testing.T) {
	h, _, sh := newBrowseFixture(t, map[string]string{
		"a.jpg":     browseTestGif,
		"b.png":     browseTestGif,
		"clip.mp4":  "notavideo",
		"notes.txt": "hi",
	})

	rec := browseGet(h, sh.Token, "")
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, body, `class="ggrid"`, "≥60%% visual folder must use the gallery layout")
	require.Contains(t, body, "/s/"+sh.Token+"/f/a.jpg")
	require.Contains(t, body, "thumb=1")
	require.Contains(t, body, `class="gbadge"`, "video tile carries a play badge")
	require.Contains(t, body, "filex", "public footer must survive")
	require.Contains(t, body, "?zip=1", "download-all button must point at the zip flow")
}

// A document-heavy folder renders the plain list layout.
func TestShareBrowse_ListLayout(t *testing.T) {
	h, _, sh := newBrowseFixture(t, map[string]string{
		"a.pdf": "x", "b.txt": "y", "c.doc": "z", "d.jpg": browseTestGif,
	})

	rec := browseGet(h, sh.Token, "")
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `class="flist"`)
	require.NotContains(t, body, `class="ggrid"`)
	require.Contains(t, body, "a.pdf")
}

// ?dir= navigates one level down (containment-checked) and traversal is
// rejected with a 404.
func TestShareBrowse_SubdirAndTraversal(t *testing.T) {
	h, _, sh := newBrowseFixture(t, map[string]string{
		"sub/inner.txt": "deep", "top.txt": "up",
	})

	rec := browseGet(h, sh.Token, "?dir=sub")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "inner.txt")
	require.Contains(t, rec.Body.String(), "sub", "subpath breadcrumb visible")

	rec = browseGet(h, sh.Token, "?dir=../outside")
	require.Equal(t, 404, rec.Code)
}

// /f/ streams a contained file inline-or-attachment, counts a download,
// and refuses traversal + unknown paths.
func TestShareBrowse_FileEndpoint(t *testing.T) {
	h, store, sh := newBrowseFixture(t, map[string]string{
		"a.jpg": browseTestGif, "sub/n.txt": "nested",
	})
	ctx := context.Background()

	rec := browseGetFile(h, sh.Token, "a.jpg", "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "image/jpeg")
	require.Contains(t, rec.Header().Get("Content-Disposition"), "inline")
	require.Equal(t, browseTestGif, rec.Body.String())

	got, err := store.GetShareByToken(ctx, sh.Token)
	require.NoError(t, err)
	require.Equal(t, 1, got.DownloadCount, "full file open counts one download")

	rec = browseGetFile(h, sh.Token, "sub/n.txt", "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")

	rec = browseGetFile(h, sh.Token, "../../etc/passwd", "")
	require.Equal(t, 404, rec.Code)
	rec = browseGetFile(h, sh.Token, "missing.bin", "")
	require.Equal(t, 404, rec.Code)
}

// ?thumb=1 streams images inline WITHOUT counting a download and 404s for
// non-image files (video tiles show a badge, not a poster).
func TestShareBrowse_ThumbRules(t *testing.T) {
	h, store, sh := newBrowseFixture(t, map[string]string{
		"a.jpg": browseTestGif, "clip.mp4": "notavideo",
	})
	ctx := context.Background()

	rec := browseGetFile(h, sh.Token, "a.jpg", "?thumb=1")
	require.Equal(t, 200, rec.Code)
	got, err := store.GetShareByToken(ctx, sh.Token)
	require.NoError(t, err)
	require.Equal(t, 0, got.DownloadCount, "thumb fetches must not count as downloads")

	rec = browseGetFile(h, sh.Token, "clip.mp4", "?thumb=1")
	require.Equal(t, 404, rec.Code)
}

// A PIN-protected share gates both the browse page (PIN form) and the /f/
// endpoint (401 without the pin, 200 with it).
func TestShareBrowse_PinEnforced(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "sec"))
	require.NoError(t, drv.Write(ctx, "sec/a.jpg", strings.NewReader(browseTestGif), int64(len(browseTestGif))))
	node, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "sec", Path: "sec",
		PathHash: mutTestPathHash(st.ID, "sec"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: node.ID, PIN: "1234"})
	require.NoError(t, err)
	h := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir()))

	// Browse without PIN → the PIN form, not the listing.
	rec := browseGet(h, sh.Token, "")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "PIN")
	require.NotContains(t, rec.Body.String(), "a.jpg")

	// Browse with PIN → listing, and links keep the pin.
	rec = browseGet(h, sh.Token, "?pin=1234")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "a.jpg")
	require.Contains(t, rec.Body.String(), "pin=1234")

	// /f/ without → 401; with → 200.
	rec = browseGetFile(h, sh.Token, "a.jpg", "")
	require.Equal(t, 401, rec.Code)
	rec = browseGetFile(h, sh.Token, "a.jpg", "?pin=1234")
	require.Equal(t, 200, rec.Code)
}

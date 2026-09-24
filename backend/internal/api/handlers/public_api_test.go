package handlers_test

// The one public surface: /api/public/*, the no-JS pages behind it, and the
// retired /p/ prefix. These are the endpoints a stranger reaches with nothing
// but a token, so most of what is asserted here is what they must NOT get.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// newPublicFixture wires the public JSON API, the no-JS share/drop pages and
// the retired-prefix redirects over one store + local driver.
func newPublicFixture(t *testing.T) (*chi.Mux, *share.Service, db.Store, *model.Storage, string) {
	t.Helper()
	mh, store, drv, st, root := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	svc := share.NewService(store)
	svc.AttachSecret("0123456789abcdef0123456789abcdef")
	shareH := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	dh := handlers.NewDrop(store, mh, svc, nil, nil, "")
	pub := handlers.NewPublicAPI(store, svc)
	pub.AttachDrop(dh)
	pub.AttachBranding(handlers.NewBrandingSource(store, false))

	r := chi.NewRouter()
	r.Get("/api/public/branding", pub.Branding)
	r.Route("/api/public/s/{token}", func(r chi.Router) {
		r.Get("/", pub.Share)
		r.Post("/pin", pub.PIN)
		r.Post("/event", pub.Event)
		r.Get("/file/{ref}", pub.File)
	})
	r.Route("/api/public/d/{token}", func(r chi.Router) {
		r.Get("/", pub.DropState)
		r.Post("/pin", pub.PIN)
		r.Post("/upload", pub.DropUpload)
	})
	r.Handle("/p/{token}", handlers.RetiredPagePrefix())
	r.Handle("/p/{token}/*", handlers.RetiredPagePrefix())
	r.Handle("/api/p/{token}", handlers.RetiredPageAPI())
	r.Handle("/api/p/{token}/*", handlers.RetiredPageAPI())
	r.Get("/s/{token}", shareH.HandleDownload)
	r.Post("/s/{token}", shareH.HandleDownload)
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)
	return r, svc, store, st, root
}

// fileNode writes bytes and catalogues them, so a share can point at a node.
func fileNode(t *testing.T, store db.Store, st *model.Storage, root, rel, content string) *model.Node {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	clean := "/" + strings.Trim(rel, "/")
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: filepath.Base(rel), Path: clean,
		PathHash: mutTestPathHash(st.ID, clean), Type: model.NodeTypeFile,
		Size: int64(len(content)), Mime: "text/plain",
	})
	require.NoError(t, err)
	return n
}

func publicPostJSON(t *testing.T, r http.Handler, url, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func publicDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// TestPublicShare_StateAndHeaders: what an unauthenticated caller learns about
// a live link, and what it must never learn.
func TestPublicShare_StateAndHeaders(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "invoices/nisan.pdf", "the bytes")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)

	rec := doGet(t, r, "/api/public/s/"+sh.Token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Header().Get("X-Robots-Tag"), "noindex")

	body := publicDecode(t, rec)
	assert.Equal(t, "file", body["kind"])
	assert.Equal(t, false, body["needs_pin"])
	assert.Equal(t, true, body["unlocked"])
	assert.Equal(t, false, body["expired"])
	assert.Equal(t, false, body["revoked"])
	assert.Nil(t, body["visits_left"], "no ceiling → null, not 0")
	node2 := body["node"].(map[string]any)
	assert.Equal(t, "nisan.pdf", node2["name"])

	// ⚠ Nothing about WHERE the file lives or WHOSE it is.
	raw := rec.Body.String()
	assert.NotContains(t, raw, "invoices/")
	assert.NotContains(t, raw, st.Name)
	assert.NotContains(t, raw, "created_by")
	assert.NotContains(t, raw, "pin_hash")
}

// TestPublicShare_UnknownTokenIsIndistinguishable: a token that never existed
// and one of the wrong kind answer the same 404 — an anonymous caller must
// not be able to separate "no such link" from "somebody else's link".
func TestPublicShare_UnknownTokenIsIndistinguishable(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	drop, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)

	unknown := doGet(t, r, "/api/public/s/"+strings.Repeat("0", 32))
	wrongKind := doGet(t, r, "/api/public/s/"+drop.Token)
	assert.Equal(t, http.StatusNotFound, unknown.Code)
	assert.Equal(t, http.StatusNotFound, wrongKind.Code)
	assert.JSONEq(t, unknown.Body.String(), wrongKind.Body.String())
}

// TestPublicShare_PINLockAfterFiveTries: the five-strikes-then-ten-minutes
// lock, which before v3 existed only on app-plugin pages — a /s/ PIN was
// walkable at the speed of HTTP.
func TestPublicShare_PINLockAfterFiveTries(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "secret.txt", "s")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PIN: "4242"})
	require.NoError(t, err)

	state := publicDecode(t, doGet(t, r, "/api/public/s/"+sh.Token))
	assert.Equal(t, true, state["needs_pin"])
	assert.Equal(t, false, state["unlocked"])
	assert.Nil(t, state["node"], "the file's name is behind the PIN")

	for i := 0; i < 4; i++ {
		rec := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/pin", `{"pin":"0000"}`)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "try %d", i)
		assert.Contains(t, rec.Body.String(), "pin_wrong")
	}
	locked := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/pin", `{"pin":"0000"}`)
	assert.Equal(t, http.StatusTooManyRequests, locked.Code)

	// ⚠ The RIGHT PIN is refused while the lock holds. A lock the correct
	// answer lifts is no lock at all against somebody walking the space.
	still := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/pin", `{"pin":"4242"}`)
	assert.Equal(t, http.StatusTooManyRequests, still.Code)

	// The strikes are on the SHARE ROW, so they survive this process.
	row, err := store.GetShareByToken(context.Background(), sh.Token)
	require.NoError(t, err)
	assert.True(t, row.PinLocked(time.Now()))
}

// TestPublicShare_PINUnlocksWithCookie: answering the PIN mints an HttpOnly
// cookie that carries no PIN and opens only this link.
func TestPublicShare_PINUnlocksWithCookie(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "secret.txt", "s")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PIN: "4242"})
	require.NoError(t, err)
	other, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PIN: "4242"})
	require.NoError(t, err)

	rec := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/pin", `{"pin":"4242"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, true, publicDecode(t, rec)["unlocked"])
	require.Len(t, rec.Result().Cookies(), 1)
	c := rec.Result().Cookies()[0]
	assert.True(t, c.HttpOnly)
	assert.NotContains(t, c.Value, "4242")

	req := httptest.NewRequest("GET", "/api/public/s/"+sh.Token, nil)
	req.AddCookie(c)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req)
	assert.Equal(t, true, publicDecode(t, rec2)["unlocked"])

	// ⚠ …and only this link: the cookie is bound to one token.
	req3 := httptest.NewRequest("GET", "/api/public/s/"+other.Token, nil)
	req3.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	rec3 := httptest.NewRecorder()
	r.ServeHTTP(rec3, req3)
	assert.Equal(t, false, publicDecode(t, rec3)["unlocked"], "one link's unlock opened another")
}

// TestPublicShare_ExpiredAndRevoked: a lapsed link answers 410 in the SAME
// shape a live one does, so a client renders "this link is gone" from the
// object it renders everything else from.
func TestPublicShare_ExpiredAndRevoked(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "old.txt", "x")

	past := time.Now().Add(-time.Hour)
	lapsed, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, ExpiresAt: &past})
	require.NoError(t, err)
	rec := doGet(t, r, "/api/public/s/"+lapsed.Token)
	require.Equal(t, http.StatusGone, rec.Code)
	body := publicDecode(t, rec)
	assert.Equal(t, true, body["expired"])
	assert.Equal(t, "file", body["kind"])

	// A spent ceiling is the other way a link dies, and it is reported as
	// `revoked` rather than `expired`: nothing ran out of time.
	one := 1
	spent, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, MaxDownloads: &one})
	require.NoError(t, err)
	require.NoError(t, svc.IncrementDownload(context.Background(), spent.ID))
	rec = doGet(t, r, "/api/public/s/"+spent.Token)
	require.Equal(t, http.StatusGone, rec.Code)
	body = publicDecode(t, rec)
	assert.Equal(t, true, body["revoked"])
	assert.Equal(t, float64(0), body["visits_left"])

	// The administrator's revoke moves the expiry to now.
	live, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)
	require.NoError(t, store.RevokeShare(context.Background(), live.ID))
	assert.Equal(t, http.StatusGone, doGet(t, r, "/api/public/s/"+live.Token).Code)
}

// TestPublicDrop_StateAndUpload: the file request's JSON half — the limits the
// screen needs, and never a listing of what is already in the folder.
func TestPublicDrop_StateAndUpload(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	require.NoError(t, os.WriteFile(filepath.Join(root, "inbox", "already-here.txt"), []byte("old"), 0o644))
	ds := `{"max_files":3,"max_file_size_mb":5,"allowed_ext":["txt"],"ask_name":true}`
	two := 2
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds, MaxUploads: &two})
	require.NoError(t, err)

	rec := doGet(t, r, "/api/public/d/"+sh.Token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := publicDecode(t, rec)
	assert.Equal(t, "drop", body["kind"])
	assert.Equal(t, "inbox", body["folder"])
	assert.Equal(t, float64(2), body["uploads_left"])
	limits := body["limits"].(map[string]any)
	assert.Equal(t, float64(3), limits["max_files"])
	assert.Equal(t, true, limits["ask_name"])
	// ⚠ Blind drop: nothing about the folder's contents.
	assert.NotContains(t, rec.Body.String(), "already-here")

	up := doDropUpload(t, r, "/api/public/d/"+sh.Token+"/upload", "", nil, []fpart{{"yeni.txt", "hello"}})
	require.Equal(t, http.StatusOK, up.Code, up.Body.String())
	assert.NotEmpty(t, findUnder(root, "inbox", "yeni.txt"))
}

// TestPublicDrop_PINGate: a file request's PIN is the same counted gate, and
// the cookie it mints is accepted by the upload endpoint.
func TestPublicDrop_PINGate(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, PIN: "4242"})
	require.NoError(t, err)

	refused := doDropUpload(t, r, "/api/public/d/"+sh.Token+"/upload", "", nil, []fpart{{"a.txt", "a"}})
	assert.Equal(t, http.StatusUnauthorized, refused.Code)

	rec := publicPostJSON(t, r, "/api/public/d/"+sh.Token+"/pin", `{"pin":"4242"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, rec.Result().Cookies(), 1)

	up := doDropUploadWithCookie(t, r, "/api/public/d/"+sh.Token+"/upload", rec.Result().Cookies()[0], []fpart{{"a.txt", "a"}})
	assert.Equal(t, http.StatusOK, up.Code, up.Body.String())
}

// TestPublicBranding_IsPublicAndWithholdsOperatorFields.
func TestPublicBranding_IsPublicAndWithholdsOperatorFields(t *testing.T) {
	r, _, store, _, _ := newPublicFixture(t)
	require.NoError(t, store.UpsertSetting(context.Background(), "branding.name", "Acme Files"))
	require.NoError(t, store.UpsertSetting(context.Background(), "branding.accent", "#ff0000"))
	require.NoError(t, store.UpsertSetting(context.Background(), "branding.theme", "dark"))
	require.NoError(t, store.UpsertSetting(context.Background(), "ui.custom_css", "body{display:none}"))
	require.NoError(t, store.UpsertSetting(context.Background(), "branding.sso_label", "Staff sign-in"))

	rec := doGet(t, r, "/api/public/branding")
	require.Equal(t, http.StatusOK, rec.Code)
	// ⚠ Revalidated, not held: the offered languages ride this answer, and a
	// pack installed a moment ago has to be on the next page load
	// (handlers/public_cache.go, public_cache_test.go).
	assert.Contains(t, rec.Header().Get("Cache-Control"), "no-cache")
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	body := publicDecode(t, rec)
	assert.Equal(t, "Acme Files", body["name"])
	assert.Equal(t, "#ff0000", body["accent"])
	assert.Equal(t, "dark", body["theme"])
	assert.Contains(t, body["locales"], "tr")
	// ⚠ The SSO button is not a stranger's business, and neither is the
	// operator's stylesheet.
	//
	// ⚠ `custom_css` is asserted absent here for a DIFFERENT reason than it
	// used to be. It is no longer "it rides /api/branding and must not ride
	// this one": since v0.43.0 it rides neither, and /api/branding is a PUBLIC
	// endpoint, not a signed-in one — the old wording got that wrong too. The
	// assertion stays because a payload built for strangers must never grow it
	// back.
	raw := rec.Body.String()
	assert.NotContains(t, raw, "custom_css")
	assert.NotContains(t, raw, "Staff sign-in")
}

// TestRetiredPagePrefix_Redirects: /p/<token> and /api/p/<token>/… are gone;
// a link already in somebody's inbox lands somewhere that can explain itself.
func TestRetiredPagePrefix_Redirects(t *testing.T) {
	r, _, _, _, _ := newPublicFixture(t)
	for _, tc := range []struct{ from, to string }{
		{"/p/abc123", "/s/abc123"},
		{"/p/abc123/anything", "/s/abc123"},
		{"/api/p/abc123", "/api/public/s/abc123"},
		{"/api/p/abc123/view", "/api/public/s/abc123"},
		{"/api/p/abc123/pin", "/api/public/s/abc123/pin"},
		{"/api/p/abc123/event", "/api/public/s/abc123/event"},
	} {
		rec := doGet(t, r, tc.from)
		assert.Equal(t, http.StatusMovedPermanently, rec.Code, tc.from)
		assert.Equal(t, tc.to, rec.Header().Get("Location"), tc.from)
	}
}

// TestNoJSFallback_StaysForNonBrowsers: the shell is only for a browser
// asking for a document. curl, which sends */* or nothing, must keep getting
// the bytes — otherwise every script that ever automated a share link breaks.
func TestNoJSFallback_StaysForNonBrowsers(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "report.txt", "the bytes")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)

	for _, accept := range []string{"", "*/*", "application/json"} {
		req := httptest.NewRequest("GET", "/s/"+sh.Token, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, accept)
		assert.Equal(t, "the bytes", rec.Body.String(), "Accept %q got a page instead of the file", accept)
	}
}

// TestNoJSFallback_PINFormAndLock: the JavaScript-less path keeps its PIN
// form, and it is behind the same counted gate as the JSON one.
func TestNoJSFallback_PINFormAndLock(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "report.txt", "the bytes")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID, PIN: "4242"})
	require.NoError(t, err)

	form := doGet(t, r, "/s/"+sh.Token)
	require.Equal(t, http.StatusOK, form.Code)
	assert.Contains(t, form.Body.String(), `name="pin"`)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/s/"+sh.Token+"?pin=0000", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	row, err := store.GetShareByToken(context.Background(), sh.Token)
	require.NoError(t, err)
	assert.True(t, row.PinLocked(time.Now()), "the no-JS path counts its strikes too")
}

// doDropUploadWithCookie is doDropUpload carrying an unlock cookie instead of
// a PIN field — how the SPA uploads once the visitor has answered the gate.
func doDropUploadWithCookie(t *testing.T, r http.Handler, url string, c *http.Cookie, files []fpart) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		part, err := mw.CreateFormFile("file[]", f.name)
		require.NoError(t, err)
		_, _ = io.WriteString(part, f.content)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestPublicApp_SurfaceGateAndKind: the app half of the public surface as the
// HTTP layer sees it. The surface itself is a wasm call, exercised end to end
// in internal/wasmplugin; what has to hold HERE is the gate around it.
func TestPublicApp_SurfaceGateAndKind(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "sozlesme.pdf", "the terms")

	plugin, err := store.CreateAppPlugin(context.Background(), &model.AppPlugin{
		Name: "sign", Version: "1.0.0", LabelJSON: `{"en":"Sign"}`, ManifestJSON: `{}`,
		WasmPath: "x", SHA256: "y", Source: model.AppPluginSourceUpload, PermissionsJSON: `[]`, Enabled: true,
	})
	require.NoError(t, err)
	sh, err := svc.Create(context.Background(), share.CreateOpts{
		NodeID: node.ID, PIN: "4242", PluginID: plugin.ID, PageID: "signer",
		Subject: "Please sign sozlesme.pdf", FilesJSON: `[{"ref":"pub:0","name":"sozlesme.pdf","file":"0-sozlesme.pdf","size":9}]`,
	})
	require.NoError(t, err)

	// Behind the PIN, the shell learns it is an app link and its subject —
	// and NOT the document: an app link's node is the anchor its state and
	// its follow-up job hang on, never a download.
	body := publicDecode(t, doGet(t, r, "/api/public/s/"+sh.Token))
	assert.Equal(t, "app", body["kind"])
	assert.Equal(t, "Please sign sozlesme.pdf", body["subject"])
	assert.Nil(t, body["node"], "an app link never hands over the storage file")

	// The surface is behind the same PIN gate as everything else.
	locked := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/event", `{"event":"open"}`)
	assert.Equal(t, http.StatusUnauthorized, locked.Code)
	assert.Contains(t, locked.Body.String(), "pin_required")

	// A link whose app is not running is gone — for the surface and for the
	// state — rather than half-answering.
	unlock := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/pin", `{"pin":"4242"}`)
	require.Equal(t, http.StatusOK, unlock.Code)
	gone := publicPostJSON(t, r, "/api/public/s/"+sh.Token+"/event", `{"event":"open"}`, unlock.Result().Cookies()[0])
	assert.Equal(t, http.StatusNotFound, gone.Code, "no registry wired: the app surface is unavailable, not a 500")

	// An ORDINARY share is not an app surface, and says so as "not found" —
	// which of the two it was is a fact about somebody else's data.
	plain, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, publicPostJSON(t, r, "/api/public/s/"+plain.Token+"/event", `{"event":"open"}`).Code)
}

// TestNoJSFallback_AppLinkListsExposedCopies: an app surface cannot run
// without JavaScript, so the no-JS body does the one useful thing it can —
// hands over the copies the app exposed, behind the same PIN.
func TestNoJSFallback_AppLinkListsExposedCopies(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "sozlesme.pdf", "the terms")
	sh, err := svc.Create(context.Background(), share.CreateOpts{
		NodeID: node.ID, PluginID: 7, PageID: "signer", Subject: "Please sign sozlesme.pdf",
		FilesJSON: `[{"ref":"pub:0","name":"sozlesme.pdf","file":"0-sozlesme.pdf","size":9}]`,
	})
	require.NoError(t, err)

	rec := doGet(t, r, "/s/"+sh.Token)
	require.Equal(t, http.StatusOK, rec.Code)
	page := rec.Body.String()
	assert.Contains(t, page, "Please sign sozlesme.pdf")
	assert.Contains(t, page, "/api/public/s/"+sh.Token+"/file/pub:0")
	// ⚠ And it is NOT the file itself: an app link's node is not a download.
	assert.NotEqual(t, "the terms", page)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

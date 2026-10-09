package handlers_test

// An upgrade, and a request an administrator approves, ask the File types
// question too (0.50, the maintainer 2026-10-01): an upgrade only about the kinds it
// ADDS - the order the administrator already has for the others is not
// reopened - and an approval the same rows the install wizard shows.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sketchManifest is the interface-only app of app_ui_test.go at a version,
// opening exts.
func sketchManifest(version string, exts ...string) string {
	e, _ := json.Marshal(exts)
	return `{"manifest_version":1,"name":"sketch","version":"` + version + `","label":{"en":"Sketch"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sketch"},"applies":{"ext":` + string(e) + `}}]}`
}

// uploadSketch sends one install or upgrade of the sketch app to path.
func (f *appFixture) uploadSketch(t *testing.T, path, manifest string, grant []string, associations any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(manifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(uiBundleZip(t))
	if grant != nil {
		g, _ := json.Marshal(map[string]any{"permissions": grant})
		_ = w.WriteField("grant", string(g))
	}
	if associations != nil {
		a, _ := json.Marshal(associations)
		_ = w.WriteField("associations", string(a))
	}
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return res.StatusCode, out
}

func fileTypeRows(v any) []string {
	rows, _ := v.([]any)
	out := []string{}
	for _, r := range rows {
		m := r.(map[string]any)
		out = append(out, fmt.Sprintf("%s .%s %s", m["capability"], m["ext"], m["default"]))
	}
	return out
}

// TestFileTypes_AnUpgradeAsksOnlyAboutTheKindsItAdds: the review lists the
// kind the new version adds and not the one it already opened; the choices
// place the new kind, and one for the old kind is refused and changes
// nothing.
func TestFileTypes_AnUpgradeAsksOnlyAboutTheKindsItAdds(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	var id float64
	_, list := f.jsonReq(t, http.MethodGet, "/api/admin/app-plugins", nil)
	for _, p := range list["plugins"].([]any) {
		if m := p.(map[string]any); m["name"] == "sketch" {
			id = m["id"].(float64)
		}
	}
	require.NotZero(t, id)
	// The administrator's order for the kind the app already opens.
	code, out := f.jsonReq(t, http.MethodPut, "/api/admin/file-types/sketch", map[string]any{"open": map[string]any{"order": []string{"builtin"}, "off": []string{}}})
	require.Equal(t, http.StatusOK, code, out)

	upgrade := fmt.Sprintf("/api/admin/app-plugins/%d/upgrade", int64(id))
	v2 := sketchManifest("1.1.0", "sketch", "sketchpad")
	code, dry := f.uploadSketch(t, upgrade+"?dry_run=1", v2, nil, nil)
	require.Equal(t, http.StatusOK, code, dry)
	assert.Equal(t, []string{"open .sketchpad first"}, fileTypeRows(dry["file_types"]), "only the kind the upgrade adds")

	grant := []string{"files:read", "files:write", "ui", "ui-viewer:.sketch", "ui-viewer:.sketchpad"}
	code, done := f.uploadSketch(t, upgrade, v2, grant, []map[string]string{
		{"capability": "open", "ext": "sketchpad", "handler": "app:sketch/editor", "place": "off"},
		{"capability": "open", "ext": "sketch", "handler": "app:sketch/editor", "place": "first"},
	})
	require.Equal(t, http.StatusOK, code, done)
	errs, _ := done["association_errors"].([]any)
	require.Len(t, errs, 1, "%v", done)
	// The server's sentence (0.55: association errors are said, not Go's
	// "open .sketch: ..."), naming the kind.
	assert.Contains(t, errs[0], ".sketch files")
	assert.Contains(t, errs[0], "not a kind this version adds")

	_, body := f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	pad := kindOf(t, body, "sketchpad")["open"].(map[string]any)
	assert.Equal(t, []string{"app:sketch/editor"}, handlerIDs(pad["off"]), "the new kind took the choice")
	old := kindOf(t, body, "sketch")["open"].(map[string]any)
	assert.Equal(t, []string{"builtin", "app:sketch/editor"}, handlerIDs(old["on"]), "the administrator's order for the old kind is untouched")
}

// An upgrade that adds no kind has no File types group.
func TestFileTypes_AnUpgradeThatAddsNoKindAsksNothing(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	_, list := f.jsonReq(t, http.MethodGet, "/api/admin/app-plugins", nil)
	var id float64
	for _, p := range list["plugins"].([]any) {
		if m := p.(map[string]any); m["name"] == "sketch" {
			id = m["id"].(float64)
		}
	}
	code, dry := f.uploadSketch(t, fmt.Sprintf("/api/admin/app-plugins/%d/upgrade?dry_run=1", int64(id)), sketchManifest("1.0.1", "sketch"), nil, nil)
	require.Equal(t, http.StatusOK, code, dry)
	_, has := dry["file_types"]
	assert.False(t, has, "%v", dry["file_types"])
}

// ── install requests ────────────────────────────────────────────────────

// uiAppSource serves the sketch app from an address: its manifest (pinned
// to the bundle) and ui.zip. The manifest can change mid-test.
type uiAppSource struct {
	mu       sync.Mutex
	manifest string
	bundle   []byte
	srv      *httptest.Server
}

func newUIAppSource(t *testing.T) *uiAppSource {
	t.Helper()
	bundle := uiBundleZip(t)
	s := &uiAppSource{bundle: bundle}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/filex-app.json":
			_, _ = w.Write([]byte(s.manifest))
		case "/ui.zip":
			_, _ = w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	s.serve(t, "1.0.0", "sketch")
	return s
}

// serve publishes the app at version, opening exts.
func (s *uiAppSource) serve(t *testing.T, version string, exts ...string) {
	t.Helper()
	sum := sha256.Sum256(s.bundle) // the bytes /ui.zip serves
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(sketchManifest(version, exts...)), &m))
	m["ui"] = map[string]any{"bundle": map[string]any{"url": "ui.zip", "sha256": hex.EncodeToString(sum[:])}}
	b, _ := json.Marshal(m)
	s.mu.Lock()
	s.manifest = string(b)
	s.mu.Unlock()
}

func (s *uiAppSource) manifestURL() string { return s.srv.URL + "/filex-app.json" }

// requestFileTypes reads a request's File types group, as the administrator
// reviewing it does.
func (f *prFix) requestFileTypes(t *testing.T, id int64) []string {
	t.Helper()
	status, raw := doReq(t, f.admin, http.MethodGet, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d", id)), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var body struct {
		Request map[string]any `json:"request"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return fileTypeRows(body.Request["file_types"])
}

func (f *prFix) fileTypes(t *testing.T) map[string]any {
	t.Helper()
	status, raw := doReq(t, f.admin, http.MethodGet, f.url("/api/admin/file-types"), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	return body
}

// TestPluginRequests_TheApprovingAdministratorPlacesTheApp: the request's
// review shows the File types rows; the approval writes the choices and its
// audit row carries them.
func TestPluginRequests_TheApprovingAdministratorPlacesTheApp(t *testing.T) {
	f := newPRFix(t)
	src := newUIAppSource(t)
	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "app", "op": "install", "manifest_url": src.manifestURL(), "reason": "eskiz dosyaları için",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	r := decodeRequest(t, raw)
	assert.Equal(t, []string{"open .sketch first"}, f.requestFileTypes(t, r.ID))

	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", r.ID)), map[string]any{
		"associations": []map[string]string{{"capability": "open", "ext": "sketch", "handler": "app:sketch/editor", "place": "off"}},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.NotContains(t, string(raw), "association_errors")

	open := kindOf(t, f.fileTypes(t), "sketch")["open"].(map[string]any)
	assert.Equal(t, []string{"app:sketch/editor"}, handlerIDs(open["off"]))
	assert.Equal(t, []string{"builtin"}, handlerIDs(open["on"]))

	approvals := f.auditActions(t, "plugin_request.approve")
	require.Len(t, approvals, 1)
	assert.Contains(t, approvals[0].Metadata, "file_types", "the approval's audit row says where the app went")
}

// TestPluginRequests_AnUpgradeRequestAsksOnlyAboutNewKinds: the review of an
// upgrade request lists only the kinds it adds, and its approval refuses a
// choice for a kind the app already handled.
func TestPluginRequests_AnUpgradeRequestAsksOnlyAboutNewKinds(t *testing.T) {
	f := newPRFix(t)
	src := newUIAppSource(t)
	status, raw := withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "app", "op": "install", "manifest_url": src.manifestURL(), "reason": "eskiz dosyaları için",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	first := decodeRequest(t, raw)
	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", first.ID)), map[string]any{})
	require.Equal(t, http.StatusOK, status, string(raw))

	src.serve(t, "1.1.0", "sketch", "sketchpad")
	status, raw = withToken(t, http.MethodPost, f.url("/api/admin/plugin-requests"), f.token, map[string]any{
		"kind": "app", "op": "upgrade", "name": "sketch", "manifest_url": src.manifestURL(), "reason": "yeni tür",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	up := decodeRequest(t, raw)
	assert.Equal(t, []string{"open .sketchpad first"}, f.requestFileTypes(t, up.ID), "only the kind the upgrade adds")

	status, raw = doReq(t, f.admin, http.MethodPost, f.url(fmt.Sprintf("/api/admin/plugin-requests/%d/approve", up.ID)), map[string]any{
		"associations": []map[string]string{
			{"capability": "open", "ext": "sketchpad", "handler": "app:sketch/editor", "place": "off"},
			{"capability": "open", "ext": "sketch", "handler": "app:sketch/editor", "place": "off"},
		},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "not a kind this version adds")

	body := f.fileTypes(t)
	assert.Equal(t, []string{"app:sketch/editor"}, handlerIDs(kindOf(t, body, "sketchpad")["open"].(map[string]any)["off"]))
	old := kindOf(t, body, "sketch")["open"].(map[string]any)
	assert.Empty(t, handlerIDs(old["off"]), "the kind the app already opened keeps its order")
	assert.Equal(t, []string{"app:sketch/editor", "builtin"}, handlerIDs(old["on"]))
}

package handlers_test

// Default apps (handlers/file_types_admin.go, internal/assoc), over real HTTP:
// the administrator's rules, who may change them, what the explorer is told,
// and the server's own line - an interface switched off for a kind is refused
// on it whatever the explorer did.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/assoc"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func (f *appFixture) jsonReq(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if len(out) == 0 {
		out["raw"] = string(raw)
	}
	return res.StatusCode, out
}

// kindOf finds one kind in a GET /api/admin/file-types answer.
func kindOf(t *testing.T, body map[string]any, ext string) map[string]any {
	t.Helper()
	kinds, _ := body["kinds"].([]any)
	for _, k := range kinds {
		m := k.(map[string]any)
		if m["ext"] == ext {
			return m
		}
	}
	t.Fatalf("no kind %q in %v", ext, body)
	return nil
}

func handlerIDs(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, h := range list {
		out = append(out, h.(map[string]any)["id"].(string))
	}
	return out
}

func TestFileTypes_TheAdministratorDecides_TheExplorerIsTold(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)

	code, body := f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, true, body["editable"])
	sketch := kindOf(t, body, "sketch")
	open := sketch["open"].(map[string]any)
	assert.Equal(t, []string{"app:sketch/editor", "builtin"}, handlerIDs(open["on"]), "the default: the app first, filex's viewer last")
	assert.Equal(t, false, open["custom"])

	code, body = f.jsonReq(t, http.MethodPut, "/api/admin/file-types/sketch", map[string]any{
		"open": map[string]any{"order": []string{"builtin"}, "off": []string{"app:sketch/editor"}},
	})
	require.Equal(t, http.StatusOK, code, body)
	open = kindOf(t, body, "sketch")["open"].(map[string]any)
	assert.Equal(t, []string{"builtin"}, handlerIDs(open["on"]))
	assert.Equal(t, []string{"app:sketch/editor"}, handlerIDs(open["off"]))
	assert.Equal(t, true, open["custom"])

	// The explorer's listing carries the rule.
	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/actions")
	require.NoError(t, err)
	var ans struct {
		OpenRules map[string]struct {
			Order []string `json:"order"`
			Off   []string `json:"off"`
		} `json:"open_rules"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&ans))
	res.Body.Close()
	require.Contains(t, ans.OpenRules, "sketch")
	assert.Equal(t, []string{"app:sketch/editor"}, ans.OpenRules["sketch"].Off)

	// Audited, with what it was and what it is.
	rows, err := f.store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == "file_association.update" {
			found = true
			assert.Equal(t, "sketch", r.TargetID)
			assert.Equal(t, "sketch", r.Metadata["ext"])
			assert.NotNil(t, r.Metadata["before"])
			assert.NotNil(t, r.Metadata["after"])
		}
	}
	assert.True(t, found, "file_association.update was written")

	// Back to the default.
	code, body = f.jsonReq(t, http.MethodDelete, "/api/admin/file-types/sketch", nil)
	require.Equal(t, http.StatusOK, code, body)
	_, err = f.store.ListFileAssociations(context.Background())
	require.NoError(t, err)
	code, body = f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, kindOf(t, body, "sketch")["open"].(map[string]any)["custom"])
}

func TestFileTypes_RefusesWhatTheKindDoesNotHave(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	for name, body := range map[string]any{
		"another app":  map[string]any{"open": map[string]any{"order": []string{"app:other/editor"}}},
		"wrong shape":  map[string]any{"open": map[string]any{"order": []string{"app:sketch"}}},
		"no thumbnail": map[string]any{"thumbnail": map[string]any{"order": []string{"builtin"}}},
		"nothing":      map[string]any{},
	} {
		code, out := f.jsonReq(t, http.MethodPut, "/api/admin/file-types/sketch", body)
		assert.Equal(t, http.StatusBadRequest, code, "%s: %v", name, out)
	}
	code, _ := f.jsonReq(t, http.MethodPut, "/api/admin/file-types/Not.A.Kind", map[string]any{"open": nil})
	assert.Equal(t, http.StatusBadRequest, code)
	rules, err := f.store.ListFileAssociations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rules, "nothing refused was stored")
}

// An API key reads the rules and cannot change them: switching a handler back
// on hands an app files, which is a signed-in administrator's decision.
func TestFileTypes_AnAPIKeyReadsAndIsRefusedAChange(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	useProductionAuthChain(t, f.store)
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "admin,read")

	status, raw := withToken(t, http.MethodGet, f.srv.URL+"/api/admin/file-types", tok, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"editable":false`)
	status, raw = withToken(t, http.MethodPut, f.srv.URL+"/api/admin/file-types/sketch", tok,
		map[string]any{"open": map[string]any{"off": []string{"app:sketch/editor"}}})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Contains(t, string(raw), "session_required")
	status, raw = withToken(t, http.MethodDelete, f.srv.URL+"/api/admin/file-types/sketch", tok, nil)
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	rules, err := f.store.ListFileAssociations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rules)
}

// ⚠⚠ The server's line: an interface switched off for a kind does not save
// that kind, nor call its module on it - an old tab or a crafted request.
func TestFileTypes_AnInterfaceSwitchedOffIsRefusedOnThatKind(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")

	code, body := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v2")
	require.Equal(t, http.StatusOK, code, body)

	code2, out := f.jsonReq(t, http.MethodPut, "/api/admin/file-types/sketch", map[string]any{
		"open": map[string]any{"off": []string{"app:sketch/editor"}},
	})
	require.Equal(t, http.StatusOK, code2, out)

	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v3")
	assert.Equal(t, http.StatusForbidden, code, body)
	assert.Contains(t, body, "handler_off")
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"), "nothing was written")

	code, body = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://")+"&name=new.sketch", "x")
	assert.Equal(t, http.StatusForbidden, code, "a save-as of that kind too: %s", body)

	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/ui/sketch/editor/call",
		strings.NewReader(`{"method":"x","paths":["main://doc.sketch"]}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, http.StatusForbidden, res.StatusCode, string(raw))
	assert.Contains(t, string(raw), "handler_off")

	_, _ = f.jsonReq(t, http.MethodDelete, "/api/admin/file-types/sketch", nil)
	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v4")
	assert.Equal(t, http.StatusOK, code, "back on: %s", body)
}

// The install review lists the kinds; the choices made there are written once
// the app runs, and only for THIS app's handlers.
func TestFileTypes_TheInstallReviewAndItsChoices(t *testing.T) {
	f := newAppFixture(t, nil)
	dry := f.installUIApp(t)
	types, _ := dry["file_types"].([]any)
	require.Len(t, types, 1, "%v", dry)
	row := types[0].(map[string]any)
	assert.Equal(t, "open", row["capability"])
	assert.Equal(t, "sketch", row["ext"])
	assert.Equal(t, "first", row["default"])
	assert.Equal(t, []string{"builtin"}, handlerIDs(row["current"]))
}

func TestFileTypes_DemoRefusesAChange(t *testing.T) {
	f := newAppFixture(t, func(c *config.Config) { c.Demo.Mode = true })
	code, out := f.jsonReq(t, http.MethodPut, "/api/admin/file-types/png", map[string]any{"open": nil})
	assert.Equal(t, http.StatusForbidden, code, out)
	code, out = f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	assert.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, false, out["editable"])
}

func TestFileTypes_MCPToolReadsIt(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	useProductionAuthChain(t, f.store)
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, f.store, admin.ID, "admin,mcp,read")
	code, body := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_file_types_list","arguments":{}}}`)
	require.Equal(t, http.StatusOK, code, body)
	// ⚠ Assert the CONTENT and the absence of a JSON-RPC error: a tool whose
	// answer is a JSON object failed output validation in the SDK while the
	// handler itself ran fine, and that failure has no `isError` in it.
	assert.NotContains(t, body, `"error":{"code"`, body)
	assert.NotContains(t, body, `"isError":true`, body)
	assert.Contains(t, body, "app:sketch/editor", body)
	assert.Contains(t, body, `sketch`, body)
}

// The demo refusal is the handler's own, not only the router's guard: the
// handler instances are reached in-process too (/api/ai/admin, MCP).
func TestFileTypes_TheHandlerItselfRefusesADemo(t *testing.T) {
	h := handlers.NewFileTypesAdmin(assoc.New(nil), true)
	r := chi.NewRouter()
	r.Put("/{ext}", h.Put)
	r.Delete("/{ext}", h.Reset)
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(m, "/png", strings.NewReader(`{"open":null}`))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, m)
		assert.Contains(t, rec.Body.String(), "demo_refused", m)
	}
}

// An install decides where THAT app goes; a choice naming any other handler
// is refused and nothing else moves.
func TestFileTypes_AnInstallPlacesOnlyItsOwnHandlers(t *testing.T) {
	f := newAppFixture(t, nil)
	bundle := uiBundleZip(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(uiOnlyManifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(bundle)
	g, _ := json.Marshal(map[string]any{"permissions": []string{"files:read", "files:write", "ui", "ui-viewer:.sketch"}})
	_ = w.WriteField("grant", string(g))
	a, _ := json.Marshal([]map[string]string{
		{"capability": "open", "ext": "sketch", "handler": "builtin", "place": "off"},
		{"capability": "open", "ext": "sketch", "handler": "app:sketch/editor", "place": "last"},
	})
	_ = w.WriteField("associations", string(a))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, string(raw))
	assert.Contains(t, string(raw), `"association_errors"`)
	assert.Contains(t, string(raw), "builtin is not this app's")

	code, body := f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code)
	open := kindOf(t, body, "sketch")["open"].(map[string]any)
	assert.Equal(t, []string{"builtin", "app:sketch/editor"}, handlerIDs(open["on"]), "its own choice (last) was written; filex's viewer stayed on")
	assert.Empty(t, handlerIDs(open["off"]))
}

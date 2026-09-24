package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// runAndDrain submits an action and waits for its op.
func (f *appFixture) runAndDrain(t *testing.T, client *http.Client, action string, body map[string]any) map[string]any {
	t.Helper()
	status, raw := doReq(t, client, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/"+action+"/run", body)
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	return f.drain(t, ans.Op.ID)
}

func (f *appFixture) listing(t *testing.T, client *http.Client, dir string) map[string]map[string]any {
	t.Helper()
	status, raw := doReq(t, client, http.MethodGet, f.srv.URL+"/api/files/manager?action=index&path="+dir, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	out := map[string]map[string]any{}
	for _, e := range resp.Files {
		out[e["basename"].(string)] = e
	}
	return out
}

// A lock taken by an app freezes the file for everyone — the admin included —
// until the app lifts it or an administrator overrides; the app's own jobs
// still write; the listing says so.
func TestAppPlugins_Lock_FreezesFileForEveryone_AppStillWrites_AdminOverrides(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/nda.txt", "draft")
	f.writeFile(t, "docs/other.txt", "free")

	// Menu rows carry the view placement (a page view opens in a new tab).
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/actions", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), `"view":"wizard","view_placement":"page"`)
	assert.NotContains(t, string(raw), `plugin:echo/applied`, "a hidden action never reaches a menu")

	// Lock (params given → no surface, straight to the queue).
	op := f.runAndDrain(t, f.admin, "lock", map[string]any{"paths": []string{"main://docs/nda.txt"}, "params": map[string]any{}})
	assert.Equal(t, "ok", op["status"], op)

	// The listing: locked, with the holder, and perm capped to viewer.
	rows := f.listing(t, f.admin, "main://docs")
	require.Contains(t, rows, "nda.txt")
	assert.Equal(t, true, rows["nda.txt"]["locked"])
	assert.Equal(t, "viewer", rows["nda.txt"]["perm"])
	lock, _ := rows["nda.txt"]["lock"].(map[string]any)
	assert.Equal(t, "echo", lock["plugin"])
	assert.Equal(t, "under signature", lock["reason"])
	// ⚠ The NAME addresses the app; the LABEL is what a person is shown. The
	// details panel's banner read "sign locked this file" where every other
	// screen said "e-Signature" (v0.43.0), so both travel. This case is the
	// WIRING — NewAppPlugins handing the registry's labels to the free
	// functions that write these payloads — which the wire fixture cannot see.
	label, _ := lock["plugin_label"].(map[string]any)
	assert.Equal(t, "Echo Fixture", label["en"], "the listing's lock did not carry the app's label")
	assert.Nil(t, rows["other.txt"]["locked"])
	assert.Equal(t, "owner", rows["other.txt"]["perm"])

	// The admin cannot rename, delete or move the folder that holds it.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "main://docs", "item": "main://docs/nda.txt", "name": "nda2.txt"})
	assert.Equal(t, http.StatusLocked, status, string(raw))
	assert.Contains(t, string(raw), `"error":"locked"`)
	// The refusal a person reads as a toast carries the label too.
	assert.Contains(t, string(raw), `"plugin_label":{"en":"Echo Fixture"`)
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/delete", map[string]any{"source": []string{"main://docs/nda.txt"}})
	assert.Equal(t, http.StatusLocked, status, string(raw))
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "main://", "item": "main://docs", "name": "docs2"})
	assert.Equal(t, http.StatusLocked, status, string(raw))
	// An upload that would overwrite the locked file is refused as well, by
	// name: the folder is writable, the FILE is not.
	status, raw = uploadMultipart(t, f.admin, f.srv.URL+"/api/files/manager?action=upload", "main://docs", "nda.txt", "overwritten")
	assert.Equal(t, http.StatusLocked, status, string(raw))
	status, raw = uploadMultipart(t, f.admin, f.srv.URL+"/api/files/manager?action=upload", "main://docs", "brand-new.txt", "fine")
	assert.Equal(t, http.StatusOK, status, string(raw))

	// …but the sibling is free, and the folder still takes new files.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "main://docs", "item": "main://docs/other.txt", "name": "free.txt"})
	assert.Equal(t, http.StatusOK, status, string(raw))

	// The locking app's own job still writes into the file (version mode
	// through the view's output override).
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"paths": []string{"main://docs/nda.txt"}, "event": "submit", "data": map[string]any{"output_mode": "version"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	op = f.drain(t, ans.Op.ID)
	assert.Equal(t, "ok", op["status"], op)
	got, err := os.ReadFile(filepath.Join(f.root, "docs", "nda.txt"))
	require.NoError(t, err)
	assert.Equal(t, "DRAFT", string(got), "written in place, not as a sibling")

	// Admin lock API: list, then lift by force with an audit line.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/locks", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"path":"docs/nda.txt"`)
	// …on the storage it names: the panel prints the name, not "Storage #1".
	assert.Contains(t, string(raw), `"storage":"`+f.st.Name+`"`)
	status, raw = doReq(t, f.admin, http.MethodDelete, f.srv.URL+"/api/admin/app-plugins/locks", map[string]any{"storage_id": f.st.ID, "path": "docs/nda.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, _ = doReq(t, f.admin, http.MethodDelete, f.srv.URL+"/api/admin/app-plugins/locks", map[string]any{"storage_id": f.st.ID, "path": "docs/nda.txt"})
	assert.Equal(t, http.StatusNotFound, status)
	entries, err := f.store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	found := false
	for _, e := range entries {
		if e.Action == "app_plugin.unlock" && e.TargetID == "docs/nda.txt" {
			found = true
		}
	}
	assert.True(t, found, "the forced unlock is audited")
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "main://docs", "item": "main://docs/nda.txt", "name": "nda2.txt"})
	assert.Equal(t, http.StatusOK, status, string(raw))
	rows = f.listing(t, f.admin, "main://docs")
	assert.Nil(t, rows["nda2.txt"]["locked"])
}

// A non-admin editor is capped the same way; the admin API is theirs to
// neither list nor lift.
func TestAppPlugins_Lock_EditorIsCappedToo(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/nda.txt", "draft")
	u := seedSharedUser(t, f.store, "ed@test.local", "EditorPass1!")
	grant(t, f.store, f.st, u, "", model.GrantEditor, true)
	ed := freshClient(t)
	testutil.LoginAs(t, f.srv, ed, "ed@test.local", "EditorPass1!")

	op := f.runAndDrain(t, ed, "lock", map[string]any{"paths": []string{"main://docs/nda.txt"}, "params": map[string]any{}})
	assert.Equal(t, "ok", op["status"], op)
	rows := f.listing(t, ed, "main://docs")
	assert.Equal(t, "viewer", rows["nda.txt"]["perm"])
	status, raw := doReq(t, ed, http.MethodPost, f.srv.URL+"/api/files/delete", map[string]any{"source": []string{"main://docs/nda.txt"}})
	assert.Equal(t, http.StatusLocked, status, string(raw))
	status, _ = doReq(t, ed, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/locks", nil)
	assert.Equal(t, http.StatusForbidden, status)
	// A viewer-capped editor cannot even start a WRITING action of another
	// kind on it… but the locking app's own job passes: unlock.
	op = f.runAndDrain(t, ed, "unlock", map[string]any{"paths": []string{"main://docs/nda.txt"}})
	assert.Equal(t, "ok", op["status"], op)
	rows = f.listing(t, ed, "main://docs")
	assert.Equal(t, "editor", rows["nda.txt"]["perm"])
}

// applies.state / no_state: the menu offer follows the state keys the app
// keeps on the file, which the listing exposes as <plugin>:<key>.
func TestAppPlugins_StateAwareActions_AndHiddenFromSurfaceOnly(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "x")

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/again/run", map[string]any{"paths": []string{"main://docs/a.txt"}})
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
	assert.Contains(t, string(raw), "not_applicable")
	op := f.runAndDrain(t, f.admin, "fresh", map[string]any{"paths": []string{"main://docs/a.txt"}})
	assert.Equal(t, "ok", op["status"], op)
	rows := f.listing(t, f.admin, "main://docs")
	assert.Nil(t, rows["a.txt"]["app_state"])

	// `upper` writes a sibling (which gets a node row) and keeps a `runs`
	// key on its input. Running it on that sibling puts the key on a file
	// the listing knows, which is where the badge has to show up.
	op = f.runAndDrain(t, f.admin, "upper", map[string]any{"paths": []string{"main://docs/a.txt"}})
	assert.Equal(t, "ok", op["status"], op)
	op = f.runAndDrain(t, f.admin, "upper", map[string]any{"paths": []string{"main://docs/a-upper.txt"}})
	assert.Equal(t, "ok", op["status"], op)
	rows = f.listing(t, f.admin, "main://docs")
	require.Contains(t, rows, "a-upper.txt")
	st, _ := json.Marshal(rows["a-upper.txt"]["app_state"])
	assert.JSONEq(t, `["echo:runs"]`, string(st))

	// The menu rule follows the key: `again` needs it, `fresh` refuses it.
	op = f.runAndDrain(t, f.admin, "again", map[string]any{"paths": []string{"main://docs/a-upper.txt"}})
	assert.Equal(t, "ok", op["status"], op)
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/fresh/run", map[string]any{"paths": []string{"main://docs/a-upper.txt"}})
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))

	// Hidden: 404 from the menu route, queued when a surface asks for it.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/applied/run", map[string]any{"paths": []string{"main://docs/a.txt"}})
	assert.Equal(t, http.StatusNotFound, status, string(raw))
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"paths": []string{"main://docs/a.txt"}, "event": "submit", "data": map[string]any{"run": "applied"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))

	// A page view opens like any other view.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/views/echo/wizard?path=main://docs/a.txt", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "page open")
}

// uploadMultipart posts one small file through the browser upload path.
func uploadMultipart(t *testing.T, client *http.Client, url, dir, name, content string) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("path", dir)
	fw, err := w.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = fw.Write([]byte(content))
	require.NoError(t, w.Close())
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

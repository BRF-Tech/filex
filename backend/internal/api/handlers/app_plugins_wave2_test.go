package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// makeReadOnly flips a storage's read-only flag the way the admin form does.
func (f *appFixture) makeReadOnly(t *testing.T, st *model.Storage) {
	t.Helper()
	cur, err := f.store.GetStorage(context.Background(), st.ID)
	require.NoError(t, err)
	cur.ReadOnly = true
	require.NoError(t, f.store.UpdateStorage(context.Background(), cur))
}

// helloSubmit posts the echo view's submit: output_mode (and output_dir) name
// the job's output, as a wizard's last step does.
func (f *appFixture) helloSubmit(t *testing.T, client *http.Client, path string, data map[string]any) (int, []byte) {
	t.Helper()
	return doReq(t, client, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"paths": []string{path}, "event": "submit", "data": data})
}

func opID(t *testing.T, raw []byte) int64 {
	t.Helper()
	var ans struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	require.NotZero(t, ans.Op.ID, string(raw))
	return ans.Op.ID
}

// Burak, 2026-09-22: on a read-only storage "Dönüştür…" is offered, the
// wizard asks where the result should go, and the result lands in the folder
// chosen. The server is the one that decides: the folder is checked like any
// write — its storage, the person's level there, filex's own folders — and a
// job that would write beside its read-only source is still refused.
func TestAppPlugins_AResultGoesIntoAChosenFolder(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["view"] = "hello"
				a["output"] = map[string]any{"mode": "sibling", "name": "{stem}-upper{ext}", "elsewhere": true}
			}
		}
	})
	dest, destRoot := f.addStorage(t, "dest")
	require.NoError(t, os.MkdirAll(filepath.Join(destRoot, "out"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(destRoot, ".filex-trash"), 0o755))
	f.writeFile(t, "docs/a.txt", "hello")
	f.makeReadOnly(t, f.st)
	src := "main://docs/a.txt"

	// Offered — the menu click opens the screen on the read-only file, and
	// the screen is told where this person's results go by default.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run", map[string]any{"paths": []string{src}})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "home=dest://", "the first storage the person may write")

	// Beside the source: still refused.
	status, raw = f.helloSubmit(t, f.admin, src, map[string]any{"output_mode": "sibling"})
	assert.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "read_only")

	// Into the chosen folder: queued, run, written THERE.
	status, raw = f.helloSubmit(t, f.admin, src, map[string]any{"output_mode": "folder", "output_dir": "dest://out"})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	op := f.drain(t, opID(t, raw))
	require.Equal(t, "ok", op["status"], op)
	got, err := os.ReadFile(filepath.Join(destRoot, "out", "a-upper.txt"))
	require.NoError(t, err, "the result is in the chosen folder: %v", op)
	assert.Equal(t, "HELLO", string(got))
	assert.Contains(t, jsonString(op["outputs"]), "dest://out/a-upper.txt", "the op names the output on ITS storage")
	_, err = os.Stat(filepath.Join(f.root, "docs", "a-upper.txt"))
	assert.True(t, os.IsNotExist(err), "nothing was written beside the read-only source")

	// Folders the server refuses, whatever the screen offered.
	for name, c := range map[string]struct {
		dir  string
		want int
	}{
		"a read-only storage":   {"main://docs", http.StatusConflict},
		"a folder that is not":  {"dest://nope", http.StatusNotFound},
		"filex's own folder":    {"dest://.filex-trash", http.StatusForbidden},
		"no storage named":      {"out", http.StatusBadRequest},
		"a storage that is not": {"ghost://x", http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			status, raw := f.helloSubmit(t, f.admin, src, map[string]any{"output_mode": "folder", "output_dir": c.dir})
			assert.Equal(t, c.want, status, string(raw))
		})
	}

	// A person who may only READ the chosen folder cannot write into it.
	u := seedSharedUser(t, f.store, "reader@test.local", "ReaderPass1!")
	grant(t, f.store, f.st, u, "", model.GrantEditor, true)
	grant(t, f.store, dest, u, "", model.GrantViewer, true)
	reader := freshClient(t)
	testutil.LoginAs(t, f.srv, reader, "reader@test.local", "ReaderPass1!")
	status, raw = f.helloSubmit(t, reader, src, map[string]any{"output_mode": "folder", "output_dir": "dest://out"})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	// …and is told no default home: there is nowhere they may write.
	status, raw = doReq(t, reader, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run", map[string]any{"paths": []string{src}})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.NotContains(t, string(raw), "home=")
}

// A job may name a folder only for an action that says its result may go
// elsewhere — every other action writes where its manifest says.
func TestAppPlugins_AFolderOnlyForAnActionThatOffersOne(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.addStorage(t, "dest")
	f.writeFile(t, "docs/a.txt", "hello")
	status, raw := f.helloSubmit(t, f.admin, "main://docs/a.txt", map[string]any{"output_mode": "folder", "output_dir": "dest://"})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "folder_not_offered")
}

// applies.writable: a flow that ends in a write is refused on a read-only
// storage at its first step, like an action whose output writes.
func TestAppPlugins_AWritableFlowIsRefusedOnAReadOnlyStorage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "outbound" {
				a["applies"] = map[string]any{"kind": "any", "writable": true}
			}
		}
	})
	f.writeFile(t, "docs/a.txt", "x")
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/actions", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), `"writable":true`, "the menu is told, so it can leave the row out")

	f.makeReadOnly(t, f.st)
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/outbound/run",
		map[string]any{"paths": []string{"main://docs/a.txt"}, "params": map[string]any{}})
	assert.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "read_only")
}

// A personal state key is its person's: the listing shows it to them as
// `<key>@me` and to nobody else, and a rule naming `<key>@me` offers — and
// runs — the action for that person only. The server is the authority: the
// run check reads the same view the listing gives.
func TestAppPlugins_PersonalStateIsForItsPersonOnly(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "again" {
				a["applies"] = map[string]any{"kind": "file", "ext": []any{"txt"}, "state": []any{"todo@me"}}
			}
		}
	})
	f.writeFile(t, "docs/a.txt", "x")
	other := seedSharedUser(t, f.store, "other@test.local", "OtherPass1!")
	grant(t, f.store, f.st, other, "", model.GrantEditor, true)
	oc := freshClient(t)
	testutil.LoginAs(t, f.srv, oc, "other@test.local", "OtherPass1!")

	op := f.runAndDrain(t, f.admin, "fresh", map[string]any{"paths": []string{"main://docs/a.txt"}, "params": map[string]any{"mine": true}})
	require.Equal(t, "ok", op["status"], op)

	stateOf := func(client *http.Client) []string {
		row := f.listing(t, client, "main://docs")["a.txt"]
		var out []string
		for _, k := range asSlice(row["app_state"]) {
			out = append(out, k.(string))
		}
		return out
	}
	assert.Contains(t, stateOf(f.admin), "echo:todo@me", "the person it was left for sees it as theirs")
	for _, k := range stateOf(oc) {
		assert.False(t, strings.HasPrefix(k, "echo:todo"), "nobody else sees another person's marker: %v", k)
	}

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/again/run",
		map[string]any{"paths": []string{"main://docs/a.txt"}, "params": map[string]any{}})
	assert.Equal(t, http.StatusAccepted, status, string(raw))
	status, raw = doReq(t, oc, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/again/run",
		map[string]any{"paths": []string{"main://docs/a.txt"}, "params": map[string]any{}})
	assert.Equal(t, http.StatusUnprocessableEntity, status, "the other person has nothing to do here: %s", raw)
}

// A lock reason the app named as a manifest message reaches every reader in
// their own language: the admin's lock list, the listing's badge and the 423
// a refused rename answers.
func TestAppPlugins_LockReasonInEveryLanguage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/n.txt", "x")
	op := f.runAndDrain(t, f.admin, "lock", map[string]any{"paths": []string{"main://docs/n.txt"}, "params": map[string]any{"msg": "Ayşe"}})
	require.Equal(t, "ok", op["status"], op)

	want := `"reason_text":{"en":"held for Ayşe","tr":"Ayşe için tutuluyor"}`
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/locks", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), want)
	assert.Contains(t, string(raw), `"reason":"held for Ayşe"`, "the plain reason is words, never the stored key")
	assert.NotContains(t, string(raw), "msg:held")

	lock := f.listing(t, f.admin, "main://docs")["n.txt"]["lock"]
	assert.Contains(t, jsonString(lock), want)

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=rename",
		map[string]any{"path": "main://docs", "item": "main://docs/n.txt", "name": "m.txt"})
	assert.Equal(t, http.StatusLocked, status, string(raw))
	assert.Contains(t, string(raw), want)
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

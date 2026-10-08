package handlers_test

// Three of an app's doors, decided by the server (0.54 audit, #212):
//
//   - B3: an app is never run on, opened on or asked about a path inside
//     filex's own folders (the trash, the version history, the thumbnail
//     cache, somebody else's drafts) — 404, the answer every by-path door
//     gives a sealed path. Before, only the person's ACL stood in the way,
//     and an admin could queue a job on `.filex-trash/…`.
//   - B9: every action row carries `read_only_ok`, the run's own rule for a
//     read-only storage, so the menu offers exactly what a click may do.
//   - B1: PutSettings refuses a value its manifest field cannot hold, in the
//     administrator's language, and stores nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppPlugins_ASealedPathIsNotAnInput(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "alpha")
	f.writeFile(t, ".filex-trash/abc123/a.txt", "deleted")
	f.writeFile(t, ".versions/docs/a.txt/1.txt", "older")

	// The same action on an ordinary file is queued: the refusal below is
	// about the path, nothing else.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"paths": []string{"main://docs/a.txt"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))

	for _, sealed := range []string{"main://.filex-trash/abc123/a.txt", "main://.versions/docs/a.txt/1.txt"} {
		before := f.census(t)
		status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
			map[string]any{"paths": []string{sealed}})
		assert.Equal(t, http.StatusNotFound, status, "%s: red before #212, the run was queued: %s", sealed, raw)
		assert.Equal(t, before, f.census(t), "%s: nothing is queued", sealed)

		// The storage_id spelling meets the same door.
		status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
			map[string]any{"storage_id": f.st.ID, "paths": []string{sealed[len("main://"):]}})
		assert.Equal(t, http.StatusNotFound, status, "%s by storage_id: %s", sealed, raw)

		// A screen opened, or answered, on it.
		status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/picks/event",
			map[string]any{"paths": []string{sealed}, "event": "change",
				"data": map[string]any{"values": map[string]any{"note": "hi"}}})
		assert.Equal(t, http.StatusNotFound, status, "%s view event: %s", sealed, raw)
	}
}

func TestAppPlugins_EveryActionRowSaysWhetherItRunsOnAReadOnlyStorage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)

	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/actions", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var list struct {
		Actions []map[string]any `json:"actions"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	byID := map[string]map[string]any{}
	for _, a := range list.Actions {
		require.Contains(t, a, "read_only_ok", "%v: red before #212, the menu worked it out itself", a["id"])
		byID[fmt.Sprint(a["id"])] = a
	}
	require.Contains(t, byID, "upper")
	require.Contains(t, byID, "engine")
	assert.Equal(t, false, byID["upper"]["read_only_ok"], "writes beside its file, no screen to choose elsewhere")
	assert.Equal(t, true, byID["engine"]["read_only_ok"], "writes nothing")

	// And the run agrees with the row (the run's 409 is the existing
	// TestAppPlugins_Run_ReadOnlyStorageRefusesWritingActions).
	f.st.ReadOnly = true
	require.NoError(t, f.store.UpdateStorage(context.Background(), f.st))
	f.writeFile(t, "a.txt", "x")
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"a.txt"}})
	assert.Equal(t, http.StatusConflict, status, string(raw))
}

func TestAppPluginsAdmin_PutSettings_RefusesAValueItsFieldCannotHold(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEchoWith(t, func(m map[string]any) {
		m["settings"] = []map[string]any{
			{"key": "copies", "type": "int", "label": "Copies", "min": 1, "max": 5},
			{"key": "format", "type": "select", "label": "Format",
				"options": []map[string]any{{"value": "pdf", "label": "PDF"}}},
		}
	})
	url := fmt.Sprintf("%s/api/admin/app-plugins/%d/settings", f.srv.URL, id)

	status, raw := doReq(t, f.admin, http.MethodPut, url, map[string]any{"values": map[string]string{"copies": "2", "format": "pdf"}})
	require.Equal(t, http.StatusOK, status, string(raw))

	status, raw = doReq(t, f.admin, http.MethodPut, url, map[string]any{"values": map[string]string{"copies": "99", "format": "pdf"}})
	require.Equal(t, http.StatusBadRequest, status, "red before #212: 99 was stored: %s", raw)
	var refusal struct {
		Error, Field, Reason, Message string
	}
	require.NoError(t, json.Unmarshal(raw, &refusal))
	assert.Equal(t, "setting_invalid", refusal.Error)
	assert.Equal(t, "copies", refusal.Field)
	assert.Equal(t, "max", refusal.Reason)
	assert.Equal(t, "Enter 5 or less.", refusal.Message)

	status, raw = doReq(t, f.admin, http.MethodPut, url, map[string]any{"values": map[string]string{"format": "exe"}})
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), `"reason":"option"`)

	status, raw = doReq(t, f.admin, http.MethodGet, url, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"copies":"2"`, "nothing a refused save carried was stored")
}

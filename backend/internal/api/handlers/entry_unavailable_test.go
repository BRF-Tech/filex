package handlers_test

// Issue #104, item 6: an entry the storage could not answer for (the sync
// marked its row, migration 00078). It is LISTED, with the flag and the
// storage's answer, and every verb on it - or on anything inside it - is
// refused on the server with 409 ENTRY_UNAVAILABLE. Hiding it in the explorer
// alone would leave an API client, an agent or an old explorer free to act on
// something nothing can vouch for.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

const noAnswer = "plugin: stat is not implemented for folders (http 500)"

// unavailableRig is a gone rig whose storage has synced (so the folder
// listing answers from the catalogue), with /Proje marked and /notlar.txt
// ordinary. Nothing of it needs to be on the disk.
func unavailableRig(t *testing.T) (*goneRig, []*model.Node, *model.Node) {
	t.Helper()
	r := newGoneRig(t)
	ctx := context.Background()
	require.NoError(t, r.raw.UpdateStorageSyncCursor(ctx, r.st.ID, time.Now(), ""))
	rows := r.tree(t)
	other := r.row(t, nil, "/notlar.txt", model.NodeTypeFile, 5)
	changed, err := r.raw.MarkNodeUnavailable(ctx, rows[0].ID, noAnswer)
	require.NoError(t, err)
	require.True(t, changed)
	return r, rows, other
}

func refusedUnavailable(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if !assert.Equal(t, http.StatusConflict, rec.Code, "%s: %s", what, rec.Body.String()) {
		return
	}
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), what)
	assert.Equal(t, "ENTRY_UNAVAILABLE", body["code"], what)
	assert.Equal(t, "main://Proje", body["path"], "%s: the refusal names the entry that is unavailable", what)
	assert.Equal(t, noAnswer, body["reason"], what)
}

func TestUnavailable_TheListingFlagsTheEntryAndNothingElse(t *testing.T) {
	r, _, _ := unavailableRig(t)
	rec := callList(t, r.mh, url.Values{"action": {"index"}, "path": {"main://"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	seen := map[string]map[string]any{}
	for _, f := range resp.Files {
		seen[fmt.Sprint(f["basename"])] = f
	}
	require.Contains(t, seen, "Proje", "an unavailable entry vanished from its folder's listing")
	assert.Equal(t, true, seen["Proje"]["unavailable"])
	assert.Equal(t, noAnswer, seen["Proje"]["unavailable_reason"])
	require.Contains(t, seen, "notlar.txt")
	_, flagged := seen["notlar.txt"]["unavailable"]
	assert.False(t, flagged, "an ordinary row carries the flag")
}

// Every verb on the entry, and on anything below it, is refused - the folder
// is not opened, nothing inside it is read, nothing is moved, renamed,
// deleted or created there.
func TestUnavailable_EveryVerbOnItOrInsideItIsRefused(t *testing.T) {
	r, rows, _ := unavailableRig(t)
	get := func(action, p string) *httptest.ResponseRecorder {
		return callList(t, r.mh, url.Values{"action": {action}, "path": {p}})
	}
	refusedUnavailable(t, get("index", "main://Proje"), "listing it")
	refusedUnavailable(t, get("index", "main://Proje/alt"), "listing a folder inside it")
	refusedUnavailable(t, get("search", "main://Proje"), "searching it")
	refusedUnavailable(t, get("preview", "main://Proje/a.txt"), "previewing a file inside it")
	refusedUnavailable(t, get("download", "main://Proje/alt/b.txt"), "downloading a file inside it")

	refusedUnavailable(t, callMutate(t, r.mh, "delete", map[string]any{
		"path": "main://", "items": []map[string]any{{"path": "main://Proje"}},
	}), "deleting it")
	refusedUnavailable(t, callMutate(t, r.mh, "delete", map[string]any{
		"path": "main://Proje", "items": []map[string]any{{"path": "main://Proje/a.txt"}},
	}), "deleting a file inside it")
	refusedUnavailable(t, callMutate(t, r.mh, "rename", map[string]any{
		"path": "main://", "item": "main://Proje", "name": "Proje2",
	}), "renaming it")
	refusedUnavailable(t, callMutate(t, r.mh, "move", map[string]any{
		"path": "main://", "items": []map[string]any{{"path": "main://Proje/a.txt"}},
	}), "moving a file out of it")
	refusedUnavailable(t, callMutate(t, r.mh, "newfolder", map[string]any{
		"path": "main://Proje", "name": "yeni",
	}), "making a folder inside it")

	// Nothing happened to it.
	for _, n := range rows {
		got, err := r.raw.GetNode(context.Background(), n.ID)
		require.NoError(t, err)
		assert.Nil(t, got.DeletedAt)
		assert.Equal(t, n.Path, got.Path)
	}
}

// By id as by path.
func TestUnavailable_TheBytesAreRefusedByID(t *testing.T) {
	r, rows, _ := unavailableRig(t)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/files/read?id=%d", rows[1].ID), nil)
	req = req.WithContext(auth.WithUser(req.Context(), &model.User{ID: r.owner, Role: model.RoleAdmin}))
	rec := httptest.NewRecorder()
	r.mh.Read(rec, req)
	refusedUnavailable(t, rec, "reading a file inside it by id")
}

// What is not inside it is untouched: the rule refuses the entry, not the
// storage.
func TestUnavailable_TheRestOfTheStorageWorks(t *testing.T) {
	r, _, other := unavailableRig(t)
	rec := callMutate(t, r.mh, "delete", map[string]any{
		"path": "main://", "items": []map[string]any{{"path": "main://notlar.txt"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	r.noRow(t, other)
}

// The mark lifted, the entry works again.
func TestUnavailable_ClearedEntryWorksAgain(t *testing.T) {
	r, rows, _ := unavailableRig(t)
	changed, err := r.raw.ClearNodeUnavailable(context.Background(), rows[0].ID)
	require.NoError(t, err)
	require.True(t, changed)
	rec := callList(t, r.mh, url.Values{"action": {"index"}, "path": {"main://Proje"}})
	assert.NotEqual(t, http.StatusConflict, rec.Code, rec.Body.String())
}

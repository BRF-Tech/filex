package handlers_test

// The trash listing says who put each row there: an id, the account's display
// name, and "self" when it was the person asking — the shape the listing
// already gives an owner (owner_id / owner_name / owner_self). A row nobody
// is named on (the scanner's, or one trashed before filex kept this) carries
// none of the three keys.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

func TestTrashList_SaysWhoDeletedEachRow(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	st := seedStorage(t, store, "main", false)
	ada, err := store.CreateUser(ctx, "ada@test.local", "", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserDisplayName(ctx, ada.ID, "Ada Lovelace"))
	bob, err := store.CreateUser(ctx, "bob@test.local", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserDisplayName(ctx, bob.ID, "Bob Marley"))

	trashed := func(name string, by *int64) {
		rel := "/" + name
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: rel, PathHash: pathkey.Hash(st.ID, rel),
			StorageKey: rel, Type: model.NodeTypeFile, Size: 3,
		})
		require.NoError(t, err)
		key := "/.filex-trash/1700000000-ef56__" + name
		require.NoError(t, store.SoftDeleteAndRetag(ctx, n.ID, key, pathkey.Hash(st.ID, key), rel))
		if by != nil {
			require.NoError(t, store.SetNodeDeletedBy(ctx, n.ID, by))
		}
	}
	trashed("mine.txt", &ada.ID)
	trashed("bobs.txt", &bob.ID)
	trashed("found-gone.txt", nil)

	h := handlers.NewTrash(trash.New(store, func(int64) (storage.Driver, error) { return nil, nil }, nil), store)
	req := httptest.NewRequest(http.MethodGet, "/api/files/manager/trash", nil)
	req = req.WithContext(auth.WithUser(req.Context(), ada))
	rec := httptest.NewRecorder()
	h.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	byName := map[string]map[string]any{}
	for _, e := range body.Entries {
		byName[e["name"].(string)] = e
	}
	require.Len(t, byName, 3)

	mine := byName["mine.txt"]
	assert.EqualValues(t, ada.ID, mine["deleted_by_id"])
	assert.Equal(t, "Ada Lovelace", mine["deleted_by_name"])
	assert.Equal(t, true, mine["deleted_by_self"], "the asker's own delete is not marked as theirs")

	bobs := byName["bobs.txt"]
	assert.EqualValues(t, bob.ID, bobs["deleted_by_id"])
	assert.Equal(t, "Bob Marley", bobs["deleted_by_name"])
	assert.NotContains(t, bobs, "deleted_by_self")

	gone := byName["found-gone.txt"]
	for _, k := range []string{"deleted_by_id", "deleted_by_name", "deleted_by_self"} {
		assert.NotContains(t, gone, k, "a row nobody is named on carries %s", k)
	}
}

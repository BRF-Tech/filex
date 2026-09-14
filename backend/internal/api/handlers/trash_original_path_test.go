package handlers_test

// A trash row is judged on where the file CAME FROM — the only path a grant,
// a confinement root or a restore is ever written about.
//
// Service.List and the restore handler both resolve that path as
// `storage_key`, falling back to the row's own `path` when the column is
// empty. Migration 00033 deliberately left those empty values alone, and the
// fallback is right for one legacy shape and wrong for the other:
//
//   - a row nothing ever renamed (the sync tombstone pass's SoftDeleteNode):
//     `path` still points where the file lived, so it IS the original path.
//   - a row whose `path` is a `.filex-trash/<stamp>__name` key — what the walk
//     minted for the trash's own bytes before it learned to skip the bin, and
//     the tombstone pass then soft-deleted where it stood (see
//     sync.reconcileTrash). The fallback hands back the BIN, and a grant on
//     the bin then decided a restore of somebody else's deleted file, and put
//     its name in that account's trash listing. The trash key records the
//     basename and nothing else, so the original folder is not recoverable
//     from the row by any reader: the answer is to refuse, not to guess.
//
// Found by @alfatm in his fork (a4241577); the fixture shape is his.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// legacyTrashRow catalogues a row at `rel` and soft-deletes it WITHOUT the
// retag: `storage_key` stays empty, exactly as a pre-00033 row has it.
func legacyTrashRow(t *testing.T, store db.Store, st *model.Storage, rel string) *model.Node {
	t.Helper()
	ctx := context.Background()
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: rel[strings.LastIndex(rel, "/")+1:], Path: rel,
		PathHash: pathkey.Hash(st.ID, rel), StorageKey: "",
		Type: model.NodeTypeFile, Size: 12,
	})
	require.NoError(t, err)
	require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
	got, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	require.Empty(t, got.StorageKey, "the fixture must reproduce the empty key, not a backfilled one")
	require.NotNil(t, got.DeletedAt)
	return got
}

func newOriginalPathFixture(t *testing.T) (db.Store, *model.Storage, *handlers.Trash) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	st := seedStorage(t, store, "main", true)
	h := handlers.NewTrash(trash.New(store, func(int64) (storage.Driver, error) { return nil, nil }, nil), store)
	h.AttachACL(acl.New(store))
	return store, st, h
}

func restoreAsUser(t *testing.T, h *handlers.Trash, user *model.User, nodeID int64) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"node_id": nodeID})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/manager/restore", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	h.Restore(rec, req.WithContext(auth.WithUser(req.Context(), user)))
	return rec
}

func listTrashAsUser(t *testing.T, h *handlers.Trash, user *model.User) ([]map[string]any, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/files/manager/trash?limit=50", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req.WithContext(auth.WithUser(req.Context(), user)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Entries, body.Total
}

// The shape the fallback gets RIGHT keeps working: a legacy row at its own
// path is judged there.
func TestTrashRestore_LegacyRowAtItsOwnPathIsStillJudgedThere(t *testing.T) {
	store, st, h := newOriginalPathFixture(t)
	row := legacyTrashRow(t, store, st, "/Ekip/notlar.md")

	viewer := seedSharedUser(t, store, "okuyucu@filex.test", "TestUserPass!1")
	editor := seedSharedUser(t, store, "yazar@filex.test", "TestUserPass!1")
	grant(t, store, st, viewer, "Ekip", model.GrantViewer, true)
	grant(t, store, st, editor, "Ekip", model.GrantEditor, true)

	entries, _ := listTrashAsUser(t, h, viewer)
	require.Len(t, entries, 1, "a viewer on the folder the file came from still sees it")
	assert.Equal(t, "/Ekip/notlar.md", entries[0]["path"])

	assert.Equal(t, http.StatusForbidden, restoreAsUser(t, h, viewer, row.ID).Code,
		"a viewer on the folder the file came from may not write it back")

	rec := restoreAsUser(t, h, editor, row.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	back, err := store.GetNode(context.Background(), row.ID)
	require.NoError(t, err)
	assert.Nil(t, back.DeletedAt, "the editor's restore did not take")
}

// binnedLegacy is the row this file is about: `path` inside the trash,
// `storage_key` empty, so nothing on the row says where it came from.
func binnedLegacy(t *testing.T, store db.Store, st *model.Storage) *model.Node {
	t.Helper()
	return legacyTrashRow(t, store, st, "/"+trash.Prefix+"/1700000000-abc__notlar.md")
}

func TestTrashRestore_LegacyRowInTheBinIsRefusedNotJudgedOnTheTrashKey(t *testing.T) {
	store, st, h := newOriginalPathFixture(t)
	row := binnedLegacy(t, store, st)

	// The only grant this account holds is on the BIN. It says nothing about
	// the folder the file actually came from, so it must not decide a restore.
	user := seedSharedUser(t, store, "kullanici@filex.test", "TestUserPass!1")
	grant(t, store, st, user, trash.Prefix, model.GrantOwner, true)

	rec := restoreAsUser(t, h, user, row.ID)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	still, err := store.GetNode(context.Background(), row.ID)
	require.NoError(t, err)
	assert.NotNil(t, still.DeletedAt,
		"the restore was decided on a grant on the bin and brought the row back to life inside it")
}

func TestTrashList_ARowWithNoOriginalPathIsNotOffered(t *testing.T) {
	store, st, h := newOriginalPathFixture(t)
	binnedLegacy(t, store, st)

	user := seedSharedUser(t, store, "kullanici@filex.test", "TestUserPass!1")
	grant(t, store, st, user, trash.Prefix, model.GrantOwner, true)

	entries, total := listTrashAsUser(t, h, user)
	assert.Empty(t, entries,
		"the listing offered another account's deleted file on the strength of a grant on `.filex-trash/`")
	assert.Zero(t, total)
}

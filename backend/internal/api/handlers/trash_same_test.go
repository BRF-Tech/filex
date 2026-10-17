package handlers_test

// POST /api/files/manager/trash/same (task #259): the sync's "Delete from this
// computer" deletes a held file only when the server still holds an identical
// copy of it. A trashed file has no download route, so the client sends its
// file's sha256 and the server compares it with the trash entry's bytes -
// for entries the caller's own trash listing shows, and saying nothing else.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

type sameFixture struct {
	store db.Store
	st    *model.Storage
	root  string
	drv   *local.Driver
	h     *handlers.Trash
}

func newSameFixture(t *testing.T) *sameFixture {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "docs", Driver: "local", MountPath: "/docs", Enabled: true, RBACEnabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		return drv, nil
	}
	h := handlers.NewTrash(trash.New(store, resolver, nil), store)
	h.AttachACL(acl.New(store))
	return &sameFixture{store: store, st: st, root: root, drv: drv, h: h}
}

// trashed writes rel with body, catalogues it and deletes it into the trash
// the way the manager's delete does.
func (f *sameFixture) trashed(t *testing.T, rel, body string) *model.Node {
	t.Helper()
	ctx := context.Background()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	clean := "/" + rel
	n, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: filepath.Base(rel), Path: clean, PathHash: pathkey.Hash(f.st.ID, clean),
		StorageKey: clean, Type: model.NodeTypeFile, Size: int64(len(body)),
	})
	require.NoError(t, err)
	out, err := trash.Put(ctx, f.drv, rel)
	require.NoError(t, err)
	require.True(t, out.Trashed)
	key := "/" + strings.Trim(out.Key, "/")
	require.NoError(t, f.store.SoftDeleteAndRetag(ctx, n.ID, key, pathkey.Hash(f.st.ID, key), clean))
	return n
}

func (f *sameFixture) ask(t *testing.T, u *model.User, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/manager/trash/same", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	f.h.Same(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	return rec
}

type sameAnswer struct {
	ID      int64 `json:"id"`
	Same    bool  `json:"same"`
	Checked bool  `json:"checked"`
}

func answers(t *testing.T, rec *httptest.ResponseRecorder) []sameAnswer {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Items []sameAnswer `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Items
}

func sha(body string) string {
	s := sha256.Sum256([]byte(body))
	return hex.EncodeToString(s[:])
}

func item(id int64, sum string) map[string]any { return map[string]any{"id": id, "sha256": sum} }

func TestTrashSame_SaysWhetherAnEntryHoldsTheSameBytes(t *testing.T) {
	f := newSameFixture(t)
	u := seedSharedUser(t, f.store, "kisi@filex.test", "TestUserPass!1")
	grant(t, f.store, f.st, u, "work", model.GrantViewer, true)
	n := f.trashed(t, "work/stale.txt", "cleaned up on the server")

	got := answers(t, f.ask(t, u, map[string]any{"items": []any{
		item(n.ID, sha("cleaned up on the server")),
		item(n.ID, sha("cleaned up on the server, but edited")),
	}}))
	require.Len(t, got, 2)
	assert.Equal(t, sameAnswer{ID: n.ID, Same: true, Checked: true}, got[0])
	assert.Equal(t, sameAnswer{ID: n.ID, Same: false, Checked: true}, got[1])
}

// ⚠ Only the caller's own trash listing: an entry somebody else deleted, in a
// folder the caller may not see, answers exactly like an entry with other
// bytes - even asked with the right hash - so the answer cannot be used to
// learn what another account's trash holds, or that the entry exists.
func TestTrashSame_AnEntryTheCallerCannotSeeIsNotTheSame(t *testing.T) {
	f := newSameFixture(t)
	owner := seedSharedUser(t, f.store, "sahip@filex.test", "TestUserPass!1")
	stranger := seedSharedUser(t, f.store, "yabanci@filex.test", "TestUserPass!1")
	grant(t, f.store, f.st, owner, "private", model.GrantEditor, true)
	n := f.trashed(t, "private/salary.txt", "nobody else's business")

	got := answers(t, f.ask(t, stranger, map[string]any{"items": []any{item(n.ID, sha("nobody else's business"))}}))
	assert.Equal(t, []sameAnswer{{ID: n.ID, Same: false, Checked: true}}, got)
	missing := answers(t, f.ask(t, stranger, map[string]any{"items": []any{item(n.ID+1000, sha("x"))}}))
	assert.Equal(t, []sameAnswer{{ID: n.ID + 1000, Same: false, Checked: true}}, missing, "an id that does not exist answers the same way")

	// The owner, who may see it, gets the real answer.
	got = answers(t, f.ask(t, owner, map[string]any{"items": []any{item(n.ID, sha("nobody else's business"))}}))
	assert.True(t, got[0].Same)
}

// A live file is not a trash entry: asking about it by id answers "not the
// same", whatever its bytes.
func TestTrashSame_ALiveFileIsNoTrashEntry(t *testing.T) {
	f := newSameFixture(t)
	u := seedSharedUser(t, f.store, "kisi@filex.test", "TestUserPass!1")
	grant(t, f.store, f.st, u, "work", model.GrantViewer, true)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "work", "live.txt"), []byte("here"), 0o644))
	live, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: "live.txt", Path: "/work/live.txt", PathHash: pathkey.Hash(f.st.ID, "/work/live.txt"),
		Type: model.NodeTypeFile, Size: 4,
	})
	require.NoError(t, err)

	got := answers(t, f.ask(t, u, map[string]any{"items": []any{item(live.ID, sha("here"))}}))
	assert.False(t, got[0].Same)
}

func TestTrashSame_RefusesWhatItCannotAnswer(t *testing.T) {
	f := newSameFixture(t)
	u := seedSharedUser(t, f.store, "kisi@filex.test", "TestUserPass!1")
	many := make([]any, 51)
	for i := range many {
		many[i] = item(int64(i+1), sha("x"))
	}
	for name, body := range map[string]any{
		"no items":         map[string]any{"items": []any{}},
		"too many":         map[string]any{"items": many},
		"not a sha256":     map[string]any{"items": []any{item(1, "abc")}},
		"upper-case hex":   map[string]any{"items": []any{item(1, strings.ToUpper(sha("x")))}},
		"not a JSON array": map[string]any{"items": "all of them"},
	} {
		rec := f.ask(t, u, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
	}
	rec := f.ask(t, u, map[string]any{"items": []any{}})
	var refusal map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &refusal))
	assert.Equal(t, "trash_same_items", refusal["error"])
	assert.NotEmpty(t, refusal["message"], "the server says it in words")
}

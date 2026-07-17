package handlers_test

/* calisma:d3 comments */
// Integration tests for the node-comment endpoints (v0.6 "Çalışma"),
// exercised through the real router (auth middleware + confine + acl
// included): CRUD, body validation, node-visibility ACL denial,
// author-or-admin delete, and trash-purge row cleanup.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// commentRow mirrors the JSON shape the handler returns per comment.
type commentRow struct {
	ID         int64  `json:"id"`
	NodeID     int64  `json:"node_id"`
	UserID     int64  `json:"user_id"`
	Body       string `json:"body"`
	AuthorName string `json:"author_name"`
	CanDelete  bool   `json:"can_delete"`
	CreatedAt  string `json:"created_at"`
}

func listComments(t *testing.T, client *http.Client, base string, nodeID int64) (int, []commentRow) {
	t.Helper()
	st, raw := doReq(t, client, http.MethodGet,
		base+"/api/files/comments?node_id="+jsonInt(nodeID), nil)
	if st != http.StatusOK {
		return st, nil
	}
	var resp struct {
		Comments []commentRow `json:"comments"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	return st, resp.Comments
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestComments_CRUD_ACL_AdminDelete_Purge(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	adminEmail, adminPw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, adminEmail, adminPw)
	ctx := context.Background()

	// RBAC-enabled storage + cached nodes: alfa/doc.txt (granted) and
	// beta/gizli.txt (no grant for the regular user).
	st, raw := doReq(t, adminClient, http.MethodPost, srv.URL+"/api/admin/storages", model.Storage{
		Name:          "cs1",
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    json.RawMessage(`{"root":"/tmp/filex-comments-test"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
		RBACEnabled:   true,
	})
	require.Equal(t, http.StatusOK, st, "create storage: %s", raw)
	var storageRow struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &storageRow))

	mkNode := func(p string) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: storageRow.ID,
			Name:      p[strings.LastIndex(p, "/")+1:],
			Path:      p,
			PathHash:  mutTestPathHash(storageRow.ID, p),
			Type:      model.NodeTypeFile,
			Size:      1,
		})
		require.NoError(t, err)
		return n
	}
	doc := mkNode("/alfa/doc.txt")
	hidden := mkNode("/beta/gizli.txt")

	uid := createUser(t, srv.URL, adminClient, "cu@test.local", "UserPass1!", model.RoleUser)
	_ = uid
	st, _ = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "cs1://alfa", "user_id": uid, "level": "viewer"})
	require.Equal(t, http.StatusOK, st, "grant viewer on alfa")

	userClient := freshClient(t)
	testutil.LoginAs(t, srv, userClient, "cu@test.local", "UserPass1!")

	// ── create: a viewer-granted user may comment on a visible node ──
	st, raw = doReq(t, userClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": doc.ID, "body": "  ilk yorum  "})
	require.Equal(t, http.StatusOK, st, "user comment on granted node: %s", raw)
	var created struct {
		Comment commentRow `json:"comment"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	assert.Equal(t, "ilk yorum", created.Comment.Body, "body is trimmed")
	assert.Equal(t, "cu@test.local", created.Comment.AuthorName, "author falls back to email")
	assert.True(t, created.Comment.CanDelete)

	// Second comment by the admin → chronological list of two.
	st, _ = doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": doc.ID, "body": "admin yorumu"})
	require.Equal(t, http.StatusOK, st)

	st, rows := listComments(t, userClient, srv.URL, doc.ID)
	require.Equal(t, http.StatusOK, st)
	require.Len(t, rows, 2)
	assert.Equal(t, "ilk yorum", rows[0].Body, "oldest first")
	assert.Equal(t, "admin yorumu", rows[1].Body)
	assert.True(t, rows[0].CanDelete, "own comment deletable")
	assert.False(t, rows[1].CanDelete, "someone else's comment not deletable for a non-admin")

	// Admin sees every row deletable (author-or-admin).
	st, adminRows := listComments(t, adminClient, srv.URL, doc.ID)
	require.Equal(t, http.StatusOK, st)
	require.Len(t, adminRows, 2)
	assert.True(t, adminRows[0].CanDelete && adminRows[1].CanDelete)

	// ── validation: empty + over-length rejected, exactly 5000 accepted ──
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": doc.ID, "body": "   "})
	assert.Equal(t, http.StatusBadRequest, st, "blank body rejected")
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": doc.ID, "body": strings.Repeat("a", 5001)})
	assert.Equal(t, http.StatusBadRequest, st, "5001-char body rejected")
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": doc.ID, "body": strings.Repeat("a", 5000)})
	assert.Equal(t, http.StatusOK, st, "5000-char body accepted")

	// ── ACL: no grant on beta → read AND write both denied ──
	st, _ = doReq(t, userClient, http.MethodGet,
		srv.URL+"/api/files/comments?node_id="+jsonInt(hidden.ID), nil)
	assert.Equal(t, http.StatusForbidden, st, "list on invisible node denied")
	st, _ = doReq(t, userClient, http.MethodPost, srv.URL+"/api/files/comments",
		map[string]any{"node_id": hidden.ID, "body": "sizamam"})
	assert.Equal(t, http.StatusForbidden, st, "comment on invisible node denied")

	// Missing node → 404.
	st, _ = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/comments?node_id=999999", nil)
	assert.Equal(t, http.StatusNotFound, st)

	// ── delete: author-or-admin ──
	adminCommentID := rows[1].ID
	st, _ = doReq(t, userClient, http.MethodDelete,
		srv.URL+"/api/files/comments/"+jsonInt(adminCommentID), nil)
	assert.Equal(t, http.StatusForbidden, st, "non-author non-admin cannot delete")

	st, _ = doReq(t, userClient, http.MethodDelete,
		srv.URL+"/api/files/comments/"+jsonInt(created.Comment.ID), nil)
	assert.Equal(t, http.StatusOK, st, "author deletes own comment")

	st, _ = doReq(t, adminClient, http.MethodDelete,
		srv.URL+"/api/files/comments/"+jsonInt(adminCommentID), nil)
	assert.Equal(t, http.StatusOK, st, "admin deletes any comment")

	// Deleting an already-deleted comment → 404 (soft-deleted rows are gone).
	st, _ = doReq(t, adminClient, http.MethodDelete,
		srv.URL+"/api/files/comments/"+jsonInt(adminCommentID), nil)
	assert.Equal(t, http.StatusNotFound, st)

	st, rows = listComments(t, userClient, srv.URL, doc.ID)
	require.Equal(t, http.StatusOK, st)
	require.Len(t, rows, 1, "only the 5000-char comment remains")

	// ── trash purge wipes the node's comment rows ──
	require.NoError(t, store.SoftDeleteNode(ctx, doc.ID))
	tsvc := trash.New(store, nil, nil)
	require.NoError(t, tsvc.PurgeOne(ctx, doc.ID))
	left, err := store.ListNodeComments(ctx, doc.ID)
	require.NoError(t, err)
	assert.Empty(t, left, "purge removed the comment rows")
	if _, err := store.GetNode(ctx, doc.ID); err == nil {
		// Node row must be hard-gone too — comments didn't outlive it.
		t.Fatalf("node row survived purge")
	}
}

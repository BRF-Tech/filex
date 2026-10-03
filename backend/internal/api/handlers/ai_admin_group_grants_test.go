package handlers_test

// The admin AI surface and a group's folder grant (PR #78). admin_grants_list
// returns people's AND groups' grants, and the two tables number their rows
// apart — the same id can be a person's grant and a group's. So a group's row
// is revoked by its own route and tool; admin_grant_revoke with a group row's
// id would take away a PERSON's grant that happens to share the number.
//
// RED PROOF (PR #78 as submitted): tools/list had no admin_group_grant_revoke
// and DELETE /api/ai/admin/grants/groups/{id} answered 404 — the only revoke
// tool, admin_grant_revoke, deletes from file_grants, so an agent handed a
// group row's id took away the person's grant with that number instead.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestAIAdmin_GroupGrantIsRevokedByItsOwnTool(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "depo", Driver: "local", MountPath: "/depo", ConfigJSON: json.RawMessage(`{"root":"/tmp/depo"}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true, RBACEnabled: true,
	})
	require.NoError(t, err)
	person, err := store.CreateFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "docs", IsDir: true, UserID: uid, Level: model.GrantViewer})
	require.NoError(t, err)
	g, err := store.CreateGroup(ctx, &model.Group{Name: "Finance"})
	require.NoError(t, err)
	groupGrant, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "docs", IsDir: true, GroupID: g.ID, Level: model.GrantViewer})
	require.NoError(t, err)
	require.Equal(t, person.ID, groupGrant.ID, "the two tables number apart: the same id names two grants")

	tok := issueToken(t, store, uid, "mcp,admin", nil)
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"admin_group_grant_revoke"`, "a group's grant has a revoke tool of its own")

	code, body = mcpPost(t, client, srv.URL+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"admin_group_grant_revoke","arguments":{"id":`+idStr(groupGrant.ID)+`}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, `"isError":true`, body)

	_, err = store.GetGroupFileGrant(ctx, groupGrant.ID)
	assert.Error(t, err, "the group's grant is revoked")
	_, err = store.GetFileGrant(ctx, person.ID)
	assert.NoError(t, err, "the person's grant with the same number is untouched")

	// The REST twin, for a caller that is not an MCP client.
	again, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "docs", IsDir: true, GroupID: g.ID, Level: model.GrantEditor})
	require.NoError(t, err)
	status, resp := fxJSON(t, "DELETE", srv.URL+"/api/ai/admin/grants/groups/"+idStr(again.ID), issueToken(t, store, uid, "admin", nil), nil)
	require.Equal(t, http.StatusOK, status, resp)
	_, err = store.GetGroupFileGrant(ctx, again.ID)
	assert.Error(t, err)
	_, err = store.GetFileGrant(ctx, person.ID)
	assert.NoError(t, err)
}

package handlers_test

// A `root:` token stays inside its root on the grant routes that name a grant
// by id — a person's (/api/files/permissions/{id}) and a group's
// (/api/files/permissions/groups/{id}). confine.Middleware rewrites path
// fields and the ?path= query; an id is not a path, so it never saw these.
//
// RED PROOF (0.49.0 for a person's grant, PR #78 for a group's): a token
// confined to alpha://Ekip, held by an account that owns alpha://Gizli too,
// changed and revoked grants on alpha://Gizli — 200 on every call.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestGrants_ARootTokenStaysInItsRootOnGrantIDs(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	for _, name := range []string{"Ekip", "Gizli"} {
		status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": name})
		require.Equal(t, http.StatusOK, status, body)
	}
	pf.StA.RBACEnabled = true
	require.NoError(t, pf.Store.UpdateStorage(ctx, pf.StA))

	// The member owns both folders, so only the token's root stands between
	// it and Gizli's grants.
	for _, p := range []string{"Ekip", "Gizli"} {
		_, err := pf.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: p, IsDir: true, UserID: pf.UserA, Level: model.GrantOwner})
		require.NoError(t, err)
	}
	colleague := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "colleague@alpha.test")
	person := func(p string) int64 {
		g, err := pf.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: p, IsDir: true, UserID: colleague, Level: model.GrantViewer})
		require.NoError(t, err)
		return g.ID
	}
	gid := newGroup(t, pf, map[string]any{"name": "Readers"})
	group := func(p string) int64 {
		g, err := pf.Store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: p, IsDir: true, GroupID: gid, Level: model.GrantViewer})
		require.NoError(t, err)
		return g.ID
	}
	outPerson, outGroup := person("Gizli"), group("Gizli")
	inPerson, inGroup := person("Ekip"), group("Ekip")

	tok := issueToken(t, pf.Store, pf.UserA, "read,write,delete,root:alpha://Ekip", nil)
	level := map[string]any{"level": model.GrantEditor}

	for what, call := range map[string][2]string{
		"a person's grant changed outside the root": {"PATCH", "/api/files/permissions/" + idStr(outPerson)},
		"a person's grant revoked outside the root": {"DELETE", "/api/files/permissions/" + idStr(outPerson)},
		"a group's grant changed outside the root":  {"PATCH", "/api/files/permissions/groups/" + idStr(outGroup)},
		"a group's grant revoked outside the root":  {"DELETE", "/api/files/permissions/groups/" + idStr(outGroup)},
	} {
		var body any
		if call[0] == "PATCH" {
			body = level
		}
		status, resp := fxJSON(t, call[0], pf.URL+call[1], tok, body)
		assert.Equal(t, http.StatusForbidden, status, "%s: %s", what, resp)
	}
	g, err := pf.Store.GetFileGrant(ctx, outPerson)
	require.NoError(t, err, "the person's grant outside the root is still there")
	assert.Equal(t, model.GrantViewer, g.Level)
	gg, err := pf.Store.GetGroupFileGrant(ctx, outGroup)
	require.NoError(t, err, "the group's grant outside the root is still there")
	assert.Equal(t, model.GrantViewer, gg.Level)

	// Inside its root the same calls still work.
	for _, call := range [][2]string{
		{"PATCH", "/api/files/permissions/" + idStr(inPerson)},
		{"PATCH", "/api/files/permissions/groups/" + idStr(inGroup)},
		{"DELETE", "/api/files/permissions/" + idStr(inPerson)},
		{"DELETE", "/api/files/permissions/groups/" + idStr(inGroup)},
	} {
		var body any
		if call[0] == "PATCH" {
			body = level
		}
		status, resp := fxJSON(t, call[0], pf.URL+call[1], tok, body)
		assert.Equal(t, http.StatusOK, status, "%s %s: %s", call[0], call[1], resp)
	}
}

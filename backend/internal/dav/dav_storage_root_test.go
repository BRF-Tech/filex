package dav

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// A whole storage given to a person or a group (#117) is a grant on the
// storage root (path prefix ""). WebDAV writes no rule of its own for it: it
// asks the same acl.Set every door asks. This pins that a mounted drive
// agrees with the web interface - the storage appears in /dav/ once given,
// every folder in it is reachable without a grant of its own, the level is
// the grant's, and a group's grant reaches its members the same way.
func TestRBACStorageRootGrantReachesTheWholeStorage(t *testing.T) {
	ha := newHarness(t)
	ctx := context.Background()
	st := ha.addStorage(t, "shared", false, true)
	uid := dbtest.SeedUserWithRole(t, ha.store, "member@example.com", "MemberPass!1", model.RoleUser)
	const who, pw = "member@example.com", "MemberPass!1"

	for _, dir := range []string{"/dav/shared/Team", "/dav/shared/Team/deep"} {
		resp := ha.req(t, "MKCOL", dir, ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode, dir)
	}
	resp := ha.req(t, http.MethodPut, "/dav/shared/Team/deep/plan.txt", ha.adminEmail, ha.adminPass, "the deep plan", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// No grant: the drive does not list it.
	resp = ha.req(t, "PROPFIND", "/dav/", who, pw, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.NotContains(t, bodyString(t, resp), "shared")

	root, err := ha.store.CreateFileGrant(ctx, &model.FileGrant{
		StorageID: st.ID, UserID: uid, PathPrefix: "", IsDir: true, Level: model.GrantViewer,
	})
	require.NoError(t, err)

	resp = ha.req(t, "PROPFIND", "/dav/", who, pw, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "shared", "given the storage, the drive lists it")
	resp = ha.req(t, http.MethodGet, "/dav/shared/Team/deep/plan.txt", who, pw, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "a folder two levels down, with no grant of its own")
	require.Equal(t, "the deep plan", bodyString(t, resp))
	resp = ha.req(t, http.MethodPut, "/dav/shared/Team/new.txt", who, pw, "x", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "the storage was given as viewer")

	// Taken back, it is gone again; a group's grant brings it back for a member.
	require.NoError(t, ha.store.DeleteFileGrant(ctx, root.ID))
	resp = ha.req(t, http.MethodGet, "/dav/shared/Team/deep/plan.txt", who, pw, "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	g, err := ha.store.CreateGroup(ctx, &model.Group{Name: "Whole storage editors"})
	require.NoError(t, err)
	require.NoError(t, ha.store.AddGroupMember(ctx, g.ID, uid))
	_, err = ha.store.CreateGroupFileGrant(ctx, &model.FileGrant{
		StorageID: st.ID, GroupID: g.ID, PathPrefix: "", IsDir: true, Level: model.GrantEditor,
	})
	require.NoError(t, err)
	resp = ha.req(t, http.MethodPut, "/dav/shared/Team/deep/notes.txt", who, pw, "notes", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "the group was given the storage as editor")
}

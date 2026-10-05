package handlers_test

// The doors 0.52 added for LDAP directory sync (GitHub PR #90): starting a
// sync, and keeping a group whose directory group is gone as a filex group.
// A run opens accounts, switches them off and moves memberships - through a
// group that gives Administrator, administrators too - so a person signed in
// to the admin panel starts it, never an API key; a folder-confined key does
// not reach /api/admin at all.

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestDirectorySyncDoors_AKeyCannotStartThem(t *testing.T) {
	f := newTokenGateFixture(t)
	ctx := context.Background()
	key := testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp,admin")
	confined := testutil.NewAPIToken(t, f.store, f.adminID, "admin,root:main://projects/acme")

	g, err := f.store.CreateGroup(ctx, &model.Group{Name: "Finance",
		Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: "cn=finance,ou=groups,dc=example,dc=com"}}})
	require.NoError(t, err)
	require.NoError(t, f.store.SetGroupDirectory(ctx, g.ID, "ldap:u-finance", "finance", model.GroupDirectoryRemoved))
	detach := "/api/admin/groups/" + strconv.FormatInt(g.ID, 10) + "/detach"

	for _, tok := range []string{key, confined} {
		code, body := f.adminCall(t, tok, http.MethodPost, "/api/admin/auth-providers/ldap/sync", "")
		assert.Equal(t, http.StatusForbidden, code, "sync: %s", body)
		code, body = f.adminCall(t, tok, http.MethodPost, detach, "")
		assert.Equal(t, http.StatusForbidden, code, "detach: %s", body)
	}
	code, body := f.adminCall(t, key, http.MethodPost, "/api/admin/auth-providers/ldap/sync", "")
	assert.Contains(t, body, "session_required", "sync: the refusal says why (%d)", code)
	code, body = f.adminCall(t, key, http.MethodPost, detach, "")
	assert.Contains(t, body, "session_required", "detach: the refusal says why (%d)", code)

	got, err := f.store.GetGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, "ldap:u-finance", got.DirectoryID, "nothing was detached by a key")

	// The administrator signed in to the panel.
	code, body = f.adminCall(t, "", http.MethodPost, detach, "")
	require.Equal(t, http.StatusOK, code, body)
	got, err = f.store.GetGroup(ctx, g.ID)
	require.NoError(t, err)
	assert.Empty(t, got.DirectoryID)
	assert.Empty(t, got.Links, "its dead LDAP link went with it")
	code, body = f.adminCall(t, "", http.MethodPost, "/api/admin/auth-providers/ldap/sync", "")
	assert.NotEqual(t, http.StatusForbidden, code, "a session reaches the sync door: %s", body)
}

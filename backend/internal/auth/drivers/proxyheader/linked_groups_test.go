package proxyheader

// A filex group linked to a group in the proxy's roles header follows the
// header, as it does an OIDC sign-in (auth.RecordSignInGroups). The header
// proxy records its groups whenever the roles header is there - allowed_groups
// set or not (signin_groups_test.go) - and only when the set changed (it runs
// on every request); the sync goes with that recording.
//
// RED PROOF (PR #78 as submitted): only the OIDC driver kept linked groups in
// step; the header proxy recorded the roles header and nothing more.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestSignIn_LinkedGroupsFollowTheHeader(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, map[string]any{"allowed_groups": "staff"})
	staff, err := store.CreateGroup(ctx, &model.Group{Name: "Staff",
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "staff"}}})
	require.NoError(t, err)

	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "guests, staff"))
	require.NoError(t, err)
	ms, err := store.ListGroupMembers(ctx, staff.ID)
	require.NoError(t, err)
	require.Len(t, ms, 1, "the header's staff put her in the linked filex group")
	assert.Equal(t, u.ID, ms[0].UserID)

	_, err = d.Authenticate(withRoles("files.example.com", "ayse@example.com", "guests"))
	require.NoError(t, err, "an existing account is not judged by allowed_groups again")
	ms, err = store.ListGroupMembers(ctx, staff.ID)
	require.NoError(t, err)
	assert.Empty(t, ms, "out of the header's group, out of the filex group")
}

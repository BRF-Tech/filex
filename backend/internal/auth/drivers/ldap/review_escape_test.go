package ldap

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GitHub PR #90 review (0.52): group_filter's %s (the person's DN) and %u
// (what they typed) are escaped (RFC 4515) before they reach the directory,
// so a name such as `x*)(uid=*` cannot widen the group search into one that
// lists every group - and with it a group that makes administrators.
func TestLDAPGroups_GroupSearchIsEscaped(t *testing.T) {
	ctx := context.Background()
	gc := &groupConn{
		fakeConn: &fakeConn{
			entries:      []*goldap.Entry{person(`cn=x*)(uid=\2a,dc=example,dc=com`, "x@example.com")},
			userPassword: "pw",
		},
	}
	store := newStore(t)
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{
		"url": "ldaps://directory.invalid", "base_dn": "dc=example,dc=com",
		"group_filter": "(|(member=%s)(memberUid=%u))",
	}))
	d.dial = func(context.Context) (conn, error) { return gc, nil }

	_, _, err := d.Login(ctx, "x*)(uid=*", "pw")
	require.NoError(t, err)
	last := gc.searches[len(gc.searches)-1]
	assert.Equal(t, `(|(member=cn=x\2a\29\28uid=\5c2a,dc=example,dc=com)(memberUid=x\2a\29\28uid=\2a))`, last.Filter)
}

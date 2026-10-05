package authsetup

import (
	"testing"

	"github.com/stretchr/testify/assert"

	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
)

// A tenant's own LDAP instance is told which tenant it belongs to: its
// directory sync opens groups there and reaches that tenant's accounts only
// (GitHub PR #90 review). Without it an unpinned tenant directory opened its
// groups install-wide, in the platform's own list.
func TestTenantOwned_LDAPKnowsItsTenant(t *testing.T) {
	cfg := map[string]any{}
	tenantOwned("ldap", 7, func(map[string]any) {})(cfg)
	assert.EqualValues(t, int64(7), cfg[authldap.OwnerTenantKey])

	platform := map[string]any{}
	tenantOwned("ldap", 0, func(map[string]any) {})(platform)
	_, has := platform[authldap.OwnerTenantKey]
	assert.False(t, has, "the platform's own instance belongs to no tenant")
}

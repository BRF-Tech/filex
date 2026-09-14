package oidc

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The admin mapping used to be read once, when the account was created: a
// person added to the admin group in the IdP after their first filex login
// never became an admin, and — the half that matters — a person REMOVED from
// it kept administering filex for good. The mapping now holds on every
// sign-in, with two accounts it will never demote: the one filex was set up
// with (the recovery login's account) and the last admin standing.
func TestMappedRoleOnLogin(t *testing.T) {
	cases := []struct {
		name       string
		current    string
		claimAdmin bool
		protected  bool
		want       string
	}{
		{"granted in the IdP after the first login → promoted", model.RoleUser, true, false, model.RoleAdmin},
		{"removed from the admin group → demoted", model.RoleAdmin, false, false, model.RoleUser},
		{"still in the group → unchanged", model.RoleAdmin, true, false, model.RoleAdmin},
		{"never an admin, still not → unchanged", model.RoleUser, false, false, model.RoleUser},
		{"a viewer set by hand is not touched without the group", model.RoleViewer, false, false, model.RoleViewer},
		{"a viewer in the admin group is promoted", model.RoleViewer, true, false, model.RoleAdmin},
		{"the setup admin / last admin is never demoted", model.RoleAdmin, false, true, model.RoleAdmin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mappedRoleOnLogin(c.current, c.claimAdmin, model.RoleUser, c.protected); got != c.want {
				t.Fatalf("mappedRoleOnLogin(%q, admin=%v, protected=%v) = %q, want %q", c.current, c.claimAdmin, c.protected, got, c.want)
			}
		})
	}
}

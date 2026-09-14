package server

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/auth"
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func driverNames(ds []auth.LoginDriver) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if n, ok := d.(interface{ Name() string }); ok {
			out = append(out, n.Name())
		}
	}
	return out
}

// Recovery sign-in joins the chain exactly when password sign-in is off and
// the operator has not turned recovery off — and it goes last, so a directory
// driver still judges its own accounts first.
func TestWithRecoveryLogin(t *testing.T) {
	_, store := testutil.NewTestDB(t)

	cases := []struct {
		name    string
		drivers []auth.LoginDriver
		enabled bool
		want    []string
		added   bool
	}{
		{"oidc only: nothing judges a password", nil, true, []string{"recovery"}, true},
		{"ldap only", []auth.LoginDriver{authldap.New(store)}, true, []string{"ldap", "recovery"}, true},
		{"local enabled: every local account already signs in", []auth.LoginDriver{authlocal.New(store)}, true, []string{"local"}, false},
		{"turned off by the operator", nil, false, []string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, added := withRecoveryLogin(c.drivers, c.enabled, store)
			if added != c.added {
				t.Fatalf("added = %v, want %v", added, c.added)
			}
			names := driverNames(got)
			if len(names) != len(c.want) {
				t.Fatalf("chain = %v, want %v", names, c.want)
			}
			for i := range names {
				if names[i] != c.want[i] {
					t.Fatalf("chain = %v, want %v", names, c.want)
				}
			}
		})
	}
}

// First run records the account it creates as the one recovery answers for —
// by id, so changing its e-mail later does not change who that is.
func TestFirstRun_RecordsTheBootstrapAdministrator(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	fr, err := FirstRun(ctx, store, t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	id, err := authlocal.BootstrapAdminID(ctx, store)
	if err != nil {
		t.Fatalf("read bootstrap administrator: %v", err)
	}
	u, err := store.GetUser(ctx, id)
	if err != nil || u == nil {
		t.Fatalf("recorded id %d does not name a user: %v", id, err)
	}
	if u.Email != fr.AdminEmail {
		t.Fatalf("recorded %s, first run created %s", u.Email, fr.AdminEmail)
	}
}

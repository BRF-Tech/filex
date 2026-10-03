package proxyheader

// The first-login rule for a person the proxy names: auto_create (the older
// auto_provision still reads), allowed_groups judged against the roles header,
// and the audit row that says why somebody was refused.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func withRoles(host, user, roles string) *http.Request {
	r := phRequest(host, user)
	if roles != "" {
		r.Header.Set("X-Auth-Roles", roles)
	}
	return r
}

func auditReasons(t *testing.T, store db.Store) []string {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			out = append(out, r.Metadata["reason"].(string))
		}
	}
	return out
}

func TestFirstLogin_DefaultOpensAnAccount(t *testing.T) {
	d, store := initDriverWithStore(t, nil)
	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "staff"))
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
	assert.Empty(t, auditReasons(t, store))
}

func TestFirstLogin_AutoCreateOffRefusesAndAudits(t *testing.T) {
	for _, key := range []string{"auto_create", "auto_provision"} {
		t.Run(key, func(t *testing.T) {
			d, store := initDriverWithStore(t, map[string]any{key: false})
			u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "staff"))
			assert.ErrorIs(t, err, auth.ErrUnauthorized, "the same answer as an unknown header")
			assert.Nil(t, u)
			_, gerr := store.GetUserByEmail(context.Background(), "ayse@example.com")
			assert.Error(t, gerr, "nothing may be created")
			assert.Equal(t, []string{auth.ReasonAutoCreateOff}, auditReasons(t, store))
		})
	}

	// An account that exists is admitted with auto_create off.
	d, store := initDriverWithStore(t, map[string]any{"auto_create": false})
	_, err := store.CreateUser(context.Background(), "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", ""))
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
}

func TestFirstLogin_AllowedGroupsAreTheDoor(t *testing.T) {
	cfg := func() map[string]any { return map[string]any{"allowed_groups": "staff, Yöneticiler"} }

	d, store := initDriverWithStore(t, cfg())
	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "guests, STAFF"))
	require.NoError(t, err)
	groups, err := store.ListUserSSOGroups(context.Background(), u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"guests", "STAFF"}, groups)

	d2, _ := initDriverWithStore(t, cfg())
	_, err = d2.Authenticate(withRoles("files.example.com", "can@example.com", "YÖNETİCİLER"))
	require.NoError(t, err, "the Turkish dotted/dotless I is one letter")

	d3, store3 := initDriverWithStore(t, cfg())
	u3, err := d3.Authenticate(withRoles("files.example.com", "bob@example.com", "guests"))
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Nil(t, u3)
	_, gerr := store3.GetUserByEmail(context.Background(), "bob@example.com")
	assert.Error(t, gerr)
	assert.Equal(t, []string{auth.ReasonGroupNotAllowed}, auditReasons(t, store3))

	d4, _ := initDriverWithStore(t, cfg())
	_, err = d4.Authenticate(withRoles("files.example.com", "eve@example.com", ""))
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "no roles header at all is outside every group")
}

func TestFirstLogin_ProbeSaysWhatTheConfigurationDoes(t *testing.T) {
	step := func(cfg map[string]any) auth.ProbeCheck {
		base := map[string]any{"trusted_proxies": "127.0.0.0/8"}
		for k, v := range cfg {
			base[k] = v
		}
		for _, c := range (&Driver{}).Probe(context.Background(), base, nil) {
			if strings.HasPrefix(c.ID, "first_login") {
				return c
			}
		}
		t.Fatal("no first_login step")
		return auth.ProbeCheck{}
	}
	assert.Equal(t, "first_login_open", step(nil).ID)
	assert.Equal(t, "first_login_closed", step(map[string]any{"auto_create": false}).ID)
	assert.Equal(t, "first_login_groups", step(map[string]any{"allowed_groups": "a"}).ID)
}

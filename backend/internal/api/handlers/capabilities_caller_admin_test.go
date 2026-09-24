package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// caller_admin is how the explorer decides between the owner's two honest
// answers for an optional service that is not configured (2026-09-21):
// "disabled with a reason for administrators, hidden for everybody else". An
// administrator is shown "Open with ONLYOFFICE — set it up under External
// services"; everybody else is not offered a button that can never work.
//
// ⚠ It has to be FALSE for everyone who cannot act on the sentence, or a
// viewer is told to go and configure a server they cannot reach, and it has to
// be false for an anonymous caller (the share and drop pages fetch this route
// before anybody signs in).
func TestCapabilities_CallerAdmin(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	adminOf := func(t *testing.T, c *http.Client) any {
		t.Helper()
		res, err := c.Get(srv.URL + "/api/files/capabilities")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out["caller_admin"]
	}

	t.Run("anonymous: false", func(t *testing.T) {
		require.Equal(t, false, adminOf(t, &http.Client{}))
	})

	t.Run("administrator session: true", func(t *testing.T) {
		email, pw := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, pw)
		require.Equal(t, true, adminOf(t, client))
	})

	t.Run("regular account: false", func(t *testing.T) {
		testutil.SeedRegularUser(t, store, "viewer@example.com", "Passw0rd!long")
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		testutil.LoginAs(t, srv, c, "viewer@example.com", "Passw0rd!long")
		require.Equal(t, false, adminOf(t, c))
	})
}

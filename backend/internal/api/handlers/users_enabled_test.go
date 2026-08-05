package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// loginStatus posts credentials and returns the status. testutil.LoginAs
// t.Fatalf's on anything but 200, which is the wrong shape for asserting
// that a login is REFUSED.
func loginStatus(t *testing.T, client *http.Client, baseURL, email, password string) int {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	require.NoError(t, err)
	resp, err := client.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestAdminUsers_EnableDisable — cutting a user's file access without
// deleting the account. olivov has the mail-side equivalent (Stalwart role
// emptied) and needed the file-side one (G4, 2026-08-04).
func TestAdminUsers_EnableDisable(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	const targetEmail, targetPass = "kisi@test.local", "TargetPass!1"
	testutil.SeedRegularUser(t, store, targetEmail, targetPass)
	target, err := store.GetUserByEmail(ctx, targetEmail)
	require.NoError(t, err)

	// login works while enabled
	loginAs := func(t *testing.T) int {
		t.Helper()
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		return loginStatus(t, &http.Client{Jar: jar}, srv.URL, targetEmail, targetPass)
	}

	t.Run("enabled by default", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodGet,
			fmt.Sprintf("%s/api/admin/users/%d", srv.URL, target.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Equal(t, true, body["enabled"], "existing users must read back enabled")
		require.Equal(t, http.StatusOK, loginAs(t))
	})

	t.Run("disable blocks login", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPatch,
			fmt.Sprintf("%s/api/admin/users/%d", srv.URL, target.ID),
			map[string]any{"enabled": false})
		require.Equal(t, http.StatusOK, status, "%v", body)

		status, body = doJSON(t, client, http.MethodGet,
			fmt.Sprintf("%s/api/admin/users/%d", srv.URL, target.ID), nil)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, false, body["enabled"])

		// 403, not 401: the credentials are valid, the account isn't allowed
		// in — the same shape a suspended tenant already gets.
		require.Equal(t, http.StatusForbidden, loginAs(t),
			"a disabled user must not be able to start a session")
	})

	t.Run("files are preserved", func(t *testing.T) {
		// The account and everything hanging off it must survive; disable is
		// not a soft delete.
		u, err := store.GetUserByEmail(ctx, targetEmail)
		require.NoError(t, err)
		require.Equal(t, target.ID, u.ID)
	})

	t.Run("re-enable restores login", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPatch,
			fmt.Sprintf("%s/api/admin/users/%d", srv.URL, target.ID),
			map[string]any{"enabled": true})
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Equal(t, http.StatusOK, loginAs(t))
	})
}

// TestAdminUsers_DisableKillsLiveAccess — disabling has to cut access the
// user ALREADY holds, not just refuse the next login. Two ways in survive a
// login gate on their own:
//
//   - a session cookie minted before the switch (auth.Middleware trusts
//     whatever the driver resolves)
//   - an API token (MiddlewareWithToken looks the user up and admits them)
//
// Either one leaves "disabled" meaning "disabled tomorrow".
func TestAdminUsers_DisableKillsLiveAccess(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, password)

	const targetEmail, targetPass = "kisi2@test.local", "TargetPass!1"
	testutil.SeedRegularUser(t, store, targetEmail, targetPass)
	target, err := store.GetUserByEmail(ctx, targetEmail)
	require.NoError(t, err)

	// A live session, established while enabled.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	sessionClient := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, sessionClient, targetEmail, targetPass)

	// A live API token.
	rawToken := "enabled-test-token-" + targetEmail
	_, err = store.CreateAPIToken(ctx, &model.APIToken{
		UserID: target.ID, Label: "enabled-test", TokenHash: apitoken.HashToken(rawToken),
	})
	require.NoError(t, err)

	probe := func(t *testing.T, c *http.Client, token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/files/quota/me", nil)
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusOK, probe(t, sessionClient, ""))
	require.Equal(t, http.StatusOK, probe(t, &http.Client{}, rawToken))

	status, body := doJSON(t, adminClient, http.MethodPatch,
		fmt.Sprintf("%s/api/admin/users/%d", srv.URL, target.ID),
		map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", body)

	require.Equal(t, http.StatusForbidden, probe(t, sessionClient, ""),
		"an existing session must stop working once the account is disabled")
	require.Equal(t, http.StatusForbidden, probe(t, &http.Client{}, rawToken),
		"an API token must stop working once its owner is disabled")
}

// TestAdminUsers_CannotDisableLastAdmin — disabling the final admin locks
// everyone out of the admin surface just as effectively as deleting them,
// which Delete and Update already refuse.
func TestAdminUsers_CannotDisableLastAdmin(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	status, body := doJSON(t, client, http.MethodPatch,
		fmt.Sprintf("%s/api/admin/users/%d", srv.URL, admin.ID),
		map[string]any{"enabled": false})
	require.Equal(t, http.StatusConflict, status, "%v", body)
}

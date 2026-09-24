package handlers_test

// An install with no encryption key cannot issue access keys, and says so
// with a CODE — the fix ("set FILEX_SECRET_KEY") goes to an administrator
// only.
//
// ⚠⚠ QA, 2026-09-21: the connections panel printed "protocolauth: no secret
// key configured; set FILEX_SECRET_KEY to issue S3 access keys" to a regular
// user who pressed "Create key" — an environment variable, in English, to a
// person who can do nothing with it.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestS3KeyWithoutSecretKey_CodeForEverybody_FixForTheAdmin(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServerWith(t, func(c *config.Config) {}, func(d *api.Deps) {
		box, err := secretbox.New("")
		require.NoError(t, err)
		d.ProtocolAuth = protocolauth.New(d.Store, d.ACL, d.Cfg.MultiTenant)
		d.ProtocolAuth.Secrets = box
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)
	testutil.SeedRegularUser(t, store, "keys-user@test.local", "UserPass1!")
	userClient := jarClient(t)
	testutil.LoginAs(t, srv, userClient, "keys-user@test.local", "UserPass1!")

	code, body := postJSON(t, userClient, srv.URL+"/api/auth/s3-keys", map[string]any{"label": "rclone"})
	require.Equal(t, http.StatusServiceUnavailable, code, "%v", body)
	assert.Equal(t, "no_secret_key", body["error"])
	assert.NotContains(t, body, "admin_hint", "a regular user is not told how the server is configured")
	for _, v := range body {
		assert.NotContains(t, v, "FILEX_", "no environment variable reaches a regular user")
	}

	code, body = postJSON(t, adminClient, srv.URL+"/api/auth/s3-keys", map[string]any{"label": "rclone"})
	require.Equal(t, http.StatusServiceUnavailable, code, "%v", body)
	assert.Equal(t, "no_secret_key", body["error"])
	assert.Contains(t, body["admin_hint"], "FILEX_SECRET_KEY", "the administrator is told what to set")
}

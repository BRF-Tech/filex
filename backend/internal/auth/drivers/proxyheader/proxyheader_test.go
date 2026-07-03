package proxyheader

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// initDriver builds a Driver bound to an in-memory store with the supplied
// config overlaid on top of the minimum-required trusted_proxies.
func initDriver(t *testing.T, cfg map[string]any) *Driver {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	d := New(store)
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["trusted_proxies"]; !ok {
		cfg["trusted_proxies"] = []string{"127.0.0.0/8", "::1/128"}
	}
	require.NoError(t, d.Init(context.Background(), cfg))
	return d
}

// TestInit_RequiresTrustedProxies — empty trusted_proxies is a hard error.
func TestInit_RequiresTrustedProxies(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := New(store)
	err := d.Init(context.Background(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trusted_proxies")
}

// TestInit_NilStore returns an error.
func TestInit_NilStore(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), map[string]any{
		"trusted_proxies": []string{"127.0.0.1/32"},
	})
	require.Error(t, err)
}

// TestInit_BareIPAccepted ensures plain "127.0.0.1" entries are accepted
// (auto-suffixed to /32).
func TestInit_BareIPAccepted(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := New(store)
	err := d.Init(context.Background(), map[string]any{
		"trusted_proxies": []string{"127.0.0.1", "10.0.0.0/8"},
	})
	require.NoError(t, err)
	assert.Len(t, d.trustedProxies, 2)
}

// TestInit_InvalidCIDR fails fast.
func TestInit_InvalidCIDR(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := New(store)
	err := d.Init(context.Background(), map[string]any{
		"trusted_proxies": []string{"not-an-ip"},
	})
	require.Error(t, err)
}

// TestInit_AnyInterfaceList — yaml may decode lists as []any.
func TestInit_AnyInterfaceList(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := New(store)
	err := d.Init(context.Background(), map[string]any{
		"trusted_proxies": []any{"127.0.0.1/32", "10.0.0.0/8"},
	})
	require.NoError(t, err)
	assert.Len(t, d.trustedProxies, 2)
}

// TestAuthenticate_UntrustedIP returns ErrUnauthorized even with valid
// X-Auth-User — this is the core security guarantee.
func TestAuthenticate_UntrustedIP(t *testing.T) {
	d := initDriver(t, nil)
	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "8.8.8.8:54321"
	r.Header.Set("X-Auth-User", "evil@attacker")
	r.Header.Set("X-Auth-Roles", "admin")

	_, err := d.Authenticate(r)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
}

// TestAuthenticate_NoHeader from a trusted IP still falls through with
// ErrUnauthorized — empty identity is not a successful auth.
func TestAuthenticate_NoHeader(t *testing.T) {
	d := initDriver(t, nil)
	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:1111"

	_, err := d.Authenticate(r)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
}

// TestAuthenticate_AutoProvision creates a user on first sight.
func TestAuthenticate_AutoProvision(t *testing.T) {
	d := initDriver(t, nil)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:2222"
	r.Header.Set("X-Auth-User", "alice")
	r.Header.Set("X-Auth-Email", "Alice@Example.Com")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "alice@example.com", u.Email, "email must be lowercased")
	assert.Equal(t, model.RoleUser, u.Role)
}

// TestAuthenticate_AutoProvisionDisabled — when auto_provision=false and
// the user does not yet exist in the DB the driver must fall through.
func TestAuthenticate_AutoProvisionDisabled(t *testing.T) {
	d := initDriver(t, map[string]any{
		"auto_provision": false,
	})

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:3333"
	r.Header.Set("X-Auth-User", "ghost")
	r.Header.Set("X-Auth-Email", "ghost@nowhere")

	_, err := d.Authenticate(r)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
}

// TestAuthenticate_RoleAdmin elevates a user when X-Auth-Roles contains
// the configured admin role.
func TestAuthenticate_RoleAdmin(t *testing.T) {
	d := initDriver(t, nil)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:4444"
	r.Header.Set("X-Auth-User", "boss")
	r.Header.Set("X-Auth-Email", "boss@example.com")
	r.Header.Set("X-Auth-Roles", "viewer, ADMIN ,editor") // case + whitespace

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role)
}

// TestAuthenticate_CustomHeaderNames honors header_user / header_email
// overrides (e.g. Cloudflare Access ships Cf-Access-Authenticated-User-Email).
func TestAuthenticate_CustomHeaderNames(t *testing.T) {
	d := initDriver(t, map[string]any{
		"header_user":  "Cf-User",
		"header_email": "Cf-Access-Authenticated-User-Email",
		"header_roles": "Cf-Roles",
	})

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("Cf-User", "u-123")
	r.Header.Set("Cf-Access-Authenticated-User-Email", "user@cf.example")
	r.Header.Set("Cf-Roles", "admin")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, "user@cf.example", u.Email)
	assert.Equal(t, model.RoleAdmin, u.Role)
}

// TestAuthenticate_FallbackEmailFromUser — when X-Auth-Email is missing
// but X-Auth-User looks like an email, use it.
func TestAuthenticate_FallbackEmailFromUser(t *testing.T) {
	d := initDriver(t, nil)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:6666"
	r.Header.Set("X-Auth-User", "User@Example.com")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", u.Email)
}

// TestAuthenticate_FallbackEmailSynthesized — non-email user id is
// stitched into a stable @proxy.local address.
func TestAuthenticate_FallbackEmailSynthesized(t *testing.T) {
	d := initDriver(t, nil)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:7777"
	r.Header.Set("X-Auth-User", "alice")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, "alice@proxy.local", u.Email)
}

// TestAuthenticate_ExistingUserReused looks up by email and does NOT
// create a duplicate row.
func TestAuthenticate_ExistingUserReused(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), map[string]any{
		"trusted_proxies": []string{"127.0.0.1/32"},
	}))

	// Pre-create the user with admin role.
	existing, err := store.CreateUser(context.Background(), "preexists@example.com", "", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "127.0.0.1:8888"
	r.Header.Set("X-Auth-User", "preexists@example.com")
	// Note: header has NO admin role — but DB role wins because we don't update.
	r.Header.Set("X-Auth-Roles", "viewer")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, existing.ID, u.ID, "should re-use existing row, not create a new one")
	assert.Equal(t, model.RoleAdmin, u.Role, "existing role preserved")
}

// TestAuthenticate_IPv6Trusted — ::1 must work end-to-end.
func TestAuthenticate_IPv6Trusted(t *testing.T) {
	d := initDriver(t, nil)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.RemoteAddr = "[::1]:9999"
	r.Header.Set("X-Auth-User", "v6user")
	r.Header.Set("X-Auth-Email", "v6@example.com")

	u, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, "v6@example.com", u.Email)
}

// TestCapabilities — header-proxy advertises no in-band sign-in.
func TestCapabilities(t *testing.T) {
	d := &Driver{}
	caps := d.Capabilities()
	assert.False(t, caps.SignIn)
	assert.False(t, caps.Logout)
	assert.False(t, caps.ChangePassword)
	assert.False(t, caps.Register)
}

// TestName ensures the registered name matches what's documented.
func TestName(t *testing.T) {
	d := &Driver{}
	assert.Equal(t, "proxy-header", d.Name())
}

// TestRegistered confirms the init() block registered the factory under
// "proxy-header" so config-driven wiring (cfg.Auth.Drivers) can find it.
func TestRegistered(t *testing.T) {
	d, err := auth.Get("proxy-header")
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "proxy-header", d.Name())
}

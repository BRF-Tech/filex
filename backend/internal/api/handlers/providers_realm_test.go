package handlers_test

// A tenant's realm through the lifecycle API (docs/MULTI-TENANCY.md, Realms):
// given at creation — the slug by default — validated and unique, and never
// changed afterwards.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestProviders_Realm(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// No realm given: the slug.
	code, acme := do("POST", "/api/admin/providers", map[string]any{"slug": "acme"})
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "acme", acme["realm"])
	acmeID := itoa(int64(acme["id"].(float64)))

	// A realm given: that one, folded.
	code, beta := do("POST", "/api/admin/providers", map[string]any{"slug": "beta-slug", "realm": "Beta"})
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "beta", beta["realm"])

	// Refused: taken, reserved, the wrong shape — and a slug that cannot be a
	// realm when no realm is given.
	code, out := do("POST", "/api/admin/providers", map[string]any{"slug": "other", "realm": "acme"})
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "realm_taken", out["error"])
	code, out = do("POST", "/api/admin/providers", map[string]any{"slug": "other", "realm": "admin"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "realm_reserved", out["error"])
	for _, bad := range []string{"acme/x", "a.b", "a@b", `a\b`, "-a"} {
		code, out = do("POST", "/api/admin/providers", map[string]any{"slug": "other", "realm": bad})
		require.Equal(t, http.StatusBadRequest, code, bad)
		require.Equal(t, "realm_invalid", out["error"], bad)
	}
	code, out = do("POST", "/api/admin/providers", map[string]any{"slug": "Has_Underscore"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "realm", out["field"])

	// ⚠ Never changed: another realm is refused, and even a slug change
	// leaves it alone.
	code, out = do("PATCH", "/api/admin/providers/"+acmeID, map[string]any{"realm": "acme2"})
	require.Equal(t, http.StatusBadRequest, code)
	require.Equal(t, "realm_immutable", out["error"])
	code, out = do("PATCH", "/api/admin/providers/"+acmeID, map[string]any{"realm": "ACME", "name": "Acme Inc", "slug": "acme-inc"})
	require.Equal(t, http.StatusOK, code, "sending the same realm back is not a change")
	require.Equal(t, "acme", out["realm"])
	require.Equal(t, "acme-inc", out["slug"])
	p, err := store.GetProviderByRealm(context.Background(), "acme")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "acme-inc", p.Slug)

	// Created as the platform's own tenant: no realm.
	code, plat := do("POST", "/api/admin/providers", map[string]any{"slug": "platform-two", "is_supertenant": true})
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "", plat["realm"])
	require.Equal(t, true, plat["is_supertenant"])
}

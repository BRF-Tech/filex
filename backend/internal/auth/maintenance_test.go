package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// Maintenance mode holds a tenant's account off on every request, not only at
// sign-in: a session or API key opened while the mode was on would otherwise
// keep working with no tenant scope at all (#167). The platform's own
// accounts, and an account with no provider, pass.
func TestMaintenanceLockout_HoldsATenantsAccountOff(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	require.NoError(t, err)
	super, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NotNil(t, super)

	reached := false
	h := MaintenanceLockout(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))
	serve := func(u *model.User) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
		if u != nil {
			req = req.WithContext(WithUser(req.Context(), u))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := serve(&model.User{ID: 7, Email: "admin@acme.test", Role: model.RoleAdmin, Enabled: true, ProviderID: &acme.ID})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached, "a tenant's administrator never reaches the handler")
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, SSOReasonMaintenance, body["reason"])

	assert.Equal(t, http.StatusNoContent, serve(&model.User{ID: 1, Role: model.RoleAdmin, Enabled: true, ProviderID: &super.ID}).Code,
		"the platform's own account passes")
	assert.True(t, reached)
	assert.Equal(t, http.StatusNoContent, serve(&model.User{ID: 2, Role: model.RoleAdmin, Enabled: true}).Code,
		"an account with no provider (the bootstrap administrator of an old install) passes")
	assert.Equal(t, http.StatusNoContent, serve(nil).Code, "an anonymous request is not this gate's business")
}

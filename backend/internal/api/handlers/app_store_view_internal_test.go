package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// The store screen's settings are per tenant in multi-tenant mode (#162):
// a person's request is answered from their own tenant's settings, a scope
// that cannot be resolved from none, and the administrator names the tenant
// whose settings they change.
func TestStoreScreen_TheScopeIsTheRequestsTenant(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/app-store", nil)
	if multi, _, scope := viewScope(r); multi || scope != appstore.ScopeDefault {
		t.Fatalf("single-tenant: %v %q", multi, scope)
	}
	five := r.WithContext(tenant.WithScope(r.Context(), &tenant.Scope{ProviderID: 5}))
	if multi, id, scope := viewScope(five); !multi || id != 5 || scope != "t5" {
		t.Fatalf("tenant 5: %v %d %q", multi, id, scope)
	}
	deny := r.WithContext(tenant.WithScope(r.Context(), tenant.DenyAll))
	if _, _, scope := viewScope(deny); scope != "t0" {
		t.Fatalf("an unresolved tenant reads %q (no tenant has t0)", scope)
	}
	// The administrator: their own tenant by default, another by its id.
	if _, id, scope, ok := settingsScope(five, ""); !ok || id != 5 || scope != "t5" {
		t.Fatalf("own: %d %q %v", id, scope, ok)
	}
	if _, id, scope, ok := settingsScope(five, "8"); !ok || id != 8 || scope != "t8" {
		t.Fatalf("tenant 8: %d %q %v", id, scope, ok)
	}
	if _, _, _, ok := settingsScope(five, "eight"); ok {
		t.Fatal("a tenant that is not an id was taken")
	}
	// Single-tenant: the tenant field is ignored.
	if multi, _, scope, ok := settingsScope(r, "8"); multi || !ok || scope != appstore.ScopeDefault {
		t.Fatalf("single-tenant with a tenant: %v %q %v", multi, scope, ok)
	}
}

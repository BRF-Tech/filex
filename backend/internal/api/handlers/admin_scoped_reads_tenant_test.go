package handlers_test

// The second half of the admin audit: the surfaces that are instance-wide but
// are NOT simply "supertenant only".
//
// Three different answers turned out to be right, and the difference matters
// more than the mechanism does:
//
//   - GATE (403 supertenant_only) — the surface drives one global process or
//     one global row and has no per-tenant form at all: webhooks, replication,
//     the search index, the job queue, /metrics. A tenant admin adding a
//     webhook target would have received every OTHER tenant's event stream, so
//     "let tenants have their own" is a feature with a schema change behind it,
//     not a gate to leave off.
//   - SCOPE (filter to the caller's storages) — the surface is a legitimate
//     tenant feature that was merely unfiltered: duplicates, sync-run history,
//     the dashboard counters, emptying trash. Gating these would have taken a
//     real capability away from tenants, which is a regression dressed as a
//     fix.
//   - CLASSIFY PER KEY — /settings, where one flat table holds both the
//     instance's OIDC issuer and the tenant's own logo.
//
// ⚠ Every case is paired with a single-tenant assertion. Those pass on `main`
// too, which is the honest form of "the vast majority of installs are
// untouched".

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// instanceWideNoTenantForm is the GATE group.
var instanceWideNoTenantForm = []struct {
	name   string
	method string
	path   string
	body   any
}{
	{"webhook targets list", http.MethodGet, "/api/admin/webhooks", nil},
	{"webhook target create", http.MethodPost, "/api/admin/webhooks",
		map[string]any{"url": "https://attacker.example/hook", "events": []string{"file.uploaded"}}},
	{"legacy webhook config read", http.MethodGet, "/api/admin/notifications/webhook-config", nil},
	{"legacy webhook config write", http.MethodPatch, "/api/admin/notifications/webhook-config",
		map[string]any{"url": "https://attacker.example/hook"}},
	{"replication targets list", http.MethodGet, "/api/admin/replication-targets", nil},
	{"replication target create", http.MethodPost, "/api/admin/replication-targets",
		map[string]any{"name": "mine", "driver": "local", "config": map[string]any{"root": "/tmp/mine"}}},
	{"replica rules list", http.MethodGet, "/api/admin/replica/rules", nil},
	{"replica settings read", http.MethodGet, "/api/admin/replica/settings", nil},
	{"replica settings write", http.MethodPatch, "/api/admin/replica/settings", map[string]any{"enabled": true}},
	{"search index stats", http.MethodGet, "/api/admin/search/stats", nil},
	{"search index rebuild", http.MethodPost, "/api/admin/search/rebuild", map[string]any{}},
	{"queue list", http.MethodGet, "/api/admin/queue", nil},
	{"queue stats", http.MethodGet, "/api/admin/queue/stats", nil},
}

func TestInstanceWideNoTenantForm_TenantAdminIsRefused(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	_, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	for _, rt := range instanceWideNoTenantForm {
		t.Run(rt.name, func(t *testing.T) {
			status, body := doJSON(t, client, rt.method, srv.URL+rt.path, rt.body)
			require.Equal(t, http.StatusForbidden, status,
				"a tenant admin reached an instance-wide surface: %v", body)
			assert.Equal(t, "supertenant_only", body["error"])
		})
	}
}

func TestInstanceWideNoTenantForm_SupertenantStillPasses(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	for _, path := range []string{
		"/api/admin/webhooks", "/api/admin/replication-targets",
		"/api/admin/replica/rules", "/api/admin/replica/settings",
		"/api/admin/search/stats", "/api/admin/queue",
	} {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+path, nil)
		assert.NotEqual(t, http.StatusForbidden, status,
			"%s refused the platform operator: %v", path, body)
	}
}

func TestInstanceWideNoTenantForm_SingleTenantAdminUnaffected(t *testing.T) {
	srv, client, store := ownershipServer(t, false)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	for _, path := range []string{
		"/api/admin/webhooks", "/api/admin/replication-targets",
		"/api/admin/replica/rules", "/api/admin/replica/settings",
		"/api/admin/search/stats", "/api/admin/queue",
	} {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+path, nil)
		assert.NotEqual(t, http.StatusForbidden, status,
			"%s refused the admin of a single-tenant install: %v", path, body)
	}
}

// ── settings: per-key classification ──────────────────────────────────────

// TestSettings_TenantMayBrandButNotRewriteTheInstance is the whole point of
// the classification. Both halves are asserted in one test on purpose: a fix
// that only refused would have been trivially "safe" and useless.
func TestSettings_TenantMayBrandButNotRewriteTheInstance(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	tenantID, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	t.Run("may brand its own tenant", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/branding.name",
			map[string]any{"value": "Diyetlif"})
		require.Equal(t, http.StatusOK, status, "%v", body)

		// The value must land under the tenant's OWN prefix, not the global key.
		assert.Equal(t, "Diyetlif", settingValue(t, store, fmt.Sprintf("tenant.%d.branding.name", tenantID)))
		assert.Empty(t, settingValue(t, store, "branding.name"),
			"a tenant admin overwrote the instance-wide branding")
	})

	for _, key := range []string{
		"auth.oidc.issuer", "antivirus.enabled", "smtp.host",
		"share.max_ttl_days", "public_url", "site_name",
		// The installation escrow record: writing it is boot-fatal, and the
		// design deliberately keeps this decision in the environment rather
		// than behind an HTTP session.
		"installation.pinned",
	} {
		t.Run("may not write "+key, func(t *testing.T) {
			before := settingValue(t, store, key)
			status, body := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+key,
				map[string]any{"value": "attacker"})
			require.Equal(t, http.StatusForbidden, status, "%v", body)
			assert.Equal(t, "supertenant_only", body["error"])
			assert.Equal(t, before, settingValue(t, store, key),
				"the refused write landed anyway")
		})
	}

	// ⚠ The already-prefixed spelling names a tenant explicitly and
	// tenantBrandingKey passes it through UNCHANGED — so accepting it would
	// let one tenant rebrand another's login page by typing their id.
	t.Run("may not brand another tenant by naming its id", func(t *testing.T) {
		other, _, _ := seedTenant(t, store, "arasboya", "admin@arasboya.test", false)
		key := fmt.Sprintf("tenant.%d.branding.name", other)
		status, _ := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+key,
			map[string]any{"value": "hijacked"})
		require.Equal(t, http.StatusForbidden, status)
		assert.Empty(t, settingValue(t, store, key))
	})

	// PATCH applies many keys at once; a refusal must write NONE of them,
	// or the caller cannot tell a partial apply from a success.
	t.Run("a mixed batch writes nothing at all", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/settings",
			map[string]any{"branding.footer_text": "ok", "site_name": "hijacked"})
		require.Equal(t, http.StatusForbidden, status)
		assert.Empty(t, settingValue(t, store, "site_name"))
		assert.Empty(t, settingValue(t, store, fmt.Sprintf("tenant.%d.branding.footer_text", tenantID)),
			"the allowed half of a refused batch was written anyway")
	})
}

func TestSettings_SingleTenantAdminWritesAnything(t *testing.T) {
	srv, client, store := ownershipServer(t, false)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	for _, key := range []string{"site_name", "auth.oidc.issuer", "branding.name"} {
		status, body := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+key,
			map[string]any{"value": "chosen"})
		require.Equal(t, http.StatusOK, status, "%s refused: %v", key, body)
		assert.Equal(t, "chosen", settingValue(t, store, key),
			"a single-tenant admin no longer owns %s", key)
	}

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/settings",
		map[string]any{"site_name": "batch", "log_level": "debug"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "batch", settingValue(t, store, "site_name"))
}

// ── the SCOPE group ───────────────────────────────────────────────────────

// TestScopedAdminReads_ShowOnlyOwnTenant asserts on CONTENT rather than status
// codes: these surfaces answered 200 before and answer 200 now, and the whole
// change is which rows come back.
func TestScopedAdminReads_ShowOnlyOwnTenant(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	mine := seedFullTenant(t, store, "diyetlif")
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	t.Run("sync-run history excludes the other tenant", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/sync-runs", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		for _, e := range body["entries"].([]any) {
			got := int64(e.(map[string]any)["storage_id"].(float64))
			assert.NotEqual(t, theirs.storage.ID, got,
				"another tenant's sync run is in this tenant's history")
		}
	})

	t.Run("the dashboard counts only this tenant's users", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		// Two per tenant (the admin plus the seeded victim), never the
		// platform total.
		assert.Equal(t, float64(2), body["total_users"],
			"the dashboard reported the platform's headcount to one tenant")
		for _, s := range body["storages"].([]any) {
			assert.NotEqual(t, theirs.storage.Name, s.(map[string]any)["name"])
		}
	})

	t.Run("the duplicates report excludes the other tenant", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/duplicates?min_size=1", nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		groups, _ := body["groups"].([]any)
		for _, g := range groups {
			for _, n := range g.(map[string]any)["nodes"].([]any) {
				got := int64(n.(map[string]any)["storage_id"].(float64))
				assert.NotEqual(t, theirs.storage.ID, got,
					"another tenant's file path is in this tenant's duplicates report")
			}
		}
	})
}

// TestTrashEmpty_StopsAtTheTenantBoundary is the most destructive request in
// the admin surface, so it is measured on the surviving rows rather than on
// the status code — the endpoint answered 200 before and answers 200 now.
func TestTrashEmpty_StopsAtTheTenantBoundary(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	mine := seedFullTenant(t, store, "diyetlif")
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty",
		map[string]any{"older_than_days": 0})
	require.Equal(t, http.StatusOK, status, "%v", body)

	// Theirs must still be there. (Ours may or may not purge — the local
	// driver is not wired in this harness — so the assertion that carries the
	// meaning is the one about the other tenant.)
	n, err := store.GetNode(t.Context(), theirs.trashed)
	require.NoError(t, err, "another tenant's trashed file was permanently destroyed")
	require.NotNil(t, n)

	_ = mine
}

// TestTrashEmpty_SingleTenantSweepsEverything — the case the scoping could
// have broken: with multi-tenancy off there is no scope, and "empty the trash"
// must still mean all of it.
func TestTrashEmpty_SingleTenantSweepsEverything(t *testing.T) {
	srv, client, store := ownershipServer(t, false)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	st := seedStorageFor(t, store, 1, "solo")
	node := seedNodeIn(t, store, st.ID, "gone.txt")
	require.NoError(t, store.SoftDeleteNode(t.Context(), node.ID))

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/trash/empty",
		map[string]any{"older_than_days": 0})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.GreaterOrEqual(t, body["scanned"], float64(1),
		"a single-tenant sweep skipped rows it used to reach")
}

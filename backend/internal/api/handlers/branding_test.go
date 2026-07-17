package handlers_test

/* wiring:e1 — branding tests: settings → public share page render (logo /
   name / accent / footer), hide_powered_by both states, GET /api/branding
   shape, settings validation + tenant scoping, and the multi-tenant
   host-overlay resolution. */

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// brandingShareFixture builds a folder share and a branding-wired Share
// handler over the shared mutate fixture.
func brandingShareFixture(t *testing.T) (*handlers.Share, *handlers.BrandingSource, db.Store, string) {
	t.Helper()
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "docs"))
	require.NoError(t, drv.Write(ctx, "docs/a.txt", strings.NewReader("alpha"), 5))
	docs, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "docs", Path: "docs",
		PathHash: mutTestPathHash(st.ID, "docs"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: docs.ID})
	require.NoError(t, err)

	src := handlers.NewBrandingSource(store, false)
	sharH := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir()))
	sharH.AttachBranding(src)
	return sharH, src, store, sh.Token
}

func getSharePage(t *testing.T, h *handlers.Share, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/s/"+token, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, req)
	return rec
}

// Configured branding must show up on the public share browse page: logo,
// display name, accent override, custom footer — with the default
// "filex ile paylaşıldı" line still visible (hide_powered_by unset).
func TestBranding_SharePageRendersIdentity(t *testing.T) {
	ctx := context.Background()
	sharH, src, store, token := brandingShareFixture(t)

	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Acme Depo"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.logo_url", "https://acme.test/logo.png"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.accent", "#ff6600"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.footer_text", "© Acme A.Ş. — tüm hakları saklıdır"))
	src.Invalidate()

	rec := getSharePage(t, sharH, token)
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()

	require.Contains(t, body, "Acme Depo", "brand name must render on the share page")
	require.Contains(t, body, `https://acme.test/logo.png`, "logo must render")
	require.Contains(t, body, "--px-accent:#ff6600", "accent override must be injected")
	require.Contains(t, body, "--px-accent-hover:", "derived hover accent must be injected")
	require.Contains(t, body, "© Acme A.Ş. — tüm hakları saklıdır", "custom footer must render")
	require.Contains(t, body, "ile paylaşıldı", "powered-by line stays visible by default")
}

// With no branding configured the page must keep the stock chrome — and the
// powered-by footer must be there (MIT vitrini, default görünür).
func TestBranding_SharePageDefaultChrome(t *testing.T) {
	sharH, _, _, token := brandingShareFixture(t)
	rec := getSharePage(t, sharH, token)
	require.Equal(t, 200, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "ile paylaşıldı", "default powered-by footer")
	require.NotContains(t, body, `<div class="pbrand">`, "no brand header without branding")
	require.NotContains(t, body, "--px-accent:#", "no accent override without branding (base style uses a spaced token)")
}

// hide_powered_by=true hides the "filex ile paylaşıldı" line but keeps the
// custom footer text; flipping it back restores the line.
func TestBranding_HidePoweredByToggle(t *testing.T) {
	ctx := context.Background()
	sharH, src, store, token := brandingShareFixture(t)

	require.NoError(t, store.UpsertSetting(ctx, "branding.footer_text", "Sadece Acme"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.hide_powered_by", "true"))
	src.Invalidate()

	body := getSharePage(t, sharH, token).Body.String()
	require.Contains(t, body, "Sadece Acme")
	require.NotContains(t, body, "ile paylaşıldı", "hide_powered_by=true must hide the powered-by line")

	require.NoError(t, store.UpsertSetting(ctx, "branding.hide_powered_by", "false"))
	src.Invalidate()
	body = getSharePage(t, sharH, token).Body.String()
	require.Contains(t, body, "Sadece Acme")
	require.Contains(t, body, "ile paylaşıldı", "hide_powered_by=false must restore the powered-by line")
}

// GET /api/branding returns the stable five-field JSON shape (zero values
// included) and reflects configured settings.
func TestBranding_PublicEndpointShape(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewBrandingSource(store, false)
	h := handlers.NewBranding(src)

	get := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/branding", nil)
		rec := httptest.NewRecorder()
		h.Get(rec, req)
		require.Equal(t, 200, rec.Code)
		var m map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
		return m
	}

	m := get()
	for _, k := range []string{"name", "logo_url", "accent", "footer_text", "hide_powered_by"} {
		_, ok := m[k]
		require.True(t, ok, "field %q must always be present", k)
	}
	require.Equal(t, false, m["hide_powered_by"])

	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Acme"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.accent", "#0a0"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.hide_powered_by", "1"))
	src.Invalidate()

	m = get()
	require.Equal(t, "Acme", m["name"])
	require.Equal(t, "#0a0", m["accent"])
	require.Equal(t, true, m["hide_powered_by"])
}

// Settings writes validate branding values (accent must be hex; bogus logo
// URI rejected) and a tenant-scoped admin's branding.* write lands under
// tenant.<id>.branding.* — surfaced back under the bare key on List.
func TestBranding_SettingsValidationAndTenantScope(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	seth := handlers.NewSettings(store)
	src := handlers.NewBrandingSource(store, true)
	seth.AttachBranding(src)

	patch := func(ctx context.Context, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()
		seth.Update(rec, req)
		return rec
	}

	// Invalid accent → 400.
	rec := patch(ctx, `{"branding.accent":"red"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	// Invalid logo scheme → 400.
	rec = patch(ctx, `{"branding.logo_url":"javascript:alert(1)"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	// Valid global write (no tenant scope) → bare key.
	rec = patch(ctx, `{"branding.name":"Global Ad"}`)
	require.Equal(t, 200, rec.Code)
	v, err := store.GetSetting(ctx, "branding.name")
	require.NoError(t, err)
	require.Equal(t, "Global Ad", v)

	// Tenant-scoped admin write → prefixed key, global untouched.
	tctx := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 7, Slug: "acme"})
	rec = patch(tctx, `{"branding.name":"Acme Ad"}`)
	require.Equal(t, 200, rec.Code)
	v, err = store.GetSetting(ctx, "tenant.7.branding.name")
	require.NoError(t, err)
	require.Equal(t, "Acme Ad", v)
	v, _ = store.GetSetting(ctx, "branding.name")
	require.Equal(t, "Global Ad", v, "tenant write must not clobber the global key")

	// Tenant List overlays its own value under the bare key and strips
	// tenant.* branding rows from the response.
	req := httptest.NewRequest("GET", "/api/admin/settings", nil).WithContext(tctx)
	lrec := httptest.NewRecorder()
	seth.List(lrec, req)
	require.Equal(t, 200, lrec.Code)
	var m map[string]string
	require.NoError(t, json.Unmarshal(lrec.Body.Bytes(), &m))
	require.Equal(t, "Acme Ad", m["branding.name"])
	for k := range m {
		require.False(t, strings.HasPrefix(k, "tenant."), "tenant-prefixed branding keys must not leak: %s", k)
	}
}

// Multi-tenant resolution: a host mapped to a provider gets that tenant's
// overlay; unknown hosts fall back to the global branding.
func TestBrandingSource_TenantOverlayByHost(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)

	p, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "acme", Name: "Acme", Host: "files.acme.test", AuthType: "local", Enabled: true,
	})
	require.NoError(t, err)

	require.NoError(t, store.UpsertSetting(ctx, "branding.name", "Filex Global"))
	require.NoError(t, store.UpsertSetting(ctx, "branding.accent", "#112233"))
	require.NoError(t, store.UpsertSetting(ctx, "tenant."+itoa64(p.ID)+".branding.name", "Acme Cloud"))

	src := handlers.NewBrandingSource(store, true)

	got := src.For(ctx, "files.acme.test:443")
	require.Equal(t, "Acme Cloud", got.Name, "tenant host must get the tenant overlay (port stripped)")
	require.Equal(t, "#112233", got.Accent, "unset tenant fields fall back to global")

	other := src.For(ctx, "unknown.example.test")
	require.Equal(t, "Filex Global", other.Name)
}

func itoa64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

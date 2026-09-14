package handlers_test

/* gorunum:v1 — operator custom CSS: the setting reaches the boot payload, the
   cap and the one refused string are enforced server-side, and the key is
   instance-wide (a tenant admin may not restyle the installation). */

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
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A saved ui.custom_css comes back on GET /api/branding — the fetch the SPA
// already makes at boot. Without a reader the row would save and do nothing.
func TestCustomCSS_ReachesBrandingPayload(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewBrandingSource(store, false)
	bh := handlers.NewBranding(src)

	payload := func() map[string]any {
		rec := httptest.NewRecorder()
		bh.Get(rec, httptest.NewRequest("GET", "/api/branding", nil))
		require.Equal(t, 200, rec.Code)
		var m map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
		return m
	}

	require.Equal(t, "", payload()["custom_css"], "unset installation ships no stylesheet")

	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSSettingKey, "  .fe { --fe-primary: #ff0000; }  "))
	src.Invalidate()
	require.Equal(t, ".fe { --fe-primary: #ff0000; }", payload()["custom_css"])

	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSSettingKey, ""))
	src.Invalidate()
	require.Equal(t, "", payload()["custom_css"], "clearing the field removes the stylesheet")
}

// Writing through the settings handler validates and busts the cache, so the
// very next boot payload carries the new sheet without waiting out the TTL.
func TestCustomCSS_SettingsWriteValidatesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	seth := handlers.NewSettings(store)
	src := handlers.NewBrandingSource(store, false)
	seth.AttachBranding(src)
	bh := handlers.NewBranding(src)

	patch := func(ctx context.Context, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(body)).WithContext(ctx)
		rec := httptest.NewRecorder()
		seth.Update(rec, req)
		return rec
	}
	put := func(ctx context.Context, value string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"value": value})
		req := httptest.NewRequest("PUT", "/api/admin/settings/"+handlers.CustomCSSSettingKey, strings.NewReader(string(body))).WithContext(ctx)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("key", handlers.CustomCSSSettingKey)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		seth.Set(rec, req)
		return rec
	}

	// Prime the cache so the write has something stale to bust.
	rec := httptest.NewRecorder()
	bh.Get(rec, httptest.NewRequest("GET", "/api/branding", nil))
	require.Equal(t, 200, rec.Code)

	body, _ := json.Marshal(map[string]string{handlers.CustomCSSSettingKey: ".fe { --fe-primary: #ff0000; }"})
	require.Equal(t, 200, patch(ctx, string(body)).Code)

	rec = httptest.NewRecorder()
	bh.Get(rec, httptest.NewRequest("GET", "/api/branding", nil))
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	require.Equal(t, ".fe { --fe-primary: #ff0000; }", m["custom_css"], "the write must invalidate the cached payload")

	// Over the cap → 400, and nothing is written.
	tooBig, _ := json.Marshal(map[string]string{handlers.CustomCSSSettingKey: strings.Repeat("a", handlers.CustomCSSMaxBytes+1)})
	require.Equal(t, http.StatusBadRequest, patch(ctx, string(tooBig)).Code)
	v, _ := store.GetSetting(ctx, handlers.CustomCSSSettingKey)
	require.Equal(t, ".fe { --fe-primary: #ff0000; }", v, "a refused write leaves the stored sheet alone")

	// A batch containing a refused stylesheet writes NOTHING, not even its
	// other keys — the classification loop runs before the write loop.
	mixed, _ := json.Marshal(map[string]string{
		"site_name":                  "Should Not Land",
		handlers.CustomCSSSettingKey: "body::after { content: '</style><script>alert(1)</script>' }",
	})
	require.Equal(t, http.StatusBadRequest, patch(ctx, string(mixed)).Code)
	v, _ = store.GetSetting(ctx, "site_name")
	require.Equal(t, "", v, "no key of a refused batch may be written")

	// The single-key endpoint enforces the same rules.
	require.Equal(t, http.StatusBadRequest, put(ctx, "a{}</STYLE>").Code)
	require.Equal(t, 200, put(ctx, ".fe { --fe-bg: #000000; }").Code)
	v, _ = store.GetSetting(ctx, handlers.CustomCSSSettingKey)
	require.Equal(t, ".fe { --fe-bg: #000000; }", v)

	// An inline SVG data URI keeps its "<" — the refusal is "</style", not "<".
	require.Equal(t, 200, put(ctx, ".fe { background: url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'></svg>\"); }").Code)
}

// Instance-wide: a confined tenant admin is refused, so one customer cannot
// restyle every other customer's installation.
func TestCustomCSS_TenantAdminRefused(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	seth := handlers.NewSettings(store)
	seth.AttachBranding(handlers.NewBrandingSource(store, true))

	tctx := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 7, Slug: "acme"})
	body, _ := json.Marshal(map[string]string{handlers.CustomCSSSettingKey: ".fe { --fe-primary: #ff0000; }"})
	req := httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(string(body))).WithContext(tctx)
	rec := httptest.NewRecorder()
	seth.Update(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	v, _ := store.GetSetting(ctx, handlers.CustomCSSSettingKey)
	require.Equal(t, "", v)
}

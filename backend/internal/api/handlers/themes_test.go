package handlers_test

/* tema:v1 — operator-defined themes: storage, serving, validation, the tenant
   refusal, the export/import round trip, and what happens to the people using
   a theme that gets deleted. */

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
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// sampleTokens builds a complete, valid variant map. `tint` lets a test tell
// the two variants apart without hand-writing two dozen hex values.
func sampleTokens(tint string) map[string]string {
	m := map[string]string{}
	for _, k := range handlers.ThemeAuthoredColorTokens() {
		m[k] = tint
	}
	// The values that have to differ for the test to mean anything.
	m["--fe-bg"] = tint
	m["--fe-radius"] = "12px"
	m["--fe-font"] = `Brand Sans, system-ui, sans-serif`
	return m
}

func sampleTheme(key, name string) *model.CustomTheme {
	return &model.CustomTheme{
		Key:         key,
		Name:        name,
		TokensLight: sampleTokens("#123456"),
		TokensDark:  sampleTokens("#abcdef"),
	}
}

// putTheme drives the admin handler the way the SPA does.
func putTheme(t *testing.T, h *handlers.Themes, ctx context.Context, key string, doc any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	// ⚠ A fixed target, with the key carried only as a chi URL parameter —
	// which is the only place the handler reads it from. Interpolating the key
	// into the path would make `httptest.NewRequest` panic on the deliberately
	// malformed keys this file exists to refuse.
	req := httptest.NewRequest("PUT", "/api/admin/themes/x", strings.NewReader(string(body))).WithContext(ctx)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("key", key)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Put(rec, req)
	return rec
}

func deleteTheme(t *testing.T, h *handlers.Themes, ctx context.Context, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/api/admin/themes/x", nil).WithContext(ctx)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("key", key)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	return rec
}

// ─────────────────── storage + serving ───────────────────

// A theme written through the admin handler comes back on the PUBLIC
// appearance endpoint, prefixed so it can never collide with a built-in id.
func TestThemes_StoredThemeReachesThePublicPayload(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewAppearanceSource(store)
	th := handlers.NewThemes(store, src)
	ph := handlers.NewAppearance(src)

	payload := func() handlers.AppearancePayload {
		rec := httptest.NewRecorder()
		ph.Get(rec, httptest.NewRequest("GET", "/api/appearance", nil))
		require.Equal(t, 200, rec.Code)
		var p handlers.AppearancePayload
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		return p
	}

	p := payload()
	require.Empty(t, p.Themes)
	require.Equal(t, "default", p.DefaultThemeID)

	require.Equal(t, 200, putTheme(t, th, ctx, "acme", handlers.ExportTheme(sampleTheme("acme", "Acme Bulut"))).Code)

	p = payload()
	require.Len(t, p.Themes, 1)
	require.Equal(t, "custom:acme", p.Themes[0].ID,
		"a custom id must be prefixed so it can never collide with night/forest/…")
	require.Equal(t, "Acme Bulut", p.Themes[0].Name)
	require.Equal(t, "#123456", p.Themes[0].Light["--fe-bg"])
	require.Equal(t, "#abcdef", p.Themes[0].Dark["--fe-bg"])
	require.Equal(t, "12px", p.Themes[0].Light["--fe-radius"])
}

// Setting the instance default, and the cache being dropped when it changes.
func TestThemes_InstanceDefaultIsServedAndInvalidated(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewAppearanceSource(store)
	th := handlers.NewThemes(store, src)
	seth := handlers.NewSettings(store)
	seth.AttachBranding(handlers.NewBrandingSource(store, false))
	seth.AttachAppearance(src)

	require.Equal(t, 200, putTheme(t, th, ctx, "acme", handlers.ExportTheme(sampleTheme("acme", "Acme"))).Code)
	// Prime the cache so the settings write has something stale to bust.
	require.Equal(t, "default", src.Payload(ctx).DefaultThemeID)

	body, _ := json.Marshal(map[string]string{handlers.DefaultThemeSettingKey: "custom:acme"})
	req := httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(string(body))).WithContext(ctx)
	rec := httptest.NewRecorder()
	seth.Update(rec, req)
	require.Equal(t, 200, rec.Code)

	require.Equal(t, "custom:acme", src.Payload(ctx).DefaultThemeID,
		"the write must invalidate the cached payload, not leave the operator waiting out the TTL")

	// A built-in id is equally valid as a default.
	require.NoError(t, store.UpsertSetting(ctx, handlers.DefaultThemeSettingKey, "forest"))
	src.Invalidate()
	require.Equal(t, "forest", src.Payload(ctx).DefaultThemeID)
}

// ─────────────────── validation ───────────────────

// ⚠⚠ The values are printed verbatim into a <style> block on pages strangers
// load, so the allowlist is the security boundary and not a nicety.
func TestThemes_RefusesValuesThatCouldEscapeAStyleBlock(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	th := handlers.NewThemes(store, handlers.NewAppearanceSource(store))

	cases := map[string]func(*handlers.ThemeExport){
		"a token that is not ours": func(d *handlers.ThemeExport) {
			d.Light["--fe-evil"] = "#000000"
		},
		"a declaration break-out": func(d *handlers.ThemeExport) {
			d.Light["--fe-bg"] = "#fff; } body { display: none"
		},
		"a url() in a colour": func(d *handlers.ThemeExport) {
			d.Light["--fe-primary"] = "url(https://evil.example/x)"
		},
		"a url() smuggled through the font stack": func(d *handlers.ThemeExport) {
			d.Light["--fe-font"] = `X, url(https://evil.example/f.woff2)`
		},
		"a style close in the font stack": func(d *handlers.ThemeExport) {
			d.Light["--fe-font"] = `X</style><script>alert(1)</script>`
		},
		"a radius that is an expression": func(d *handlers.ThemeExport) {
			d.Light["--fe-radius"] = "expression(alert(1))"
		},
		"a missing authored colour": func(d *handlers.ThemeExport) {
			delete(d.Dark, "--fe-primary-ink")
		},
		"an empty dark variant": func(d *handlers.ThemeExport) {
			d.Dark = map[string]string{}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			doc := handlers.ExportTheme(sampleTheme("acme", "Acme"))
			mutate(&doc)
			rec := putTheme(t, th, ctx, "acme", doc)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			stored, err := store.GetCustomTheme(ctx, "acme")
			require.NoError(t, err)
			require.Nil(t, stored, "a refused theme must not be stored")
		})
	}
}

// The slug is what becomes a palette id, so it is bounded and lowercase.
func TestThemes_RefusesBadKeys(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	th := handlers.NewThemes(store, handlers.NewAppearanceSource(store))

	for _, key := range []string{"", "-leading", "has space", "has/slash", strings.Repeat("a", 41), "ÜST"} {
		doc := handlers.ExportTheme(sampleTheme("placeholder", "Acme"))
		// Put the bad key in the BODY too: an empty URL parameter is the one
		// case where the body's key is the one that counts, which is what lets
		// an imported file keep its own key.
		doc.Key = key
		rec := putTheme(t, th, ctx, key, doc)
		require.NotEqual(t, 200, rec.Code, "key %q must be refused", key)
	}

	// A key that only needs normalising is accepted, and stored normalised.
	doc := handlers.ExportTheme(sampleTheme("placeholder", "Acme"))
	doc.Key = "  Acme-Bulut  "
	require.Equal(t, 200, putTheme(t, th, ctx, "", doc).Code)
	stored, err := store.GetCustomTheme(ctx, "acme-bulut")
	require.NoError(t, err)
	require.NotNil(t, stored, "the key is trimmed and lowercased, not refused")
}

// ─────────────────── export / import ───────────────────

// A theme built for one customer must survive being carried to another.
func TestThemes_ExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	th := handlers.NewThemes(store, handlers.NewAppearanceSource(store))

	original := sampleTheme("acme", "Acme Bulut — Kurumsal")
	require.Equal(t, 200, putTheme(t, th, ctx, "acme", handlers.ExportTheme(original)).Code)

	// "Export" is the list the admin screen downloads from.
	rec := httptest.NewRecorder()
	th.List(rec, httptest.NewRequest("GET", "/api/admin/themes", nil))
	require.Equal(t, 200, rec.Code)
	var listed struct {
		Themes []handlers.ThemeExport `json:"themes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Themes, 1)

	file, err := json.Marshal(listed.Themes[0])
	require.NoError(t, err)

	// …and "import" is the same document parsed back.
	back, err := handlers.ImportTheme(file)
	require.NoError(t, err)
	require.Equal(t, original.Key, back.Key)
	require.Equal(t, original.Name, back.Name, "a Turkish display name must survive the round trip")
	require.Equal(t, original.TokensLight, back.TokensLight)
	require.Equal(t, original.TokensDark, back.TokensDark)

	// Importing it under a NEW key re-keys it without editing the file.
	require.Equal(t, 200, putTheme(t, th, ctx, "acme-two", listed.Themes[0]).Code)
	all, err := store.ListCustomThemes(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// A file this version cannot read is refused whole, rather than half-imported.
func TestThemes_ImportRefusesForeignDocuments(t *testing.T) {
	_, err := handlers.ImportTheme([]byte(`not json at all`))
	require.Error(t, err)

	future, _ := json.Marshal(map[string]any{"filex_theme": 99, "key": "x", "name": "X"})
	_, err = handlers.ImportTheme(future)
	require.Error(t, err)
	require.Contains(t, err.Error(), "format")

	// A document with the right marker but a bad palette is still refused.
	bad, _ := json.Marshal(map[string]any{
		"filex_theme": 1, "key": "x", "name": "X",
		"light": map[string]string{"--fe-bg": "#fff"}, "dark": map[string]string{},
	})
	_, err = handlers.ImportTheme(bad)
	require.Error(t, err)
}

// ─────────────────── deletion and fallback ───────────────────

// ⚠⚠ THE POINT OF THE WHOLE RESOLUTION DESIGN. Deleting a theme must put the
// people who were using it back on the stock palette, and it must do so
// without anything having to go and find them.
func TestThemes_DeletedThemeFallsBackToTheDefault(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewAppearanceSource(store)
	th := handlers.NewThemes(store, src)

	require.Equal(t, 200, putTheme(t, th, ctx, "acme", handlers.ExportTheme(sampleTheme("acme", "Acme"))).Code)
	require.NoError(t, store.UpsertSetting(ctx, handlers.DefaultThemeSettingKey, "custom:acme"))
	src.Invalidate()
	require.Equal(t, "custom:acme", src.Payload(ctx).DefaultThemeID)

	// While it exists, somebody's stored choice resolves to itself.
	live, err := store.ListCustomThemes(ctx)
	require.NoError(t, err)
	require.Equal(t, "custom:acme", handlers.ResolveThemeID("custom:acme", live))

	require.Equal(t, 200, deleteTheme(t, th, ctx, "acme").Code)

	// The instance default no longer points at a theme nobody can see…
	require.Equal(t, "default", src.Payload(ctx).DefaultThemeID)
	stored, _ := store.GetSetting(ctx, handlers.DefaultThemeSettingKey)
	require.Equal(t, "default", stored)

	// …and so does the choice of a PERSON who was using it, without that
	// person's row ever having been touched.
	gone, err := store.ListCustomThemes(ctx)
	require.NoError(t, err)
	require.Empty(t, gone)
	require.Equal(t, "default", handlers.ResolveThemeID("custom:acme", gone),
		"a palette id that names nothing must resolve to the stock palette, not be handed back")

	// Deleting a theme that was never there is a 404, not a cheerful 200.
	require.Equal(t, http.StatusNotFound, deleteTheme(t, th, ctx, "never-existed").Code)
}

// Built-in ids keep resolving to themselves; anything unknown does not.
func TestThemes_ResolveThemeID(t *testing.T) {
	custom := []*model.CustomTheme{{Key: "acme"}}
	require.Equal(t, "night", handlers.ResolveThemeID("night", custom))
	require.Equal(t, "default", handlers.ResolveThemeID("", custom))
	require.Equal(t, "default", handlers.ResolveThemeID("no-such-palette", custom))
	require.Equal(t, "default", handlers.ResolveThemeID("custom:missing", custom))
	require.Equal(t, "custom:acme", handlers.ResolveThemeID("custom:acme", custom))
	// A bare key without the prefix is NOT a custom theme — the prefix is what
	// keeps the two namespaces apart.
	require.Equal(t, "default", handlers.ResolveThemeID("acme", custom))
}

// ─────────────────── tenancy ───────────────────

// ⚠⚠ The table has no tenant column, so a theme written by one customer's
// admin would paint every other customer's users. Instance operator only.
func TestThemes_TenantAdminRefused(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewAppearanceSource(store)
	th := handlers.NewThemes(store, src)

	// Seed one theme as the operator, so the delete below has a real target
	// and a refusal cannot be mistaken for "nothing to delete".
	require.Equal(t, 200, putTheme(t, th, ctx, "acme", handlers.ExportTheme(sampleTheme("acme", "Acme"))).Code)

	tctx := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 7, Slug: "other"})

	rec := putTheme(t, th, tctx, "tenant-brand", handlers.ExportTheme(sampleTheme("tenant-brand", "Tenant")))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "supertenant_only")

	rec = deleteTheme(t, th, tctx, "acme")
	require.Equal(t, http.StatusForbidden, rec.Code)

	listRec := httptest.NewRecorder()
	th.List(listRec, httptest.NewRequest("GET", "/api/admin/themes", nil).WithContext(tctx))
	require.Equal(t, http.StatusForbidden, listRec.Code)

	// Nothing the tenant tried changed anything.
	all, err := store.ListCustomThemes(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "acme", all[0].Key)

	// The SUPERTENANT of the same multi-tenant instance still passes.
	sctx := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 1, Slug: "root", IsSupertenant: true})
	require.Equal(t, 200, putTheme(t, th, sctx, "ok", handlers.ExportTheme(sampleTheme("ok", "OK"))).Code)
}

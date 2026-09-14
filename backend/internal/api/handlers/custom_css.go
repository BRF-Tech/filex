package handlers

/* ===== gorunum:v1 — operator custom CSS =====

One settings row, `ui.custom_css`, holding a stylesheet the operator pastes on
the admin Settings page. Besides the shipped theme gallery this is the escape
hatch: an installation that wants its own look overrides the `--fe-*` tokens
here and every filex surface in the browser wears it.

WHERE IT IS READ: `BrandingSource.For` (branding.go) puts it on the
`GET /api/branding` payload — the one appearance fetch the SPA already makes on
every page load, before a session exists, so the login screen is styled too. The
browser injects it as the text of a single <style data-filex-custom> element
appended last in <head> (web/src/lib/customCss.ts); it is never parsed as HTML.

WHO MAY WRITE IT: `/api/admin/settings` is behind `auth.RequireAdmin`, and the
key is deliberately NOT in `tenantScopedSettingKey` (settings.go) — it lands in
one global row, so in multi-tenant mode a tenant admin is refused and only the
supertenant can restyle the instance. The admin page says as much in the field
hint: it applies to everyone using this installation.

The validator lives HERE, called from the Settings HANDLER rather than from a
route, because the same handler is mounted under /api/ai/admin and invoked
in-process by the `admin_settings_set` / `admin_settings_update` MCP tools
(handlers/ai_admin.go) — a route-level check would guard one of three doors
(filex lesson #88). */

import (
	"errors"
	"strings"
)

// CustomCSSSettingKey is the settings row holding the operator stylesheet.
const CustomCSSSettingKey = "ui.custom_css"

// CustomCSSMaxBytes caps the stored sheet. It travels on a public, 60s-cached
// boot payload every visitor fetches, so it is a budget and not just a
// database guard; 64 KiB is roughly ten times the largest hand-written token
// override we ship. The admin page shows the same number and counts UTF-8
// bytes, so the two agree on a sheet with Turkish comments in it.
const CustomCSSMaxBytes = 64 * 1024

// validateCustomCSS checks an operator stylesheet before it is persisted.
// Empty always passes (clearing the field).
func validateCustomCSS(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > CustomCSSMaxBytes {
		return errors.New("ui.custom_css is capped at 64 KB")
	}
	// The SPA sets this as a style element's textContent, which an HTML parser
	// never re-reads, so this cannot break out there. It is refused anyway
	// because "</style" is the one string that would end a <style> block if
	// this value ever reached a server-rendered page, and a stylesheet has no
	// reason to contain it. Everything else CSS legitimately needs — including
	// "<" inside an inline data:image/svg+xml url() — still passes.
	if strings.Contains(strings.ToLower(value), "</style") {
		return errors.New("ui.custom_css must be a stylesheet: \"</style\" is not allowed")
	}
	return nil
}

// customCSSFromSettings reads the operator stylesheet out of a settings map.
// Instance-wide on purpose: unlike branding.*, there is no tenant.<id>.
// overlay, because one installation renders one look.
func customCSSFromSettings(m map[string]string) string {
	return strings.TrimSpace(m[CustomCSSSettingKey])
}

/* ===== /gorunum:v1 ===== */

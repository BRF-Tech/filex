package handlers

/* ===== wiring:e1 — kurumsal kimlik (branding) =====

Settings-driven branding for every public-facing surface (share / drop /
PIN pages + the admin SPA login). Five keys in the existing key-value
settings store — no schema change:

	branding.name            display name shown next to (or instead of) the logo
	branding.logo_url        http(s) / site-relative URL or a data:image/… URI (≤256KB)
	branding.accent          #rgb / #rrggbb hex — overrides the --px-accent tokens
	branding.footer_text     free-text footer line on public pages
	branding.hide_powered_by "true" hides the "filex ile paylaşıldı" line (default: visible)

Multi-tenant (provider=tenant) mode: the settings table is instance-global
(NOT tenant-scoped), so per-tenant branding is stored under a prefixed key
`tenant.<providerID>.branding.<leaf>` in the SAME table. The Settings
handler rewrites reads/writes for non-supertenant admins transparently
(see settings.go), and public pages resolve their tenant from the request
Host via GetProviderByHost — tenant keys overlay the global defaults.
Single-tenant installs never see the prefix. No migration required.

`GET /api/branding` is public (pre-login: the SPA login page needs it). */

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// brandingLogoMaxBytes caps an inline data-URI logo stored in settings.
const brandingLogoMaxBytes = 256 * 1024

// brandingCacheTTL bounds how stale a public page's branding may be after an
// admin edit (writes through the Settings handler invalidate immediately;
// the TTL only covers out-of-band DB writes).
const brandingCacheTTL = 15 * time.Second

// brandingAccentRe accepts #rgb / #rrggbb (what the accent override injects
// verbatim into a <style> block — the regex doubles as the XSS gate).
var brandingAccentRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// brandingLeaves are the recognised branding.* leaf keys.
var brandingLeaves = []string{"name", "logo_url", "accent", "footer_text", "hide_powered_by"}

// BrandingConfig is the effective branding payload — the JSON shape of
// GET /api/branding and the input to the public-page chrome builder.
type BrandingConfig struct {
	Name          string `json:"name"`
	LogoURL       string `json:"logo_url"`
	Accent        string `json:"accent"`
	FooterText    string `json:"footer_text"`
	HidePoweredBy bool   `json:"hide_powered_by"`
}

// BrandingSource resolves the effective branding for a request host from the
// settings store, with a small TTL cache (public share pages are hot paths).
// A nil *BrandingSource is safe everywhere and yields the zero config —
// unwired handlers keep their exact pre-branding output.
type BrandingSource struct {
	Store       db.Store
	MultiTenant bool

	mu    sync.Mutex
	cache map[string]brandingCacheEntry
}

type brandingCacheEntry struct {
	cfg BrandingConfig
	exp time.Time
}

// NewBrandingSource constructs a BrandingSource.
func NewBrandingSource(store db.Store, multiTenant bool) *BrandingSource {
	return &BrandingSource{Store: store, MultiTenant: multiTenant, cache: map[string]brandingCacheEntry{}}
}

// Invalidate drops the cache — called after any branding settings write.
func (b *BrandingSource) Invalidate() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.cache = map[string]brandingCacheEntry{}
	b.mu.Unlock()
}

// ForRequest resolves branding for an HTTP request (host-scoped in
// multi-tenant mode). Nil-receiver safe.
func (b *BrandingSource) ForRequest(r *http.Request) BrandingConfig {
	if b == nil || r == nil {
		return BrandingConfig{}
	}
	return b.For(r.Context(), r.Host)
}

// For resolves the effective branding for a host: global branding.* keys,
// overlaid (in multi-tenant mode) by the host's tenant.<id>.branding.* keys.
func (b *BrandingSource) For(ctx context.Context, host string) BrandingConfig {
	if b == nil || b.Store == nil {
		return BrandingConfig{}
	}
	host = normalizeBrandingHost(host)
	cacheKey := host
	if !b.MultiTenant {
		cacheKey = "" // single-tenant: one config for every host
	}

	b.mu.Lock()
	if e, ok := b.cache[cacheKey]; ok && time.Now().Before(e.exp) {
		b.mu.Unlock()
		return e.cfg
	}
	b.mu.Unlock()

	m, err := b.Store.ListSettings(ctx)
	if err != nil {
		return BrandingConfig{}
	}
	cfg := brandingFromMap(m, "branding.")
	if b.MultiTenant && host != "" {
		if p, perr := b.Store.GetProviderByHost(ctx, host); perr == nil && p != nil && !p.IsSupertenant {
			overlayBrandingFromMap(&cfg, m, fmt.Sprintf("tenant.%d.branding.", p.ID))
		}
	}

	b.mu.Lock()
	b.cache[cacheKey] = brandingCacheEntry{cfg: cfg, exp: time.Now().Add(brandingCacheTTL)}
	b.mu.Unlock()
	return cfg
}

// normalizeBrandingHost lowercases and strips any :port from a request Host.
func normalizeBrandingHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	host = strings.TrimSuffix(host, "]")
	host = strings.TrimPrefix(host, "[")
	return host
}

// brandingFromMap builds a config from prefix-keyed settings entries.
func brandingFromMap(m map[string]string, prefix string) BrandingConfig {
	var cfg BrandingConfig
	overlayBrandingFromMap(&cfg, m, prefix)
	return cfg
}

// overlayBrandingFromMap overrides cfg fields for every non-empty
// prefix-keyed entry (empty values leave the base untouched, so a tenant
// only diverges on the fields it actually set).
func overlayBrandingFromMap(cfg *BrandingConfig, m map[string]string, prefix string) {
	if v := strings.TrimSpace(m[prefix+"name"]); v != "" {
		cfg.Name = v
	}
	if v := strings.TrimSpace(m[prefix+"logo_url"]); v != "" {
		cfg.LogoURL = v
	}
	if v := strings.TrimSpace(m[prefix+"accent"]); brandingAccentRe.MatchString(v) {
		cfg.Accent = v
	}
	if v := strings.TrimSpace(m[prefix+"footer_text"]); v != "" {
		cfg.FooterText = v
	}
	if v := strings.TrimSpace(m[prefix+"hide_powered_by"]); v != "" {
		cfg.HidePoweredBy = brandingBool(v)
	}
}

// brandingBool parses the boolish strings the settings store round-trips.
func brandingBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ─────────────────── settings write helpers (used by settings.go) ───────────────────

// isBrandingSettingKey reports whether key is a branding key (bare or
// already tenant-prefixed).
func isBrandingSettingKey(key string) bool {
	return strings.HasPrefix(key, "branding.") || (strings.HasPrefix(key, "tenant.") && strings.Contains(key, ".branding."))
}

// tenantBrandingKey rewrites a bare branding.* key to the caller tenant's
// prefixed key. Supertenant / single-tenant admins keep the global key.
func tenantBrandingKey(ctx context.Context, key string) string {
	if !strings.HasPrefix(key, "branding.") {
		return key
	}
	if sc, ok := tenant.FromContext(ctx); ok && sc != nil && !sc.IsSupertenant && sc.ProviderID > 0 {
		return fmt.Sprintf("tenant.%d.%s", sc.ProviderID, key)
	}
	return key
}

// validateBrandingSetting sanity-checks a branding value before it is
// persisted. Empty values always pass (clearing a field).
func validateBrandingSetting(key, value string) error {
	if value == "" {
		return nil
	}
	leaf := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		leaf = key[i+1:]
	}
	switch leaf {
	case "accent":
		if !brandingAccentRe.MatchString(strings.TrimSpace(value)) {
			return errors.New("branding.accent must be a #rgb or #rrggbb hex color")
		}
	case "logo_url":
		v := strings.TrimSpace(value)
		switch {
		case strings.HasPrefix(v, "data:"):
			if !strings.HasPrefix(v, "data:image/") {
				return errors.New("branding.logo_url data URI must be an image")
			}
			if len(v) > brandingLogoMaxBytes {
				return fmt.Errorf("branding.logo_url data URI exceeds %d KB", brandingLogoMaxBytes/1024)
			}
		case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"), strings.HasPrefix(v, "/"):
			if len(v) > 2048 {
				return errors.New("branding.logo_url is too long")
			}
		default:
			return errors.New("branding.logo_url must be an http(s) URL, a site-relative path or a data:image/… URI")
		}
	case "name", "footer_text":
		if len(value) > 400 {
			return errors.New("branding text fields are capped at 400 bytes")
		}
	case "hide_powered_by":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "0", "true", "false", "yes", "no", "on", "off":
		default:
			return errors.New("branding.hide_powered_by must be a boolean")
		}
	}
	return nil
}

// overlayTenantBrandingSettings adjusts an admin settings read for the
// caller's tenant: its own tenant.<id>.branding.* values are surfaced under
// the bare branding.* keys (what the Branding admin page binds to), and every
// tenant.*.branding.* row is stripped so tenants never see each other's
// branding. Supertenant / single-tenant reads are returned untouched.
func overlayTenantBrandingSettings(ctx context.Context, m map[string]string) {
	sc, ok := tenant.FromContext(ctx)
	if !ok || sc == nil || sc.IsSupertenant || sc.ProviderID <= 0 {
		return
	}
	own := fmt.Sprintf("tenant.%d.branding.", sc.ProviderID)
	for _, leaf := range brandingLeaves {
		if v, exists := m[own+leaf]; exists && strings.TrimSpace(v) != "" {
			m["branding."+leaf] = v
		}
	}
	for k := range m {
		if strings.HasPrefix(k, "tenant.") && strings.Contains(k, ".branding.") {
			delete(m, k)
		}
	}
}

// ─────────────────── GET /api/branding ───────────────────

// Branding serves the public branding endpoint.
type Branding struct {
	Source *BrandingSource
}

// NewBranding constructs the public branding handler.
func NewBranding(src *BrandingSource) *Branding { return &Branding{Source: src} }

// Get returns the effective branding for the request host. Public (no auth):
// the admin SPA login page fetches it before any session exists.
func (h *Branding) Get(w http.ResponseWriter, r *http.Request) {
	cfg := h.Source.ForRequest(r)
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, cfg)
}

// ─────────────────── public-page chrome ───────────────────

// publicChrome bundles the per-request branded fragments the public page
// templates render: an accent CSS override, a logo+name header, and the
// footer (custom text + the default "filex ile paylaşıldı" line, which
// stays visible unless hide_powered_by is set).
type publicChrome struct {
	BrandCSS  template.HTML
	BrandHead template.HTML
	FooterTR  template.HTML
	FooterEN  template.HTML
}

// publicChromeFor computes the chrome for one request. Nil src / zero
// branding yields fragments identical to the historical constants.
func publicChromeFor(src *BrandingSource, r *http.Request) publicChrome {
	return chromeFor(src.ForRequest(r))
}

// chromeFor builds the branded fragments from a resolved config.
func chromeFor(cfg BrandingConfig) publicChrome {
	c := publicChrome{
		FooterTR: template.HTML(publicFooterTR),
		FooterEN: template.HTML(publicFooterEN),
	}

	// Accent override: injected AFTER publicPageStyle, so the plain :root
	// selector wins over both the light and dark defaults (same specificity,
	// later in document order). The value is regex-gated hex — safe inline.
	if brandingAccentRe.MatchString(cfg.Accent) {
		hover := brandingDarken(cfg.Accent, 0.85)
		rr, gg, bb := brandingRGB(cfg.Accent)
		c.BrandCSS = template.HTML(fmt.Sprintf(
			`<style>:root{--px-accent:%s;--px-accent-hover:%s;--px-accent-soft:rgba(%d,%d,%d,0.14)}</style>`,
			cfg.Accent, hover, rr, gg, bb))
	}

	// Header: logo and/or display name above the card.
	if cfg.LogoURL != "" || cfg.Name != "" {
		var b strings.Builder
		b.WriteString(`<div class="pbrand">`)
		if cfg.LogoURL != "" {
			b.WriteString(`<img class="pbrand__logo" src="` + template.HTMLEscapeString(cfg.LogoURL) + `" alt="">`)
		}
		if cfg.Name != "" {
			b.WriteString(`<span class="pbrand__name">` + template.HTMLEscapeString(cfg.Name) + `</span>`)
		}
		b.WriteString(`</div>`)
		c.BrandHead = template.HTML(b.String())
	}

	// Footer: optional custom line + the powered-by line (default ON).
	if cfg.FooterText != "" || cfg.HidePoweredBy {
		custom := ""
		if cfg.FooterText != "" {
			custom = `<div class="pfoot__custom">` + template.HTMLEscapeString(cfg.FooterText) + `</div>`
		}
		wrap := func(powered string) template.HTML {
			if custom == "" && powered == "" {
				return template.HTML("")
			}
			return template.HTML(`<div class="pfoot">` + custom + powered + `</div>`)
		}
		poweredTR, poweredEN := publicFooterTR, publicFooterEN
		if cfg.HidePoweredBy {
			poweredTR, poweredEN = "", ""
		}
		c.FooterTR = wrap(poweredTR)
		c.FooterEN = wrap(poweredEN)
	}
	return c
}

// brandingRGB parses a validated #rgb / #rrggbb hex into components.
func brandingRGB(hexColor string) (r, g, b int) {
	h := strings.TrimPrefix(hexColor, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	_, _ = fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b)
	return
}

// brandingDarken scales a validated hex color toward black (factor < 1) for
// the hover token.
func brandingDarken(hexColor string, factor float64) string {
	r, g, b := brandingRGB(hexColor)
	scale := func(v int) int {
		n := int(float64(v) * factor)
		if n < 0 {
			return 0
		}
		if n > 255 {
			return 255
		}
		return n
	}
	return fmt.Sprintf("#%02x%02x%02x", scale(r), scale(g), scale(b))
}

/* ===== /wiring:e1 ===== */

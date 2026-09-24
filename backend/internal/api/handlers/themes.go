package handlers

/* ===== tema:v1 — operator-defined themes =====

"Make filex look like OUR product", expressed as data rather than as a
stylesheet. A theme is a name plus two maps of `--fe-*` custom properties —
one for the light variant, one for the dark — stored in the `custom_themes`
table (migration 00051) and handed to the browser beside the built-in palettes
in packages/core/src/lib/themes.ts. Picking one is exactly like picking Night
Blue; the only thing that marks it out is the `custom:` prefix on its id.

WHY TOKENS AND NOT CSS. The two live side by side in this product and they are
not the same tool. A token set cannot lie: every value is a colour, a length or
a font stack, checked against a pattern before it is stored, and there is no
value an operator can type that makes a delete button look like a cancel
button. Raw CSS (custom_css.go) can do anything, which is why it is off by
default, supertenant-only, and kept away from the login page and from the
screen that turns it off. An operator who only wants their brand colours should
never have to reach for the dangerous tool, and after this round they do not.

WHERE IT IS SERVED FROM, and why that is not /api/branding:

	GET    /api/appearance          public, 60s cache — { themes, default_theme_id }
	GET    /api/admin/themes        supertenant — the same list, for the editor
	PUT    /api/admin/themes/{key}  supertenant — create or replace (also the import)
	DELETE /api/admin/themes/{key}  supertenant

/api/branding resolves per HOST and is overlaid per tenant; themes are
instance-wide and have no tenant column. Putting them on that payload would
have meant a hot, host-keyed cache carrying kilobytes of JSON that is identical
for every host. A separate endpoint with its own cache says what is true: one
installation, one set of themes.

WHO MAY WRITE: `requireSupertenant`, for the reason the table has no tenant
column — a theme written by one tenant's admin would paint every other tenant's
users. The instance DEFAULT is a settings key (`ui.default_theme`), which
`allowSettingWrite` already refuses to a tenant admin because it is not in
`tenantScopedSettingKey`. Both doors, one rule.

⚠ DELETION AND FALLBACK. Deleting a theme does NOT hunt down the people using
it. It does not have to: every id is RESOLVED against the list of themes that
exist — on the server for the instance default (`ResolveThemeID` below) and in
the browser for a person's own choice (`themeById` returns undefined for an
unknown id and the explorer falls back to the stock palette). Resolution cannot
miss a storage location the way a cleanup pass can, and there is already more
than one of those. */

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// CustomThemePrefix marks a custom theme's palette id apart from every
// built-in one. Stored WITHOUT it (custom_themes.theme_key), served WITH it.
const CustomThemePrefix = "custom:"

// DefaultThemeSettingKey names the palette a person sees before they have
// chosen one — and the ONLY palette an anonymous visitor's share page can
// follow, since that page has no account to read a preference from.
const DefaultThemeSettingKey = "ui.default_theme"

// BuiltinDefaultThemeID is the stock palette: the product's own colours, and
// the answer whenever a named theme cannot be resolved.
const BuiltinDefaultThemeID = "default"

// builtinThemeIDs are the palette ids packages/core/src/lib/themes.ts ships.
// The server needs them for one job only — deciding whether `ui.default_theme`
// names something that exists — so this is a list of ids and not a second copy
// of the palettes.
//
// ⚠ web/tests/api/customThemePalette.test.ts asserts this list against the
// THEMES array it mirrors. A palette added to the client and not here would be
// refused as an instance default silently, with the operator's choice
// reverting to stock the next time the page loaded.
var builtinThemeIDs = []string{
	BuiltinDefaultThemeID, "night", "forest", "amber", "lilac", "contrast", "gray", "terminal",
}

/* ------------------------------------------------------------------ */
/* The token set                                                       */
/* ------------------------------------------------------------------ */

// themeColorTokens are the colours an operator actually composes.
//
// The set is the smallest one that can carry a brand without leaving a hole
// somebody has to notice by looking. Grouped as the editor groups them:
//
//   - two grounds: the page, and anything raised off it (cards, menus, bars)
//   - two inks: ordinary text and the quieter kind
//   - one line: the border colour every separator is derived from
//   - the primary and the three companions it cannot do without — `hover` (the
//     pressed state), `soft` (the tint that marks "this is the selected
//     thing") and `ink` (what is legible ON that tint). Those are tokens
//     rather than derivations because themes.ts records what happened when two
//     of them were missing: nine rules in base.css kept painting stock blue
//     inside a palette that was not blue, and nothing errored.
//   - three signals: danger, warning, success. They ARE themeable here even
//     though the built-in palettes deliberately freeze them, because an
//     operator matching a corporate palette is the one case where "our red" is
//     a real requirement, and because leaving them out would put a bright
//     stock red inside an otherwise muted brand.
//
// Everything else is DERIVED by the editor and stored in the same map, so the
// server keeps complete palettes and the browser does no colour maths at paint
// time. See web/src/lib/themeTokens.ts for the derivations.
var themeColorTokens = []string{
	"--fe-bg",
	"--fe-bg-elev",
	"--fe-text",
	"--fe-text-muted",
	"--fe-border",
	"--fe-primary",
	"--fe-primary-hover",
	"--fe-primary-soft",
	"--fe-primary-ink",
	"--fe-danger",
	"--fe-warning",
	"--fe-ok",
}

// themeDerivedColorTokens are computed by the editor from the set above and
// stored alongside it. Listed here because the VALIDATOR has to accept them —
// a token the client may store but the server does not know is a token that
// silently disappears on the next save.
var themeDerivedColorTokens = []string{
	"--fe-bg-hover",
	"--fe-bg-selected",
	"--fe-border-soft",
	"--fe-border-strong",
	"--fe-text-on-primary",
	"--fe-danger-hover",
	"--fe-keep-ok",
}

// themeLengthTokens are the metrics. `--fe-radius` is the one an operator
// types; the other three are the editor's derivations from it.
//
// ⚠ Metrics are stored in BOTH variants even though they are set once. The
// alternative — columns of their own — would mean the stored shape stopped
// being `ThemeDef`, and every consumer would need to know that two of the
// tokens live somewhere else.
var themeLengthTokens = []string{
	"--fe-radius", "--fe-radius-sm", "--fe-radius-md", "--fe-radius-lg",
}

// themeFontTokens are the face. One token, deliberately: `--fe-font-mono` is
// not themeable because a monospace stack that is not monospace breaks the one
// thing it is for (aligned hashes, sizes and paths).
var themeFontTokens = []string{"--fe-font"}

// ThemeAuthoredColorTokens exposes the authored colour list so a token added
// here and not to the editor is caught by a test rather than discovered.
func ThemeAuthoredColorTokens() []string { return append([]string(nil), themeColorTokens...) }

// ThemeAllTokens exposes the whole allowlist, in declaration order.
func ThemeAllTokens() []string {
	out := append([]string(nil), themeColorTokens...)
	out = append(out, themeDerivedColorTokens...)
	out = append(out, themeLengthTokens...)
	return append(out, themeFontTokens...)
}

// themeTokenKind classifies a token name, or returns "" when it is not one a
// theme may carry.
//
// ⚠⚠ THIS IS AN ALLOWLIST, and the direction matters for the same reason it
// does in allowSettingWrite. These values are interpolated verbatim into a
// <style> block on a page a stranger loads. A denylist would mean a token
// invented next year is injectable until somebody remembers to forbid it.
func themeTokenKind(name string) string {
	for _, k := range themeColorTokens {
		if k == name {
			return "color"
		}
	}
	for _, k := range themeDerivedColorTokens {
		if k == name {
			return "color"
		}
	}
	for _, k := range themeLengthTokens {
		if k == name {
			return "length"
		}
	}
	for _, k := range themeFontTokens {
		if k == name {
			return "font"
		}
	}
	return ""
}

var (
	// Hex only — #rgb, #rgba, #rrggbb, #rrggbbaa. Deliberately NOT rgb()/hsl():
	// a function call means parentheses and commas in a value that is about to
	// be printed inside a <style> block, and every built-in palette is already
	// hex. The narrowest grammar that does the job is the one that cannot be
	// argued with.
	themeHexRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	// A radius: `0`, or up to two digits with one optional decimal, in px.
	themeLengthRe = regexp.MustCompile(`^(?:0|\d{1,2}(?:\.\d)?px)$`)
	// A font stack: names, quotes, spaces, commas, dots, hyphens. No
	// parentheses (so `url(` cannot appear), no semicolons or braces (so the
	// declaration cannot be escaped), no angle brackets, no backslash.
	themeFontRe = regexp.MustCompile(`^[A-Za-z0-9 ,._"'-]{1,200}$`)
	// A slug. Lowercase so the id is stable across databases that compare
	// case-insensitively, and bounded so it fits the MySQL unique key.
	themeKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// themeNameMaxRunes caps the display name — it sits under a palette card.
const themeNameMaxRunes = 60

// validateThemeTokens checks one variant's map, returning the first problem.
func validateThemeTokens(variant string, m map[string]string) error {
	if len(m) == 0 {
		return fmt.Errorf("theme %s variant has no tokens", variant)
	}
	for name, value := range m {
		kind := themeTokenKind(name)
		if kind == "" {
			return fmt.Errorf("theme %s variant: %q is not a themeable token", variant, name)
		}
		value = strings.TrimSpace(value)
		switch kind {
		case "color":
			if !themeHexRe.MatchString(value) {
				return fmt.Errorf("theme %s variant: %s must be a hex colour like #2f6ceb", variant, name)
			}
		case "length":
			if !themeLengthRe.MatchString(value) {
				return fmt.Errorf("theme %s variant: %s must be a px length like 8px", variant, name)
			}
		case "font":
			if !themeFontRe.MatchString(value) {
				return fmt.Errorf("theme %s variant: %s must be a plain font stack", variant, name)
			}
		}
	}
	// Every authored colour must be present in BOTH variants. The built-in
	// palettes are held to exactly this rule by
	// web/tests/api/themeTokenParity.test.ts, for the same reason: a palette
	// missing one token does not look broken, it looks like the stock colour
	// turned up in one corner of somebody's brand.
	for _, k := range themeColorTokens {
		if strings.TrimSpace(m[k]) == "" {
			return fmt.Errorf("theme %s variant is missing %s", variant, k)
		}
	}
	return nil
}

// ValidateCustomTheme checks a whole theme before it is stored or exported.
// It normalises in place (trimmed name, lowercased key) so callers store
// exactly what was checked.
func ValidateCustomTheme(t *model.CustomTheme) error {
	if t == nil {
		return errors.New("theme is empty")
	}
	key := strings.TrimSpace(strings.ToLower(t.Key))
	if !themeKeyRe.MatchString(key) {
		return errors.New("theme key must be 1-40 characters of a-z, 0-9 and -")
	}
	t.Key = key
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return errors.New("theme name is required")
	}
	if utf8.RuneCountInString(t.Name) > themeNameMaxRunes {
		return fmt.Errorf("theme name is capped at %d characters", themeNameMaxRunes)
	}
	if err := validateThemeTokens("light", t.TokensLight); err != nil {
		return err
	}
	return validateThemeTokens("dark", t.TokensDark)
}

/* ------------------------------------------------------------------ */
/* Resolution                                                          */
/* ------------------------------------------------------------------ */

// ResolveThemeID answers "which palette should this be?" for an id that may
// name a theme which no longer exists.
//
// ⚠⚠ THIS IS HOW DELETION IS SAFE. Nothing rewrites the stored choices of the
// people who were using a deleted theme; every read RESOLVES instead. A
// cleanup pass has to know every place an id can be written — localStorage in
// one browser, a per-user document in the database, the instance default in
// settings — and it is wrong the moment a fourth place appears. Resolution is
// right by construction: an id that is not in the list of themes that exist is
// the stock palette, everywhere.
func ResolveThemeID(requested string, custom []*model.CustomTheme) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return BuiltinDefaultThemeID
	}
	for _, id := range builtinThemeIDs {
		if id == requested {
			return requested
		}
	}
	if key, ok := strings.CutPrefix(requested, CustomThemePrefix); ok {
		for _, t := range custom {
			if t != nil && t.Key == key {
				return requested
			}
		}
	}
	return BuiltinDefaultThemeID
}

/* ------------------------------------------------------------------ */
/* Export / import                                                     */
/* ------------------------------------------------------------------ */

// ThemeExportVersion is the shape marker carried by an exported file. A theme
// built for one customer is meant to be carried to another, which means the
// file outlives the version that wrote it; a marker is what lets a later filex
// refuse a file it cannot read instead of importing half of it.
const ThemeExportVersion = 1

// ThemeExport is the JSON form of a theme — what "Export" downloads and what
// "Import" reads.
//
// ⚠ It carries the KEY, so importing the same file twice replaces rather than
// duplicates. Carrying a theme to another instance and then carrying a fix
// over is the ordinary case, and a second "Acme (1)" would be the wrong answer
// to it; the admin screen names the key it is about to overwrite.
type ThemeExport struct {
	Filex int               `json:"filex_theme"`
	Key   string            `json:"key"`
	Name  string            `json:"name"`
	Light map[string]string `json:"light"`
	Dark  map[string]string `json:"dark"`
}

// ExportTheme renders a stored theme as its portable document.
func ExportTheme(t *model.CustomTheme) ThemeExport {
	return ThemeExport{
		Filex: ThemeExportVersion,
		Key:   t.Key,
		Name:  t.Name,
		Light: t.TokensLight,
		Dark:  t.TokensDark,
	}
}

// ImportTheme parses and validates a portable document.
func ImportTheme(raw []byte) (*model.CustomTheme, error) {
	var doc ThemeExport
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("this file is not a filex theme (it is not valid JSON)")
	}
	if doc.Filex != ThemeExportVersion {
		return nil, fmt.Errorf("this file says it is theme format %d; this filex reads format %d",
			doc.Filex, ThemeExportVersion)
	}
	t := &model.CustomTheme{
		Key:         doc.Key,
		Name:        doc.Name,
		TokensLight: doc.Light,
		TokensDark:  doc.Dark,
	}
	if err := ValidateCustomTheme(t); err != nil {
		return nil, err
	}
	return t, nil
}

/* ------------------------------------------------------------------ */
/* Public appearance payload                                           */
/* ------------------------------------------------------------------ */

// ThemeWire is one palette as the browser receives it — the same shape as a
// built-in `ThemeDef`, with the id already prefixed.
type ThemeWire struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Light map[string]string `json:"light"`
	Dark  map[string]string `json:"dark"`
}

// AppearancePayload is GET /api/appearance.
type AppearancePayload struct {
	Themes         []ThemeWire `json:"themes"`
	DefaultThemeID string      `json:"default_theme_id"`
}

// appearanceCacheTTL bounds staleness on the public endpoint. Writes through
// the admin handler invalidate immediately; the TTL only covers a row changed
// out of band.
const appearanceCacheTTL = 15 * time.Second

// AppearanceSource resolves and caches the instance's themes.
//
// ONE cache entry, not one per host: unlike branding there is no tenant
// overlay, because the table has no tenant column and the admin routes are
// supertenant-only. A nil receiver is safe and yields the stock answer.
type AppearanceSource struct {
	Store db.Store

	mu     sync.Mutex
	cached *AppearancePayload
	exp    time.Time
}

// NewAppearanceSource constructs the shared source.
func NewAppearanceSource(store db.Store) *AppearanceSource {
	return &AppearanceSource{Store: store}
}

// Invalidate drops the cache — called after any theme or default write.
func (a *AppearanceSource) Invalidate() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.cached = nil
	a.mu.Unlock()
}

// Payload returns the effective appearance for this instance.
func (a *AppearanceSource) Payload(ctx context.Context) AppearancePayload {
	if a == nil || a.Store == nil {
		return AppearancePayload{Themes: []ThemeWire{}, DefaultThemeID: BuiltinDefaultThemeID}
	}
	a.mu.Lock()
	if a.cached != nil && time.Now().Before(a.exp) {
		p := *a.cached
		a.mu.Unlock()
		return p
	}
	a.mu.Unlock()

	themes, err := a.Store.ListCustomThemes(ctx)
	if err != nil {
		// A database that cannot answer must not take the login page with it:
		// the stock palette is a complete, correct answer.
		return AppearancePayload{Themes: []ThemeWire{}, DefaultThemeID: BuiltinDefaultThemeID}
	}
	wire := make([]ThemeWire, 0, len(themes))
	for _, t := range themes {
		wire = append(wire, ThemeWire{
			ID:    CustomThemePrefix + t.Key,
			Name:  t.Name,
			Light: t.TokensLight,
			Dark:  t.TokensDark,
		})
	}
	def := BuiltinDefaultThemeID
	if m, serr := a.Store.ListSettings(ctx); serr == nil {
		def = ResolveThemeID(m[DefaultThemeSettingKey], themes)
	}
	p := AppearancePayload{Themes: wire, DefaultThemeID: def}

	a.mu.Lock()
	a.cached = &p
	a.exp = time.Now().Add(appearanceCacheTTL)
	a.mu.Unlock()
	return p
}

// DefaultTokens returns the token map of the instance default theme for the
// requested variant, or nil when the default is a built-in palette.
//
// ⚠ This is what the PUBLIC pages use, and it reads the instance default and
// nothing else. A share page, a PIN gate and a signing page are looked at by
// people with no account here; resolving anybody's personal palette on them
// would be both impossible and a disclosure — the visitor would learn which
// palette the sender had picked.
func (a *AppearanceSource) DefaultTokens(ctx context.Context, dark bool) map[string]string {
	p := a.Payload(ctx)
	for _, t := range p.Themes {
		if t.ID != p.DefaultThemeID {
			continue
		}
		if dark {
			return t.Dark
		}
		return t.Light
	}
	return nil
}

/* ------------------------------------------------------------------ */
/* HTTP: public                                                        */
/* ------------------------------------------------------------------ */

// Appearance serves the public themes endpoint.
type Appearance struct{ Source *AppearanceSource }

// NewAppearance constructs the public handler.
func NewAppearance(src *AppearanceSource) *Appearance { return &Appearance{Source: src} }

// Get returns the instance's themes and default. Public (no auth): the login
// page and every share page need it before a session exists.
//
// ⚠ It carries NO raw CSS. That used to ride /api/branding, which is how the
// login page ended up wearing an operator stylesheet it must not wear — see
// custom_css.go.
func (h *Appearance) Get(w http.ResponseWriter, r *http.Request) {
	// ⚠ Revalidated rather than held for a minute — a theme just composed is
	// on the next page load (public_cache.go).
	writePublicJSON(w, r, h.Source.Payload(r.Context()))
}

/* ------------------------------------------------------------------ */
/* HTTP: admin                                                         */
/* ------------------------------------------------------------------ */

// themesAreInstanceWide is the sentence a tenant admin reads on refusal.
const themesAreInstanceWide = "themes are instance-wide: a theme written here would paint every tenant's users, so only the platform operator may change them"

// Themes handles /api/admin/themes.
type Themes struct {
	Store      db.Store
	Appearance *AppearanceSource
}

// NewThemes constructs the admin handler.
func NewThemes(store db.Store, src *AppearanceSource) *Themes {
	return &Themes{Store: store, Appearance: src}
}

// List returns every stored theme, for the editor.
func (h *Themes) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, themesAreInstanceWide) {
		return
	}
	themes, err := h.Store.ListCustomThemes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]ThemeExport, 0, len(themes))
	for _, t := range themes {
		out = append(out, ExportTheme(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": out})
}

// Put creates or replaces one theme. This is ALSO the import path: the body is
// the exported document, so "import" on the admin screen is a file read plus
// this call — one shape, one validator, no second way in.
func (h *Themes) Put(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, themesAreInstanceWide) {
		return
	}
	var doc ThemeExport
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// The URL names the theme; a body that disagrees loses, so the same
	// document can be re-keyed on import without editing the file by hand.
	if k := strings.TrimSpace(chi.URLParam(r, "key")); k != "" {
		doc.Key = k
	}
	if doc.Filex == 0 {
		doc.Filex = ThemeExportVersion
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	t, err := ImportTheme(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpsertCustomTheme(r.Context(), t); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.Appearance.Invalidate()
	writeJSON(w, http.StatusOK, ExportTheme(t))
}

// Delete removes a theme.
//
// ⚠ It does NOT touch anybody's stored preference — see ResolveThemeID. The
// one thing it does clean is the instance DEFAULT, and only because that is a
// choice an operator made deliberately: leaving `ui.default_theme` pointing at
// a deleted theme would resolve correctly but read as a bug in the admin
// screen, which would show a default nobody can find in the list.
func (h *Themes) Delete(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, themesAreInstanceWide) {
		return
	}
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	removed, err := h.Store.DeleteCustomTheme(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such theme"})
		return
	}
	if m, serr := h.Store.ListSettings(r.Context()); serr == nil {
		if m[DefaultThemeSettingKey] == CustomThemePrefix+key {
			_ = h.Store.UpsertSetting(r.Context(), DefaultThemeSettingKey, BuiltinDefaultThemeID)
		}
	}
	h.Appearance.Invalidate()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ------------------------------------------------------------------ */
/* Public-page CSS                                                     */
/* ------------------------------------------------------------------ */

// pxFromFe maps the explorer's token names onto the ones the dependency-free
// public pages use (publicPageStyle in share.go).
//
// ⚠ Two namespaces exist because the public pages are rendered by the Go
// binary with no bundler and no access to packages/core; they were written
// with their own short `--px-*` set long before themes existed. Mapping is the
// honest fix — the alternative is teaching a theme to emit two sets of names,
// which would put the public pages' private vocabulary into every exported
// theme file.
//
// `--px-bg1`/`--px-bg2` are the page's gradient: the page ground and the
// raised one, which is what the stock pair already is — a barely-there wash
// rather than two different colours.
var pxFromFe = []struct{ px, fe string }{
	{"--px-bg1", "--fe-bg"},
	{"--px-bg2", "--fe-bg-elev"},
	{"--px-card", "--fe-bg-elev"},
	{"--px-fg", "--fe-text"},
	{"--px-muted", "--fe-text-muted"},
	{"--px-line", "--fe-border"},
	{"--px-accent", "--fe-primary"},
	{"--px-accent-hover", "--fe-primary-hover"},
	{"--px-accent-soft", "--fe-primary-soft"},
	{"--px-ok", "--fe-ok"},
	{"--px-err", "--fe-danger"},
}

// publicThemeCSS renders the instance default theme as a <style> block for the
// public pages, or "" when there is nothing to say.
//
// ⚠ Every value has already passed themeHexRe / themeFontRe on the way into
// the database, and is re-checked HERE before it is printed. A row could have
// been written by an older version, by a future one, or by somebody with a
// database client; this function prints into a <style> block on a page a
// stranger loads, so it trusts nothing it did not just check.
func publicThemeCSS(light, dark map[string]string) string {
	lightDecls := pxDecls(light)
	darkDecls := pxDecls(dark)
	if lightDecls == "" && darkDecls == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<style>")
	if lightDecls != "" {
		b.WriteString(":root{" + lightDecls + "}")
	}
	if darkDecls != "" {
		b.WriteString("@media (prefers-color-scheme: dark){:root{" + darkDecls + "}}")
	}
	b.WriteString("</style>")
	return b.String()
}

// pxDecls renders one variant's mapped declarations, dropping anything that
// does not re-validate.
func pxDecls(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	var b strings.Builder
	for _, pair := range pxFromFe {
		v := strings.TrimSpace(m[pair.fe])
		if v == "" || !themeHexRe.MatchString(v) {
			continue
		}
		b.WriteString(pair.px + ":" + v + ";")
	}
	// The face travels too — a brand that is a typeface rather than a colour
	// is still a brand, and the public page is the surface strangers judge.
	if f := strings.TrimSpace(m["--fe-font"]); f != "" && themeFontRe.MatchString(f) {
		b.WriteString("--px-font:" + f + ";")
	}
	return b.String()
}

/* ===== /tema:v1 ===== */

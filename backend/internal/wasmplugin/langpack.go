package wasmplugin

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Language packs: the languages apps add to filex itself ─────────────
//
// An app's `ui_locales` carries a language for the INTERFACE — every string
// of the explorer and of the admin panel, by filex's own keys. An app that
// carries nothing else (wire.Manifest.IsLanguagePack) is a language pack: it
// installs from its manifest alone, has no module, and never starts a
// runtime instance. docs/PLUGIN-KIT.md → "Writing a language pack" is the
// contract; this file is the host's half of it.

// The two kinds of app, as Status.Kind and DryRunAnswer.Kind say them.
const (
	KindApp          = "app"
	KindLanguagePack = "language_pack"
)

func kindOf(m *Manifest) string {
	if m != nil && m.IsLanguagePack() {
		return KindLanguagePack
	}
	return KindApp
}

func sortedTags(m map[string]map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// wasmPath is the module file a staged install writes; "" when there is none.
func (s *staged) wasmPath() string {
	if s.wasm == nil {
		return ""
	}
	return "plugin.wasm"
}

// stagePack is stage() for a language pack: the manifest IS the payload.
//
// ⚠⚠ The integrity rules do not disappear with the module — they move to the
// manifest. `sha256` (a URL install's pin) and the detached signature an
// instance with FILEX_PLUGIN_TRUSTED_KEYS demands are checked against the
// MANIFEST's hash, exactly as a module's would be. Otherwise "this instance
// only runs signed apps" would quietly stop meaning anything for the one kind
// of app that rewrites every string a visitor reads.
func (r *Registry) stagePack(m *Manifest, in *InstallInput) (*staged, error) {
	if in.Wasm != nil {
		return nil, installErr(ErrCodeManifestInvalid, "this manifest is a language pack — it declares no action, screen, public page, setting or permission, so there is nothing a module could run and filex does not take one. Install the manifest alone, or declare what the module does.")
	}
	// "manifest": a pack has no module, so the pin and the signature are over
	// the manifest's own bytes — exactly as uploaded or fetched.
	sum, signd, err := r.checkIntegrity("manifest", in.Manifest, in.SHA256, in.Signature)
	if err != nil {
		return nil, err
	}
	return &staged{m: m, sum: sum, signd: signd}, nil
}

// ── Coverage ───────────────────────────────────────────────────────────

// LanguageRow is one language an app adds, as the Apps screens show it.
//
// ⚠ Coverage is measured against the CURRENT catalogue — the one this very
// binary's interface draws from — not against whatever the pack's author had
// when they wrote it. A pack written for v0.43 and run on v0.45 says how much
// of v0.45 it covers, and every string it lacks falls back to English.
type LanguageRow struct {
	Code string `json:"code"`
	// Keys the pack carries for this language (empty values not counted).
	Keys int `json:"keys"`
	// Translated is how many of those the current catalogue has; Unknown is
	// the rest — keys filex no longer has, or never had (typos). Both are
	// only meaningful when Total > 0.
	Translated int `json:"translated"`
	Unknown    int `json:"unknown"`
	// Total is the size of the current catalogue; 0 means this binary was
	// built without one (a development build), and coverage is unknown.
	Total int `json:"total"`
	// Percent is floor(100 × Translated / Total): 100 only when nothing is
	// missing, so "100 %" is never a rounding of "almost".
	Percent int `json:"percent"`
	// RTL: a right-to-left language (wire.IsRTL) — the interface is laid out
	// right to left in it.
	RTL bool `json:"rtl"`
}

// catalogue is the set of every key filex's interface looks up, loaded once
// at start-up from the file the web build embeds (SetCatalogue).
type catalogue map[string]struct{}

// SetCatalogue gives the registry the current string catalogue — every key
// of the explorer's and the admin panel's English tables — so the Apps
// screens can say how much of it a language covers.
//
// ⚠ Called with the keys of `admin/i18n/filex-catalogue-en.json`, which the
// web build writes (web/vite.config.ts → scripts/lib/i18n-catalogue.mjs) and
// the binary embeds. A binary built without the web build has none, and
// coverage then reads "unknown" rather than a made-up percentage.
func (r *Registry) SetCatalogue(keys []string) {
	c := make(catalogue, len(keys))
	for _, k := range keys {
		c[k] = struct{}{}
	}
	r.cat.Store(&c)
}

func (c catalogue) has(k string) bool { _, ok := c[k]; return ok }

// pluralFormBase splits `<base>_<category>` for the five CLDR category
// suffixes (zero, one, two, few, many; the plain key is `other`).
func pluralFormBase(k string) (string, bool) {
	i := strings.LastIndexByte(k, '_')
	if i <= 0 {
		return "", false
	}
	switch k[i+1:] {
	case "zero", "one", "two", "few", "many":
		return k[:i], true
	}
	return "", false
}

// ParseCatalogue reads the embedded catalogue file (a flat key → English
// object — the exact shape of one `ui_locales` language) into its keys.
func ParseCatalogue(b []byte) ([]string, error) {
	var flat map[string]string
	if err := json.Unmarshal(b, &flat); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(flat))
	for k := range flat {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// LanguageRows describes every language m adds, with its coverage. Nil-safe
// on the receiver (no catalogue → coverage unknown).
func (r *Registry) LanguageRows(m *Manifest) []LanguageRow {
	if m == nil || len(m.UILocales) == 0 {
		return nil
	}
	var cat catalogue
	if r != nil {
		if p := r.cat.Load(); p != nil {
			cat = *p
		}
	}
	out := make([]LanguageRow, 0, len(m.UILocales))
	for _, code := range sortedTags(m.UILocales) {
		row := LanguageRow{Code: code, RTL: wire.IsRTL(code), Total: len(cat)}
		for k, v := range m.UILocales[code] {
			if strings.TrimSpace(v) == "" {
				// An empty value is untranslated: the browser skips it and
				// shows English, so it must not count as coverage either.
				continue
			}
			row.Keys++
			if cat == nil {
				continue
			}
			if _, ok := cat[k]; ok {
				row.Translated++
			} else if base, isForm := pluralFormBase(k); isForm && cat.has(base) {
				// A plural form the catalogue does not list — `x_few`, `x_two`
				// for a language with more categories than English (the CLDR
				// grammar, docs/PLUGIN-KIT.md). Extra words for a key filex
				// HAS: neither coverage (the plain form is what counts) nor
				// "unknown" (it is not a typo or a stale key).
				continue
			} else {
				row.Unknown++
			}
		}
		if row.Total > 0 {
			row.Percent = row.Translated * 100 / row.Total
		}
		out = append(out, row)
	}
	return out
}

// ── What the interface is offered ──────────────────────────────────────

// UILocaleInfo is one language the running apps add, as GET
// /api/public/branding lists it. ⚠ No strings: those are fetched for the one
// language a visitor actually picks (UILocale, GET /api/public/ui-locales/
// {code}). A complete language is ~150 KB of JSON, and the branding answer is
// read by every public page and every panel start.
type UILocaleInfo struct {
	Code string `json:"code"`
	// Source is always "plugin" here; the field is the client's
	// PublicLocaleOption, which also describes the built-in languages.
	Source string `json:"source"`
	// Plugin names the app that brought it (the first, by name, when several
	// do) — shown beside the language so nobody wonders where it came from.
	Plugin string `json:"plugin"`
	RTL    bool   `json:"rtl,omitempty"`
}

// UILocaleList is every language the running apps add, by tag.
func (r *Registry) UILocaleList() []UILocaleInfo {
	seen := map[string]bool{}
	var out []UILocaleInfo
	for _, p := range r.All() { // sorted by name: the first app is stable
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		for _, code := range sortedTags(p.Manifest.UILocales) {
			if seen[code] {
				continue
			}
			seen[code] = true
			out = append(out, UILocaleInfo{Code: code, Source: "plugin", Plugin: p.Row.Name, RTL: wire.IsRTL(code)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// UILocale is one language's strings, merged over every running app that
// ships it; nil when none does.
//
// ⚠ First app (by name) wins a contested key — arbitrary, but stable across
// restarts, which is what matters to somebody reading the screen. ⚠ An empty
// value is left out rather than served: an app that has not translated a
// string yet must fall back to English, not blank it.
func (r *Registry) UILocale(code string) map[string]string {
	code = strings.ToLower(strings.TrimSpace(code))
	var out map[string]string
	for _, p := range r.All() {
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		strs, ok := p.Manifest.UILocales[code]
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(strs))
		}
		for k, v := range strs {
			if _, taken := out[k]; taken || strings.TrimSpace(v) == "" {
				continue
			}
			out[k] = v
		}
	}
	return out
}

// UIString is ONE key of one language, under the same rules as UILocale
// (running apps only, first app by name wins, an empty value is no value) —
// without building the whole ~3 000-key map. It is how the server's own
// catalogue (internal/srvtext) reads a pack: a mail or a public page asks for
// a few dozen keys, and UILocale would merge every string of every pack for
// each of them.
func (r *Registry) UIString(code, key string) (string, bool) {
	if r == nil {
		return "", false
	}
	code = strings.ToLower(strings.TrimSpace(code))
	for _, p := range r.All() {
		if state, _ := p.State(); state != StateRunning {
			continue
		}
		if v, ok := p.Manifest.UILocales[code][key]; ok && strings.TrimSpace(v) != "" {
			return v, true
		}
	}
	return "", false
}

// UILocaleCodes lists the languages the running apps add, sorted — the tags
// the server's catalogue may answer in besides English and Turkish.
func (r *Registry) UILocaleCodes() []string {
	if r == nil {
		return nil
	}
	rows := r.UILocaleList()
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Code)
	}
	return out
}

// catalogueRef is the Registry field type — an atomic pointer, because the
// catalogue is set once at start-up and then read by every Apps request
// without a lock.
type catalogueRef = atomic.Pointer[catalogue]

package plugintest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Languages ──────────────────────────────────────────────────────────
//
// A plugin declares the languages it speaks (`manifest.languages`). The
// host refuses to install one whose describe answer has a Text missing one
// of them — but a Text inside a SCREEN is built at call time, so nothing
// before this kit could see it. That is how a signature pad came to say
// "Çiz / Yaz / Yükle" under an English heading: the surface was half in
// one language and no test looked.
//
// Two checks, because a screen carries two kinds of string:
//
//   - a Text (`{en: …, tr: …}`) — filex picks the language, so every
//     declared language must be there: CheckLanguages.
//   - a string the PLUGIN already picked for the call's locale (a field
//     label, an option label) — drawing the same screen once per language
//     and comparing is the only way to see it: CheckLocaleParity.

var langTag = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// HumanKeys are the property names that hold a string a person reads. The
// locale comparison only looks at these (and at every Text): an id, a key
// or an option's value is the same in every language by design, and
// warning about those would bury the one line that matters.
var HumanKeys = map[string]bool{
	"label": true, "help": true, "placeholder": true, "text": true,
	"title": true, "toast": true, "message": true, "empty": true,
	"hint": true, "description": true, "summary": true, "note": true,
	"caption": true, "subject": true, "heading": true, "reason": true,
}

// LangOpts tunes the language checks.
type LangOpts struct {
	// Strict makes warnings fail the test too.
	Strict bool
	// SameAllowed are strings that are legitimately identical in every
	// language — format names ("PNG", "JPEG"), brands, units. Compare
	// exactly.
	SameAllowed []string
	// MinSameLen is the shortest string the "same in two languages" check
	// looks at (default 4): "OK", "PDF" and "%" are not translations that
	// went missing.
	MinSameLen int
}

func (o LangOpts) allowed(s string) bool {
	for _, x := range o.SameAllowed {
		if x == s {
			return true
		}
	}
	return false
}

func (o LangOpts) worthComparing(s string) bool {
	min := o.MinSameLen
	if min == 0 {
		min = 4
	}
	if len([]rune(s)) < min || o.allowed(s) {
		return false
	}
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 2
}

// CheckLanguages fails t when a Text on the screen is missing a language
// the manifest promised.
func CheckLanguages(t TB, m wire.Manifest, s *wire.Surface) {
	t.Helper()
	InspectLanguages(m, s, LangOpts{}).Report(t, false)
}

// InspectLanguages walks every Text of a surface — the title, the toast,
// the button labels, the field errors and every Text a node carries — and
// reports the ones that do not speak every declared language.
func InspectLanguages(m wire.Manifest, s *wire.Surface, opts LangOpts) Report {
	if s == nil {
		return Report{}.err("surface", "the plugin answered no surface")
	}
	langs := Languages(m)
	var r Report
	for _, tx := range SurfaceTexts(s, langs) {
		r = append(r, inspectText(tx.Where, tx.Text, langs, opts)...)
	}
	return r
}

// CheckManifestLanguages fails t when a Text in the manifest itself — the
// plugin's label, an action's label, a public page's title — is missing a
// declared language. This is the one the host also checks at install; a
// test says so before the install does.
func CheckManifestLanguages(t TB, m wire.Manifest) {
	t.Helper()
	InspectManifestLanguages(m, LangOpts{}).Report(t, false)
}

// InspectManifestLanguages is CheckManifestLanguages' finding list.
func InspectManifestLanguages(m wire.Manifest, opts LangOpts) Report {
	langs := Languages(m)
	var r Report
	add := func(where string, t wire.Text) {
		if len(t) == 0 {
			return
		}
		r = append(r, inspectText(where, t, langs, opts)...)
	}
	add("manifest.label", m.Label)
	add("manifest.description", m.Description)
	for k, v := range m.PermissionReasons {
		add("manifest.permission_reasons."+k, v)
	}
	for i, a := range m.Actions {
		add(fmt.Sprintf("manifest.actions[%d] (%s).label", i, a.ID), a.Label)
		add(fmt.Sprintf("manifest.actions[%d] (%s).confirm", i, a.ID), a.Confirm)
	}
	for i, v := range m.Views {
		add(fmt.Sprintf("manifest.views[%d] (%s).label", i, v.ID), v.Label)
	}
	for i, p := range m.PublicPages {
		add(fmt.Sprintf("manifest.public_pages[%d] (%s).label", i, p.ID), p.Label)
	}
	for k, v := range m.Messages {
		add("manifest.messages."+k, v)
	}
	return r
}

func inspectText(where string, t wire.Text, langs []string, opts LangOpts) Report {
	var r Report
	if len(t) == 0 {
		return r.err(where, "an empty Text: nothing is shown in any language")
	}
	if strings.TrimSpace(t["en"]) == "" {
		r = r.err(where, "no `en` — English is required everywhere a Text appears; every other language falls back to it")
	}
	for _, l := range langs {
		if l == "en" {
			continue
		}
		if strings.TrimSpace(t[l]) == "" {
			r = r.err(where, "the manifest promises %q, but this Text has no %s (it would silently fall back to English: %q)", l, l, clip(t["en"], 60))
		}
	}
	declared := map[string]bool{}
	for _, l := range langs {
		declared[l] = true
	}
	for l := range t {
		if !declared[l] {
			r = r.warn(where, "carries %q, which manifest.languages does not declare; filex never asks for it", l)
		}
	}
	// The same words under two languages: either the translation is
	// missing, or a string was pasted into both.
	seen := map[string][]string{}
	for l, v := range t {
		if !declared[l] || !opts.worthComparing(v) {
			continue
		}
		seen[v] = append(seen[v], l)
	}
	for v, ls := range seen {
		if len(ls) > 1 {
			sort.Strings(ls)
			r = r.warn(where, "the same words in %s: %q — a missing translation reads exactly like this", strings.Join(ls, " and "), clip(v, 60))
		}
	}
	return r
}

// TextAt is one Text and where on the screen it was found.
type TextAt struct {
	Where string
	Text  wire.Text
}

// SurfaceTexts collects every Text a surface carries, with its path.
func SurfaceTexts(s *wire.Surface, langs []string) []TextAt {
	var out []TextAt
	if s == nil {
		return out
	}
	if len(s.Title) > 0 {
		out = append(out, TextAt{"surface.title", s.Title})
	}
	if len(s.Toast) > 0 {
		out = append(out, TextAt{"surface.toast", s.Toast})
	}
	keys := make([]string, 0, len(s.Errors))
	for k := range s.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, TextAt{"surface.errors[" + k + "]", s.Errors[k]})
	}
	for i, a := range s.Actions {
		if len(a.Label) > 0 {
			out = append(out, TextAt{fmt.Sprintf("surface.actions[%d] (%s).label", i, a.ID), a.Label})
		}
	}
	WalkNodes(s.Nodes, func(where string, n wire.Node) {
		if len(n.Props) == 0 {
			return
		}
		var generic map[string]any
		if err := remarshal(n.Props, &generic); err != nil {
			return
		}
		collectTexts(where+".props", generic, langs, &out)
	})
	return out
}

func collectTexts(prefix string, v any, langs []string, out *[]TextAt) {
	if t, ok := AsText(v, langs); ok {
		*out = append(*out, TextAt{prefix, t})
		return
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectTexts(prefix+"."+k, x[k], langs, out)
		}
	case []any:
		for i, e := range x {
			collectTexts(fmt.Sprintf("%s[%d]", prefix, i), e, langs, out)
		}
	}
}

// AsText recognises a Text: every key is a language tag, every value is a
// string, and it either carries `en` (required everywhere) or speaks only
// languages the plugin declared. The second condition is what keeps a row
// of cells like {"id": "…"} from being read as Indonesian.
func AsText(v any, langs []string) (wire.Text, bool) {
	declared := map[string]bool{}
	for _, l := range langs {
		declared[l] = true
	}
	take := func(keys []string, get func(string) (string, bool)) (wire.Text, bool) {
		if len(keys) == 0 {
			return nil, false
		}
		t := wire.Text{}
		allDeclared := true
		for _, k := range keys {
			if !langTag.MatchString(k) {
				return nil, false
			}
			s, ok := get(k)
			if !ok {
				return nil, false
			}
			if !declared[k] {
				allDeclared = false
			}
			t[k] = s
		}
		if _, hasEN := t["en"]; !hasEN && !allDeclared {
			return nil, false
		}
		return t, true
	}
	switch x := v.(type) {
	case wire.Text:
		if len(x) == 0 {
			return nil, false
		}
		return x, true
	case map[string]string:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		return take(keys, func(k string) (string, bool) { s, ok := x[k]; return s, ok })
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		return take(keys, func(k string) (string, bool) {
			s, ok := x[k].(string)
			return s, ok
		})
	}
	return nil, false
}

// ── locale parity ──────────────────────────────────────────────────────

// CheckLocaleParity fails t when the same screen, drawn once per declared
// language, is not the same screen: a section only one language has, or a
// string that is empty in one. Strings that came out identical in two
// languages are reported as warnings — that is what a missing translation
// looks like from the outside.
func CheckLocaleParity(t TB, byLocale map[string]*wire.Surface) {
	t.Helper()
	InspectLocaleParity(byLocale, LangOpts{}).Report(t, false)
}

// CheckLocaleParityOpts is CheckLocaleParity with an allowlist for strings
// that are the same in every language on purpose (format names, brands).
func CheckLocaleParityOpts(t TB, byLocale map[string]*wire.Surface, opts LangOpts) {
	t.Helper()
	InspectLocaleParity(byLocale, opts).Report(t, opts.Strict)
}

// InspectLocaleParity compares the same screen drawn in several languages.
func InspectLocaleParity(byLocale map[string]*wire.Surface, opts LangOpts) Report {
	var r Report
	if len(byLocale) < 2 {
		return r.warn("locales", "only %d language was drawn; nothing to compare", len(byLocale))
	}
	locales := make([]string, 0, len(byLocale))
	for l := range byLocale {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	ref := "en"
	if _, ok := byLocale[ref]; !ok {
		ref = locales[0]
	}

	flat := map[string]map[string]leaf{}
	paths := map[string]bool{}
	for _, l := range locales {
		f := map[string]leaf{}
		flatten("surface", toGeneric(byLocale[l]), l, locales, f)
		flat[l] = f
		for p := range f {
			paths[p] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)

	for _, p := range ordered {
		var have, missing []string
		for _, l := range locales {
			if _, ok := flat[l][p]; ok {
				have = append(have, l)
			} else {
				missing = append(missing, l)
			}
		}
		if len(missing) > 0 {
			r = r.err(p, "the %s screen has this, the %s screen does not — the same screen must have the same shape in every language",
				strings.Join(have, "/"), strings.Join(missing, "/"))
			continue
		}
		if !flat[ref][p].human {
			continue
		}
		var blank []string
		byValue := map[string][]string{}
		for _, l := range locales {
			v := strings.TrimSpace(flat[l][p].value)
			if v == "" {
				blank = append(blank, l)
			}
			byValue[flat[l][p].value] = append(byValue[flat[l][p].value], l)
		}
		if len(blank) > 0 && len(blank) < len(locales) {
			r = r.err(p, "empty in %s but not in the others — a screen must not go blank in one language", strings.Join(blank, "/"))
			continue
		}
		for v, ls := range byValue {
			if len(ls) > 1 && opts.worthComparing(v) {
				sort.Strings(ls)
				r = r.warn(p, "identical in %s: %q — either it is a name, or the %s screen is showing %s words",
					strings.Join(ls, " and "), clip(v, 60), ls[len(ls)-1], ls[0])
			}
		}
	}
	return r
}

type leaf struct {
	value string
	human bool
}

func toGeneric(v any) any {
	var out any
	if err := remarshal(v, &out); err != nil {
		return nil
	}
	return out
}

// flatten reduces a surface to path → string leaves. A Text collapses to
// the one string that locale shows, which is what makes the comparison
// meaningful: a Text carries every language whatever the call asked for.
func flatten(prefix string, v any, locale string, langs []string, out map[string]leaf) {
	if t, ok := AsText(v, langs); ok {
		out[prefix] = leaf{value: t.Get(locale), human: true}
		return
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(prefix+"."+k, x[k], locale, langs, out)
		}
	case []any:
		for i, e := range x {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), e, locale, langs, out)
		}
	case string:
		out[prefix] = leaf{value: x, human: HumanKeys[lastKey(prefix)]}
	case bool:
		out[prefix] = leaf{value: fmt.Sprint(x)}
	case float64:
		out[prefix] = leaf{value: fmt.Sprint(x)}
	case nil:
		out[prefix] = leaf{value: ""}
	default:
		out[prefix] = leaf{value: fmt.Sprint(x)}
	}
}

func lastKey(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.Index(path, "["); i > 0 {
		path = path[:i]
	}
	return path
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

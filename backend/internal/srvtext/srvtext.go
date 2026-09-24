// Package srvtext is the catalogue of every sentence the SERVER writes to a
// person: e-mails, the no-JavaScript pages behind a share or drop link, the
// Apps install review's permission descriptions, the NOT.txt beside a drop.
//
// ⚠⚠ Why it exists. Language packs (wasmplugin langpack.go) translate
// everything the BROWSER draws, from two catalogues it owns. What the server
// composed knew two languages, as `if lang == "tr"` pairs scattered over the
// handlers — and a third language fell through to whichever branch was the
// `else`. Measured 2026-09-22 with a Spanish pack on the account and the
// explorer sending `locale: "es"`: the share-link e-mail arrived in TURKISH
// (mail_templates.go's default branch), the drop notice to the owner too, and
// the drop page a Spanish browser opened said "Send files".
//
// One catalogue now answers every one of them:
//
//   - built-ins: locales/en.json and locales/tr.json, flat dotted keys under
//     `server.` — the exact shape of one language of a pack's `ui_locales`;
//   - packs: a pack's `ui_locales[<lang>]` carries `server.*` keys beside the
//     interface's own (ONE namespace, one file per language for a translator;
//     docs/PLUGIN-KIT.md "Writing a language pack" → "Text the server writes");
//   - every key a pack lacks falls back to English, never to a raw key and
//     never to the other built-in language.
//
// THE GRAMMAR (the translator's contract; scripts/i18n-validate.mjs checks it
// and Text re-checks it at run time):
//
//   - `{name}` — a placeholder: ASCII letters, digits and `_` between braces.
//     Replaced verbatim (HTML-escaped by the page when the text lands in HTML).
//     A translation must carry EXACTLY the placeholders its English carries:
//     not one fewer (the link, the PIN or the address would silently vanish
//     from an e-mail) and not one more (it would print as written).
//   - plurals by CLDR category: `<key>_zero|_one|_two|_few|_many`, the plain
//     `<key>` being `other` and the fallback — plural.go has the rules.
//   - nothing else is syntax: `@`, `|`, `%`, `{{`, and a brace that does not
//     close around a name are ordinary characters.
//
// ⚠ It is a package-level catalogue on purpose: the language of a mail is
// decided in a dozen handlers, a queue worker and a plugin host call, and
// threading one more dependency through each constructor would be a dozen
// chances to forget it. SetPacks and SetDefault are called once at start-up
// (server.go); tests swap them and restore.
package srvtext

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Prefix starts every key of this catalogue — the namespace a pack's
// `ui_locales` shares with the interface's own keys.
const Prefix = "server."

//go:embed locales/en.json locales/tr.json
var localeFS embed.FS

// builtin is the two shipped languages, loaded once. A malformed file is a
// build defect, not a runtime condition: it panics at start-up, and
// srvtext_test.go reads both files so the defect never reaches a binary.
var builtin = func() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		b, err := localeFS.ReadFile("locales/" + lang + ".json")
		if err != nil {
			panic(fmt.Sprintf("srvtext: %s: %v", lang, err))
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("srvtext: %s: %v", lang, err))
		}
		out[lang] = m
	}
	return out
}()

// Builtin returns a copy of one shipped language's table (en or tr), for tests
// and tooling; nil for any other tag.
func Builtin(lang string) map[string]string {
	src := builtin[lang]
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// BuiltinLanguages is the languages filex ships, sorted: ["en", "tr"].
func BuiltinLanguages() []string {
	out := make([]string, 0, len(builtin))
	for l := range builtin {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Keys is every key of the English catalogue, sorted.
func Keys() []string {
	out := make([]string, 0, len(builtin["en"]))
	for k := range builtin["en"] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── what language packs add ────────────────────────────────────────────

// Packs is what the running language packs add: the app registry
// (wasmplugin.Registry) satisfies it.
type Packs interface {
	// UIString is one key of one language, first running app (by name) wins;
	// ok=false when no running app carries a non-empty value for it.
	UIString(code, key string) (string, bool)
	// UILocaleCodes lists the languages the running apps add, sorted.
	UILocaleCodes() []string
}

// StaticPacks is a fixed set of pack languages ({lang: {key: text}}) —
// what tests and tools hand SetPacks instead of a running registry.
type StaticPacks map[string]map[string]string

// UIString implements Packs.
func (s StaticPacks) UIString(code, key string) (string, bool) {
	v, ok := s[code][key]
	return v, ok && strings.TrimSpace(v) != ""
}

// UILocaleCodes implements Packs.
func (s StaticPacks) UILocaleCodes() []string {
	out := make([]string, 0, len(s))
	for c := range s {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

type packsBox struct{ p Packs }

var packs atomic.Pointer[packsBox]

// SetPacks wires the language packs. nil unwires them (built-ins only).
func SetPacks(p Packs) {
	if p == nil {
		packs.Store(nil)
		return
	}
	packs.Store(&packsBox{p: p})
}

func currentPacks() Packs {
	if b := packs.Load(); b != nil {
		return b.p
	}
	return nil
}

var instanceDefault atomic.Pointer[string]

// SetDefault records the instance's default language (FILEX_DEFAULT_LOCALE):
// the answer Pick falls back to before English.
func SetDefault(tag string) {
	t := norm(tag)
	instanceDefault.Store(&t)
}

// ── which language ─────────────────────────────────────────────────────

func norm(tag string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tag)), "_", "-")
}

func primary(tag string) string {
	if i := strings.IndexByte(tag, '-'); i > 0 {
		return tag[:i]
	}
	return tag
}

// offered: a shipped language, or one a running pack adds.
func offered(tag string) bool {
	if _, ok := builtin[tag]; ok {
		return true
	}
	if p := currentPacks(); p != nil {
		for _, c := range p.UILocaleCodes() {
			if c == tag {
				return true
			}
		}
	}
	return false
}

// Resolve turns a language tag into the offered language that serves it, or
// "" when nothing does: the tag itself (`es`, `pt-br`), else its primary
// subtag (`es-MX` → `es`), else the one regional variant on offer (`pt` →
// `pt-br`) — the same matching the browser's picker does
// (packages/core lib/uiLocales), so a mail and the screen agree.
func Resolve(tag string) string {
	t := norm(tag)
	if t == "" || t == "*" {
		return ""
	}
	if offered(t) {
		return t
	}
	pri := primary(t)
	if pri != t && offered(pri) {
		return pri
	}
	if p := currentPacks(); p != nil {
		for _, c := range p.UILocaleCodes() {
			if primary(c) == pri {
				return c
			}
		}
	}
	return ""
}

// Pick is the first of the candidates that Resolve serves; then the instance
// default; then English. Callers list the candidates in the order THEIR flow
// decides (the recipient's account, the sender's choice, the request…) — this
// only answers which of them the catalogue can speak.
func Pick(candidates ...string) string {
	for _, c := range candidates {
		if r := Resolve(c); r != "" {
			return r
		}
	}
	if d := instanceDefault.Load(); d != nil {
		if r := Resolve(*d); r != "" {
			return r
		}
	}
	return "en"
}

// FromAcceptLanguage is the first tag of an Accept-Language header that an
// offered language serves, or "". Browsers list their preference first, so
// q-values are ignored (the public pages always read it this way).
func FromAcceptLanguage(h string) string {
	for _, part := range strings.Split(h, ",") {
		tag := strings.TrimSpace(part)
		if i := strings.IndexByte(tag, ';'); i >= 0 {
			tag = strings.TrimSpace(tag[:i])
		}
		if r := Resolve(tag); r != "" {
			return r
		}
	}
	return ""
}

// IsRTL reports a right-to-left language (wire.IsRTL: the one list the
// install review, the validator and the mail's Content-Language share).
func IsRTL(lang string) bool { return wire.IsRTL(lang) }

// ── the grammar ────────────────────────────────────────────────────────

var placeholderRe = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// Placeholders lists the distinct `{name}` placeholders of s, sorted.
func Placeholders(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// Vars are the named values a string's placeholders take.
type Vars map[string]string

// Fill replaces every `{name}` whose name is in vars. ⚠ A placeholder vars
// does not name is LEFT AS WRITTEN — a visible defect a test catches — rather
// than deleted into a sentence with a hole in it.
func Fill(tpl string, vars Vars) string {
	if len(vars) == 0 {
		return tpl
	}
	return placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// fits reports whether a translation in lang may stand in for key: its
// placeholders are exactly the English ones.
//
// For a category form (`<base>_one`, `_few`…) the English to compare with is
// the PLAIN form's: every placeholder it has is required, except `{count}`
// in a category that holds a single number in lang (impliesNumber) — "1 day",
// "يوم واحد" — and nothing else is allowed. ⚠ The gate is `counted` (the
// English PLAIN key exists), not "the English has a `_one`": a pack may write
// a form for a sentence English says one way for every number — plural.go.
//
// ⚠⚠ This is the run-time half of the validator's rule, and it is not
// redundant: a pack is installed by an administrator, not by its translator,
// and nothing forces anybody to run the validator. A Spanish share mail whose
// translation dropped `{pin}` would be delivered WITHOUT THE PIN; refusing
// the translation here delivers the English line with the PIN instead.
func fits(s, key, lang string) bool {
	if base, cat, ok := splitForm(key); ok && counted(base) {
		want := Placeholders(builtin["en"][base])
		have := map[string]bool{}
		for _, g := range Placeholders(s) {
			have[g] = true
		}
		allowed := map[string]bool{}
		for _, w := range want {
			allowed[w] = true
			if !have[w] && !(w == "count" && impliesNumber(lang, cat)) {
				return false
			}
		}
		for g := range have {
			if !allowed[g] {
				return false
			}
		}
		return true
	}
	en, ok := builtin["en"][key]
	if !ok {
		return false
	}
	want := Placeholders(en)
	got := Placeholders(s)
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

// lookup is one language's value for one key: a running pack first (it may
// overlay a shipped language), then the built-in table.
func lookup(lang, key string) (string, bool) {
	if p := currentPacks(); p != nil {
		if v, ok := p.UIString(lang, key); ok && strings.TrimSpace(v) != "" && fits(v, key, lang) {
			return v, true
		}
	}
	if v, ok := builtin[lang][key]; ok && strings.TrimSpace(v) != "" && fits(v, key, lang) {
		return v, true
	}
	return "", false
}

// wrote reports whether any language in lang's fallback chain has a usable
// value for key — what says a category form exists at all.
func wrote(lang, key string) bool {
	for _, l := range chain(lang) {
		if _, ok := lookup(l, key); ok {
			return true
		}
	}
	return false
}

// chain is the order languages are asked in: the language, its primary
// subtag, English.
func chain(lang string) []string {
	l := norm(lang)
	out := make([]string, 0, 3)
	if l != "" {
		out = append(out, l)
		if p := primary(l); p != l {
			out = append(out, p)
		}
	}
	if l != "en" {
		out = append(out, "en")
	}
	return out
}

// template is key's text in lang, before its placeholders are filled.
// count, when given, picks the plural form: in each language of the chain,
// the form of THAT language's category for the number, then that language's
// plain form — so a language that wrote only the plain form keeps its own
// words for every number — and only then the next language.
func template(lang, key string, count *int) string {
	plural := count != nil && counted(key)
	for _, l := range chain(lang) {
		if plural {
			if c := Category(l, *count); c != "other" {
				if v, ok := lookup(l, key+"_"+c); ok {
					return v
				}
			}
		}
		if v, ok := lookup(l, key); ok {
			return v
		}
	}
	// A key the English catalogue does not have is a programming error; the
	// key itself on the page is what makes it findable (srvtext_test.go
	// scans the tree for literal keys that do not exist).
	return key
}

// Template is key's text in lang with its placeholders unfilled — for a
// page script that fills them in the browser.
func Template(lang, key string) string { return template(lang, key, nil) }

// TemplateN is Template for a sentence about count things.
func TemplateN(lang, key string, count int) string { return template(lang, key, &count) }

// Text is key in lang, placeholders filled.
func Text(lang, key string, vars Vars) string { return Fill(template(lang, key, nil), vars) }

// Plural is Text for a sentence about count things: the form of count's CLDR
// category in lang (see plural.go), and `{count}` is always filled with the
// number.
func Plural(lang, key string, count int, vars Vars) string {
	v := Vars{"count": fmt.Sprint(count)}
	for k, x := range vars {
		v[k] = x
	}
	return Fill(template(lang, key, &count), v)
}

// Table is every key under prefix in lang, with the prefix taken off — the
// shape a page template indexes (`{{.T.pin_title}}`). Placeholders are left
// unfilled.
//
// A sentence about a count also gets one entry per category lang HAS
// (`drop_done_sub_few`…; the plain entry is `other`), each already resolved
// through the fallbacks for a number of that category — so a page script
// picks `T[key + "_" + Intl.PluralRules(lang).select(n)] || T[key]` and
// never has to know the rules. English's `_one` is only listed when lang has
// a `one` category.
func Table(lang, prefix string) map[string]string {
	en := builtin["en"]
	out := make(map[string]string, len(en)/2)
	ci := info(lang)
	for k := range en {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if base, _, ok := splitForm(k); ok && counted(base) {
			continue // the base lists its forms below
		}
		short := strings.TrimPrefix(k, prefix)
		out[short] = template(lang, k, nil)
		// ⚠ Every key is asked, not only the ones ENGLISH inflects: a pack
		// writes a form for the sentence ITS language inflects, and English
		// having one wording for every number says nothing about Arabic. A
		// category NOBODY in the chain wrote is left out — the page script
		// reads `T[key+"_"+category] || T[key]` and the plain entry above is
		// the answer for it.
		for _, c := range ci.cats {
			if c == "other" {
				continue // the plain entry above
			}
			if !wrote(lang, k+"_"+c) {
				continue
			}
			n := ci.rep[c]
			out[short+"_"+c] = template(lang, k, &n)
		}
	}
	return out
}

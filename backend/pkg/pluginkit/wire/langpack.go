package wire

import (
	"sort"
	"strings"
)

// ── Language packs ─────────────────────────────────────────────────────
//
// `ui_locales` lets an app add a language to filex ITSELF. A language is the
// whole interface — every string of the explorer AND of the admin panel —
// so a pack that carries one is a dictionary, and the limits below are sized
// for a complete translation, not for a handful of strings.
//
// ⚠ These numbers are shared by the host (internal/wasmplugin refuses a
// manifest over them) and by pluginkit/plugintest (which tells an author
// before the host does), and they are mirrored by scripts/i18n-validate.mjs
// — the translator's tool, which cannot import Go. web/tests/i18n/
// langPackContract.test.ts reads this file and the script and fails when the
// two disagree, so change them together.

// The catalogue these limits were sized against, re-measured on the shipped
// v0.43.0 tree: 3 593 keys — 1 555 in the explorer's catalogue
// (packages/core/src/locales/en.ts), 1 760 in the admin panel's
// (web/src/locales/en.json), 223 in the server's (the text filex writes into
// mail, notifications and the pages behind a link) and 55 drawn by both
// front-end catalogues — adding up to 194 961 bytes of UTF-8 for keys and
// values in English and 210 279 in Turkish. A script whose letters take two
// bytes (Arabic, Cyrillic, Greek, Hebrew) roughly doubles the value half, so a
// COMPLETE translation today is about 335 KB.
//
// ⚠ Re-measure it here when the catalogue moves: `node scripts/i18n-export.mjs`
// prints the key count, and the numbers above are what the ceilings below are
// justified by. They were still the 2026-09-21 figures (2 900 keys) after two
// waves of work had taken the catalogue to 3 593.
const (
	// MaxUILocaleBytes caps one language of one pack: the UTF-8 bytes of its
	// keys plus its values. 1 MiB is three times the largest complete
	// translation the catalogue allows today — room for the catalogue to
	// grow by half again in the wordiest two-byte script, and still a bound a
	// browser downloads in one breath.
	MaxUILocaleBytes = 1 << 20
	// MaxUILocalesBytes caps every language of one manifest together, so an
	// app may bundle several complete languages (a regional set, say)
	// without a manifest becoming a way to park arbitrary data on a server.
	MaxUILocalesBytes = 4 << 20
	// MaxUILocaleKeyBytes caps one key. The longest key in the catalogue is
	// 52 bytes (`userSettings.notifications.events.file_upload_failed`).
	MaxUILocaleKeyBytes = 128
	// MaxUILocaleValueBytes caps one translated string. The longest English
	// string is 606 bytes (`demo.alsoIncluded`); a language that runs 30 %
	// longer in a script of two-byte letters needs ~1.6 KB, so 4 KiB is room
	// to spare without admitting a paragraph that would wreck a layout.
	MaxUILocaleValueBytes = 4096
	// MaxManifestBytes caps the manifest DOCUMENT as it is uploaded or
	// fetched. The four mebibytes of strings above can arrive inflated: a
	// JSON writer that escapes non-ASCII (Python's json.dump does, by
	// default) writes each two-byte letter as a six-byte `\uXXXX` — three
	// times the bytes — so the document ceiling is the strings' ceiling times
	// three plus room for the rest of the manifest.
	MaxManifestBytes = 16 << 20
)

// IsLanguagePack reports whether the manifest is a language pack and
// nothing else: it adds at least one language to filex and declares nothing
// a module could run — no action, no screen, no public page, no setting, no
// permission, and no module address.
//
// ⚠⚠ Such an app has NO module. It installs from its manifest alone, never
// starts a runtime instance, and is listed as a language pack. A translator
// must not need a Go toolchain to ship a translation, and a module that
// could do nothing would still be a binary the administrator is asked to
// trust.
//
// ⚠ `wasm` present means "there is a module" and keeps the manifest an
// ordinary app, so an app that was built the old way (a module whose only
// job was to carry `ui_locales`) keeps loading exactly as it did.
func (m *Manifest) IsLanguagePack() bool {
	return len(m.UILocales) > 0 && m.Wasm == nil &&
		len(m.Actions) == 0 && len(m.Views) == 0 && len(m.PublicPages) == 0 &&
		len(m.Settings) == 0 && len(m.Permissions) == 0
}

// UILocaleKeyOK reports whether k may be a key of a language pack: dotted
// segments of letters, digits, `_` and `-` — the shape every key of both
// catalogues has — and none of the three segments that name a JavaScript
// object's machinery.
//
// ⚠⚠ The browser turns a dotted key into nested objects to feed the admin
// panel's vue-i18n (`a.b.c` → {a: {b: {c}}}). A segment called `__proto__`
// would walk that loop straight into Object.prototype and plant a property on
// every object of every visitor's page — public share pages included. The
// shape rule alone admits `__proto__` (underscores are legal), so the three
// are refused by name, here and again in the browser.
func UILocaleKeyOK(k string) bool {
	if k == "" || len(k) > MaxUILocaleKeyBytes {
		return false
	}
	for _, seg := range strings.Split(k, ".") {
		if seg == "" {
			return false
		}
		switch seg {
		case "__proto__", "constructor", "prototype":
			return false
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return true
}

// rtlLanguages are primary language subtags whose usual script is written
// right to left.
var rtlLanguages = map[string]bool{
	"ar": true, "arc": true, "ckb": true, "dv": true, "fa": true, "he": true,
	"iw": true, "khw": true, "ks": true, "nqo": true, "pnb": true, "prs": true,
	"ps": true, "sd": true, "syr": true, "ug": true, "ur": true, "yi": true,
}

// rtlScripts are ISO 15924 script subtags written right to left. A tag that
// names its script (`az-arab`, `ar-latn`) is decided by the script, not by
// the language.
var rtlScripts = map[string]bool{
	"adlm": true, "arab": true, "hebr": true, "nkoo": true, "rohg": true,
	"syrc": true, "thaa": true,
}

// IsRTL reports whether a language tag is written right to left. It is the
// ONE list: the `rtl` flag on the offered-language rows, the explorer's and
// the admin panel's layout direction, and the server-rendered pages' `dir`
// all come from it (docs/RTL.md).
func IsRTL(tag string) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(tag)), "-")
	for _, p := range parts[1:] {
		if len(p) == 4 {
			return rtlScripts[p]
		}
	}
	return rtlLanguages[parts[0]]
}

// textGet is Text.Get: lang, then its base language (`pt` for `pt-br`), then
// English, then any — per string, so an app that ships Spanish shows it to a
// Spanish reader and one that does not shows English (never another language
// it happens to have, and never a key).
//
// ⚠ The host passes the reader's REAL language now (a language pack's
// included: "es", "pt-br"); it used to narrow everything to en/tr, so an app
// could not be seen in a third language even if it shipped one. English and
// Turkish read exactly as before.
func textGet(t Text, lang string) string {
	if t == nil {
		return ""
	}
	if s, ok := t[lang]; ok && s != "" {
		return s
	}
	if i := strings.IndexByte(lang, '-'); i > 0 {
		if s, ok := t[lang[:i]]; ok && s != "" {
			return s
		}
	}
	if s, ok := t["en"]; ok && s != "" {
		return s
	}
	// Last resort, deterministic: a Text with no English reads the same
	// every time (it used to depend on map order).
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if t[k] != "" {
			return t[k]
		}
	}
	return ""
}

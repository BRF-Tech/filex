package srvtext

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withPacks installs a fixed set of pack languages for one test.
func withPacks(t *testing.T, p StaticPacks) {
	t.Helper()
	SetPacks(p)
	t.Cleanup(func() { SetPacks(nil) })
}

func withDefault(t *testing.T, tag string) {
	t.Helper()
	SetDefault(tag)
	t.Cleanup(func() { SetDefault("") })
}

// ── the built-in tables ────────────────────────────────────────────────

// Every key is under the one prefix, Turkish has no key English lacks, and
// every Turkish value passes the same placeholder rule a pack's must — a
// built-in that did not would silently render in English.
func TestBuiltins_AreOneCatalogue(t *testing.T) {
	en, tr := Builtin("en"), Builtin("tr")
	require.NotEmpty(t, en)
	for k := range en {
		assert.True(t, strings.HasPrefix(k, Prefix), "%s is outside %q", k, Prefix)
		if base, one := strings.CutSuffix(k, "_one"); one {
			_, ok := en[base]
			assert.True(t, ok, "%s is a singular with no plain form", k)
		}
	}
	for k, v := range tr {
		_, ok := en[k]
		assert.True(t, ok, "tr has %s, which English does not — a key nobody asks for", k)
		assert.True(t, fits(v, k, "tr"), "tr %s = %q does not carry the English placeholders %v", k, v, Placeholders(en[k]))
	}
	for k := range en {
		if strings.HasSuffix(k, "_one") {
			continue // Turkish writes no singular form
		}
		_, ok := tr[k]
		assert.True(t, ok, "tr is missing %s", k)
	}
}

// The grammar is `{name}` and nothing else: a printf verb or a template
// action in an English value would be a placeholder only Go understood, and a
// translator copying the style would ship something that prints as written.
func TestBuiltins_SpeakOnlyTheCatalogueGrammar(t *testing.T) {
	bad := regexp.MustCompile(`%[sdvqf]|\{\{|\}\}|\{'`)
	for _, lang := range BuiltinLanguages() {
		for k, v := range Builtin(lang) {
			assert.False(t, bad.MatchString(v), "%s %s = %q uses syntax the catalogue does not have", lang, k, v)
		}
	}
}

// ⚠ A key used in code that the catalogue lacks renders as the key itself.
// This scans the Go tree for literal "server.…" keys and fails on any the
// English table does not carry (keys assembled at run time — the permission
// ids, the share kinds, the public table — are covered by their own tests).
func TestEveryLiteralKeyInTheTreeExists(t *testing.T) {
	en := Builtin("en")
	lit := regexp.MustCompile(`"(server\.[a-z_]+(?:\.[A-Za-z0-9_]*[A-Za-z0-9])+)"`)
	root := filepath.Join("..", "..")
	var missing []string
	seen := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "embed" || d.Name() == "testdata" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range lit.FindAllStringSubmatch(string(b), -1) {
			seen++
			if _, ok := en[m[1]]; !ok {
				missing = append(missing, p+": "+m[1])
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, seen, 20, "the scan must find the keys the mails use (a scan of nothing passes everything)")
	assert.Empty(t, missing)
}

// ── which language ─────────────────────────────────────────────────────

func TestResolve_BuiltinsPacksRegions(t *testing.T) {
	assert.Equal(t, "tr", Resolve("tr-TR"))
	assert.Equal(t, "en", Resolve(" EN_gb "))
	assert.Equal(t, "", Resolve("es"), "no pack: Spanish is not spoken")
	assert.Equal(t, "", Resolve(""))
	assert.Equal(t, "", Resolve("*"))

	withPacks(t, StaticPacks{"es": {"server.mail.greeting": "Hola:"}, "pt-br": {"server.mail.greeting": "Olá,"}})
	assert.Equal(t, "es", Resolve("es"))
	assert.Equal(t, "es", Resolve("es-MX"), "a region falls back to its language")
	assert.Equal(t, "pt-br", Resolve("pt"), "a language finds its one regional pack")
	assert.Equal(t, "", Resolve("de"))
}

func TestPick_FlowOrderThenDefaultThenEnglish(t *testing.T) {
	assert.Equal(t, "en", Pick())
	assert.Equal(t, "en", Pick("de", "xx"), "nothing spoken: English, not the other built-in")
	assert.Equal(t, "tr", Pick("", "de", "tr", "en"))
	withDefault(t, "tr")
	assert.Equal(t, "tr", Pick("de"), "the instance default comes before English")
	withPacks(t, StaticPacks{"es": {"server.mail.greeting": "Hola:"}})
	assert.Equal(t, "es", Pick("es", "tr"))
}

func TestFromAcceptLanguage(t *testing.T) {
	withPacks(t, StaticPacks{"es": {"server.mail.greeting": "Hola:"}})
	assert.Equal(t, "es", FromAcceptLanguage("es-ES,es;q=0.9,en;q=0.8"))
	assert.Equal(t, "tr", FromAcceptLanguage("de-DE, tr;q=0.5"))
	assert.Equal(t, "", FromAcceptLanguage("de-DE, fr"))
	assert.Equal(t, "", FromAcceptLanguage(""))
}

// ── the grammar ────────────────────────────────────────────────────────

func TestFill_NamedAndVisibleWhenMissing(t *testing.T) {
	assert.Equal(t, "Ana dropped 2 files into x", Fill("{who} dropped {count} files into {folder}", Vars{"who": "Ana", "count": "2", "folder": "x"}))
	assert.Equal(t, "a {nope} b", Fill("a {nope} b", Vars{"x": "y"}), "an unknown placeholder stays visible")
	assert.Equal(t, "{ not a placeholder } {a-b}", Fill("{ not a placeholder } {a-b}", Vars{"a": "1"}))
	assert.Equal(t, []string{"count", "name"}, Placeholders("{name} {count} {name}"))
}

// ── the catalogue answers ──────────────────────────────────────────────

func TestText_PackThenEnglishPerKey(t *testing.T) {
	withPacks(t, StaticPacks{"es": {
		"server.mail.label.file": "Archivo: {name}",
		"server.mail.greeting":   "   ", // untranslated: blank is not a translation
	}})
	assert.Equal(t, "Archivo: a.pdf", Text("es", "server.mail.label.file", Vars{"name": "a.pdf"}))
	assert.Equal(t, "Hello,", Text("es", "server.mail.greeting", nil), "a key the pack lacks is English")
	assert.Equal(t, "Merhaba,", Text("tr", "server.mail.greeting", nil))
	assert.Equal(t, "Hello,", Text("de", "server.mail.greeting", nil), "never the other built-in")
	assert.Equal(t, "Archivo: a.pdf", Text("es-MX", "server.mail.label.file", Vars{"name": "a.pdf"}), "a region reads its language")
	assert.Equal(t, "server.nope", Text("es", "server.nope", nil), "a key nobody has shows as itself")
}

// ⚠⚠ The run-time half of the validator: a translation that lost a
// placeholder — the download link, the PIN — is refused and the English is
// sent, so the recipient still gets the link.
func TestText_ATranslationThatDropsOrAddsAPlaceholderIsRefused(t *testing.T) {
	withPacks(t, StaticPacks{"es": {
		"server.mail.label.pin":    "PIN: (pregunte al remitente)",   // lost {pin}
		"server.mail.label.file":   "Archivo: {name} {size}",         // invented {size}
		"server.mail.label.folder": "Carpeta: {nombre}",              // renamed
		"server.mail.label.size":   "Tamaño: {size}",                 // fine
		"server.perm.http":         "Hace peticiones HTTP a {host}.", // fine
	}})
	assert.Equal(t, "PIN: 4321", Text("es", "server.mail.label.pin", Vars{"pin": "4321"}))
	assert.Equal(t, "File: a", Text("es", "server.mail.label.file", Vars{"name": "a", "size": "1 B"}))
	assert.Equal(t, "Folder: d", Text("es", "server.mail.label.folder", Vars{"name": "d"}))
	assert.Equal(t, "Tamaño: 1 B", Text("es", "server.mail.label.size", Vars{"size": "1 B"}))
}

func TestPlural_OneThenTheLanguagesOwnPlainForm(t *testing.T) {
	assert.Equal(t, "This link is valid for 1 day.", Plural("en", "server.mail.valid_days", 1, nil))
	assert.Equal(t, "This link is valid for 7 days.", Plural("en", "server.mail.valid_days", 7, nil))
	assert.Equal(t, "Bu bağlantı 1 gün geçerlidir.", Plural("tr", "server.mail.valid_days", 1, nil),
		"Turkish writes no singular: its plain form, not the English singular")

	withPacks(t, StaticPacks{
		"es": {"server.mail.valid_days": "Válido {count} días.", "server.mail.valid_days_one": "Válido un día."},
		"fr": {"server.mail.valid_days": "Valable {count} jours."},
		"de": {"server.mail.valid_days_one": "Gültig {count} Tag."},
	})
	assert.Equal(t, "Válido un día.", Plural("es", "server.mail.valid_days", 1, nil))
	assert.Equal(t, "Válido 3 días.", Plural("es", "server.mail.valid_days", 3, nil))
	assert.Equal(t, "Valable 1 jours.", Plural("fr", "server.mail.valid_days", 1, nil),
		"a pack with only the plain form keeps its language for one")
	assert.Equal(t, "Gültig 1 Tag.", Plural("de", "server.mail.valid_days", 1, nil), "`{count}` is allowed in a singular")
	assert.Equal(t, "This link is valid for 2 days.", Plural("de", "server.mail.valid_days", 2, nil))
}

func TestTable_CompleteWithResolvedSingulars(t *testing.T) {
	withPacks(t, StaticPacks{"es": {"server.public.drop_title": "Enviar archivos", "server.public.file_count": "{count} archivos"}})
	tbl := Table("es", Prefix+"public.")
	var want []string
	for k := range Builtin("en") {
		if strings.HasPrefix(k, Prefix+"public.") {
			want = append(want, strings.TrimPrefix(k, Prefix+"public."))
		}
	}
	var got []string
	for k := range tbl {
		got = append(got, k)
	}
	sort.Strings(want)
	sort.Strings(got)
	assert.Equal(t, want, got, "every page key, so a template never renders an empty hole")
	assert.Equal(t, "Enviar archivos", tbl["drop_title"])
	assert.Equal(t, "Send", tbl["drop_send"])
	assert.Equal(t, "{count} archivos", tbl["file_count_one"], "the language's plain form stands in for its missing singular")
	// ⚠ Unfilled: Table hands the page the template, and English's singular
	// carries `{count}` like every other form (v0.43.0 — a form that types a
	// literal 1 reads "1 dossier" in French for zero).
	assert.Equal(t, "{count} folder", tbl["folder_count_one"])
}

func TestIsRTL(t *testing.T) {
	assert.True(t, IsRTL("ar"))
	assert.True(t, IsRTL("fa-IR"))
	assert.False(t, IsRTL("es"))
}

// ── CLDR plural categories ─────────────────────────────────────────────

// ⚠⚠ The break test the Arabic translator asked for: six forms, each number
// in its own category. With the "`_one` when 1" rule every one of these but
// 1 printed the plain form.
func TestPlural_ArabicSixForms(t *testing.T) {
	withPacks(t, StaticPacks{"ar": {
		"server.public.file_count_zero": "لا توجد ملفات",
		"server.public.file_count_one":  "ملف واحد",
		"server.public.file_count_two":  "ملفان",
		"server.public.file_count_few":  "{count} ملفات",
		"server.public.file_count_many": "{count} ملفًا",
		"server.public.file_count":      "{count} ملف",
	}})
	assert.Equal(t, []string{"zero", "one", "two", "few", "many", "other"}, Categories("ar"))
	for n, want := range map[int]string{
		0: "لا توجد ملفات", 1: "ملف واحد", 2: "ملفان", 3: "3 ملفات", 11: "11 ملفًا", 100: "100 ملف",
	} {
		assert.Equal(t, want, Plural("ar", "server.public.file_count", n, nil), "n=%d (%s)", n, Category("ar", n))
	}
	// The page script's table carries every form, resolved.
	tbl := Table("ar", Prefix+"public.")
	assert.Equal(t, "ملفان", tbl["file_count_two"])
	assert.Equal(t, "{count} ملف", tbl["file_count"])
	assert.Equal(t, "Send files", tbl["drop_title"])
}

func TestPlural_RussianAndTheImpliedNumber(t *testing.T) {
	withPacks(t, StaticPacks{"ru": {
		// ⚠ Russian `one` is 1, 21, 31, 101… — a form that leaves the number
		// out would say "один файл" for 21 files. Refused; the plain form reads.
		"server.public.file_count_one":  "один файл",
		"server.public.file_count_few":  "{count} файла",
		"server.public.file_count_many": "{count} файлов",
		"server.public.file_count":      "{count} файла",
	}})
	assert.Equal(t, []string{"one", "few", "many", "other"}, Categories("ru"))
	assert.Equal(t, "21 файла", Plural("ru", "server.public.file_count", 21, nil))
	assert.Equal(t, "3 файла", Plural("ru", "server.public.file_count", 3, nil))
	assert.Equal(t, "5 файлов", Plural("ru", "server.public.file_count", 5, nil))
	assert.False(t, impliesNumber("ru", "one"))
	assert.True(t, impliesNumber("ar", "two"))
	assert.True(t, impliesNumber("en", "one"))
	assert.False(t, impliesNumber("fr", "one"), "French one is 0 and 1")
}

// ⚠⚠ A sentence ENGLISH says one way for every number is still a plural in
// a language that inflects it. `server.app.wake.scheduled` ("{count}
// scheduled") reads the same for 1 and for 3 in English and so has no `_one`;
// the rule used to be "English wrote a `_one`", which made every pack's form
// for these three wake-up sentences dead weight — shipped, validated by
// scripts/i18n-validate.mjs, and never read, while the SAME pack's forms
// worked wherever the browser drew the string (packages/core countedKey has
// no such gate). The two catalogues now answer the same question.
func TestPlural_PackInflectsWhereEnglishDoesNot(t *testing.T) {
	require.NotContains(t, Builtin("en"), "server.app.wake.scheduled_one",
		"the premise: English writes no form for this one")
	withPacks(t, StaticPacks{
		"ar": {
			"server.app.wake.scheduled":      "{count} مجدولة",
			"server.app.wake.scheduled_zero": "لا شيء مجدول",
			"server.app.wake.scheduled_one":  "واحدة مجدولة",
			"server.app.wake.scheduled_two":  "اثنتان مجدولتان",
			"server.app.wake.scheduled_few":  "{count} مجدولات",
		},
		"ru": {
			"server.app.wake.refused":      "{count} отклонено ({reason})",
			"server.app.wake.refused_few":  "{count} отклонены ({reason})",
			"server.app.wake.refused_many": "{count} отклонённых ({reason})",
		},
	})
	for n, want := range map[int]string{
		0: "لا شيء مجدول", 1: "واحدة مجدولة", 2: "اثنتان مجدولتان", 3: "3 مجدولات", 100: "100 مجدولة",
	} {
		assert.Equal(t, want, Plural("ar", "server.app.wake.scheduled", n, nil), "n=%d (%s)", n, Category("ar", n))
	}
	v := srvVars("reason", "busy")
	assert.Equal(t, "3 отклонены (busy)", Plural("ru", "server.app.wake.refused", 3, v))
	assert.Equal(t, "5 отклонённых (busy)", Plural("ru", "server.app.wake.refused", 5, v))
	assert.Equal(t, "21 отклонено (busy)", Plural("ru", "server.app.wake.refused", 21, v),
		"Russian `one` is also 21 — its own plain form, not a form it did not write")
	// English is untouched by any of it.
	assert.Equal(t, "3 scheduled", Plural("en", "server.app.wake.scheduled", 3, nil))
	assert.Equal(t, "1 scheduled", Plural("en", "server.app.wake.scheduled", 1, nil))
}

func srvVars(k, v string) Vars { return Vars{k: v} }

func TestPlural_TurkishAndEnglishUnchanged(t *testing.T) {
	assert.Equal(t, "1 file", Plural("en", "server.public.file_count", 1, nil))
	assert.Equal(t, "0 files", Plural("en", "server.public.file_count", 0, nil))
	assert.Equal(t, "1 dosya", Plural("tr", "server.public.file_count", 1, nil),
		"Turkish has a `one` category but writes no form for it: its own plain form, not English's")
}

// ⚠⚠ No sentence the server writes may be chosen by an inline language pair
// any more — `if lang == "tr" { … } else { … }`, a helper taking `en, tr
// string`, a `HasPrefix(lang, "tr")`. Each of those sends every THIRD
// language (a language pack's Spanish, German, Arabic…) the `else` branch.
// The last four (account_rules.go, ai_tokens_admin.go, auth_providers.go and
// wasmplugin/schedule.go, plus authsetup.FromWords) moved into `server.*` in
// v0.43.0; this scan fails the day a new one is written.
//
// Comments are stripped before the scan (this package's own doc quotes the
// pattern), and _test.go files and testdata are not product code.
func TestNoInlineLanguagePairsInTheTree(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`[!=]=\s*"tr"`),
		regexp.MustCompile(`"tr"\s*[!=]=`),
		regexp.MustCompile(`HasPrefix\([^)]*,\s*"tr"\)`),
		regexp.MustCompile(`\ben,\s*tr\s+string\b`),
		regexp.MustCompile(`\btr\s*:?=\s*langOf\(`),
	}
	comment := regexp.MustCompile(`(?m)//.*$`)
	root := filepath.Join("..", "..")
	var hits []string
	files := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "embed" || d.Name() == "testdata" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files++
		src := comment.ReplaceAllString(string(b), "")
		for i, line := range strings.Split(src, "\n") {
			for _, re := range patterns {
				if re.MatchString(line) {
					hits = append(hits, p+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, files, 200, "the scan must walk the server's source (a scan of nothing passes everything)")
	assert.Empty(t, hits, "an inline en/tr pair: put the sentence in locales/en.json + tr.json under server.* and say it with srvtext.Text / srvtext.Plural")
}

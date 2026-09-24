package srvtext

import (
	"strings"
	"sync"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// ── Plurals: CLDR cardinal categories ──────────────────────────────────
//
// ⚠⚠ Why not "`_one` when the count is 1". That rule is English's. Arabic
// has six forms (zero, one, two, few, many, other), Russian, Polish, Czech and
// Ukrainian need few/many, French says "0 fichier" — and a translator given
// only one|other writes label-and-number constructions ("عدد الأيام: 7")
// instead of sentences (the Arabic pack's glossary, "Platform notes" 1). The
// categories are the Unicode CLDR's, the same ones the browser's
// Intl.PluralRules answers — so the explorer, the admin panel and the server
// pick the same form for the same number in the same language.
//
// THE GRAMMAR, for a sentence about `{count}` things:
//
//   - `<key>_zero`, `<key>_one`, `<key>_two`, `<key>_few`, `<key>_many` — the
//     form for that category. Only the categories the language HAS are ever
//     read (Categories); a form for another is dead weight.
//   - `<key>` — the `other` form, and the fallback. There is no `_other`.
//   - a language's missing category form falls back to its OWN plain form —
//     never to the English singular glued into a Spanish sentence — and only a
//     language with neither falls back to English.
//   - a category that holds exactly one number in the language (`one` in
//     English, `zero`/`one`/`two` in Arabic) may leave `{count}` out: the
//     word IS the number ("يوم واحد", "1 day"). Anywhere else it must keep
//     it — Russian `one` is also 21, 31, 101.
//
// ⚠ Go's CLDR tables (golang.org/x/text, CLDR 32) are older than the
// browser's; the rules for integers have not changed for the languages filex
// is translated into, and a category the two disagree on falls back to the
// plain form rather than to a wrong one.

// cldrOrder is the order categories are listed (and admin-panel forms are
// written) in.
var cldrOrder = []string{"zero", "one", "two", "few", "many", "other"}

var formName = map[plural.Form]string{
	plural.Other: "other", plural.Zero: "zero", plural.One: "one",
	plural.Two: "two", plural.Few: "few", plural.Many: "many",
}

// Category is the CLDR cardinal category of the integer n in lang
// ("one", "few", "other"…). An unknown tag reads by its base language, then
// as English.
func Category(lang string, n int) string {
	if n < 0 {
		n = -n
	}
	f := plural.Cardinal.MatchPlural(tagOf(lang), n%10000000, 0, 0, 0, 0)
	if s, ok := formName[f]; ok {
		return s
	}
	return "other"
}

var tags sync.Map // lang → language.Tag

func tagOf(lang string) language.Tag {
	l := norm(lang)
	if v, ok := tags.Load(l); ok {
		return v.(language.Tag)
	}
	t, err := language.Parse(l)
	if err != nil {
		t = language.English
	}
	tags.Store(l, t)
	return t
}

type catInfo struct {
	cats []string       // the language's categories, CLDR order
	rep  map[string]int // one number that falls in each
	uniq map[string]bool
}

var catCache sync.Map // lang → *catInfo

// info samples 0…999: every integer rule CLDR has for the languages it
// covers repeats within that range, which is what lets a category be called
// "exactly one number" with confidence.
func info(lang string) *catInfo {
	l := norm(lang)
	if v, ok := catCache.Load(l); ok {
		return v.(*catInfo)
	}
	count := map[string]int{}
	rep := map[string]int{}
	for n := 0; n < 1000; n++ {
		c := Category(l, n)
		if count[c] == 0 {
			rep[c] = n
		}
		count[c]++
	}
	ci := &catInfo{rep: rep, uniq: map[string]bool{}}
	for _, c := range cldrOrder {
		// ⚠ `other` is ALWAYS a category, as Intl.PluralRules lists it: in
		// Russian or Polish no integer falls in it (only fractions do), but it
		// is the plain form — the fallback every language has — and the
		// admin panel counts it among the forms a string must carry.
		if count[c] > 0 || c == "other" {
			ci.cats = append(ci.cats, c)
			ci.uniq[c] = count[c] == 1
		}
	}
	catCache.Store(l, ci)
	return ci
}

// Categories lists the plural categories lang has, in CLDR order
// (English: one, other; Arabic: zero, one, two, few, many, other).
func Categories(lang string) []string {
	return append([]string(nil), info(lang).cats...)
}

// impliesNumber reports whether category c holds exactly one integer in lang
// — the form may then leave `{count}` out.
func impliesNumber(lang, c string) bool { return info(lang).uniq[c] }

// splitForm splits `<base>_<category>` for the five category suffixes.
func splitForm(key string) (base, cat string, ok bool) {
	i := strings.LastIndexByte(key, '_')
	if i <= 0 {
		return "", "", false
	}
	switch c := key[i+1:]; c {
	case "zero", "one", "two", "few", "many":
		return key[:i], c, true
	}
	return "", "", false
}

// counted reports whether key may take category forms: English wrote the
// plain sentence, so `<key>_<category>` is a form OF it and a caller may ask
// for it with a number.
//
// ⚠⚠ THE BROWSER'S RULE, and now this one. packages/core
// (composables/useLocale → countedKey/pickMessage) asks only two things: did
// the caller pass a number, and did the LANGUAGE write the form for that
// number's category. This package used to ask a third — `IsPluralBase`, "does
// the ENGLISH have a `_one`" — and English writes `_one` only where English
// itself inflects. Three sentences the wake-up report counts with
// (`server.app.wake.scheduled`, `.refused`, `.beyond`: "{count} scheduled")
// read the same in English for 1 and for 3, so they had no `_one`, so a
// pack's Arabic dual and Russian few were dead weight: shipped, validated by
// scripts/i18n-validate.mjs, never read — while the SAME pack's forms worked
// everywhere the browser drew them. Two catalogues that disagree about what a
// plural IS are two catalogues a translator cannot trust. The old predicate
// is deleted rather than kept beside this one: two answers to one question is
// how they drifted apart.
func counted(key string) bool {
	_, ok := builtin["en"][key]
	return ok
}

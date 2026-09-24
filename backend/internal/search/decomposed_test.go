package search

// A name written decomposed must answer the word the user typed.
//
// Measured on a production catalogue on 2026-09-24: of 169 471 names, 71 388
// were stored in Unicode normalisation form D — `Gu` + U+0308 COMBINING
// DIAERESIS + `rol` where a keyboard types the single character `ü` — because
// the macOS clients that uploaded them hand filenames over decomposed, and
// filex stores the name it is given. A search box delivers the composed form
// (NFC). Nothing on either path normalised either side, so every query word
// with a Turkish letter in it (ü ö ç ş ğ İ) found nothing but the ~10% of
// names that happened to be composed, and a user who typed a file's name
// exactly as it is shown got an empty list.
//
// The names below are made up; the shape (a common prefix every file in the
// storage carries, a person's name, a dotted date, Turkish words) is the one
// the report was about.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	gurelName = "Plan - Ayşe Gürel - 2026.08.28 - Yeni 2 Günlük Ara Öğün.pdf"
	ipekName  = "Plan - İpek Ada Yılmaz - 2026.09.21 - 2 Günlük Ara Öğün.pdf"
	folder    = "/Müşteri Veritabanı/2026"
)

func nfd(s string) string { return norm.NFD.String(s) }

// stem is a filename without its extension: what people type.
func stem(name string) string { return strings.TrimSuffix(name, ".pdf") }

func TestNormalize_DecomposedNameIsTheComposedName(t *testing.T) {
	for _, name := range []string{gurelName, ipekName, folder} {
		if got, want := Normalize(nfd(name)), Normalize(name); got != want {
			t.Errorf("Normalize(NFD %q) = %q, want %q", name, got, want)
		}
	}
	// The mark is not a separator. Before, `Gu` + U+0308 + `rel` became
	// the two words `gu rel`, and no query word could ever match either.
	if got := Normalize(nfd("Gürel")); got != "gürel" {
		t.Errorf("Normalize(NFD Gürel) = %q, want %q", got, "gürel")
	}
	// Capital dotted I decomposes to I + U+0307 and lower-cases to `i`.
	if got := Normalize(nfd("İpek")); got != "ipek" {
		t.Errorf("Normalize(NFD İpek) = %q, want %q", got, "ipek")
	}
}

// TestNormalize_MarkWithoutPrecomposedFormStaysInItsWord: after composition
// some marks are still marks — every Devanagari vowel sign is one. A mark
// belongs to the letter before it, so it must not split the word there.
func TestNormalize_MarkWithoutPrecomposedFormStaysInItsWord(t *testing.T) {
	if got, want := Normalize("हिन्दी-नोट.txt"), "हिन्दी नोट txt"; got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestScoreName_DecomposedNameAnswersComposedQuery(t *testing.T) {
	name := nfd(gurelName)
	path := nfd(folder + "/" + gurelName)
	for _, q := range []string{"Gürel", "GÜREL", "Ayşe Gürel", "günlük öğün", stem(gurelName), gurelName} {
		if got := PrepareQuery(q).ScoreName(name, path); !got.OK {
			t.Errorf("query %q must match the decomposed name, got %+v", q, got)
		}
	}
	if got := PrepareQuery(stem(gurelName)).ScoreName(name, path); got.Tier != TierExact {
		t.Errorf("the full name typed as shown is an exact match, got %s", got.Tier)
	}
	// Composed/decomposed is not a difference in the folder either.
	if got := PrepareQuery("Müşteri Gürel").ScoreName(name, path); !got.OK || got.Tier != TierPath {
		t.Errorf("a folder word must still answer through the path, got %+v", got)
	}

	ipek := nfd(ipekName)
	for _, q := range []string{"İpek", "ipek", "İPEK", "İpek Ada Yılmaz", stem(ipekName)} {
		if got := PrepareQuery(q).ScoreName(ipek, "/"+ipek); !got.OK {
			t.Errorf("query %q must match the decomposed name, got %+v", q, got)
		}
	}
}

// TestScoreName_ComposedNameAnswersDecomposedQuery is the same bug the other
// way round: a name pasted from a Finder window arrives decomposed.
func TestScoreName_ComposedNameAnswersDecomposedQuery(t *testing.T) {
	for _, q := range []string{nfd("Ayşe Gürel"), nfd(stem(gurelName))} {
		if got := PrepareQuery(q).ScoreName(gurelName, "/"+gurelName); !got.OK {
			t.Errorf("decomposed query %q must match the composed name, got %+v", q, got)
		}
	}
}

func TestSearch_DecomposedNamesAreFoundByTypedWords(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	for i, name := range []string{nfd(gurelName), nfd(ipekName), "Plan - Ahmet Kaya - 2026.08.28 - Yeni.pdf"} {
		n := fileNode(int64(i+1), name, nfd(folder)+"/"+name, "e")
		if err := idx.IndexNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]int64{
		"Gürel":                   1,
		"Ayşe Gürel":              1,
		stem(gurelName):           1,
		"İpek Ada Yılmaz":         2,
		"ipek":                    2,
		stem(ipekName):            2,
		nfd(stem(ipekName)):       2,
		"Müşteri Veritabanı Ayşe": 1,
	}
	for q, want := range cases {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(hits) == 0 || hits[0].NodeID != want {
			t.Errorf("query %q: want node %d first, got %+v", q, want, hits)
		}
	}
}

// TestIndexSchemaVersion_DecomposedNamesNeedRebuild: a v2 index holds every
// decomposed name's words in pieces, so it has to be rebuilt to find them.
func TestIndexSchemaVersion_DecomposedNamesNeedRebuild(t *testing.T) {
	dir := t.TempDir() + "/idx.bleve"
	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.bleve.SetInternal([]byte(indexVersionKey), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if !reopened.NeedsRebuild() {
		t.Error("an index written with schema 2 split decomposed names; it must ask for a rebuild")
	}
}

// TestNormalize_FullyLowerCasedDottedCapitalI: lower-casing `İ` the full
// Unicode way — JavaScript's toLowerCase(), Python's lower() — gives `i`
// followed by U+0307 COMBINING DOT ABOVE, and composition leaves that pair
// alone. Nobody types it, but a client or an agent that lower-cases a query
// before sending it does, and so does software that lower-cases a filename.
// The dot adds nothing to a letter that already has one.
func TestNormalize_FullyLowerCasedDottedCapitalI(t *testing.T) {
	if got := Normalize("i\u0307pek"); got != "ipek" {
		t.Errorf("Normalize(i+U+0307 pek) = %q, want %q", got, "ipek")
	}
	ipek := nfd(ipekName)
	if got := PrepareQuery("i\u0307pek ada yılmaz").ScoreName(ipek, "/"+ipek); !got.OK {
		t.Errorf("a fully lower-cased query must match the name, got %+v", got)
	}
	if got := PrepareQuery("ipek").ScoreName("i\u0307pek.pdf", "/i\u0307pek.pdf"); !got.OK {
		t.Errorf("a name lower-cased that way must answer `ipek`, got %+v", got)
	}
	if !matchesAll(PlanFallback("ipek").Terms, "i\u0307pek.pdf") {
		t.Error("the fallback must reach a name lower-cased that way")
	}
	if !matchesAll(PlanFallback("i\u0307pek").Terms, ipek) {
		t.Error("a fully lower-cased query must reach the decomposed name")
	}
}

// likeMatches is how SQLite's LIKE answers a `%word%` pattern: case folded
// for ASCII letters only, every other character compared as it is.
func likeMatches(pattern, s string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(pattern, "%"), "%")
	return strings.Contains(asciiFold(s), asciiFold(inner))
}

func asciiFold(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// storedForms is every way the catalogue can hold one name: composed or
// decomposed, as typed, upper-cased, or lower-cased.
func storedForms(name string) []string {
	var out []string
	for _, c := range []string{
		name,
		strings.ToUpperSpecial(unicode.TurkishCase, name),
		strings.ToLowerSpecial(unicode.TurkishCase, name),
	} {
		out = append(out, norm.NFC.String(c), norm.NFD.String(c))
	}
	return out
}

// TestPlanFallback_EveryWordReachesTheDatabase is the other half of the
// report. The fallback used to send only the LONGEST word to the database and
// check the rest in Go, over at most 1000 rows sorted by name. When that word
// is one every file shares (in the report it was in 64 483 of 169 471 names),
// the file being looked for sits far past row 1000 and is never seen — so the
// more of a name you typed, the less you found, which looked like "spaces
// break search". Every word is a condition in the query now.
func TestPlanFallback_EveryWordReachesTheDatabase(t *testing.T) {
	plan := PlanFallback(stem(gurelName))
	if len(plan.Words) < 10 {
		t.Fatalf("want every word of the name, got %q", plan.Words)
	}
	for _, w := range plan.Words {
		if !hasTerm(plan.Terms, SQLLike(w)) {
			t.Errorf("word %q is not a term the database checks: %q", w, plan.Terms)
		}
	}
	for _, name := range []string{gurelName, ipekName} {
		for _, q := range []string{stem(name), nfd(stem(name)), strings.ToLower(stem(name))} {
			plan := PlanFallback(q)
			for _, stored := range storedForms(name) {
				if !matchesAll(plan.Terms, stored) {
					t.Errorf("query %q: the stored name %q misses a term the database checks", q, stored)
				}
			}
		}
	}
	// The spellings are about how ONE word is stored; they must not make a
	// different word match.
	if matchesAll(PlanFallback("Gürel").Terms, "Plan - Ahmet Kaya.pdf") {
		t.Error("a name without the word must not match")
	}
}

func TestPlanFallback_TurkishCapitalI(t *testing.T) {
	for _, q := range []string{"ipek", "İpek", "İPEK", "IPEK"} {
		terms := PlanFallback(q).Terms
		if len(terms) == 0 {
			t.Fatalf("query %q: no terms", q)
		}
		for _, stored := range []string{"İpek", "İPEK", nfd("İpek"), "ipek", "Ipek"} {
			if !matchesAll(terms, stored) {
				t.Errorf("query %q must reach the stored name %q", q, stored)
			}
		}
	}
	// Dotless ı is its own letter: typing `yılmaz` reaches `YILMAZ` (its
	// upper case is a plain I) without the database needing to know Turkish.
	if !matchesAll(PlanFallback("yılmaz").Terms, "YILMAZ.pdf") {
		t.Error("`yılmaz` must reach an upper-cased `YILMAZ`")
	}
}

// TestPlanFallback_CheapTermsFirst: the order is only the work, but the work
// is what made the first version of this fix four times slower than the bug.
// A term costs one comparison per pattern for every row that reaches it, so
// the one-pattern terms — plain words, and the run every spelling of a
// Turkish word shares — go before the ones with a pattern per spelling.
func TestPlanFallback_CheapTermsFirst(t *testing.T) {
	plan := PlanFallback("Archive Gürel 2026")
	seenMulti := false
	for _, term := range plan.Terms {
		if len(term) > 1 {
			seenMulti = true
			continue
		}
		if seenMulti {
			t.Fatalf("a one-pattern term after a multi-pattern one: %q", plan.Terms)
		}
	}
	// `arch` is in every spelling of `archive`, `rel` in every one of
	// `gürel`; `2026` has a single spelling to begin with.
	for _, want := range []string{"%arch%", "%rel%", "%2026%"} {
		if !hasTerm(plan.Terms, want) {
			t.Errorf("want the one-pattern term %s, got %q", want, plan.Terms)
		}
	}
}

// TestPlanFallback_PastedParagraphStaysAQuery: a query is not a document.
// Every word used to be one placeholder at most; now it is up to seven, and
// SQLite refuses a statement with more than 32 766 of them — a pasted page
// would have turned the search into a 500. Past maxFallbackTerms the rest of
// the words are left to the scorer, which checks every piece anyway.
func TestPlanFallback_PastedParagraphStaysAQuery(t *testing.T) {
	words := make([]string, 0, 400)
	for i := 0; i < 399; i++ {
		words = append(words, fmt.Sprintf("kelime%03d", i))
	}
	// Letters no other word has, so no other word can answer it as a
	// subsequence — and short enough to sort past the cap.
	words = append(words, "zzqq")
	q := strings.Join(words, " ")
	plan := PlanFallback(q)
	if len(plan.Terms) > maxFallbackTerms {
		t.Fatalf("%d terms, want at most %d", len(plan.Terms), maxFallbackTerms)
	}
	if hasTerm(plan.Terms, "%zzqq%") {
		t.Fatal("the fixture needs `zzqq` past the cap")
	}
	if !plan.Accepts(q+".txt", "/"+q+".txt") {
		t.Error("a name holding every word must still be accepted")
	}
	name := strings.Join(words[:399], " ") + ".txt"
	if plan.Accepts(name, "/"+name) {
		t.Error("a word the database was not asked about must still be required")
	}
}

func TestSharedRun(t *testing.T) {
	cases := map[string]string{
		"archive": "arch", // `i` may be written `İ`
		"gürel":   "rel",
		"öğün":    "n",
		"ipek":    "pek",
		"2026":    "2026",
		"ığü":     "",
	}
	for word, want := range cases {
		if got := sharedRun(word); got != want {
			t.Errorf("sharedRun(%q) = %q, want %q", word, got, want)
		}
	}
}

func hasTerm(terms [][]string, pattern string) bool {
	for _, term := range terms {
		for _, p := range term {
			if p == pattern {
				return true
			}
		}
	}
	return false
}

func matchesAll(terms [][]string, s string) bool {
	for _, spellings := range terms {
		ok := false
		for _, p := range spellings {
			if likeMatches(p, s) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

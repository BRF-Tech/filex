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
	"slices"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
)

const (
	gurelName = "Plan - Ayşe Gürel - 2026.08.28 - Yeni 2 Günlük Ara Öğün.pdf"
	ipekName  = "Plan - İpek Ada Yılmaz - 2026.09.21 - 2 Günlük Ara Öğün.pdf"
	folder    = "/Müşteri Veritabanı/2026"
)

func nfd(s string) string { return norm.NFD.String(s) }

// combiningDot is U+0307, spelt without an escape so no tool can turn it into
// an invisible character in this file.
var combiningDot = string(rune(0x0307))

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
	if !dbAnswers(PlanFallback("ipek").match, "i\u0307pek.pdf") {
		t.Error("the fallback must reach a name lower-cased that way")
	}
	if !dbAnswers(PlanFallback("i\u0307pek").match, ipek) {
		t.Error("a fully lower-cased query must reach the decomposed name")
	}
}

// dbAnswers is what the database answers for a plan, in Go: every run is in
// the stored bytes as they are (the engine's own LIKE, which folds ASCII case
// and nothing else), and every word is in the name folded by
// internal/namefold (fx_match on SQLite; PostgreSQL and MySQL are held to the
// same answer by TestSearchNodesOnEveryEngine).
func dbAnswers(m model.NameMatch, stored string) bool {
	for _, r := range m.Runs {
		if !strings.Contains(asciiFold(stored), asciiFold(r)) {
			return false
		}
	}
	f := namefold.String(stored)
	for _, w := range m.Words {
		if !strings.Contains(f, w) {
			return false
		}
	}
	return len(m.Words) > 0
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
// decomposed, as typed, upper-cased or lower-cased — the Turkish way and the
// default way, which disagree about exactly the i's.
func storedForms(name string) []string {
	var out []string
	for _, c := range []string{
		name,
		strings.ToUpperSpecial(unicode.TurkishCase, name),
		strings.ToLowerSpecial(unicode.TurkishCase, name),
		strings.ToUpper(name),
		strings.ToLower(name),
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
// break search". Every word is a condition in the query now, and whatever
// form the name is stored in, the database finds it.
func TestPlanFallback_EveryWordReachesTheDatabase(t *testing.T) {
	plan := PlanFallback(stem(gurelName))
	if len(plan.Words) < 10 {
		t.Fatalf("want every word of the name, got %q", plan.Words)
	}
	for _, w := range plan.Words {
		if !slices.Contains(plan.match.Words, w) {
			t.Errorf("word %q is not a condition the database checks: %q", w, plan.match.Words)
		}
	}
	for _, name := range []string{gurelName, ipekName} {
		for _, q := range []string{stem(name), nfd(stem(name)), strings.ToLower(stem(name)), strings.ToUpper(stem(name))} {
			plan := PlanFallback(q)
			for _, stored := range storedForms(name) {
				if !dbAnswers(plan.match, stored) {
					t.Errorf("query %q: the database misses the stored name %q (%+v)", q, stored, plan.match)
				}
			}
		}
	}
	// The forms are about how ONE word is stored; they must not make a
	// different word match.
	if dbAnswers(PlanFallback("Gürel").match, "Plan - Ahmet Kaya.pdf") {
		t.Error("a name without the word must not match")
	}
}

// TestPlanFallback_TurkishCapitals: the four i's are one letter on the way to
// the database too. PR #46 as proposed sent `çalişma` for a typed `ÇALIŞMA`
// in lower, upper and title case — `ÇALİŞMA`, never `ÇALIŞMA` — so on SQLite,
// whose LIKE folds ASCII only, an all-caps Turkish name typed in capitals
// found nothing, and neither did `kış` for `KIŞ LİSTESİ.xlsx`.
func TestPlanFallback_TurkishCapitals(t *testing.T) {
	for _, c := range []struct {
		queries []string
		stored  []string
	}{
		{[]string{"KIŞ LİSTESİ", "kış listesi", "Kış Listesi", "KIŞ"}, []string{"KIŞ LİSTESİ.xlsx", "kış listesi.xlsx", nfd("KIŞ LİSTESİ.xlsx")}},
		{[]string{"ÇALIŞMA", "çalışma", "çalişma"}, []string{"ÇALIŞMA.txt", "Çalışma.txt", nfd("ÇALIŞMA.txt")}},
		{[]string{"ipek", "İpek", "İPEK", "IPEK", "i" + combiningDot + "pek"}, []string{"İpek", "İPEK", nfd("İpek"), "ipek", "Ipek", "i" + combiningDot + "pek"}},
		// Dotless ı is the fourth i: typing `yılmaz` reaches `YILMAZ`, and
		// typing `YILMAZ` reaches `yılmaz`.
		{[]string{"yılmaz", "YILMAZ", "yilmaz"}, []string{"YILMAZ.pdf", "yılmaz.pdf", "Yılmaz.pdf"}},
	} {
		for _, q := range c.queries {
			m := PlanFallback(q).match
			for _, stored := range c.stored {
				if !dbAnswers(m, stored) {
					t.Errorf("query %q must reach the stored name %q (%+v)", q, stored, m)
				}
			}
		}
	}
}

// TestPlanFallback_RunsFirstAndLongestFirst: the order is only the work, but
// the work is what made the first version of PR #46 four times slower than
// the bug. The runs — ASCII text every stored form holds as it is — are what
// the engine checks natively, before the comparison through the normaliser
// (on SQLite a Go function, a few microseconds a row).
func TestPlanFallback_RunsFirstAndLongestFirst(t *testing.T) {
	m := PlanFallback("Archive Gürel 2026").match
	// `2026` is all plain: the store looks for the word itself natively.
	if want := []string{"arch", "rel"}; !slices.Equal(m.Runs, want) {
		t.Errorf("runs longest first, plain words left out: got %q, want %q", m.Runs, want)
	}
	if want := []string{"archive", "gürel", "2026"}; !slices.Equal(m.Words, want) {
		t.Errorf("words longest first: got %q, want %q", m.Words, want)
	}
	if m.Prefer != "archive" {
		t.Errorf("the longest word ranks, got %q", m.Prefer)
	}
}

// TestPlanFallback_PastedParagraphStaysAQuery: a query is not a document. A
// pasted page would otherwise become hundreds of conditions. Past
// maxFallbackTerms the rest of the words are left to the scorer, which
// checks every piece anyway.
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
	if n := len(plan.match.Words) + len(plan.match.Runs); n > maxFallbackTerms {
		t.Fatalf("%d conditions, want at most %d", n, maxFallbackTerms)
	}
	if slices.Contains(plan.match.Words, "zzqq") {
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

// TestPlanFallback_NoWord: a query with nothing alphanumeric in it asks for
// itself as text — escaped by the store, never LIKE grammar (the AI surface
// used to send `%%`, every row).
func TestPlanFallback_NoWord(t *testing.T) {
	m := PlanFallback("  *** ").match
	if !slices.Equal(m.Words, []string{"***"}) || len(m.Runs) != 0 || m.Prefer != "" {
		t.Errorf("got %+v", m)
	}
	if !dbAnswers(m, "a***b.txt") || dbAnswers(m, "ab.txt") {
		t.Error("`***` is three asterisks")
	}
	if got := PlanFallback("   ").match; len(got.Words) != 0 {
		t.Errorf("an empty query asks for nothing, got %+v", got)
	}
}

func TestSharedRun(t *testing.T) {
	cases := map[string]string{
		"archive": "arch", // `i` may be stored `İ` or `ı`
		"gürel":   "rel",  // a Mac stores `ü` as `u` + a mark
		"öğün":    "n",
		"ipek":    "pe", // `k` may be stored as U+212A KELVIN SIGN
		"jam":     "am", // a stored `j` may carry a dot above
		"2026":    "2026",
		"iğü":     "",
	}
	for word, want := range cases {
		if got := sharedRun(word); got != want {
			t.Errorf("sharedRun(%q) = %q, want %q", word, got, want)
		}
	}
}

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
	"strings"
	"testing"

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

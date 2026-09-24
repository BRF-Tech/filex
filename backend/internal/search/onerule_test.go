package search

import (
	"context"
	"testing"
)

// TestSearch_TheFourIsAreOneLetterOnTheIndexPath: internal/namefold calls I,
// ı, İ and i one letter, and so must every half of the index path — the
// normalised fields, the scorer, and the legacy fields Bleve's own analyser
// lower-cased (it lowers `IŞIK` to `işik` and leaves `ışık` alone). Before
// v0.43.0 a Turkish name in capitals did not answer the word typed in lower
// case at all: `kış` against `KIŞ LİSTESİ.xlsx` is `kış` against `kiş` to
// strings.ToLower, on the index path and without it.
func TestSearch_TheFourIsAreOneLetterOnTheIndexPath(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	for i, name := range []string{"KIŞ LİSTESİ.xlsx", "ışıkları.txt", "IŞIK.pdf", "rapor.txt"} {
		if err := idx.IndexNode(ctx, fileNode(int64(i+1), name, "/tr/"+name, "e")); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string][]int64{
		"kış":         {1},
		"Kış listesi": {1},
		"liste":       {1},
		// One word typed in capitals, part of a lower-case name: only the
		// legacy wildcard on the name as written can reach it (the whole-word
		// match and the typo pass cannot), and that is where `ı` and `I` met.
		"IŞIK": {2, 3},
		"ışık": {2, 3},
	}
	for q, want := range cases {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		got := map[int64]bool{}
		for _, h := range hits {
			got[h.NodeID] = true
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("query %q: want node %d among %+v", q, id, hits)
			}
		}
	}
}

// TestScoreName_PathIdentityFoldsTheFourIs: the query that IS the path is an
// exact match, whatever the case of its i's. strings.EqualFold, the old test,
// calls `ışık/notlar.txt` and `IŞIK/notlar.txt` two different paths.
func TestScoreName_PathIdentityFoldsTheFourIs(t *testing.T) {
	for _, c := range [][2]string{
		{"ışık/notlar.txt", "/IŞIK/notlar.txt"},
		{"IŞIK/notlar.txt", "/ışık/notlar.txt"},
	} {
		got := PrepareQuery(c[0]).ScoreName("notlar.txt", c[1])
		if !got.OK || got.Tier != TierExact || got.Score != PathIdentityScore {
			t.Errorf("query %q, path %q: want the path identity, got %+v", c[0], c[1], got)
		}
	}
}

// TestSearch_LegacyFieldsHoldTheComposedName: one word that is only part of
// a word in the name has one way into the index — the wildcard over the name
// as written, Bleve's legacy field — so that field has to hold the name
// composed too. A Mac's `Gu`+U+0308+`rel` does not contain `üre`.
func TestSearch_LegacyFieldsHoldTheComposedName(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	if err := idx.IndexNode(ctx, fileNode(1, nfd("Gürel.pdf"), nfd("/Müşteri/Gürel.pdf"), "e")); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"üre", "ÜRE"} {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].NodeID != 1 {
			t.Errorf("query %q must find the decomposed name, got %+v", q, hits)
		}
	}
}

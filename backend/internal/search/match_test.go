package search

import (
	"context"
	"fmt"
	"testing"
)

// TestNameMatcher_IsTheSearchRule: the explorer's "Filter in this folder" box
// used its own rule (an accent-stripped substring) and its comment called it
// the search's. These are the cases where the two disagreed (audit D5); the
// box now asks the server, which answers with this.
func TestNameMatcher_IsTheSearchRule(t *testing.T) {
	for _, c := range []struct {
		q, name string
		want    bool
	}{
		{"invoice 2026", "invoice_2026.pdf", true},   // separators are one
		{"invoice 2026", "invoice-final.pdf", false}, // every word
		{"musteri", "müşteri.pdf", false},            // accents count (namefold)
		{"müşteri", "Müşteri listesi.xlsx", true},
		{"IŞIK", "ışık.txt", true}, // the four i's are one letter
		{"rep", "report.txt", true},
		{"", "anything", true},
		{"***", "a***b.txt", true}, // no letter or digit: the text itself
	} {
		if got := MatchName(c.q, c.name); got != c.want {
			t.Errorf("MatchName(%q, %q) = %v, want %v", c.q, c.name, got, c.want)
		}
	}
}

// TestNameMatcher_AgreesWithTheFallback: a name the index-less search accepts
// is a name the matcher accepts (the folder above it aside).
func TestNameMatcher_AgreesWithTheFallback(t *testing.T) {
	for _, c := range []struct{ q, name string }{
		{"main go", "main.go"}, {"report 2026", "Report_2026_final.docx"}, {"kış", "KIŞ LİSTESİ.xlsx"},
	} {
		plan := PlanFallback(c.q)
		if plan.Accepts(c.name, "/"+c.name) != MatchName(c.q, c.name) {
			t.Errorf("%q / %q: the fallback and the matcher disagree", c.q, c.name)
		}
	}
}

// TestSearchPage_TheNarrowingCountsBeforeTheLimit: the index asks the caller's
// narrowing (Filter.Accept - kind, size, date, folder, owner on the catalogue
// row) about each candidate BEFORE the limit counts it (audit D6). Twelve
// reports, only the last two acceptable, a limit of two: both come back.
// Narrowing after the limit (what the explorer did in the browser) would have
// kept two refused reports and then dropped them, leaving nothing.
func TestSearchPage_TheNarrowingCountsBeforeTheLimit(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("report-%02d.txt", i)
		if err := idx.IndexNode(ctx, fileNode(int64(i), name, "/"+name, "e")); err != nil {
			t.Fatal(err)
		}
	}
	accept := func(id int64) bool { return id >= 11 }
	hits, _, err := idx.SearchPage(ctx, "report", 2, ScopeName, &Filter{Accept: accept})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want the 2 acceptable ones", len(hits))
	}
	for _, h := range hits {
		if h.NodeID < 11 {
			t.Fatalf("hit %d is one the narrowing refused", h.NodeID)
		}
	}
	_, more, _ := idx.SearchPage(ctx, "report", 1, ScopeName, &Filter{Accept: accept})
	if !more {
		t.Fatal("two acceptable matches do not fit a page of one, and the page says so")
	}
}

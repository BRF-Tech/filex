package handlers_test

// The demo tells a visitor what to type. This test is what keeps that true.
//
// Measured on demo.filex.sh on 2026-09-07, with the exact strings the product
// printed at the time:
//
//	invoice 2026  -> 0        (splash: "“invoice 2026” finds invoice_2026.pdf")
//	invoice       -> 0
//	tag:report    -> 0        (search box placeholder: "e.g. invoice 2026, tag:report")
//	mian.go       -> 2
//	package main  -> 2
//
// So search worked and the two queries the product suggested were the two that
// failed — on the most likely first interaction a visitor has. There is no
// file with "invoice" in its name anywhere in the demo tree, and no tag called
// "report" (or any tag at all: tags are per-user rows a human clicks in).
//
// ⚠ The demo tree is NOT seeded from this repo. It is a byte copy on the host
// (/root/filex/demo-golden, restored nightly), so "seed a matching file" would
// live one restore away from gone — see docs/DEMO.md. The suggestion is what
// this repo owns, so the suggestion is what changed, and this test binds it to
// the recorded corpus in search_issue15_demo_test.go.
//
// The strings under test live in:
//
//	web/src/locales/{en,tr}.json  →  demo.features.searchBody
//	web/src/locales/{en,tr}.json  →  search.queryPlaceholder
//
// Change one of those examples and change the table below with it.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// demoAdvertisedQueries are the example searches the demo prints, and the file
// each one promises. Every row must be answerable ON THE DEMO CORPUS.
var demoAdvertisedQueries = []struct {
	query string
	want  string
}{
	// Separator-blind: the file on disk is Budget-2026.csv, the visitor types
	// a space. This is the replacement for "invoice 2026", chosen because the
	// demo tree actually holds it.
	{"budget 2026", "/Documents/Budget-2026.csv"},
	// One typo forgiven. Already true before this change, and kept because it
	// is the more surprising half of the claim.
	{"mian.go", "/Code/main.go"},
}

func TestDemoCorpus_AdvertisedQueriesReturnWhatTheyPromise(t *testing.T) {
	base, client := seedDemoCorpus(t)

	for _, c := range demoAdvertisedQueries {
		got := demoPaths(doSearch(t, base, client, c.query, ""))
		t.Logf("advertised %-14q -> %v", c.query, got)
		require.Contains(t, got, c.want,
			"the demo tells visitors to type %q; it must find %s", c.query, c.want)
	}
}

// TestDemoCorpus_TheOldSuggestionsFoundNothing is the red half: proof that the
// strings this change removed were not a matter of taste.
//
// If a future demo tree does grow an invoice, this test starts failing and the
// honest fix is to delete it — not to loosen it.
func TestDemoCorpus_TheOldSuggestionsFoundNothing(t *testing.T) {
	base, client := seedDemoCorpus(t)

	for _, q := range []string{"invoice 2026", "invoice", "tag:report"} {
		got := demoPaths(doSearch(t, base, client, q, ""))
		t.Logf("withdrawn  %-14q -> %v", q, got)
		require.Empty(t, got, "query %q was advertised and answered nothing", q)
	}
}

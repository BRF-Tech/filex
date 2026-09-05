package search

// Issue #15 — forgiving filename search.
//
// The fixture is the exact set the issue was measured against, so every
// row of the report's table is a test rather than a claim.

import (
	"context"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/brf-tech/filex/backend/internal/model"
)

// issueFixture seeds the six files the issue was reported and measured
// against and returns a name lookup for readable assertions.
func issueFixture(t *testing.T) (*Index, map[int64]string) {
	t.Helper()
	ctx := context.Background()
	idx := newTestIndex(t)
	files := []struct {
		id   int64
		name string
		path string
	}{
		{1, "main.go", "/main.go"},
		{2, "foo-bar.txt", "/foo-bar.txt"},
		{3, "invoice_2026.pdf", "/invoice_2026.pdf"},
		{4, "annual report 2025.docx", "/annual report 2025.docx"},
		{5, "readme.txt", "/readme.txt"},
		{6, "notes.md", "/Documents/notes.md"},
	}
	names := map[int64]string{}
	for _, f := range files {
		if err := idx.IndexNode(ctx, fileNode(f.id, f.name, f.path, "e"+f.name)); err != nil {
			t.Fatal(err)
		}
		names[f.id] = f.name
	}
	return idx, names
}

func hitNames(hits []Hit, names map[int64]string) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, names[h.NodeID])
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestSearch_IssueTable is the reporter's table, verbatim, plus the rows
// measured while triaging it. `main go` and `foo bar` already passed
// before the fix; they are here because the fix must not lose them.
func TestSearch_IssueTable(t *testing.T) {
	ctx := context.Background()
	idx, names := issueFixture(t)

	cases := []struct {
		query string
		want  string
	}{
		// The report's four examples.
		{"main go", "main.go"},
		{"foo bar", "foo-bar.txt"},
		{"invoice 2026", "invoice_2026.pdf"},
		{"mian.go", "main.go"},
		// Rows that already worked; regressions here are the real risk.
		{"main.go", "main.go"},
		{"report 2025", "annual report 2025.docx"},
		{"invoice", "invoice_2026.pdf"},
		{"2026", "invoice_2026.pdf"},
		{"main", "main.go"},
		{"go", "main.go"},
		{"bar", "foo-bar.txt"},
		{"2025", "annual report 2025.docx"},
		// Separator blindness is symmetric: every spelling of the same
		// name has to reach the same file.
		{"foo_bar", "foo-bar.txt"},
		{"foo.bar", "foo-bar.txt"},
		{"invoice-2026", "invoice_2026.pdf"},
		{"invoice.2026", "invoice_2026.pdf"},
		{"annual-report-2025", "annual report 2025.docx"},
		// Case must not matter either.
		{"MAIN GO", "main.go"},
		{"Invoice 2026", "invoice_2026.pdf"},
		// More typos, at both fuzziness bands.
		{"raedme", "readme.txt"},
		{"invocie 2026", "invoice_2026.pdf"},
		// Folder hit.
		{"documents notes", "notes.md"},
	}
	for _, c := range cases {
		hits, err := idx.SearchScoped(ctx, c.query, 20, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		got := hitNames(hits, names)
		if !contains(got, c.want) {
			t.Errorf("query %q: want %q in results, got %v", c.query, c.want, got)
		}
	}
}

// TestSearch_MultiWordNarrows guards the other half of the multi-word
// fix: extra words must NARROW. A disjunction of per-word wildcards
// would have made `invoice 2026` rank `annual report 2025.docx`
// alongside the file that actually matches both words.
//
// The pre-#15 OR match on `name` is deliberately still in the query (it
// is the only multi-word path an un-rebuilt index has), so partial
// matches still COME BACK — the contract is that they come back LAST.
func TestSearch_MultiWordNarrows(t *testing.T) {
	ctx := context.Background()
	idx, names := issueFixture(t)

	hits, err := idx.SearchScoped(ctx, "invoice 2026", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	got := hitNames(hits, names)
	if len(got) == 0 || got[0] != "invoice_2026.pdf" {
		t.Fatalf("the file matching BOTH words must rank first, got %v", got)
	}
	for _, h := range hits[1:] {
		if h.Tier <= TierPath {
			t.Errorf("%q matched only part of the query but ranks as %s",
				names[h.NodeID], h.Tier)
		}
	}
}

// TestSearch_RankingContract is the issue's explicit ask: exact matches
// still rank first. With fuzziness in the query that can no longer be
// left to merged Bleve scores, so the order is asserted here.
func TestSearch_RankingContract(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	names := map[int64]string{}
	seed := func(id int64, name, path string) {
		if err := idx.IndexNode(ctx, fileNode(id, name, path, "e")); err != nil {
			t.Fatal(err)
		}
		names[id] = name
	}
	seed(1, "report.txt", "/report.txt")             // exact
	seed(2, "report-final.txt", "/report-final.txt") // prefix
	seed(3, "q1-report.txt", "/q1-report.txt")       // name, not a prefix
	seed(4, "summary.txt", "/reports/summary.txt")   // path only
	seed(5, "reprot.txt", "/reprot.txt")             // typo away from the query

	hits, err := idx.SearchScoped(ctx, "report", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name string
		tier Tier
	}{
		{"report.txt", TierExact},
		{"report-final.txt", TierPrefix},
		{"q1-report.txt", TierName},
		{"summary.txt", TierPath},
		{"reprot.txt", TierFuzzy},
	}
	got := hitNames(hits, names)
	if len(hits) != len(want) {
		t.Fatalf("want %d hits, got %d: %v", len(want), len(hits), got)
	}
	for i, w := range want {
		if got[i] != w.name {
			t.Errorf("position %d: want %q, got %q (full order %v)", i, w.name, got[i], got)
		}
		if hits[i].Tier != w.tier {
			t.Errorf("%q: want tier %s, got %s", w.name, w.tier, hits[i].Tier)
		}
	}
}

// TestSearch_NameHitsStillBeatContentHits is the frozen pre-v0.2
// contract. Ranking now sorts the whole result list, so this is the test
// that says the sort may never float a content-only hit above a name
// hit — not even a weak fuzzy one.
func TestSearch_NameHitsStillBeatContentHits(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)

	weakName := fileNode(1, "buget.txt", "/buget.txt", "e1") // typo distance from "budget"
	contentOnly := fileNode(2, "unrelated.md", "/unrelated.md", "e2")
	if err := idx.IndexNode(ctx, weakName); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNode(ctx, contentOnly); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := idx.IndexNodeContent(ctx, contentOnly, "budget budget budget budget budget"); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := idx.SearchScoped(ctx, "budget", 10, ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %+v", hits)
	}
	if hits[0].NodeID != 1 || hits[0].Matched != MatchedName {
		t.Errorf("a fuzzy NAME hit must still outrank a strong content hit: %+v", hits)
	}
	if hits[1].NodeID != 2 || hits[1].Matched != MatchedContent {
		t.Errorf("content-only hit must come last: %+v", hits)
	}
}

// TestSearch_TagFilter covers the tag: filter at the index layer — the
// node-ID set the handler resolves from the database.
func TestSearch_TagFilter(t *testing.T) {
	ctx := context.Background()
	idx, names := issueFixture(t)

	// "source" = main.go (1); "invoice" = invoice_2026.pdf (3).
	only := func(ids ...int64) *Filter { return &Filter{Restrict: true, IncludeIDs: ids} }

	// Free text plus a tag filter — the reporter's `main go tag:source`.
	hits, err := idx.SearchFiltered(ctx, "main go", 20, ScopeName, only(1))
	if err != nil {
		t.Fatal(err)
	}
	if got := hitNames(hits, names); len(got) != 1 || got[0] != "main.go" {
		t.Errorf("`main go tag:source` want [main.go], got %v", got)
	}

	// The same text with a tag that does not cover it returns nothing —
	// a filter narrows, it never falls back to ignoring itself.
	hits, err = idx.SearchFiltered(ctx, "main go", 20, ScopeName, only(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("tag filter must exclude non-members, got %v", hitNames(hits, names))
	}

	// A tag nobody uses resolves to an empty set, which means no results
	// rather than every result.
	hits, err = idx.SearchFiltered(ctx, "main go", 20, ScopeName, &Filter{Restrict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("unknown tag must return nothing, got %v", hitNames(hits, names))
	}

	// -tag: exclusion.
	hits, err = idx.SearchFiltered(ctx, "txt", 20, ScopeName, &Filter{ExcludeIDs: []int64{5}})
	if err != nil {
		t.Fatal(err)
	}
	got := hitNames(hits, names)
	if contains(got, "readme.txt") {
		t.Errorf("-tag: must drop the excluded node, got %v", got)
	}
	if !contains(got, "foo-bar.txt") {
		t.Errorf("-tag: must keep everything else, got %v", got)
	}

	// The filter runs inside the engine, so `limit` counts filtered
	// results. Post-filtering would return 0 here: an unfiltered top-1
	// for "txt" is not guaranteed to be the tagged node.
	hits, err = idx.SearchFiltered(ctx, "txt", 1, ScopeName, only(2))
	if err != nil {
		t.Fatal(err)
	}
	if got := hitNames(hits, names); len(got) != 1 || got[0] != "foo-bar.txt" {
		t.Errorf("limit must count FILTERED hits, got %v", got)
	}
}

func TestParseQuery(t *testing.T) {
	cases := []struct {
		raw     string
		text    string
		tags    []string
		exclude []string
	}{
		{"main go", "main go", nil, nil},
		{"main go tag:source", "main go", []string{"source"}, nil},
		{"tag:invoice", "", []string{"invoice"}, nil},
		{"TAG:Source", "", []string{"source"}, nil},
		{"tag:a tag:b", "", []string{"a", "b"}, nil},
		{"report -tag:archive", "report", nil, []string{"archive"}},
		{`tag:"quarterly report" q1`, "q1", []string{"quarterly report"}, nil},
		// A bare tag: is not a filter — somebody may be looking for a
		// file called exactly that.
		{"tag:", "tag:", nil, nil},
		{"", "", nil, nil},
	}
	for _, c := range cases {
		got := ParseQuery(c.raw)
		if got.Text != c.text {
			t.Errorf("%q: text = %q, want %q", c.raw, got.Text, c.text)
		}
		if !equalStrings(got.Tags, c.tags) {
			t.Errorf("%q: tags = %v, want %v", c.raw, got.Tags, c.tags)
		}
		if !equalStrings(got.ExcludeTags, c.exclude) {
			t.Errorf("%q: exclude = %v, want %v", c.raw, got.ExcludeTags, c.exclude)
		}
		if got.HasTagFilter() != (len(c.tags)+len(c.exclude) > 0) {
			t.Errorf("%q: HasTagFilter = %v", c.raw, got.HasTagFilter())
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalize(t *testing.T) {
	cases := [][2]string{
		{"main.go", "main go"},
		{"foo-bar.txt", "foo bar txt"},
		{"invoice_2026.pdf", "invoice 2026 pdf"},
		{"annual report 2025.docx", "annual report 2025 docx"},
		{"/Documents/notes.md", "documents notes md"},
		{"MAIN.GO", "main go"},
		{"  ...  ", ""},
		{"rapor-şubat.txt", "rapor şubat txt"},
		{"a__b--c..d", "a b c d"},
	}
	for _, c := range cases {
		if got := Normalize(c[0]); got != c[1] {
			t.Errorf("Normalize(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestRankName(t *testing.T) {
	cases := []struct {
		query, name, path string
		want              Tier
	}{
		{"main go", "main.go", "/main.go", TierExact},
		{"main.go", "main.go", "/main.go", TierExact},
		{"main", "main.go", "/main.go", TierExact},
		{"invoice 2026", "invoice_2026.pdf", "/invoice_2026.pdf", TierExact},
		{"report", "report-final.txt", "/report-final.txt", TierPrefix},
		{"rep", "report.txt", "/report.txt", TierPrefix},
		{"report", "q1-report.txt", "/q1-report.txt", TierName},
		{"report", "summary.txt", "/reports/summary.txt", TierPath},
		{"report", "reprot.txt", "/reprot.txt", TierFuzzy},
	}
	for _, c := range cases {
		if got := RankName(c.query, c.name, c.path); got != c.want {
			t.Errorf("RankName(%q, %q) = %s, want %s", c.query, c.name, got, c.want)
		}
	}
}

// TestSearch_LegacyDocumentsStillMatch is the upgrade story.
//
// A document written by a pre-#15 build has no name_norm field. This
// simulates one by writing the old shape straight into a fresh index,
// then asserts that everything the old query could find, the new query
// still finds — the separator-blind extras simply do not apply to it.
// If this ever fails, upgrading without a rebuild silently loses results.
func TestSearch_LegacyDocumentsStillMatch(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)

	type legacyDoc struct {
		StorageID int64  `json:"storage_id"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		Type      string `json:"type"`
	}
	if err := idx.bleve.Index("1", legacyDoc{1, "square.jpg", "/pics/square.jpg", "file"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"square.jpg", "squ", "SQUARE", "pics", "jpg"} {
		hits, err := idx.SearchScoped(ctx, q, 10, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(hits) != 1 {
			t.Errorf("legacy document lost for query %q: %+v", q, hits)
		}
	}
}

// TestIndexSchemaVersion_Stale checks the operator-facing half of the
// upgrade story: an index written by an older build is reported as
// wanting a rebuild, and a rebuild clears the flag.
func TestIndexSchemaVersion_Stale(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir() + "/idx.bleve"

	// A brand-new index is stamped and never asks for a rebuild.
	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if idx.NeedsRebuild() {
		t.Error("a fresh index must not ask for a rebuild")
	}
	// Fake an index written before the marker existed.
	if err := idx.bleve.SetInternal([]byte(indexVersionKey), []byte("1")); err != nil {
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
		t.Fatal("an index with an older schema must report NeedsRebuild")
	}
	if !reopened.Stats().NeedsRebuild {
		t.Error("Stats must carry the flag so the admin endpoint can show it")
	}
	if err := reopened.RebuildAll(ctx, stubLister{}); err != nil {
		t.Fatal(err)
	}
	if reopened.NeedsRebuild() {
		t.Error("a rebuild must clear the drift")
	}
}

// TestFuzzyPassIsConditional records the cost decision: the expensive
// typo-tolerant pass only runs when the strict pass came back short.
func TestFuzzyPassIsConditional(t *testing.T) {
	ctx := context.Background()
	idx, names := issueFixture(t)

	// limit=1 and a strict hit exists → the fuzzy pass is skipped, so
	// the near-miss neighbour never enters the result set.
	hits, err := idx.SearchScoped(ctx, "readme", 1, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitNames(hits, names); len(got) != 1 || got[0] != "readme.txt" {
		t.Errorf("want exactly [readme.txt], got %v", got)
	}
}

// BenchmarkNameSearch measures what fuzziness costs. Run with:
//
//	go test ./internal/search/ -run xxx -bench NameSearch -benchtime 200x
func BenchmarkNameSearch(b *testing.B) {
	ctx := context.Background()
	idx, err := Open(b.TempDir() + "/idx.bleve")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	words := []string{"invoice", "report", "budget", "backup", "archive", "photo", "notes", "draft", "final", "summary"}
	exts := []string{"pdf", "txt", "docx", "md", "csv"}
	id := int64(1)
	for i := 0; i < 20000; i++ {
		name := words[i%len(words)] + "_" + words[(i/7)%len(words)] + "-" + itoa(i) + "." + exts[i%len(exts)]
		n := &model.Node{
			ID: id, StorageID: 1, Name: name,
			Path: "/" + words[i%len(words)] + "/" + name,
			Type: model.NodeTypeFile, Etag: "e",
		}
		if err := idx.IndexNode(ctx, n); err != nil {
			b.Fatal(err)
		}
		id++
	}

	bench := func(name string, run func()) {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				run()
			}
		})
	}
	// The pre-#15 query, for comparison: two leading-wildcard scans plus a
	// match. This is the cost baseline the additions are measured against.
	bench("legacy_trio_only", func() {
		wc := "*invoice*"
		mq := bleve.NewMatchQuery("invoice")
		mq.SetField("name")
		w1 := bleve.NewWildcardQuery(wc)
		w1.SetField("name")
		w2 := bleve.NewWildcardQuery(wc)
		w2.SetField("path")
		if _, err := runNameSearch(idx.bleve, bleve.NewDisjunctionQuery(mq, w1, w2), 50); err != nil {
			b.Fatal(err)
		}
	})
	bench("strict_singleword", func() { _, _ = idx.SearchScoped(ctx, "invoice", 50, ScopeName) })
	bench("strict_multiword", func() { _, _ = idx.SearchScoped(ctx, "invoice report", 50, ScopeName) })
	bench("fuzzy_forced", func() {
		res, err := runNameSearch(idx.bleve, fuzzyNameQuery("invoce reprot"), 50)
		if err != nil || res == nil {
			b.Fatal(err)
		}
	})
	bench("miss_triggers_fuzzy", func() { _, _ = idx.SearchScoped(ctx, "invocie", 50, ScopeName) })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestSearch_AllDigitWordsAreNotFuzzy is the measurement behind the
// digits exemption.
//
// Reported on the demo corpus: searching `2026` returned `annual report
// 2025.docx` as its SECOND hit, because 2025 is one edit from 2026 and
// the typo pass applies to every word. A near-miss digit is not a
// misspelling of the same number — it is a different year, a different
// invoice, a different order. The words a person mistypes and the numbers
// they look up are different kinds of token, and only one of them wants
// forgiving.
//
// The three assertions are one change and its two fences: the wrong
// neighbour goes away, every literal path onto a number stays, and the
// typo tolerance a real word gets is untouched.
func TestSearch_AllDigitWordsAreNotFuzzy(t *testing.T) {
	ctx := context.Background()
	idx, names := issueFixture(t)

	// The bug itself. `2026` must not drag in the 2025 file.
	hits, err := idx.SearchScoped(ctx, "2026", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	got := hitNames(hits, names)
	if contains(got, "annual report 2025.docx") {
		t.Errorf("`2026` returned a 2025 file — a different number is not a typo; got %v", got)
	}
	// ...and the same in the other direction, so the fix is a rule rather
	// than a special case for one fixture row.
	hits, err = idx.SearchScoped(ctx, "2025", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if got = hitNames(hits, names); contains(got, "invoice_2026.pdf") {
		t.Errorf("`2025` returned a 2026 file; got %v", got)
	}

	// Prefix / exact / substring on digits must not change. These are the
	// rows the exemption could plausibly have broken, so they are asserted
	// here as well as in the issue table.
	for _, c := range []struct{ query, want string }{
		{"2026", "invoice_2026.pdf"},
		{"2025", "annual report 2025.docx"},
		{"invoice 2026", "invoice_2026.pdf"},
		{"invoice-2026", "invoice_2026.pdf"},
	} {
		hits, err := idx.SearchScoped(ctx, c.query, 20, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		if got := hitNames(hits, names); !contains(got, c.want) {
			t.Errorf("query %q must still find %q, got %v", c.query, c.want, got)
		}
	}

	// And a genuine word typo still resolves — the exemption is about
	// digits, not about switching typo tolerance off.
	hits, err = idx.SearchScoped(ctx, "mian.go", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if got = hitNames(hits, names); !contains(got, "main.go") {
		t.Errorf("`mian.go` must still find main.go, got %v", got)
	}
}

// TestFuzzinessFor_Digits pins the rule at the unit that decides it, so a
// future change to the length bands cannot quietly re-enable it for
// numbers.
func TestFuzzinessFor_Digits(t *testing.T) {
	for _, c := range []struct {
		word string
		want int
	}{
		{"2026", 0},       // a year: 2025 is a different year
		{"1000123456", 0}, // an invoice number, long enough for two edits
		{"12", 0},         // short words were already exact
		{"main", 1},       // a word of the same length still forgives one
		{"readme", 1},     //
		{"attachment", 2}, // long words still forgive two
		{"v2024x", 1},     // MIXED: still fuzzy, see fuzzinessFor's comment
	} {
		if got := fuzzinessFor(c.word); got != c.want {
			t.Errorf("fuzzinessFor(%q) = %d, want %d", c.word, got, c.want)
		}
	}
}

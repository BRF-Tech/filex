package search

// Issue #15, second round — VS Code Quick Open style filename scoring.
//
// The reporter came back after v0.29.0 with three complaints, and the
// fixture below is the folder he measured against on demo.filex.sh:
//
//	"Path. I can only search by filename, not by path. Code/main.go and
//	 example/main.go are the same thing to the search, and `Code main`
//	 returns nothing. VS Code does this fine, I can't do it here. And
//	 with real fuzzy the word order shouldn't matter either, `main code`
//	 should still find Code/main.go"
//
// Every test here is one of those sentences.

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// dirNode is fileNode's directory sibling — the demo has a `Code`
// FOLDER as well as the file inside it, and the folder is exactly the
// kind of half-match the scorer has to drop.
func dirNode(id int64, name, path string) *model.Node {
	return &model.Node{
		ID: id, StorageID: 1, Name: name, Path: path,
		Type: model.NodeTypeDirectory, Etag: "d" + name,
	}
}

// demoFixture mirrors https://demo.filex.sh: two files called main.go in
// different folders, plus the content-bearing files that turned `Code
// main` into nine results there.
func demoFixture(t *testing.T) (*Index, map[int64]string) {
	t.Helper()
	ctx := context.Background()
	idx := newTestIndex(t)
	paths := map[int64]string{}
	seedFile := func(id int64, name, path, content string) {
		n := fileNode(id, name, path, "e"+path)
		if err := idx.IndexNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if err := idx.IndexNodeContent(ctx, n, content); err != nil {
				t.Fatal(err)
			}
		}
		paths[id] = path
	}
	seedDir := func(id int64, name, path string) {
		if err := idx.IndexNode(ctx, dirNode(id, name, path)); err != nil {
			t.Fatal(err)
		}
		paths[id] = path
	}
	seedDir(123, "Code", "/Code")
	seedDir(8, "example", "/example")
	seedFile(126, "main.go", "/Code/main.go", "package main\nfunc main() {}\n")
	seedFile(100, "main.go", "/example/main.go", "package main\n// example code\n")
	// The content noise: files that mention "code" and nothing else the
	// query asked for. On the demo these were seven of the nine results.
	seedFile(1, "README.md", "/README.md", "filex source code lives here")
	seedFile(92, "demo.py", "/example/demo.py", "# some python code")
	seedFile(101, "notebook.ipynb", "/example/notebook.ipynb", "a code cell")
	seedFile(105, "sample.html", "/example/sample.html", "<code>hello</code>")
	return idx, paths
}

func hitPaths(hits []Hit, paths map[int64]string) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, paths[h.NodeID])
	}
	return out
}

func equalPaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestSearch_QueryPiecesAreOrderIndependent is the reporter's headline:
// `Code main` and `main code` must both find Code/main.go, and must find
// ONLY it. The folder `/Code` matches the word "code" and nothing else
// the user typed, so it is a half-match and gets dropped — that is the
// difference between a filter and a suggestion.
func TestSearch_QueryPiecesAreOrderIndependent(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	for _, q := range []string{"Code main", "main code"} {
		hits, err := idx.SearchScoped(ctx, q, 20, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		got := hitPaths(hits, paths)
		if !equalPaths(got, []string{"/Code/main.go"}) {
			t.Errorf("query %q: want exactly [/Code/main.go], got %v", q, got)
		}
	}
}

// TestSearch_SameNameDistinguishedByFolder is "Code/main.go and
// example/main.go are the same thing to the search". They are not the
// same thing: naming a folder has to exclude the other one.
func TestSearch_SameNameDistinguishedByFolder(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	cases := []struct{ query, want, mustNotContain string }{
		{"Code/main.go", "/Code/main.go", "/example/main.go"},
		{"example/main.go", "/example/main.go", "/Code/main.go"},
		{"Code main.go", "/Code/main.go", "/example/main.go"},
	}
	for _, c := range cases {
		hits, err := idx.SearchScoped(ctx, c.query, 20, ScopeName)
		if err != nil {
			t.Fatalf("%q: %v", c.query, err)
		}
		got := hitPaths(hits, paths)
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("query %q: want %q first, got %v", c.query, c.want, got)
		}
		if contains(got, c.mustNotContain) {
			t.Errorf("query %q names a folder, so %q must not be a result; got %v",
				c.query, c.mustNotContain, got)
		}
	}
}

// TestSearch_PieceMatchingNothingDropsCandidate is the filter half of
// the scorer. A query piece nothing in the candidate answers means the
// candidate is not a result, however well the other pieces scored.
func TestSearch_PieceMatchingNothingDropsCandidate(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	hits, err := idx.SearchScoped(ctx, "Code zzzz", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitPaths(hits, paths); len(got) != 0 {
		t.Errorf("nothing answers both `Code` and `zzzz`, want no results, got %v", got)
	}
}

// TestSearch_ShorterPathWinsWithinTier: two files really are called
// main.go, so the tier cannot separate them. VS Code breaks that tie by
// preferring the shorter path (fallbackCompare); before this change the
// tiebreak was the database id, which is not a ranking, it is an
// accident of insert order.
func TestSearch_ShorterPathWinsWithinTier(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	hits, err := idx.SearchScoped(ctx, "main.go", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	got := hitPaths(hits, paths)
	if len(got) < 2 {
		t.Fatalf("want both main.go files, got %v", got)
	}
	if got[0] != "/Code/main.go" {
		t.Errorf("equal scores must prefer the shorter path, want /Code/main.go first, got %v", got)
	}
}

// TestSearch_ContentSideNarrowsOnMultiWord is the nine-result noise on
// the demo. The name side has required every word since v0.29.0, but the
// content side was still a default-OR match query, so `Code main`
// returned every file that merely said "code".
func TestSearch_ContentSideNarrowsOnMultiWord(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	hits, err := idx.SearchScoped(ctx, "Code main", 20, ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	got := hitPaths(hits, paths)
	for _, noise := range []string{"/README.md", "/example/demo.py", "/example/notebook.ipynb", "/example/sample.html"} {
		if contains(got, noise) {
			t.Errorf("%q says `code` but not `main`; a two-word query must not return it: %v", noise, got)
		}
	}
	if len(got) == 0 || got[0] != "/Code/main.go" {
		t.Errorf("want /Code/main.go first, got %v", got)
	}
}

// TestSearch_TypoIsStillFoundAndRanksLast is the line we do NOT take
// from VS Code. `mian.go` is not a subsequence of `main.go`, so Quick
// Open would find nothing; our edit-distance pass finds it. It stays,
// ranked below every subsequence match.
func TestSearch_TypoIsStillFoundAndRanksLast(t *testing.T) {
	ctx := context.Background()
	idx, paths := demoFixture(t)

	hits, err := idx.SearchScoped(ctx, "mian.go", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	got := hitPaths(hits, paths)
	if !contains(got, "/Code/main.go") || !contains(got, "/example/main.go") {
		t.Fatalf("the typo pass must still reach both main.go files, got %v", got)
	}
	for _, h := range hits {
		if h.Tier != TierFuzzy {
			t.Errorf("%s: a typo hit must be tier fuzzy, got %s", paths[h.NodeID], h.Tier)
		}
	}
}

// TestSearch_RankingClassesStayOrdered is the issue's explicit ask —
// exact above prefix above the rest — restated for the classes the
// scorer introduces, so the order in docs/SEARCH.md is a test and not a
// claim.
func TestSearch_RankingClassesStayOrdered(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	paths := map[int64]string{}
	seed := func(id int64, name, path string) {
		if err := idx.IndexNode(ctx, fileNode(id, name, path, "e"+path)); err != nil {
			t.Fatal(err)
		}
		paths[id] = path
	}
	seed(1, "report.txt", "/report.txt")             // exact
	seed(2, "report-final.txt", "/report-final.txt") // prefix
	seed(3, "q1-report.txt", "/q1-report.txt")       // subsequence in the name
	seed(4, "summary.txt", "/reports/summary.txt")   // only the folder answers
	seed(5, "reprot.txt", "/reprot.txt")             // one transposition away

	hits, err := idx.SearchScoped(ctx, "report", 20, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		path string
		tier Tier
	}{
		{"/report.txt", TierExact},
		{"/report-final.txt", TierPrefix},
		{"/q1-report.txt", TierName},
		{"/reports/summary.txt", TierPath},
		{"/reprot.txt", TierFuzzy},
	}
	got := hitPaths(hits, paths)
	if len(hits) != len(want) {
		t.Fatalf("want %d hits, got %d: %v", len(want), len(hits), got)
	}
	for i, w := range want {
		if got[i] != w.path {
			t.Errorf("position %d: want %q, got %q (full order %v)", i, w.path, got[i], got)
		}
		if hits[i].Tier != w.tier {
			t.Errorf("%q: want tier %s, got %s", w.path, w.tier, hits[i].Tier)
		}
	}
}

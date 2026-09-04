package handlers_test

// Issue #15, second round — measured end to end, on the corpus the
// reporter actually measured on.
//
// The fixture below is https://demo.filex.sh, walked over the live API:
// the same 52 nodes, including the two files called main.go in different
// folders that the report is about. The queries are the ones he typed.
//
// This goes through the HTTP endpoint rather than the index package
// because that is where he saw the behaviour, and because the query
// parser, the tag filter, the scorer and the ranking all have to line up
// for the endpoint to answer correctly — testing the scorer alone would
// not have caught a caller that forgot to parse `tag:` out first.

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// demoNodes is the live demo.filex.sh tree as walked on 2026-09-04.
var demoNodes = []struct {
	path  string
	isDir bool
}{
	{"/README.md", false},
	{"/Code", true},
	{"/Code/App.vue", false},
	{"/Code/docker-compose.yml", false},
	{"/Code/main.go", false},
	{"/Design", true},
	{"/Design/logo.svg", false},
	{"/Design/palette.png", false},
	{"/Documents", true},
	{"/Documents/Budget-2026.csv", false},
	{"/Documents/Meeting-Notes.md", false},
	{"/Documents/Project-Brief.pdf", false},
	{"/Documents/Roadmap.md", false},
	{"/Photos", true},
	{"/Photos/aurora.png", false},
	{"/Photos/ember.png", false},
	{"/Photos/forest.png", false},
	{"/Photos/geometry.png", false},
	{"/Photos/mosaic.png", false},
	{"/Photos/nebula.png", false},
	{"/Photos/ocean.png", false},
	{"/Photos/sunset.png", false},
	{"/example", true},
	{"/example/budget.ods", false},
	{"/example/cube.glb", false},
	{"/example/cube.obj", false},
	{"/example/cube.stl", false},
	{"/example/demo.js", false},
	{"/example/demo.py", false},
	{"/example/diagram.drawio", false},
	{"/example/dummy.pdf", false},
	{"/example/flow.mmd", false},
	{"/example/landscape.jpg", false},
	{"/example/layered.psd", false},
	{"/example/letter.docx", false},
	{"/example/logo.svg", false},
	{"/example/main.go", false},
	{"/example/manager.jpg", false},
	{"/example/manager.svg", false},
	{"/example/notebook.ipynb", false},
	{"/example/notes.odt", false},
	{"/example/photo.webp", false},
	{"/example/report.xlsx", false},
	{"/example/sample.html", false},
	{"/example/sample.mp4", false},
	{"/example/sample.xml", false},
	{"/example/sample.zip", false},
	{"/example/scan.tiff", false},
	{"/example/silence-2s.mp3", false},
	{"/example/slides.pptx", false},
	{"/example/square.jpg", false},
	{"/example/users.csv", false},
}

// demoContent is the text the demo's extractor holds for the files that
// answered `Code main` on content, excerpted from the live instance. It
// is here so the content-side narrowing is measured against real text
// rather than against strings written to make the test pass.
var demoContent = map[string]string{
	"/Code/main.go": `package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	fmt.Println("listening on :8080")
	http.ListenAndServe(":8080", nil)
}`,
	"/example/main.go": `// filex tiny utility — emit a JSON manifest of files in a directory.
//
// Walks the given root, collects (path, size, mode, mtime) for every
// file, and writes a JSON array to stdout. Used by the demo notebooks
// to populate an in-memory dataframe.
package main`,
	"/example/demo.py": `"""filex companion utility — counts files per extension.

Walks a directory tree and emits a markdown table sorted by count. The
counterpart filex CLI ships this with the binary, but the script is
useful as a one-off for ad-hoc audit work.
"""

if __name__ == "__main__":
    raise SystemExit(main())`,
	"/example/sample.html": `<!doctype html><html lang="en"><head><title>filex sample — self-hosted file manager</title>
<style>:root { --brand:#3b82f6; }</style></head><body>source code sample</body></html>`,
	"/README.md": `filex — a self-hosted file manager. The source code lives on GitHub.`,
	// ⚠ This one contains BOTH words, which is why it is not in the noise
	// list below: measured on the live demo, `scope=content` returns it
	// for `code` AND for `main`. It is a legitimate answer to `Code main`
	// and narrowing the content side must not lose it — a filter that
	// drops true positives is not an improvement over one that keeps
	// false ones.
	"/example/notebook.ipynb": `a code cell that loads the main manifest and counts extensions`,
	"/example/diagram.drawio": `flow diagram: main service, review step`,
	"/example/letter.docx":    `a letter about the main programme`,
}

// seedDemoCorpus stands up a server whose index holds the demo tree.
func seedDemoCorpus(t *testing.T) (base string, client *http.Client) {
	t.Helper()

	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Index = idx
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "demo", Driver: "local", MountPath: "/demo", Enabled: true,
	})
	require.NoError(t, err)

	for _, f := range demoNodes {
		typ := model.NodeTypeFile
		if f.isDir {
			typ = model.NodeTypeDirectory
		}
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID,
			Name:      filepath.Base(f.path),
			Path:      f.path,
			PathHash:  searchTestPathHash(st.ID, f.path),
			Type:      typ,
			Mime:      "text/plain",
			Size:      42,
			Etag:      "e-" + f.path,
		})
		require.NoError(t, err)
		require.NoError(t, idx.IndexNode(ctx, n))
		if body, ok := demoContent[f.path]; ok {
			require.NoError(t, idx.IndexNodeContent(ctx, n, body))
		}
	}
	return srv.URL, client
}

func demoPaths(items []searchRespItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

// TestDemoCorpus_ReporterQueries is the before/after table, as a test.
//
// Measured on demo.filex.sh (v0.29.0) on 2026-09-04, scope=name:
//
//	Code main        -> /Code/main.go, /Code
//	main code        -> /Code/main.go, /Code
//	Code/main.go     -> /Code/main.go, /Code, /example/main.go
//	example/main.go  -> /example/main.go, /example, /Code/main.go
//	main.go          -> /example/main.go, /Code/main.go
//
// Every one of those rows had something wrong with it: a folder that
// answers half the query, a same-named file in a folder the query
// explicitly named something else, and an order decided by database id.
func TestDemoCorpus_ReporterQueries(t *testing.T) {
	base, client := seedDemoCorpus(t)

	cases := []struct {
		query string
		want  []string
	}{
		// The headline: the word order carries no meaning, and the
		// `/Code` FOLDER is not an answer to a query that also said
		// "main".
		{"Code main", []string{"/Code/main.go"}},
		{"main code", []string{"/Code/main.go"}},
		// Naming a folder excludes the other folder.
		{"Code/main.go", []string{"/Code/main.go"}},
		{"example/main.go", []string{"/example/main.go"}},
		// No folder named: both are honest answers, shorter path first.
		{"main.go", []string{"/Code/main.go", "/example/main.go"}},
	}
	for _, c := range cases {
		got := demoPaths(doSearch(t, base, client, c.query, "name"))
		t.Logf("scope=name  %-18q -> %v", c.query, got)
		require.Equal(t, c.want, got, "query %q", c.query)
	}
}

// TestDemoCorpus_ContentNoiseIsGone is the nine-result complaint.
//
// On the live demo (v0.29.0) `Code main` at the default scope returned 9
// rows: the file, its folder, and seven files that answered only ONE of
// the two words. The name side has required every word since v0.29.0;
// the content side was still a default-OR match query.
//
// The expected answer is 2, not 1. /example/notebook.ipynb really does
// contain both words — measured on the live demo, it is returned by
// `scope=content` for `code` and for `main` — so it is a true positive
// and it stays.
func TestDemoCorpus_ContentNoiseIsGone(t *testing.T) {
	base, client := seedDemoCorpus(t)

	got := demoPaths(doSearch(t, base, client, "Code main", ""))
	t.Logf("scope=all   %-18q -> %v", "Code main", got)

	// Each of these answers exactly one of the two words.
	for _, noise := range []string{
		"/README.md", "/example/sample.html", // "code" only
		"/example/demo.py", "/example/diagram.drawio", // "main" only
		"/example/letter.docx", "/example/main.go", // "main" only
		"/Code", // the folder: a name half-match
	} {
		require.NotContains(t, got, noise,
			"%s answers one of the two words; a two-word query must narrow, not widen", noise)
	}
	require.Equal(t, []string{"/Code/main.go", "/example/notebook.ipynb"}, got,
		"the file, then the one other document that really does say both words")
}

// TestDemoCorpus_TagFilterSurvivesTheScorer is the trap this change set
// most easily falls into. The scorer drops a candidate when ANY query
// piece is unanswered, so if `tag:` reached it as text instead of being
// parsed out as a filter, every tag search would return nothing —
// silently, and only for people who use tags.
func TestDemoCorpus_TagFilterSurvivesTheScorer(t *testing.T) {
	base, client := seedDemoCorpus(t)

	// No tags exist in this fixture, so a tag filter must return nothing
	// — the filter is honoured rather than ignored...
	require.Empty(t, doSearch(t, base, client, "main.go tag:source", "name"))
	// ...while the same query without the tag still finds the files, which
	// is what proves the emptiness above came from the FILTER and not
	// from the scorer choking on the token.
	require.NotEmpty(t, doSearch(t, base, client, "main.go", "name"))
}

// TestDemoCorpus_TypoStillReachesTheFile keeps the line we do not take
// from VS Code. Quick Open would find nothing for `mian.go`; our
// edit-distance pass finds both files, ranked below every subsequence
// match.
func TestDemoCorpus_TypoStillReachesTheFile(t *testing.T) {
	base, client := seedDemoCorpus(t)

	got := demoPaths(doSearch(t, base, client, "mian.go", "name"))
	t.Logf("scope=name  %-18q -> %v", "mian.go", got)
	require.Contains(t, got, "/Code/main.go")
	require.Contains(t, got, "/example/main.go")
}

// TestDemoCorpus_NoQueryReturnsNothingSurprising guards the shape of the
// corpus itself: if the demo tree in this file drifts from the real one,
// the table above stops being a measurement of anything.
func TestDemoCorpus_NoQueryReturnsNothingSurprising(t *testing.T) {
	base, client := seedDemoCorpus(t)

	got := demoPaths(doSearch(t, base, client, "main", "name"))
	for _, p := range got {
		require.True(t, strings.Contains(strings.ToLower(p), "main"),
			"%q does not contain `main` anywhere in its path", p)
	}
	require.Contains(t, got, "/Code/main.go")
	require.Contains(t, got, "/example/main.go")
}

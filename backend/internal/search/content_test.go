package search

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "idx.bleve"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func fileNode(id int64, name, path, etag string) *model.Node {
	return &model.Node{
		ID:        id,
		StorageID: 1,
		Name:      name,
		Path:      path,
		Mime:      "text/plain",
		Type:      model.NodeTypeFile,
		Size:      100,
		Etag:      etag,
	}
}

func TestIndexNodeContent_PreservesMetadataAndContent(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	n := fileNode(1, "rapor.txt", "/belgeler/rapor.txt", "etag-1")

	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNodeContent(ctx, n, "quarterly budget planning document"); err != nil {
		t.Fatal(err)
	}

	// Content search hits after the content update…
	hits, err := idx.SearchScoped(ctx, "budget", 10, ScopeContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].NodeID != 1 {
		t.Fatalf("content hit missing: %+v", hits)
	}
	// …and the name fields survived the content reindex.
	hits, err = idx.SearchScoped(ctx, "rapor", 10, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("name search lost after content update: %+v", hits)
	}

	// A later metadata reindex (rename) must keep the content.
	n.Name = "rapor-final.txt"
	n.Path = "/belgeler/rapor-final.txt"
	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	hits, err = idx.SearchScoped(ctx, "budget", 10, ScopeContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("content lost after metadata reindex: %+v", hits)
	}
}

func TestSearchScoped_ScopesAndSnippets(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)

	// Node A matches "budget" only via content; node B only via name;
	// node C via both.
	a := fileNode(1, "rapor.txt", "/a/rapor.txt", "e1")
	b := fileNode(2, "budget.txt", "/a/budget.txt", "e2")
	c := fileNode(3, "budget-plan.txt", "/a/budget-plan.txt", "e3")
	for _, n := range []*model.Node{a, b, c} {
		if err := idx.IndexNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.IndexNodeContent(ctx, a, "the annual budget forecast lives here"); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNodeContent(ctx, c, "budget details for the plan"); err != nil {
		t.Fatal(err)
	}

	find := func(hits []Hit, id int64) *Hit {
		for i := range hits {
			if hits[i].NodeID == id {
				return &hits[i]
			}
		}
		return nil
	}

	// scope=name → only b and c.
	hits, err := idx.SearchScoped(ctx, "budget", 10, ScopeName)
	if err != nil {
		t.Fatal(err)
	}
	if find(hits, 1) != nil || find(hits, 2) == nil || find(hits, 3) == nil {
		t.Fatalf("name scope wrong: %+v", hits)
	}
	for _, h := range hits {
		if h.Matched != MatchedName || h.Snippet != "" {
			t.Fatalf("name hit shape wrong: %+v", h)
		}
	}

	// scope=content → only a and c, with «budget» snippets.
	hits, err = idx.SearchScoped(ctx, "budget", 10, ScopeContent)
	if err != nil {
		t.Fatal(err)
	}
	if find(hits, 2) != nil || find(hits, 1) == nil || find(hits, 3) == nil {
		t.Fatalf("content scope wrong: %+v", hits)
	}
	for _, h := range hits {
		if h.Matched != MatchedContent {
			t.Fatalf("content hit matched=%q", h.Matched)
		}
		if !strings.Contains(h.Snippet, "«budget»") {
			t.Fatalf("snippet missing «» markers: %q", h.Snippet)
		}
		if strings.Contains(h.Snippet, "<") {
			t.Fatalf("snippet leaked HTML: %q", h.Snippet)
		}
	}

	// scope=all → all three; name hits first; c reported once as "both".
	hits, err = idx.SearchScoped(ctx, "budget", 10, ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("all scope want 3 hits, got %+v", hits)
	}
	if hits[0].Matched == MatchedContent || hits[1].Matched == MatchedContent {
		t.Fatalf("name hits must rank first: %+v", hits)
	}
	hc := find(hits, 3)
	if hc == nil || hc.Matched != MatchedBoth || !strings.Contains(hc.Snippet, "«budget»") {
		t.Fatalf("both-hit shape wrong: %+v", hc)
	}
	ha := find(hits, 1)
	if ha == nil || ha.Matched != MatchedContent {
		t.Fatalf("content-only hit wrong: %+v", ha)
	}

	// Legacy Search() stays name-scoped (manager filter back-compat).
	legacy, err := idx.Search(ctx, "budget", 10)
	if err != nil {
		t.Fatal(err)
	}
	if find(legacy, 1) != nil {
		t.Fatalf("legacy Search must not return content-only hits: %+v", legacy)
	}
}

func TestContentHook_FiresOnDriftOnly(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)

	var fired []int64
	idx.SetContentHook(func(_ context.Context, n *model.Node) { fired = append(fired, n.ID) })

	n := fileNode(1, "notlar.md", "/notlar.md", "etag-a")
	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("hook should fire on first index, fired=%v", fired)
	}

	// Content lands → fingerprint recorded → same-etag reindex is silent.
	if err := idx.IndexNodeContent(ctx, n, "içerik"); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("hook must not re-fire without drift, fired=%v", fired)
	}

	// Etag drift → re-fire.
	n.Etag = "etag-b"
	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 2 {
		t.Fatalf("hook should fire on drift, fired=%v", fired)
	}

	// Directories never fire.
	dir := &model.Node{ID: 9, StorageID: 1, Name: "klasör", Path: "/klasör", Type: model.NodeTypeDirectory}
	if err := idx.IndexNode(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 2 {
		t.Fatalf("hook fired for a directory, fired=%v", fired)
	}
}

type stubLister struct{ nodes []*model.Node }

func (s stubLister) AllNodesForIndex(_ context.Context) ([]*model.Node, error) {
	return s.nodes, nil
}

func TestRebuildAll_ContentVariant(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)

	var fired []int64
	idx.SetContentHook(func(_ context.Context, n *model.Node) { fired = append(fired, n.ID) })

	lister := stubLister{nodes: []*model.Node{
		fileNode(1, "a.txt", "/a.txt", "e1"),
		fileNode(2, "b.txt", "/b.txt", "e2"),
		{ID: 3, StorageID: 1, Name: "dir", Path: "/dir", Type: model.NodeTypeDirectory},
	}}

	// Plain rebuild: metadata only, no content hooks.
	if err := idx.RebuildAll(ctx, lister); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 0 {
		t.Fatalf("plain rebuild must not fire hooks, fired=%v", fired)
	}

	// Content rebuild: hooks fire for the two files (index is fresh, so
	// every eligible file re-enqueues), never for the directory.
	if err := idx.RebuildAllWithContent(ctx, lister); err != nil {
		t.Fatal(err)
	}
	if len(fired) != 2 {
		t.Fatalf("content rebuild want 2 hook fires, got %v", fired)
	}
}

func TestContentFingerprint(t *testing.T) {
	n := fileNode(1, "x", "/x", "etag-1")
	if ContentFingerprint(n) != "etag-1" {
		t.Fatalf("etag must win: %q", ContentFingerprint(n))
	}
	n.Etag = ""
	mt := time.UnixMilli(1234567)
	n.BackendMtime = &mt
	if ContentFingerprint(n) != "100:1234567" {
		t.Fatalf("size:mtime fallback wrong: %q", ContentFingerprint(n))
	}
}

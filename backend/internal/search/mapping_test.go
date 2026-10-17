package search

// The index mapping (task #261, schema v4): every copy of a document nobody
// reads is gone, and every query still finds what it found.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/blevesearch/bleve/v2"
	index "github.com/blevesearch/bleve_index_api"
)

func indexedWithContent(t *testing.T) *Index {
	t.Helper()
	idx := openBatchIndex(t)
	n := contentNode(7)
	n.Name, n.Path = "quarterly_report.txt", "/finance/quarterly_report.txt"
	ctx := context.Background()
	if err := idx.IndexNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := idx.IndexNodeContent(ctx, n, "the harbour budget was approved in october"); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestMapping_StoresOnlyWhatIsReadBack(t *testing.T) {
	idx := indexedWithContent(t)
	d, err := idx.bleve.Document("7")
	if err != nil || d == nil {
		t.Fatalf("document: %v", err)
	}
	// A document read back is rebuilt from what was STORED: every field it
	// carries is one the index kept a copy of.
	var stored []string
	d.VisitFields(func(f index.Field) {
		stored = append(stored, f.Name())
	})
	sort.Strings(stored)
	want := []string{"content", "content_sig", "name", "path"}
	if len(stored) != len(want) {
		t.Fatalf("stored fields %v, want %v", stored, want)
	}
	for i := range want {
		if stored[i] != want[i] {
			t.Fatalf("stored fields %v, want %v", stored, want)
		}
	}
}

// Nothing is in `_all`: a query that names no field finds nothing, and every
// query filex makes names its field.
func TestMapping_NothingGoesIntoAll(t *testing.T) {
	idx := indexedWithContent(t)
	res, err := idx.bleve.Search(bleve.NewSearchRequest(bleve.NewMatchQuery("harbour")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Errorf("an unfielded query matched %d documents through _all", res.Total)
	}
}

// …and every way filex searches still finds it: by name, separator-blind,
// fuzzily, and by content with a highlighted snippet.
func TestMapping_EverySearchStillFinds(t *testing.T) {
	idx := indexedWithContent(t)
	ctx := context.Background()
	for _, q := range []string{"quarterly", "quarterly report", "quartely"} {
		if hits := idx.SafeSearchScoped(ctx, q, 10, ScopeName); len(hits) != 1 {
			t.Errorf("name %q: %d hits, want 1", q, len(hits))
		}
	}
	hits := idx.SafeSearchScoped(ctx, "harbour budget", 10, ScopeContent)
	if len(hits) != 1 {
		t.Fatalf("content: %d hits, want 1", len(hits))
	}
	if hits[0].Snippet == "" {
		t.Error("the content hit has no snippet: term vectors on content are gone")
	}
	st := idx.Stats()
	if st.Files != 1 {
		t.Errorf("file count %d, want 1 (the type field)", st.Files)
	}
}

// An index made by v3's default mapping is stale under v4, so the automatic
// repair rebuilds it into the new mapping once.
func TestMapping_AV3IndexIsStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx.bleve")
	bx, err := bleve.New(dir, bleve.NewIndexMapping())
	if err != nil {
		t.Fatal(err)
	}
	if err := bx.SetInternal([]byte(indexVersionKey), []byte("3")); err != nil {
		t.Fatal(err)
	}
	_ = bx.Close()
	idx, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if !idx.NeedsRebuild() {
		t.Error("a v3 index is not reported as needing a rebuild")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

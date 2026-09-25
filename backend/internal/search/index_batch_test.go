package search

import (
	"context"
	"reflect"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// IndexNodes (issue #70) is IndexNode for a folder's worth of rows in one
// write, on IndexNode's own rules: every document lands, extracted content
// already in the index is carried over, and the content hook fires for the
// files whose fingerprint drifted — and for nothing else.
//
// Break: drop the storedContent line in IndexNodes — the second batch wipes
// rapor.txt's extracted text and fires the hook for it again.
func TestIndexNodes_OneWriteOnIndexNodesRules(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	var fired []int64
	idx.SetContentHook(func(_ context.Context, n *model.Node) { fired = append(fired, n.ID) })

	rapor := fileNode(1, "rapor.txt", "/belgeler/rapor.txt", "etag-1")
	not := fileNode(2, "not.md", "/belgeler/not.md", "etag-a")
	dir := &model.Node{ID: 3, StorageID: 1, Name: "belgeler", Path: "/belgeler", Type: model.NodeTypeDirectory}
	if err := idx.IndexNodes(ctx, []*model.Node{rapor, not, dir}); err != nil {
		t.Fatal(err)
	}
	if got := idx.Stats().DocCount; got != 3 {
		t.Fatalf("three documents, got %d", got)
	}
	if want := []int64{1, 2}; !reflect.DeepEqual(fired, want) {
		t.Fatalf("the hook fires for the two files, not the folder: %v", fired)
	}

	if err := idx.IndexNodeContent(ctx, rapor, "quarterly budget planning"); err != nil {
		t.Fatal(err)
	}
	not.Etag = "etag-b"
	if err := idx.IndexNodes(ctx, []*model.Node{rapor, not}); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2, 2}; !reflect.DeepEqual(fired, want) {
		t.Fatalf("only the drifted file fires again: %v", fired)
	}
	hits, err := idx.SearchScoped(ctx, "budget", 10, ScopeContent)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].NodeID != 1 {
		t.Fatalf("rapor.txt's extracted text survived the batch: %+v", hits)
	}
	if err := idx.IndexNodes(ctx, nil); err != nil {
		t.Fatalf("an empty batch is nothing to do: %v", err)
	}
}

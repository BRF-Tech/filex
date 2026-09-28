package writehook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Observers are named: installing one again under its name REPLACES it (a
// router built twice in one process must not run it twice), another name
// stands beside it, nil removes it — and one that panics does not turn a
// write that already happened into a failure.
func TestObserveWrites_NamedReplaceableAndSafe(t *testing.T) {
	t.Cleanup(func() {
		ObserveWrites("a", nil)
		ObserveWrites("b", nil)
		ObserveWrites("boom", nil)
	})
	var got []string
	ObserveWrites("a", func(_ context.Context, _ int64, n *model.Node, _ string, replaced bool) {
		got = append(got, "a1:"+n.Name)
	})
	ObserveWrites("a", func(_ context.Context, _ int64, n *model.Node, _ string, replaced bool) {
		if replaced {
			got = append(got, "a2:"+n.Name)
		}
	})
	ObserveWrites("boom", func(context.Context, int64, *model.Node, string, bool) { panic("bookkeeping bug") })
	ObserveWrites("b", func(_ context.Context, _ int64, n *model.Node, origin string, _ bool) {
		got = append(got, "b:"+origin)
	})

	EmitWritten(context.Background(), 1, &model.Node{ID: 7, Name: "x.fxe", Path: "/x.fxe", Type: model.NodeTypeFile}, OriginManager, Replaced)
	assert.Equal(t, []string{"a2:x.fxe", "b:manager"}, got, "a replaced, not doubled; the panic skipped; b still ran")

	got = nil
	ObserveWrites("a", nil)
	EmitWritten(context.Background(), 1, &model.Node{ID: 7, Name: "x.fxe", Path: "/x.fxe", Type: model.NodeTypeFile}, OriginDAV, Created)
	assert.Equal(t, []string{"b:dav"}, got)

	got = nil
	EmitWritten(context.Background(), 1, &model.Node{ID: 8, Name: "d", Path: "/d", Type: model.NodeTypeDirectory}, OriginManager, Created)
	assert.Empty(t, got, "a folder is not a file write")
}

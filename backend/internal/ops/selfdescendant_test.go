package ops

import (
	"errors"
	"testing"
)

// A folder must not be copied or moved into itself.
//
// Red proof (measured 2026-09-13 against a live local storage, before this
// guard): POST /api/files/move {"source":["qldemo://agent-desc-test"],
// "target":"qldemo://agent-desc-test/child"} answered 202, and the queue row
// finished `failed` with
// `rename /tmp/filex-qldemo/agent-desc-test /tmp/filex-qldemo/agent-desc-test/child/agent-desc-test: invalid argument`.
// Nothing was lost, but the person who clicked was told nothing until they went
// looking in the operations tray, and what they found there was an errno and a
// server path.
func TestRefuseSelfDescendant(t *testing.T) {
	cases := []struct {
		name        string
		srcStorage  int64
		dstStorage  int64
		sources     []string
		dest        string
		wantRefused bool
	}{
		{
			name: "into its own child", srcStorage: 1, dstStorage: 1,
			sources: []string{"docs"}, dest: "docs/child/", wantRefused: true,
		},
		{
			name: "into a deep descendant", srcStorage: 1, dstStorage: 1,
			sources: []string{"a"}, dest: "a/b/c/d/", wantRefused: true,
		},
		{
			name:       "leading slashes and doubled separators do not smuggle it past",
			srcStorage: 1, dstStorage: 1,
			sources: []string{"/a/"}, dest: "/a//b/", wantRefused: true,
		},
		{
			name: "traversal in the dest is collapsed first", srcStorage: 1, dstStorage: 1,
			sources: []string{"a"}, dest: "a/b/../c/", wantRefused: true,
		},
		{
			name: "one bad source in a batch refuses the batch", srcStorage: 1, dstStorage: 1,
			sources: []string{"ok.txt", "a"}, dest: "a/sub/", wantRefused: true,
		},
		{
			name: "the storage root into any folder", srcStorage: 1, dstStorage: 1,
			sources: []string{""}, dest: "anywhere/", wantRefused: true,
		},
		// --- allowed ---
		{
			name: "an ordinary move to a sibling", srcStorage: 1, dstStorage: 1,
			sources: []string{"a"}, dest: "b/", wantRefused: false,
		},
		{
			name: "a file out of the folder it lives in", srcStorage: 1, dstStorage: 1,
			sources: []string{"a/f.txt"}, dest: "b/", wantRefused: false,
		},
		{
			name:       "a prefix that is not a path boundary is a different folder",
			srcStorage: 1, dstStorage: 1,
			sources: []string{"a"}, dest: "ab/", wantRefused: false,
		},
		{
			name: "into its own PARENT is fine", srcStorage: 1, dstStorage: 1,
			sources: []string{"a/b"}, dest: "a/", wantRefused: false,
		},
		{
			name:       "a rename to the same path is a no-op, not a descendant",
			srcStorage: 1, dstStorage: 1,
			sources: []string{"a"}, dest: "a", wantRefused: false,
		},
		{
			// ⚠ Two storages that happen to use the same folder name are two
			// different trees. Refusing this would break a legitimate paste
			// between depolar, which is a supported operation.
			name: "same name, different storage", srcStorage: 1, dstStorage: 2,
			sources: []string{"a"}, dest: "a/sub/", wantRefused: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := refuseSelfDescendant(c.srcStorage, c.dstStorage, c.sources, c.dest)
			refused := errors.Is(err, ErrIntoOwnDescendant)
			if refused != c.wantRefused {
				t.Fatalf("refused = %v (err %v), want %v", refused, err, c.wantRefused)
			}
		})
	}
}

// The guard has to sit in SubmitTo, not in one of the two handlers: the unified
// /api/files/ops endpoint and the per-verb /api/files/move both funnel here, and
// a check in either one alone leaves the other open.
func TestSubmitToRefusesSelfDescendantBeforeQueueing(t *testing.T) {
	s := &Service{} // no db — the refusal must land before any insert
	for _, kind := range []string{OpMove, OpCopy} {
		_, err := s.SubmitTo(t.Context(), kind, 1, 1, []string{"docs"}, "docs/child/")
		if !errors.Is(err, ErrIntoOwnDescendant) {
			t.Fatalf("%s: err = %v, want ErrIntoOwnDescendant (a nil db would have panicked on insert)", kind, err)
		}
	}
}

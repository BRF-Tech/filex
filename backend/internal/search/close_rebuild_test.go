package search

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
)

// v0.53.0's first full matrix on GitHub: a rebuild that was still running
// when the handlers test closed its index reached swap, which closed the
// already closed live index again, and Bleve panicked ("close of closed
// channel") - on a server, a shutdown during an automatic rebuild. Close now
// forgets the live index, swap refuses a closed one, and the replacement
// closes at most once.
func TestClose_ThenARebuildsSwap_NothingClosesTwice(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "idx.bleve"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	created, err := bleve.New(pendingPath(idx.path), bleve.NewIndexMapping())
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	fresh := &closeOnce{bleveIndex: created}

	if err := idx.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("a second Close must be a no-op, got %v", err)
	}
	if err := idx.swap(fresh); !errors.Is(err, errIndexClosed) {
		t.Fatalf("swap on a closed index: want errIndexClosed, got %v", err)
	}
	if idx.Enabled() {
		t.Fatal("a closed index must not report itself enabled")
	}
	// The rebuild's deferred cleanup closes the replacement; so may swap.
	if err := fresh.Close(); err != nil {
		t.Fatalf("close replacement: %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatalf("a second Close of the replacement must be a no-op, got %v", err)
	}
}

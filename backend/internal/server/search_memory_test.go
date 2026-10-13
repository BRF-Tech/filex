package server

import (
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// The budget is a clamped share of the memory filex was given: an operator who
// asks for more than half gets half, and nothing below one percent.
func TestSetSearchMemoryBudget_ClampsTheShare(t *testing.T) {
	given := sysmem.Allocated()
	if given.Bytes == 0 {
		t.Skip("this platform reports no memory")
	}
	for _, c := range []struct{ asked, used int }{{25, 25}, {0, 25}, {90, 50}, {-5, 1}} {
		idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
		if err != nil {
			t.Fatal(err)
		}
		setSearchMemoryBudget(idx, c.asked)
		limit := idx.MemoryLimit()
		want := given.Bytes / 100 * uint64(c.used)
		// The limit is the baseline (what the process holds now) plus the
		// budget: it lies above the budget by less than the process itself.
		if limit < want || limit > want+sysmem.InUse()+(64<<20) {
			t.Errorf("asked %d%%: limit %d MiB, want %d MiB plus the baseline", c.asked, limit>>20, want>>20)
		}
		_ = idx.Close()
	}
}

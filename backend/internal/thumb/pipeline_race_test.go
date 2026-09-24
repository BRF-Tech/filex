package thumb

import (
	"context"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The storage resolver attaches drivers from every queue worker goroutine
// while others generate thumbnails. Unguarded, this crashed a full e2e run
// on 2026-09-21: "fatal error: concurrent map writes" at AttachStorage.
// Run with -race to see the unguarded version reported; without -race the
// runtime's own map check usually kills the process within these iterations.
func TestPipeline_AttachStorageIsSafeUnderConcurrency(t *testing.T) {
	p := New(nil, t.TempDir(), Capabilities{})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				p.AttachStorage(int64(g*10000+i), nil)
				_ = p.GenerateThumb(context.Background(), &model.Node{Type: model.NodeTypeFile, StorageID: -1 - int64(i)}) // never attached: returns before any I/O
			}
		}(g)
	}
	wg.Wait()
}

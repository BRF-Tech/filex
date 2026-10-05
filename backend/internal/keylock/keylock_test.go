package keylock

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLock_OneHolderPerKeyManyKeysAtOnce(t *testing.T) {
	var m Map
	var inA, maxA atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := m.Lock("a")
			n := inA.Add(1)
			for {
				old := maxA.Load()
				if n <= old || maxA.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inA.Add(-1)
			unlock()
		}()
	}
	// Another key is not held up by "a".
	done := make(chan struct{})
	go func() {
		unlock := m.Lock("b")
		unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("key b waited for key a")
	}
	wg.Wait()
	if maxA.Load() != 1 {
		t.Fatalf("%d holders of one key at once", maxA.Load())
	}
	if m.Len() != 0 {
		t.Fatalf("%d locks kept after every holder left", m.Len())
	}
}

func TestLock_UnlockTwiceIsOnce(t *testing.T) {
	var m Map
	unlock := m.Lock("a")
	unlock()
	unlock()
	u2 := m.Lock("a")
	u2()
	if m.Len() != 0 {
		t.Fatalf("%d locks kept", m.Len())
	}
}

package sync

import (
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/realtime"
)

// frames records what the lazy catalogue announces.
type frames struct {
	mu  gosync.Mutex
	got []string
}

func (f *frames) EmitChange(_ int64, dir string, ev realtime.ChangeEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kind := "real"
	if ev.Derived {
		kind = "derived"
	}
	f.got = append(f.got, kind+":"+dir)
}

func (f *frames) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

// A desktop sync pair mirrors every folder under its root, opened or not. In
// behaviour B nothing else ever lists a folder nobody opens, so while a pair
// is connected its subtree is walked again every refresh interval: a file
// added outside filex deep inside it reaches the catalogue and is announced as
// a real change (the desktop's tree_change and action=changes). Folders
// outside the pair are left alone.
//
// Break: make refreshPairs return before walking — the file never arrives.
func TestLazyPair_TheMirroredSubtreeIsKeptCurrent(t *testing.T) {
	l := newLazyLab(t, map[string]any{"lazy_fill": "on_open"})
	fr := &frames{}
	l.lc.emit = func() ChangeEmitter { return fr }
	l.write(t, "cift/alt/a.txt", "a")
	l.write(t, "baska/b.txt", "b")
	var roots []string
	l.lc.s.pairs = func(int64) []string { return roots }
	idle := func() bool {
		l.lc.mu.Lock()
		defer l.lc.mu.Unlock()
		return len(l.lc.pairs) == 0
	}

	roots = []string{"/cift"}
	l.lc.refreshPairs(l.ctx)
	require.Eventually(t, func() bool { return l.row("/cift/alt/a.txt") != nil && idle() }, 10*time.Second, 10*time.Millisecond,
		"the pair's subtree is catalogued")
	assert.Nil(t, l.row("/baska/b.txt"), "nothing outside the pair")

	// Changed outside filex; nobody opens the folder.
	l.write(t, "cift/alt/yeni.txt", "yeni")
	l.lc.cfg.refreshAge = time.Nanosecond
	l.lc.refreshPairs(l.ctx)
	require.Eventually(t, func() bool { return l.row("/cift/alt/yeni.txt") != nil && idle() }, 10*time.Second, 10*time.Millisecond,
		"the next refresh finds it")
	assert.Contains(t, fr.list(), "real:cift/alt", "and announces it as a change")

	// The pair disconnected: nothing is walked any more.
	roots = nil
	l.write(t, "cift/alt/sonra.txt", "x")
	l.lc.refreshPairs(l.ctx)
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, l.row("/cift/alt/sonra.txt"))
}

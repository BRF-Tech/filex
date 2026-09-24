package ops

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A delete job's progress row is written at most about once a second. It used
// to be written after every single item — tens of thousands of UPDATEs for a
// large job, on the one connection SQLite has, for a tray that polls every
// two seconds anyway.
func TestProgressGate_AtMostOncePerInterval(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	cur := t0
	g := &progressGate{every: time.Second, now: func() time.Time { return cur }}
	var at []time.Duration
	for _, dt := range []time.Duration{
		0, 100 * time.Millisecond, 999 * time.Millisecond,
		time.Second, 1500 * time.Millisecond,
		2100 * time.Millisecond, 2200 * time.Millisecond,
	} {
		cur = t0.Add(dt)
		g.maybe(func() { at = append(at, dt) })
	}
	assert.Equal(t, []time.Duration{0, time.Second, 2100 * time.Millisecond}, at,
		"the first item is reported at once, then no more than once a second")
}

// Sources nested inside another source of the same delete are dropped and
// counted with the source that takes them along.
func TestTopMostSources(t *testing.T) {
	cases := []struct {
		name   string
		in     []string
		top    []string
		counts []int
	}{
		{"unrelated", []string{"a.txt", "b.txt"}, []string{"a.txt", "b.txt"}, []int{0, 0}},
		{"child listed before its folder", []string{"docs/a.txt", "docs", "x"}, []string{"docs", "x"}, []int{1, 0}},
		{"deep descendants", []string{"docs", "docs/sub/b.txt", "docs/sub"}, []string{"docs"}, []int{2}},
		{"spelling does not matter", []string{"/docs/", "docs/a.txt", "docs//b.txt"}, []string{"/docs/"}, []int{2}},
		{"a duplicate is the same source", []string{"a.txt", "/a.txt"}, []string{"a.txt"}, []int{1}},
		// A sibling whose name merely STARTS with the folder's is not inside it.
		{"prefix is not an ancestor", []string{"docs", "docs2/a.txt", "docs.txt"}, []string{"docs", "docs2/a.txt", "docs.txt"}, []int{0, 0, 0}},
		// The storage root is never treated as a folder that takes everything
		// along: it cannot be trashed, and the other items must still be tried.
		{"root is not an ancestor", []string{"", "a.txt"}, []string{"", "a.txt"}, []int{0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			top, counts := topMostSources(c.in)
			assert.Equal(t, c.top, top)
			assert.Equal(t, c.counts, counts)
		})
	}
}

package mountfs

import (
	"strconv"
	"strings"
	"sync"
)

// The read cache.
//
// ⚠ It is the difference between a usable mount and an unusable one. A program
// reading a video asks for tens of kilobytes at a time; without a cache that is
// one HTTPS request per read, and over a link with 40 ms of latency the file
// plays at a few hundred kilobytes a second. Blocks turn a sequential read into
// a handful of large requests and a seek into exactly one.
//
// ⚠⚠ It is keyed by (file, block) and dropped wholesale when the file changes
// through this mount. What it CANNOT see is somebody else changing the file on
// the server: a block held here is stale until it is evicted, which is the
// trade every network filesystem makes and the reason AttrTTL exists next to
// it. A mount is not a substitute for the app when two people edit the same
// file at once.

type blockCache struct {
	mu     sync.Mutex
	max    int
	blocks map[string][]byte
	// order is the insertion order, for eviction. A slice rather than a proper
	// LRU because the access pattern is overwhelmingly sequential: the block
	// that has been in the cache longest is nearly always the right one to drop.
	order []string
}

func newBlockCache(max int) *blockCache {
	if max < 4 {
		max = 4
	}
	return &blockCache{max: max, blocks: map[string][]byte{}}
}

func blockKey(remote string, idx int64) string {
	return remote + "\x00" + strconv.FormatInt(idx, 10)
}

func (c *blockCache) get(remote string, idx int64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.blocks[blockKey(remote, idx)]
	return b, ok
}

func (c *blockCache) put(remote string, idx int64, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := blockKey(remote, idx)
	if _, exists := c.blocks[k]; exists {
		return
	}
	c.blocks[k] = data
	c.order = append(c.order, k)
	for len(c.order) > c.max {
		delete(c.blocks, c.order[0])
		c.order = c.order[1:]
	}
}

// dropFile forgets every block of one file.
//
// ⚠ Called after ANY change to that file through this mount — a write, a
// delete, a rename. Skipping it is how a file reads back as its previous
// contents right after being written, which looks like the write was lost.
func (c *blockCache) dropFile(remote string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := remote + "\x00"
	kept := c.order[:0]
	for _, k := range c.order {
		if strings.HasPrefix(k, prefix) {
			delete(c.blocks, k)
			continue
		}
		kept = append(kept, k)
	}
	c.order = kept
}

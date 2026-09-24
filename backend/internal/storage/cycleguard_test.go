package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// A REAL symlink loop on disk, walked through the real local driver.
//
// ⚠ The shape matters: `real/cycle -> <root>` is the loop the investigation
// measured, and it reached depth 81 / 123 visits on Linux and depth 127 / 192
// on Windows before the operating system's own link limit ended it. Nothing in
// filex stopped it — the walk was finite only because the driver used to call
// every symlink KindSymlink and the walkers descend on KindDirectory.
func cyclicRoot(t *testing.T) (*local.Driver, string) {
	t.Helper()
	root := t.TempDir()
	inner := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(inner, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "f.txt"), []byte("x"), 0o644))
	if err := os.Symlink(root, filepath.Join(inner, "cycle")); err != nil {
		t.Skipf("this host cannot create symlinks: %v", err)
	}
	d := &local.Driver{}
	require.NoError(t, d.Init(context.Background(), map[string]any{"path": root}))
	return d, root
}

// countingDriver records every List so a runaway walk is visible as a number
// rather than as a hang.
type countingDriver struct {
	storage.Driver
	lists int
	maxAt int
}

func (c *countingDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	c.lists++
	if n := len(splitSegments(p)); n > c.maxAt {
		c.maxAt = n
	}
	if c.lists > 5000 {
		// A real runaway would be stopped by the OS eventually; this keeps a
		// FAILING test from taking the whole suite down with it.
		return nil, context.Canceled
	}
	return c.Driver.List(ctx, p)
}

func splitSegments(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// The guard, exercised through the collision scanner — one of the three
// walkers that had no cycle protection at all.
func TestScanKindCollisions_TerminatesOnASymlinkLoop(t *testing.T) {
	d, _ := cyclicRoot(t)
	cd := &countingDriver{Driver: d}

	_, err := storage.ScanKindCollisions(context.Background(), cd, "/")
	require.NoError(t, err, "the walk did not terminate on its own")

	// ⚠ The number is the assertion. Without a guard this reached 123 visits
	// on Linux and 192 on Windows; a handful is what a two-directory storage
	// costs, plus the one extra pass the guard pays before it recognises the
	// loop (see CycleGuard: a link is recorded on descent, not up front).
	assert.Less(t, cd.lists, 20,
		"the collision scanner walked the loop %d times — the cycle guard is not engaging", cd.lists)
	assert.Less(t, cd.maxAt, storage.MaxWalkDepth,
		"the walk went deeper than the backstop allows")
}

// The guard must not refuse an ordinary directory, and must not refuse two
// DIFFERENT links that happen to sit side by side.
func TestCycleGuard_LetsOrdinaryTreesThrough(t *testing.T) {
	g := storage.NewCycleGuard()

	plain := storage.Object{Path: "/a", Kind: storage.KindDirectory}
	assert.True(t, g.Enter(plain, 1))
	assert.True(t, g.Enter(plain, 2), "a directory with no resolved target is never refused")

	one := storage.Object{Path: "/one", Kind: storage.KindDirectory,
		Metadata: map[string]string{storage.MetaLinkTarget: "/srv/data/x"}}
	two := storage.Object{Path: "/two", Kind: storage.KindDirectory,
		Metadata: map[string]string{storage.MetaLinkTarget: "/srv/data/y"}}
	assert.True(t, g.Enter(one, 1))
	assert.True(t, g.Enter(two, 1), "two links to different places are both walkable")

	// The same target under a second name is the loop.
	again := storage.Object{Path: "/deep/one-again", Kind: storage.KindDirectory,
		Metadata: map[string]string{storage.MetaLinkTarget: "/srv/data/x"}}
	assert.False(t, g.Enter(again, 2),
		"a link resolving to a directory the walk is already inside must be refused")
}

func TestCycleGuard_DepthBackstopStopsADriverThatReportsNoTarget(t *testing.T) {
	g := storage.NewCycleGuard()
	// No metadata at all — a plugin backend, or a driver that resolves links
	// without saying so. Only the depth cap can stop this one.
	o := storage.Object{Path: "/x", Kind: storage.KindDirectory}
	assert.True(t, g.Enter(o, storage.MaxWalkDepth-1))
	assert.False(t, g.Enter(o, storage.MaxWalkDepth))
	assert.False(t, g.Enter(o, storage.MaxWalkDepth+50))
}

// The local driver has to actually REPORT the resolved target, or the guard
// above has nothing to key on and every walker silently loses its protection.
func TestLocalDriver_ReportsAResolvedTargetForAFollowedDirectoryLink(t *testing.T) {
	d, root := cyclicRoot(t)
	objs, err := d.List(context.Background(), "real")
	require.NoError(t, err)

	var link storage.Object
	for _, o := range objs {
		if o.Name == "cycle" {
			link = o
		}
	}
	require.Equal(t, storage.KindDirectory, link.Kind,
		"an in-root directory link must be navigable — this is issue #34")
	require.Equal(t, storage.LinkFollowed, link.Metadata[storage.MetaLinkState])

	target := link.Metadata[storage.MetaLinkTarget]
	require.NotEmpty(t, target, "no resolved target: every walker's cycle guard is now blind")
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, realRoot, target, "the reported target is not the directory the link leads to")
}

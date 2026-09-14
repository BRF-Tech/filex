package trash

// TakeBack (and putChildren) strip the source prefix off every path the walk
// reports. The walk reports storage-relative paths with NO leading slash, and
// Restore hands TakeBack `nodes.path`, which HAS one — so the strip was a
// no-op and a folder restored on an object store landed under
// `<original>/.filex-trash/<key>/…` while the restore reported success. And a
// walk that found objects and moved none of them reported nil, so the row was
// un-trashed over a path holding nothing.
//
// Found by @alfatm in his fork (32e8935e); the tests are his, ported.

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ──────────────────── production path spelling (leading slash) ────────────────────

// The production callers spell paths the way `nodes.path` stores them — with a
// leading slash — while the walk reports what it found without one, and the
// real drivers strip it before the backend ever sees it (s3.Driver.key). memCore
// keeps keys verbatim, so trimming here is what makes the two spellings
// comparable and keeps these tests about WHERE in the tree the bytes landed.
func (c *memCore) keysNormalized() []string {
	out := c.keys()
	for i, k := range out {
		out[i] = strings.TrimLeft(k, "/")
	}
	sort.Strings(out)
	return out
}

// snapshot is the whole store as normalized key → bytes, for byte-for-byte
// comparison across a round trip.
func (c *memCore) snapshot() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.files))
	for k, b := range c.files {
		out[strings.TrimLeft(k, "/")] = string(b)
	}
	return out
}

// Restore calls TakeBack with `n.Path` and the resolved destination, both
// carrying the leading slash `nodes.path` stores. On an object store the
// one-shot rename 404s (nothing sits at a bare prefix), so the per-object walk
// IS the normal path — and it has to strip the trash key from each object's
// path, not append the whole key to the destination.
func TestTakeBackWalksFolderWithProductionSpelling(t *testing.T) {
	key := Prefix + "/1700000000-abc123__proje"
	core := newCore(key+"/notlar.txt", key+"/alt/derin.txt")

	require.NoError(t, TakeBack(context.Background(), moverDrv{core}, "/"+key, "/proje"))

	require.Equal(t, []string{"proje/alt/derin.txt", "proje/notlar.txt"}, core.keysNormalized(),
		"the tree must land under the destination, not under destination/<trash key>")
}

// The same mismatch on the way IN: a folder trashed with the production
// spelling must put its contents directly under the trash key, not one level
// deeper under a copy of its own path.
func TestPutFolderWithProductionSpellingLandsDirectlyUnderTheKey(t *testing.T) {
	core := newCore("proje/notlar.txt", "proje/alt/derin.txt")

	out, err := Put(context.Background(), moverDrv{core}, "/proje")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.Equal(t, 2, out.Files)

	require.Equal(t, []string{out.Key + "/alt/derin.txt", out.Key + "/notlar.txt"}, core.keysNormalized())
}

// Put then TakeBack, both with the production spelling, on the object-store
// shape where neither one-shot rename can work: the tree has to come back
// exactly as it left, bytes included.
func TestPutThenTakeBackRoundTripsFolderOnObjectStore(t *testing.T) {
	ctx := context.Background()
	core := newCore("proje/notlar.txt", "proje/alt/derin.txt")
	before := core.snapshot()

	out, err := Put(ctx, moverDrv{core}, "/proje")
	require.NoError(t, err)
	require.True(t, out.Trashed)

	require.NoError(t, TakeBack(ctx, moverDrv{core}, "/"+out.Key, "/proje"))
	require.Equal(t, before, core.snapshot(), "round trip must restore the tree byte for byte")
}

// ──────────────────── a restore that moved nothing ────────────────────

// vanishedMoverDrv is an object store whose listing still reports objects the
// backend no longer has: an out-of-band delete, a bucket lifecycle rule, a
// trash key emptied by something other than filex. Listed paths in `gone` 404
// on Move; a nil `gone` means every object has vanished.
type vanishedMoverDrv struct {
	*memCore
	gone map[string]bool
}

func (d vanishedMoverDrv) Move(_ context.Context, s, t string) error {
	if d.gone == nil || d.gone[strings.TrimLeft(s, "/")] {
		return storage.ErrNotFound
	}
	return d.move(s, t)
}
func (d vanishedMoverDrv) Delete(_ context.Context, p string) error { return d.del(p) }

// The lie: the walk finds objects, every single Move 404s, and TakeBack
// reports success. The caller then un-trashes the row and answers 200, so the
// listing grows a folder with nothing behind it.
func TestTakeBackFailsWhenEveryListedObjectHasVanished(t *testing.T) {
	key := Prefix + "/1700000000-abc123__proje"
	core := newCore(key+"/notlar.txt", key+"/alt/derin.txt")

	err := TakeBack(context.Background(), vanishedMoverDrv{memCore: core}, "/"+key, "/proje")

	require.Error(t, err, "a restore that moved zero bytes must not report success")
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.Equal(t, []string{key + "/alt/derin.txt", key + "/notlar.txt"}, core.keysNormalized(),
		"a failed restore must not delete the trash key it could not empty")
}

// A PARTIALLY missing tree is legitimate: one object went out of band, the
// rest are real and must come back. Only "found files, moved none" is a lie.
func TestTakeBackRestoresTheSurvivorsOfAPartialTree(t *testing.T) {
	key := Prefix + "/1700000000-abc123__proje"
	core := newCore(key+"/notlar.txt", key+"/alt/derin.txt")
	drv := vanishedMoverDrv{memCore: core, gone: map[string]bool{key + "/alt/derin.txt": true}}

	require.NoError(t, TakeBack(context.Background(), drv, "/"+key, "/proje"))
	// The phantom stays listable in the fake (that is what makes it a phantom),
	// so the assertion is about the survivor: it landed at the destination and
	// the partial tree did not turn the whole restore into an error.
	require.Equal(t, []string{key + "/alt/derin.txt", "proje/notlar.txt"}, core.keysNormalized(),
		"what survived comes back; what was already gone cannot")
}

// The single-object path is already honest and must stay that way: nothing at
// the key at all, and nothing under it, surfaces the original 404 rather than
// a silent success.
func TestTakeBackSurfacesTheErrorWhenTheTrashKeyIsEmpty(t *testing.T) {
	core := newCore("baska.txt")

	err := TakeBack(context.Background(), moverDrv{core}, "/"+Prefix+"/1700000000-abc123__yok", "/yok")

	require.ErrorIs(t, err, storage.ErrNotFound)
	require.Equal(t, []string{"baska.txt"}, core.keysNormalized())
}

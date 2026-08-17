package trash

// Unit tests for the shared "put these bytes in the trash" helper.
//
// The invariant every delete surface leans on: Put reports Trashed ONLY when
// the bytes are preserved and restorable. Callers derive their writehook from
// that flag, so a surface cannot announce file.trashed for data it actually
// destroyed — which is exactly what the AI folder path used to do on a driver
// that could delete but not move.

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ───────────────────────── in-memory object store ─────────────────────────

// memCore is a flat key→bytes store, the shape of an object store: there are
// no directory objects, only keys that happen to share a prefix.
type memCore struct {
	mu    sync.Mutex
	files map[string][]byte
}

func newCore(keys ...string) *memCore {
	c := &memCore{files: map[string][]byte{}}
	for _, k := range keys {
		c.files[k] = []byte("içerik:" + k)
	}
	return c
}

func (c *memCore) Init(context.Context, map[string]any) error { return nil }
func (c *memCore) Name() string                               { return "mem" }
func (c *memCore) Capabilities() storage.Capabilities         { return storage.Capabilities{} }

func (c *memCore) Read(_ context.Context, p string) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.files[p]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

func (c *memCore) Stat(_ context.Context, p string) (storage.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.files[p]; ok {
		return storage.Object{Path: p, Name: p, Size: int64(len(b)), Kind: storage.KindFile}, nil
	}
	for k := range c.files {
		if strings.HasPrefix(k, strings.TrimRight(p, "/")+"/") {
			return storage.Object{Path: p, Name: p, Kind: storage.KindDirectory}, nil
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

// List synthesises directory entries from shared key prefixes.
func (c *memCore) List(_ context.Context, dir string) ([]storage.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := strings.Trim(dir, "/")
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]storage.Object{}
	for k, b := range c.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if rest == "" {
			continue
		}
		if i := strings.Index(rest, "/"); i >= 0 {
			name := rest[:i]
			seen[name] = storage.Object{Path: prefix + name, Name: name, Kind: storage.KindDirectory}
			continue
		}
		seen[rest] = storage.Object{Path: k, Name: rest, Size: int64(len(b)), Kind: storage.KindFile}
	}
	out := make([]storage.Object, 0, len(seen))
	for _, o := range seen {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (c *memCore) keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.files))
	for k := range c.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *memCore) move(src, dst string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.files[src]
	if !ok {
		return storage.ErrNotFound // no object at a bare prefix — S3 behaviour
	}
	delete(c.files, src)
	c.files[dst] = b
	return nil
}

func (c *memCore) copy(src, dst string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.files[src]
	if !ok {
		return storage.ErrNotFound
	}
	c.files[dst] = append([]byte(nil), b...)
	return nil
}

func (c *memCore) del(p string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.files[p]; !ok {
		return storage.ErrNotFound
	}
	delete(c.files, p)
	return nil
}

// Capability-selective wrappers. Which optional interfaces a driver satisfies
// is the whole point of these tests, so each variant exposes exactly one set.
type moverDrv struct{ *memCore }

func (d moverDrv) Move(_ context.Context, s, t string) error { return d.move(s, t) }
func (d moverDrv) Delete(_ context.Context, p string) error  { return d.del(p) }

type copyOnlyDrv struct{ *memCore }

func (d copyOnlyDrv) Copy(_ context.Context, s, t string) error { return d.copy(s, t) }
func (d copyOnlyDrv) Delete(_ context.Context, p string) error  { return d.del(p) }

type deleteOnlyDrv struct{ *memCore }

func (d deleteOnlyDrv) Delete(_ context.Context, p string) error { return d.del(p) }

// ────────────────────────────────── tests ─────────────────────────────────

func TestPutMovesFileIntoTrash(t *testing.T) {
	core := newCore("rapor.txt")
	out, err := Put(context.Background(), moverDrv{core}, "rapor.txt")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.True(t, strings.HasPrefix(out.Key, Prefix+"/"))
	require.Contains(t, out.Key, "__rapor.txt")
	require.Equal(t, []string{out.Key}, core.keys(), "the bytes moved, they were not copied or dropped")
}

// An object store has no object at a folder prefix, so the single Move fails
// with 404 while the contents are perfectly real. Put must fall back to moving
// each object and keep the sub-structure, or a restore would flatten the tree.
func TestPutWalksFolderWhenPrefixHasNoObject(t *testing.T) {
	core := newCore("proje/bir.txt", "proje/alt/iki.txt")
	out, err := Put(context.Background(), moverDrv{core}, "proje")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.Equal(t, 2, out.Files)
	require.Equal(t, []string{out.Key + "/alt/iki.txt", out.Key + "/bir.txt"}, core.keys())
}

// The decisive case. A driver that can delete but not move/copy cannot
// preserve anything — Put must refuse and leave every byte in place, so the
// caller fires file.deleted rather than a file.trashed nobody can act on.
func TestPutRefusesWhenBytesCannotBePreserved(t *testing.T) {
	core := newCore("proje/bir.txt", "proje/alt/iki.txt")
	out, err := Put(context.Background(), deleteOnlyDrv{core}, "proje")
	require.ErrorIs(t, err, ErrUnsupported)
	require.False(t, out.Trashed, "must never claim a trash it did not perform")
	require.Equal(t, []string{"proje/alt/iki.txt", "proje/bir.txt"}, core.keys(),
		"a refusal must not destroy anything")
}

// Without Move but with Copy the bytes CAN survive, so they must.
func TestPutFallsBackToCopyThenDelete(t *testing.T) {
	core := newCore("rapor.txt")
	out, err := Put(context.Background(), copyOnlyDrv{core}, "rapor.txt")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.Equal(t, []string{out.Key}, core.keys(), "source removed only after the copy landed")
}

// A copy that fails must leave the original alone — losing the source to a
// half-finished trash operation would be the worst possible outcome.
func TestPutKeepsSourceWhenCopyFails(t *testing.T) {
	core := newCore("rapor.txt")
	_, err := Put(context.Background(), failingCopyDrv{core}, "rapor.txt")
	require.Error(t, err)
	require.Equal(t, []string{"rapor.txt"}, core.keys())
}

type failingCopyDrv struct{ *memCore }

func (d failingCopyDrv) Copy(context.Context, string, string) error {
	return errors.New("backend refused the copy")
}
func (d failingCopyDrv) Delete(_ context.Context, p string) error { return d.del(p) }

// Nothing at the path is not an error: a stale index row or an out-of-band
// delete reports Missing so the caller can drop the row without failing a
// whole batch.
func TestPutReportsMissingRatherThanFailing(t *testing.T) {
	core := newCore("baska.txt")
	out, err := Put(context.Background(), moverDrv{core}, "yok.txt")
	require.NoError(t, err)
	require.True(t, out.Missing)
	require.False(t, out.Trashed)
	require.Equal(t, []string{"baska.txt"}, core.keys())
}

// Trashing something already in the trash would bury the original path and
// leave Restore pointing inside the trash.
func TestPutRefusesPathsAlreadyInTrash(t *testing.T) {
	core := newCore(Prefix + "/123-abc__rapor.txt")
	_, err := Put(context.Background(), moverDrv{core}, Prefix+"/123-abc__rapor.txt")
	require.Error(t, err)
	require.Len(t, core.keys(), 1)
}

// A folder walk must never drag the trash itself into a new trash key.
func TestPutSkipsTheTrashBucketWhileWalking(t *testing.T) {
	core := newCore("proje/bir.txt", Prefix+"/999-zz__eski.txt")
	out, err := Put(context.Background(), moverDrv{core}, "proje")
	require.NoError(t, err)
	require.Equal(t, 1, out.Files)
	require.Contains(t, core.keys(), Prefix+"/999-zz__eski.txt", "pre-existing trash left untouched")
}

// TakeBack is Put's inverse and must work on the same drivers Put worked on —
// otherwise trashing is a one-way trip for a backend without Move.
func TestTakeBackRestoresFileAndFolder(t *testing.T) {
	ctx := context.Background()

	t.Run("file via copy-only driver", func(t *testing.T) {
		core := newCore("rapor.txt")
		out, err := Put(ctx, copyOnlyDrv{core}, "rapor.txt")
		require.NoError(t, err)
		require.NoError(t, TakeBack(ctx, copyOnlyDrv{core}, out.Key, "rapor.txt"))
		require.Equal(t, []string{"rapor.txt"}, core.keys())
	})

	t.Run("folder rebuilt with its tree", func(t *testing.T) {
		core := newCore("proje/bir.txt", "proje/alt/iki.txt")
		out, err := Put(ctx, moverDrv{core}, "proje")
		require.NoError(t, err)
		require.NoError(t, TakeBack(ctx, moverDrv{core}, out.Key, "proje"))
		require.Equal(t, []string{"proje/alt/iki.txt", "proje/bir.txt"}, core.keys())
	})
}

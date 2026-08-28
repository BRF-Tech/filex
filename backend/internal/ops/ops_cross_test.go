package ops_test

// Cross-storage copy/move — the "copy in one depo, paste into the next" path.
//
// What these lock down, in order of how badly they bit:
//
//  1. The bytes land in the DESTINATION storage. Before this existed the queue
//     had one storage id, so the destination's prefix was stripped and the
//     relative path was applied to the SOURCE storage: the paste answered "ok"
//     and wrote the file into a folder it invented inside the depo the user was
//     copying from.
//  2. A whole tree travels, empty folders included.
//  3. A move deletes the source only after the copy is verified — and NOT when
//     the destination driver lied about what it stored.
//  4. The file's own mtime survives the trip when the target can hold one.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type crossFixture struct {
	svc          *ops.Service
	store        db.Store
	drvA, drvB   storage.Driver
	stA, stB     *model.Storage
	rootA, rootB string
}

// newCrossFixture wires two local storages, A and B, into one ops service.
// `wrapB` may replace B's driver (used to fake a backend that accepts a write
// and stores something else).
func newCrossFixture(t *testing.T, wrapB func(storage.Driver) storage.Driver) *crossFixture {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()

	rawA, rawB := &local.Driver{}, &local.Driver{}
	require.NoError(t, rawA.Init(ctx, map[string]any{"root": rootA}))
	require.NoError(t, rawB.Init(ctx, map[string]any{"root": rootB}))

	var drvA storage.Driver = rawA
	var drvB storage.Driver = rawB
	if wrapB != nil {
		drvB = wrapB(rawB)
	}

	mk := func(name, root string) *model.Storage {
		cfg := strings.ReplaceAll(strings.ReplaceAll(root, `\`, `\\`), `"`, `\"`)
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
			ConfigJSON: json.RawMessage(`{"root":"` + cfg + `"}`),
		})
		require.NoError(t, err)
		return st
	}
	stA, stB := mk("alpha", rootA), mk("beta", rootB)

	resolver := func(id int64) (storage.Driver, error) {
		switch id {
		case stA.ID:
			return drvA, nil
		case stB.ID:
			return drvB, nil
		}
		return nil, fmt.Errorf("unknown id %d", id)
	}
	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(handlers.NewManager(store, resolver))

	return &crossFixture{svc: svc, store: store, drvA: drvA, drvB: drvB, stA: stA, stB: stB, rootA: rootA, rootB: rootB}
}

// run submits a cross-storage op (alpha → beta) and waits for it to finish.
func (f *crossFixture) run(t *testing.T, kind string, sources []string, dest string) *ops.Op {
	t.Helper()
	ctx := context.Background()
	op, err := f.svc.SubmitTo(ctx, kind, f.stA.ID, f.stB.ID, sources, dest)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, err := f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		switch cur.Status {
		case ops.StatusOK, ops.StatusFailed, ops.StatusPartial:
			return cur
		}
		if time.Now().After(deadline) {
			t.Fatalf("op %d (%s) never finished; status=%s err=%s", op.ID, kind, cur.Status, cur.Error)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func writeA(t *testing.T, f *crossFixture, rel, body string) {
	t.Helper()
	abs := filepath.Join(f.rootA, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

func readB(t *testing.T, f *crossFixture, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.rootB, filepath.FromSlash(rel)))
	require.NoError(t, err, "expected %s in the DESTINATION storage", rel)
	return string(b)
}

// ---------- copy ----------

func TestCross_CopyFile_LandsInDestinationStorage(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "gezegen.txt", "merhaba")
	require.NoError(t, os.MkdirAll(filepath.Join(f.rootB, "hedef"), 0o755))

	op := f.run(t, ops.OpCopy, []string{"gezegen.txt"}, "hedef/")
	require.Equal(t, ops.StatusOK, op.Status, "copy failed: %s", op.Error)

	require.Equal(t, "merhaba", readB(t, f, "hedef/gezegen.txt"))
	// The source is untouched…
	src, err := os.ReadFile(filepath.Join(f.rootA, "gezegen.txt"))
	require.NoError(t, err)
	require.Equal(t, "merhaba", string(src))
	// …and nothing was invented inside the SOURCE storage, which is exactly
	// what the old single-storage queue did.
	_, err = os.Stat(filepath.Join(f.rootA, "hedef"))
	require.True(t, os.IsNotExist(err), "the source storage must not grow the destination's folder")
}

func TestCross_CopyFile_IsVisibleInTheDestinationListing(t *testing.T) {
	ctx := context.Background()
	f := newCrossFixture(t, nil)
	writeA(t, f, "rapor.txt", "veri")

	op := f.run(t, ops.OpCopy, []string{"rapor.txt"}, "/")
	require.Equal(t, ops.StatusOK, op.Status, "copy failed: %s", op.Error)

	// The DB mirror is what directory listings read. A copy nobody mirrors is
	// a file the user cannot see until some later scan finds it.
	node, err := f.store.GetNodeByPath(ctx, f.stB.ID, crossHash(f.stB.ID, "rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, node, "the pasted file must exist in the DESTINATION storage's node index")
	require.Equal(t, f.stB.ID, node.StorageID)
	require.EqualValues(t, len("veri"), node.Size)

	// And it must NOT have been mirrored into the source storage.
	ghost, _ := f.store.GetNodeByPath(ctx, f.stA.ID, crossHash(f.stA.ID, "rapor.txt"))
	require.Nil(t, ghost, "no node for the copy may appear under the source storage")
}

func TestCross_CopyTree_TakesTheWholeSubtree(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "proje/README.md", "# proje")
	writeA(t, f, "proje/src/main.go", "package main")
	require.NoError(t, os.MkdirAll(filepath.Join(f.rootA, "proje", "bos"), 0o755))

	op := f.run(t, ops.OpCopy, []string{"proje"}, "/")
	require.Equal(t, ops.StatusOK, op.Status, "copy failed: %s", op.Error)

	require.Equal(t, "# proje", readB(t, f, "proje/README.md"))
	require.Equal(t, "package main", readB(t, f, "proje/src/main.go"))
	// An empty folder the user made is part of what they pasted.
	fi, err := os.Stat(filepath.Join(f.rootB, "proje", "bos"))
	require.NoError(t, err, "empty subfolder must survive the trip")
	require.True(t, fi.IsDir())
}

func TestCross_Copy_KeepsTheSourceMtime(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "eski.txt", "gecmis")
	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	require.NoError(t, os.Chtimes(filepath.Join(f.rootA, "eski.txt"), want, want))

	op := f.run(t, ops.OpCopy, []string{"eski.txt"}, "/")
	require.Equal(t, ops.StatusOK, op.Status, "copy failed: %s", op.Error)

	fi, err := os.Stat(filepath.Join(f.rootB, "eski.txt"))
	require.NoError(t, err)
	require.WithinDuration(t, want, fi.ModTime().UTC(), 2*time.Second,
		"a pasted file stamped 'now' reads as changed to every later sync run")
}

func TestCross_Copy_DoesNotOverwriteAnExistingName(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "notlar.txt", "yeni")
	require.NoError(t, os.WriteFile(filepath.Join(f.rootB, "notlar.txt"), []byte("eski"), 0o644))

	op := f.run(t, ops.OpCopy, []string{"notlar.txt"}, "/")
	require.Equal(t, ops.StatusOK, op.Status, "copy failed: %s", op.Error)

	require.Equal(t, "eski", readB(t, f, "notlar.txt"), "the file already there must survive")
	require.Equal(t, "yeni", readB(t, f, "notlar-copy.txt"))
}

// ---------- move ----------

func TestCross_Move_RemovesTheSourceOnlyAfterItArrived(t *testing.T) {
	f := newCrossFixture(t, nil)
	writeA(t, f, "tasi/beni.txt", "yuk")

	op := f.run(t, ops.OpMove, []string{"tasi"}, "/")
	require.Equal(t, ops.StatusOK, op.Status, "move failed: %s", op.Error)

	require.Equal(t, "yuk", readB(t, f, "tasi/beni.txt"))
	_, err := os.Stat(filepath.Join(f.rootA, "tasi"))
	require.True(t, os.IsNotExist(err), "a cross-storage move deletes the source (Burak, 2026-08-29)")

	// The listing must stop showing it on the source side too.
	ctx := context.Background()
	gone, _ := f.store.GetNodeByPath(ctx, f.stA.ID, crossHash(f.stA.ID, "tasi"))
	require.Nil(t, gone, "the moved-away source must leave the source listing")
}

// shortWriter accepts a write and stores fewer bytes than it was given —
// the shape of a backend that answers 200 and truncates (a full disk, a proxy
// that cut the body). A move that trusts the write here deletes the only good
// copy.
type shortWriter struct {
	storage.Driver
}

func (s shortWriter) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	w, ok := s.Driver.(storage.Writer)
	if !ok {
		return storage.ErrUnsupported
	}
	return w.Write(ctx, p, io.LimitReader(r, 2), 2)
}

func TestCross_Move_KeepsTheSourceWhenTheDestinationTruncates(t *testing.T) {
	f := newCrossFixture(t, func(d storage.Driver) storage.Driver { return shortWriter{Driver: d} })
	writeA(t, f, "degerli.txt", "tam icerik")

	op := f.run(t, ops.OpMove, []string{"degerli.txt"}, "/")
	require.Equal(t, ops.StatusFailed, op.Status, "a short write must fail the op, not pass quietly")

	body, err := os.ReadFile(filepath.Join(f.rootA, "degerli.txt"))
	require.NoError(t, err, "the source must still be there when the copy did not fully arrive")
	require.Equal(t, "tam icerik", string(body))
}

// crossHash is the node index's path key — the same md5(path + NUL + LE
// storage id) opsPathHash builds in ops_dbsync_test.go, reused here so the two
// suites cannot disagree about where a node is looked up.
func crossHash(storageID int64, p string) string { return opsPathHash(storageID, p) }

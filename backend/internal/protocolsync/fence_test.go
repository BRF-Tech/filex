package protocolsync_test

// A folder moved or trashed object by object (an object store) fences its
// prefixes instead of holding the storage's row gate (rowgate.FenceCtx,
// fenceOf): the storage's scan goes on everywhere else while it runs, and
// leaves the folder's old and new prefixes alone until the rows have
// followed. A file, and any change on a storage with real folders, holds the
// gate as before.
//
// Break: hold rowgate.MoveCtx in hold() whatever fenceOf says - the scan
// cannot take the gate half way (FencedHalfWay fails).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// newFencedFixture is newGatedFixture on an object store: the Syncer's
// resolver hands out the driver's object-store view.
func newFencedFixture(t *testing.T) *gatedFixture {
	t.Helper()
	f := newGatedFixture(t)
	f.sy.WithResolver(func(int64) (storage.Driver, error) { return f.drv.ObjectStore(), nil })
	dir := f.live("Leon")
	require.NotNil(t, dir, "fixture: the folder has no row")
	require.Equal(t, model.NodeTypeDirectory, dir.Type)
	t.Cleanup(func() {
		// A red test must not leave a fence standing on a storage id the
		// next test's database reuses.
		f.drv.Finish()
	})
	return f
}

func TestRelocate_AFolderOnAnObjectStoreFencesItsPrefixesNotTheStorage(t *testing.T) {
	f := newFencedFixture(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error { return f.drv.Move(ctx, "Leon", "Leo") })
	}()
	gatetest.FencedHalfWay(t, f.st.ID, f.drv, []string{"Leon", "Leon/a.txt", "Leo", "Leo/a.txt"}, "Baska/b.txt", func() bool {
		n := f.live("Leo/a.txt")
		return n != nil && n.ID == f.row.ID && f.live("Leon/a.txt") == nil
	})
	require.NoError(t, <-done)
}

func TestDiscard_AFolderOnAnObjectStoreFencesItsPrefixNotTheStorage(t *testing.T) {
	f := newFencedFixture(t)
	ctx := context.Background()
	dir := f.live("Leon")
	type answer struct {
		trashed bool
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		out, err := f.sy.Discard(ctx, f.st, f.drv.ObjectStore(), "Leon")
		done <- answer{out.Trashed, err}
	}()
	gatetest.FencedHalfWay(t, f.st.ID, f.drv, []string{"Leon", "Leon/a.txt"}, "Leo", func() bool {
		n, err := f.store.GetNode(context.Background(), dir.ID)
		return err == nil && n != nil && n.DeletedAt != nil
	})
	got := <-done
	require.NoError(t, got.err)
	require.True(t, got.trashed, "the folder did not reach the trash")
}

// A file is one object, on an object store too: it holds the gate as before.
func TestRelocate_AFileOnAnObjectStoreStillHoldsTheGate(t *testing.T) {
	f := newFencedFixture(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon/a.txt", "Leon/b.txt", func(ctx context.Context) error {
			return f.drv.Move(ctx, "Leon/a.txt", "Leon/b.txt")
		})
	}()
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool {
		n := f.live("Leon/b.txt")
		return n != nil && n.ID == f.row.ID
	})
	require.NoError(t, <-done)
	require.True(t, rowgate.Fences(f.st.ID).Empty(), "a file move set a fence")
}

// A long move that fails half way - its job cancelled, the driver gone -
// lets its fence go, and the rows stay where they were.
func TestRelocate_AFolderMoveThatFailsLetsItsFenceGo(t *testing.T) {
	f := newFencedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	err := f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error {
		if !rowgate.Fences(f.st.ID).Covers("Leon/a.txt") {
			t.Error("the move ran without its fence")
		}
		return context.Canceled
	})
	require.True(t, errors.Is(err, context.Canceled), "Relocate = %v", err)
	require.True(t, rowgate.Fences(f.st.ID).Empty(), "a failed long move left its fence standing")
	n := f.live("Leon/a.txt")
	require.NotNil(t, n)
	require.Equal(t, f.row.ID, n.ID)
}

// A long move that panics lets its fence go (Relocate's defer).
func TestRelocate_AFolderMoveThatPanicsLetsItsFenceGo(t *testing.T) {
	f := newFencedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	func() {
		defer func() { require.NotNil(t, recover(), "the panic did not come through") }()
		_ = f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error { panic("driver") })
	}()
	require.True(t, rowgate.Fences(f.st.ID).Empty(), "a panic left the fence standing")
}

// A long move whose client goes while a judgement holds the gate sets no
// fence and moves nothing.
func TestRelocate_AFolderMoveWhoseClientWentSetsNoFence(t *testing.T) {
	f := newFencedFixture(t)
	f.drv.Arm(false)
	judged := rowgate.Judge(f.st.ID)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error {
			t.Error("a move whose client had gone moved")
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		judged()
		t.Fatal("the move kept waiting after its client went")
	}
	judged()
	require.True(t, errors.Is(err, context.Canceled), "Relocate = %v", err)
	require.True(t, rowgate.Fences(f.st.ID).Empty(), "a move that never started set a fence")
}

// seedUncatalogued puts Yeni/b.txt on the fixture's storage with no row: a
// folder of a lazily catalogued storage nobody has opened, or objects another
// tool wrote to the bucket.
func (f *gatedFixture) seedUncatalogued(t *testing.T) {
	t.Helper()
	f.drv.Arm(false)
	require.NoError(t, f.drv.Write(context.Background(), "Yeni/b.txt", strings.NewReader("b"), 1))
	f.drv.Arm(true)
	require.Nil(t, f.live("Yeni"), "fixture: the folder has a row")
}

// A folder with no row yet is asked of the storage (one Stat) and fenced like
// a catalogued one.
//
// Break: answer nil in fenceOf when the source has no row - the move holds
// the storage's gate, and the scan cannot take it half way.
func TestRelocate_AnUncataloguedFolderOnAnObjectStoreIsFencedToo(t *testing.T) {
	f := newFencedFixture(t)
	f.seedUncatalogued(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Yeni", "Eski", func(ctx context.Context) error { return f.drv.Move(ctx, "Yeni", "Eski") })
	}()
	gatetest.FencedHalfWay(t, f.st.ID, f.drv, []string{"Yeni", "Yeni/b.txt", "Eski", "Eski/b.txt"}, "Leon",
		func() bool { return f.live("Yeni") == nil })
	require.NoError(t, <-done)
}

func TestDiscard_AnUncataloguedFolderOnAnObjectStoreIsFencedToo(t *testing.T) {
	f := newFencedFixture(t)
	f.seedUncatalogued(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		_, err := f.sy.Discard(ctx, f.st, f.drv.ObjectStore(), "Yeni")
		done <- err
	}()
	gatetest.FencedHalfWay(t, f.st.ID, f.drv, []string{"Yeni", "Yeni/b.txt"}, "Leon", func() bool { return true })
	require.NoError(t, <-done)
}

// An uncatalogued FILE is one object: the Stat says so, and it holds the gate.
func TestRelocate_AnUncataloguedFileOnAnObjectStoreHoldsTheGate(t *testing.T) {
	f := newFencedFixture(t)
	f.seedUncatalogued(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Yeni/b.txt", "Yeni/c.txt", func(ctx context.Context) error {
			return f.drv.Move(ctx, "Yeni/b.txt", "Yeni/c.txt")
		})
	}()
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool { return true })
	require.NoError(t, <-done)
	require.True(t, rowgate.Fences(f.st.ID).Empty(), "a file move set a fence")
}

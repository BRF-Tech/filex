package protocolsync_test

// The protocol servers' shared two-step frame (gated.go, issue #201): each of
// Relocate, Discard and Purge holds the storage's row gate from the first byte
// it changes to the last row it writes, so a storage scan's judgement
// (rowgate.Judge) waits for the rows and never sees the storage half way.
//
// Break: take the rowgate call out of any of the three - its test's scan gets
// the gate while the bytes have changed and the rows have not.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

type gatedFixture struct {
	store db.Store
	st    *model.Storage
	sy    *protocolsync.Syncer
	drv   *gatetest.Driver
	row   *model.Node
}

// newGatedFixture is a storage on a driver that stops its next Move or Delete
// half way, holding Leon/a.txt on the storage and in the catalogue.
func newGatedFixture(t *testing.T) *gatedFixture {
	t.Helper()
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	st, err := raw.CreateStorage(ctx, &model.Storage{
		Name: "Local", Driver: "local", MountPath: "/local", Enabled: true,
		ConfigJSON: []byte(`{"path":"/tmp/does-not-matter"}`),
	})
	require.NoError(t, err)
	d := gatetest.New(t)
	d.Arm(false)
	require.NoError(t, d.Write(ctx, "Leon/a.txt", strings.NewReader("a"), 1))
	sy := protocolsync.New(raw, nil, nil, "test")
	row, _, ok := sy.WriteRows(ctx, st, "Leon/a.txt", 1, "text/plain")
	require.True(t, ok)
	d.Arm(true)
	return &gatedFixture{store: raw, st: st, sy: sy, drv: d, row: row}
}

func (f *gatedFixture) live(rel string) *model.Node {
	n, err := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, rel))
	if err != nil {
		return nil
	}
	return n
}

func TestRelocate_HoldsTheRowGateUntilTheRowsFollow(t *testing.T) {
	f := newGatedFixture(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error { return f.drv.Move(ctx, "Leon", "Leo") })
	}()
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool {
		n := f.live("Leo/a.txt")
		return n != nil && n.ID == f.row.ID
	})
	require.NoError(t, <-done)
}

func TestDiscard_HoldsTheRowGateUntilTheRowIsInTheTrash(t *testing.T) {
	f := newGatedFixture(t)
	ctx := context.Background()
	type answer struct {
		trashed bool
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		out, err := f.sy.Discard(ctx, f.st, f.drv, "Leon/a.txt")
		done <- answer{out.Trashed, err}
	}()
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool {
		n, err := f.store.GetNode(context.Background(), f.row.ID)
		return err == nil && n != nil && n.DeletedAt != nil
	})
	got := <-done
	require.NoError(t, got.err)
	require.True(t, got.trashed, "the bytes did not reach the trash")
}

func TestPurge_HoldsTheRowGateUntilTheRowsAreGone(t *testing.T) {
	f := newGatedFixture(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Purge(ctx, f.st, "Leon/a.txt", func(ctx context.Context) error { return f.drv.Delete(ctx, "Leon/a.txt") })
	}()
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool { return f.live("Leon/a.txt") == nil })
	require.NoError(t, <-done)
}

// A move that fails leaves the rows where they are: the bytes did not move.
func TestRelocate_RowsStayWhenTheBytesDoNotMove(t *testing.T) {
	f := newGatedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	err := f.sy.Relocate(ctx, f.st, "Yok", "Leo", func(ctx context.Context) error { return f.drv.Move(ctx, "Yok", "Leo") })
	require.Error(t, err)
	n := f.live("Leon/a.txt")
	require.NotNil(t, n)
	require.Equal(t, f.row.ID, n.ID)
}

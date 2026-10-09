package protocolsync_test

// sec055: a protocol verb waits for the storage's row gate on the request's
// own context, detaches only once it holds the gate, and asks what it checked
// before it asked for the gate again under it (gated.go).
//
// Up to 0.55's first cut the verb detached first and then waited without a
// context (rowgate.Move): a client that gave up while a storage judgement held
// the gate left a goroutine that took the gate later and made the change all
// the same, on checks made before the wait - a late MOVE replaced a file that
// had landed on its destination meanwhile, a late DELETE trashed whatever was
// at the path by then.
//
// Break: in gated.go's hold, take the gate with rowgate.Move instead of
// rowgate.MoveCtx (the cancelled request takes the gate and moves), or skip
// the checks (the late MOVE replaces the new file, the late DELETE trashes the
// rewritten one).

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

func (f *gatedFixture) content(t *testing.T, rel string) string {
	t.Helper()
	rc, err := f.drv.Read(context.Background(), rel)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

// waitingFor is how long a test lets a verb reach the gate and start waiting
// behind the judgement it holds.
const waitingFor = 200 * time.Millisecond

func TestRelocate_ACancelledRequestTakesNoGateAndMovesNothing(t *testing.T) {
	f := newGatedFixture(t)
	f.drv.Arm(false)
	judged := rowgate.Judge(f.st.ID) // a storage scan judging the storage
	defer judged()

	ctx, cancel := context.WithCancel(context.Background())
	moved := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon", "Leo", func(ctx context.Context) error {
			moved <- struct{}{}
			return f.drv.Move(ctx, "Leon", "Leo")
		})
	}()
	time.Sleep(waitingFor)
	cancel() // the client gives up while the judgement holds the gate

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		judged()
		t.Fatal("a verb whose client had gone kept waiting for the row gate")
	}
	judged()

	// The gate is free: the request that went took nothing.
	release, ok := rowgate.TryJudge(f.st.ID)
	require.True(t, ok, "the cancelled request holds the row gate")
	release()
	select {
	case <-moved:
		t.Fatal("the cancelled request moved the bytes")
	default:
	}
	n := f.live("Leon/a.txt")
	require.NotNil(t, n, "the cancelled request moved the rows")
	require.Equal(t, f.row.ID, n.ID)
	_, err := f.drv.Stat(context.Background(), "Leon/a.txt")
	require.NoError(t, err, "the cancelled request moved the bytes")
}

func TestRelocate_ALateMoveDoesNotReplaceAFileThatLandedMeanwhile(t *testing.T) {
	f := newGatedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	judged := rowgate.Judge(f.st.ID)
	defer judged()

	// The destination was free when the MOVE was checked.
	_, err := f.drv.Stat(ctx, "Leon/b.txt")
	require.ErrorIs(t, err, storage.ErrNotFound)
	done := make(chan error, 1)
	go func() {
		done <- f.sy.Relocate(ctx, f.st, "Leon/a.txt", "Leon/b.txt", func(ctx context.Context) error {
			return f.drv.Move(ctx, "Leon/a.txt", "Leon/b.txt")
		}, protocolsync.StillFree(f.drv, "Leon/b.txt", "Leon/a.txt"))
	}()
	time.Sleep(waitingFor)
	// Someone else's file lands on the name while the MOVE waits.
	require.NoError(t, f.drv.Write(ctx, "Leon/b.txt", strings.NewReader("new"), 3))
	judged()

	select {
	case err := <-done:
		require.ErrorIs(t, err, storage.ErrTakenMeanwhile)
		require.True(t, errors.Is(err, os.ErrExist), "a taken destination is answered as the conflict it is")
	case <-time.After(10 * time.Second):
		t.Fatal("the MOVE never got the row gate")
	}
	require.Equal(t, "new", f.content(t, "Leon/b.txt"), "the late MOVE replaced the file that landed on its destination")
	require.Equal(t, "a", f.content(t, "Leon/a.txt"), "the refused MOVE moved its source")
	n := f.live("Leon/a.txt")
	require.NotNil(t, n)
	require.Equal(t, f.row.ID, n.ID, "the refused MOVE moved the rows")
}

func TestDiscard_ALateDeleteLeavesAFileThatReplacedTheOneSeen(t *testing.T) {
	f := newGatedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	seen, err := f.drv.Stat(ctx, "Leon/a.txt")
	require.NoError(t, err)
	judged := rowgate.Judge(f.st.ID)
	defer judged()

	done := make(chan error, 1)
	go func() {
		_, err := f.sy.Discard(ctx, f.st, f.drv, "Leon/a.txt", protocolsync.StillAsSeen(f.drv, "Leon/a.txt", seen))
		done <- err
	}()
	time.Sleep(waitingFor)
	require.NoError(t, f.drv.Write(ctx, "Leon/a.txt", strings.NewReader("rewritten"), 9))
	judged()

	select {
	case err := <-done:
		require.ErrorIs(t, err, storage.ErrChangedMeanwhile)
	case <-time.After(10 * time.Second):
		t.Fatal("the DELETE never got the row gate")
	}
	require.Equal(t, "rewritten", f.content(t, "Leon/a.txt"), "the late DELETE trashed the file that replaced the one it was asked about")
	n, err := f.store.GetNode(ctx, f.row.ID)
	require.NoError(t, err)
	require.NotNil(t, n)
	require.Nil(t, n.DeletedAt, "the refused DELETE put the row in the trash")
}

// PurgeTree deletes a folder's objects a batch at a time, each batch's rows
// with its bytes under the gate, and the folder's rows last.
func TestPurgeTree_EachBatchsRowsFollowItsBytesUnderTheGate(t *testing.T) {
	f := newGatedFixture(t)
	f.drv.Arm(false)
	ctx := context.Background()
	require.NoError(t, f.drv.Write(ctx, "Leon/b.txt", strings.NewReader("b"), 1))
	_, _, ok := f.sy.WriteRows(ctx, f.st, "Leon/b.txt", 1, "text/plain")
	require.True(t, ok)
	was := protocolsync.PurgeBatch
	protocolsync.PurgeBatch = 1
	t.Cleanup(func() { protocolsync.PurgeBatch = was })
	f.drv.Arm(true)

	done := make(chan error, 1)
	go func() {
		done <- f.sy.PurgeTree(ctx, f.st, f.drv, "Leon", func(context.Context) ([]string, error) {
			return []string{"Leon/a.txt", "Leon/b.txt"}, nil
		})
	}()
	// The first object's delete stands half way: the gate is held, and once
	// it is let go the first object's row is gone with its bytes.
	gatetest.HeldHalfWay(t, f.st.ID, f.drv, func() bool { return f.live("Leon/a.txt") == nil })
	require.NoError(t, <-done)
	require.Nil(t, f.live("Leon/b.txt"))
	require.Nil(t, f.live("Leon"))
}

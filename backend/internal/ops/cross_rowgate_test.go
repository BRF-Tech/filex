package ops_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// Issue #201: a queued move between two storages ends with a two-step change
// on the source - its bytes are deleted, then its rows are dropped
// (SyncHardDelete) - and it holds the source storage's row gate
// (internal/rowgate) from the one to the other. Judged half way, a storage
// scan saw a live row whose bytes were gone and dropped it on its own; the
// queue's hard delete then found no row, and its file.deleted event and the
// rows' own bookkeeping went missing.
//
// Break: take rowgate.Change out of crossTransfer's source delete - the scan's
// judgement gets the gate while the source row is still there.
func TestCross_Move_HoldsTheSourceRowGateUntilTheRowsGo(t *testing.T) {
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()

	rawA, rawB := &local.Driver{}, &local.Driver{}
	require.NoError(t, rawA.Init(ctx, map[string]any{"root": rootA}))
	require.NoError(t, rawB.Init(ctx, map[string]any{"root": rootB}))
	drvA := gatetest.Wrap(rawA)
	t.Cleanup(drvA.Finish)
	drvA.Arm(false)

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
			return rawB, nil
		}
		return nil, fmt.Errorf("unknown id %d", id)
	}
	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(handlers.NewManager(store, resolver))

	const body = "yuk"
	require.NoError(t, drvA.Write(ctx, "tasi.txt", strings.NewReader(body), int64(len(body))))
	_, err := store.CreateNode(ctx, &model.Node{
		StorageID: stA.ID, Name: "tasi.txt", Path: "tasi.txt",
		PathHash: opsPathHash(stA.ID, "tasi.txt"), Type: model.NodeTypeFile, Size: int64(len(body)),
	})
	require.NoError(t, err)
	drvA.Arm(true)

	op, err := svc.SubmitTo(ctx, ops.OpMove, stA.ID, stB.ID, []string{"tasi.txt"}, "/")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go svc.Run(run)

	gatetest.HeldHalfWay(t, stA.ID, drvA, func() bool {
		n, _ := store.GetNodeByPath(context.Background(), stA.ID, opsPathHash(stA.ID, "tasi.txt"))
		return n == nil
	})

	done := waitForOp(t, svc, op.ID)
	svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "move op error: %s", done.Error)
	got, err := os.ReadFile(filepath.Join(rootB, "tasi.txt"))
	require.NoError(t, err, "the file arrived in the destination storage")
	assert.Equal(t, body, string(got))
}

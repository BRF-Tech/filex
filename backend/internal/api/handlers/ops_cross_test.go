package handlers_test

// The HTTP half of the cross-storage paste: POST /api/files/copy|move with a
// `target` in a DIFFERENT depo than the sources.
//
// The bug this locks down was invisible from the client's side: the endpoint
// resolved the storage from the SOURCES only, stripped the target's
// `<adapter>://` prefix and handed the leftover relative path to the source
// storage. So `alpha://a.txt` → `beta://hedef` answered 202 ("queued"), and the
// file appeared at `alpha://hedef/a.txt` — a folder invented inside the depo
// the user was copying FROM. Nothing failed; the file was just never where
// they put it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type crossHTTPFixture struct {
	oh           *handlers.Ops
	svc          *ops.Service
	stA, stB     *model.Storage
	rootA, rootB string
}

func newCrossHTTPFixture(t *testing.T, betaReadOnly bool) *crossHTTPFixture {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	rootA, rootB := t.TempDir(), t.TempDir()

	drvA, drvB := &local.Driver{}, &local.Driver{}
	require.NoError(t, drvA.Init(ctx, map[string]any{"root": rootA}))
	require.NoError(t, drvB.Init(ctx, map[string]any{"root": rootB}))

	mk := func(name, root string, ro bool) *model.Storage {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ReadOnly: ro,
			ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
		})
		require.NoError(t, err)
		return st
	}
	stA := mk("alpha", rootA, false)
	stB := mk("beta", rootB, betaReadOnly)

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

	return &crossHTTPFixture{oh: handlers.NewOps(svc, store), svc: svc, stA: stA, stB: stB, rootA: rootA, rootB: rootB}
}

func (f *crossHTTPFixture) post(t *testing.T, verb string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/files/"+verb, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	switch verb {
	case "copy":
		f.oh.SubmitCopy(rec, req)
	case "move":
		f.oh.SubmitMove(rec, req)
	default:
		t.Fatalf("unknown verb %q", verb)
	}
	return rec
}

// drain runs the worker until the queue is empty (or the deadline passes).
func (f *crossHTTPFixture) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		list, err := f.svc.List(ctx, "")
		require.NoError(t, err)
		busy := false
		for _, op := range list {
			if op.Status == ops.StatusPending || op.Status == ops.StatusRunning {
				busy = true
			}
		}
		if !busy {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("queue never drained")
}

func TestOpsHTTP_Copy_AcrossStorages_WritesIntoTheTarget(t *testing.T) {
	f := newCrossHTTPFixture(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootA, "gezegen.txt"), []byte("merhaba"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.rootB, "hedef"), 0o755))

	rec := f.post(t, "copy", map[string]any{
		"source": []string{"alpha://gezegen.txt"},
		"target": "beta://hedef",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	// The queued row must name the destination storage — without it the worker
	// has no way to know where "hedef" is.
	var got struct {
		Op struct {
			StorageID     int64 `json:"storage_id"`
			DestStorageID int64 `json:"dest_storage_id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, f.stA.ID, got.Op.StorageID)
	require.Equal(t, f.stB.ID, got.Op.DestStorageID, "the target's storage must be carried on the op")

	f.drain(t)

	body, err := os.ReadFile(filepath.Join(f.rootB, "hedef", "gezegen.txt"))
	require.NoError(t, err, "the file must land in the TARGET depo")
	require.Equal(t, "merhaba", string(body))
	_, err = os.Stat(filepath.Join(f.rootA, "hedef"))
	require.True(t, os.IsNotExist(err), "nothing may be invented inside the source depo")
}

func TestOpsHTTP_Move_AcrossStorages_LeavesTheSourceEmpty(t *testing.T) {
	f := newCrossHTTPFixture(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootA, "tasi.txt"), []byte("yuk"), 0o644))

	rec := f.post(t, "move", map[string]any{
		"source":    []string{"alpha://tasi.txt"},
		"target":    "beta://",
		"sourceDir": "alpha://",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	f.drain(t)

	body, err := os.ReadFile(filepath.Join(f.rootB, "tasi.txt"))
	require.NoError(t, err)
	require.Equal(t, "yuk", string(body))
	_, err = os.Stat(filepath.Join(f.rootA, "tasi.txt"))
	require.True(t, os.IsNotExist(err), "a cross-depo cut removes the original")
}

func TestOpsHTTP_Copy_IntoReadOnlyStorage_IsRefused(t *testing.T) {
	f := newCrossHTTPFixture(t, true)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootA, "x.txt"), []byte("x"), 0o644))

	rec := f.post(t, "copy", map[string]any{
		"source": []string{"alpha://x.txt"},
		"target": "beta://",
	})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	// The refusal has to say what to do about it — a bare code sends the user
	// back to try the same paste again.
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp["error"], "read-only")
	require.NotEmpty(t, resp["hint"])

	entries, err := os.ReadDir(f.rootB)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing may be written into a read-only depo")
}

func TestOpsHTTP_Copy_UnknownTargetStorage_Is400(t *testing.T) {
	f := newCrossHTTPFixture(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootA, "x.txt"), []byte("x"), 0o644))

	rec := f.post(t, "copy", map[string]any{
		"source": []string{"alpha://x.txt"},
		"target": "yok-boyle-depo://klasor",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "unknown adapter")
}

func TestOpsHTTP_Copy_SameStorage_StillQueuesAsBefore(t *testing.T) {
	f := newCrossHTTPFixture(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(f.rootA, "y.txt"), []byte("ayni"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(f.rootA, "ic"), 0o755))

	rec := f.post(t, "copy", map[string]any{
		"source": []string{"alpha://y.txt"},
		"target": "alpha://ic",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	f.drain(t)

	body, err := os.ReadFile(filepath.Join(f.rootA, "ic", "y.txt"))
	require.NoError(t, err, "the same-storage path must keep working untouched")
	require.Equal(t, "ayni", string(body))
}

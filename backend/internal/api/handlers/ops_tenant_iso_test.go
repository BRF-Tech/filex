package handlers_test

// The ops queue across the tenant boundary.
//
// Two separate failures live here and they are not the same shape:
//
//   - SUBMIT takes the storage from the REQUEST BODY (`storage_id`,
//     `dest_storage_id`) or from the `<adapter>://` prefix of a source path,
//     resolves it with `Store.GetStorageByName` — a method `tenantstore` does
//     not wrap — and then asks only the ACL. The ACL is tenant-blind and on an
//     `rbac_enabled=false` storage (the migration default) it answers *editor*
//     for a plain user, so it stops nobody. That makes this a cross-tenant
//     WRITE, and `delete` makes it destructive.
//
//   - LIST/STATUS read `pending_ops` with no predicate at all. The rows carry
//     `storage_id`, `sources_json` and `dest`, i.e. other tenants' live file
//     paths.
//
// Measured on the unfixed build, before the fix in ops.go — the exact
// observations are recorded on each test.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// drainOps runs the queue worker until nothing is pending. The red proof for
// `delete` is not the 202 — it is the file being gone from the other tenant's
// disk afterwards.
func (f *mtFix) drainOps(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.Ops.Run(runCtx)
	defer f.Ops.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		list, err := f.Ops.List(ctx, "")
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

// TestOpsSubmit_PerVerbDeleteCannotReachAnotherTenant
//
// RED PROOF (unfixed code): a plain member of tenant `alpha` posting
//
//	POST /api/files/delete  {"source":["bravo://gizli.txt"]}
//
// got `202 Accepted` with body
//
//	{"op":{"id":1,"kind":"delete","storage_id":2,"sources":["gizli.txt"],
//	       "total":1,"done":0,"failed":0,"status":"pending",…}}
//
// — note `storage_id:2`, which is tenant bravo's storage — and after the
// worker ran, `bravo/gizli.txt` was GONE from disk. A user of one customer
// deleted another customer's file with one HTTP call.
func TestOpsSubmit_PerVerbDeleteCannotReachAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "gizli.txt", "bravo's private file")
	victim := filepath.Join(f.RootB, "gizli.txt")

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"bravo://gizli.txt"},
	})
	require.Equal(t, http.StatusNotFound, status, "a foreign storage must be indistinguishable from one that does not exist: %v", body)

	f.drainOps(t)
	_, err := os.Stat(victim)
	require.NoError(t, err, "the other tenant's file must still be on disk")
}

// TestOpsSubmit_PerVerbCopyCannotWriteIntoAnotherTenant — the same door in the
// other direction: sources in the caller's own depo, `target` naming somebody
// else's. RED PROOF: 202, and `bravo/dropped/kendi.txt` appeared in the other
// tenant's storage after the drain.
func TestOpsSubmit_PerVerbCopyCannotWriteIntoAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "kendi.txt", "alpha's own file")
	require.NoError(t, os.MkdirAll(filepath.Join(f.RootB, "dropped"), 0o755))

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
		"source": []string{"alpha://kendi.txt"},
		"target": "bravo://dropped",
	})
	require.Equal(t, http.StatusNotFound, status, "%v", body)

	f.drainOps(t)
	_, err := os.Stat(filepath.Join(f.RootB, "dropped", "kendi.txt"))
	require.True(t, os.IsNotExist(err), "nothing may be written into another tenant's storage")
}

// TestOpsSubmit_UnifiedEndpointCannotNameAnotherTenantsStorage — `POST
// /api/files/ops` takes the ids as plain integers in the body, which is the
// bluntest form of the same hole. RED PROOF: both variants answered
// `202 Accepted` with the foreign id echoed back on the queued row.
func TestOpsSubmit_UnifiedEndpointCannotNameAnotherTenantsStorage(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "kendi.txt", "alpha's own file")
	f.seedFile(t, f.StB, f.RootB, "gizli.txt", "bravo's private file")

	t.Run("source storage", func(t *testing.T) {
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops", map[string]any{
			"kind": "delete", "storage_id": f.StB.ID, "sources": []string{"gizli.txt"},
		})
		require.Equal(t, http.StatusNotFound, status, "%v", body)
	})

	t.Run("destination storage", func(t *testing.T) {
		status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops", map[string]any{
			"kind": "copy", "storage_id": f.StA.ID, "dest_storage_id": f.StB.ID,
			"sources": []string{"kendi.txt"}, "dest": "/",
		})
		require.Equal(t, http.StatusNotFound, status, "%v", body)
	})

	f.drainOps(t)
	_, err := os.Stat(filepath.Join(f.RootB, "gizli.txt"))
	require.NoError(t, err, "the other tenant's file must still be on disk")
}

// TestOpsSubmit_OwnTenantStillWorks — the boundary must not cost a tenant its
// own feature. Same call, own depo, still 202 and still executed.
func TestOpsSubmit_OwnTenantStillWorks(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StA, f.RootA, "silinecek.txt", "mine to delete")

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"alpha://silinecek.txt"},
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)

	f.drainOps(t)
	_, err := os.Stat(filepath.Join(f.RootA, "silinecek.txt"))
	require.True(t, os.IsNotExist(err), "a tenant must still be able to delete its own file")
}

// TestOpsSubmit_SupertenantStillReachesEveryStorage — the platform operator is
// confine-exempt by design (docs/MULTI-TENANCY.md); a fix that locked them out
// would break support work.
func TestOpsSubmit_SupertenantStillReachesEveryStorage(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "operator.txt", "bravo file the operator may touch")

	status, body := doJSON(t, f.Super, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"bravo://operator.txt"},
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
}

// TestOpsList_DoesNotLeakAnotherTenantsQueue
//
// RED PROOF (unfixed code): after bravo queued a delete of
// `bravo://muhasebe-2026.txt`, `GET /api/files/ops` as an alpha member
// answered `200` with
//
//	{"ops":[{"id":1,"kind":"delete","storage_id":2,
//	         "sources":["muhasebe-2026.txt"],"status":"pending",…}]}
//
// The row is the other customer's file name and its storage id. `GET
// /api/files/ops/1` answered `200` with the same row.
func TestOpsList_DoesNotLeakAnotherTenantsQueue(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "muhasebe-2026.txt", "bravo bookkeeping")
	f.seedFile(t, f.StA, f.RootA, "benim.txt", "alpha's own")

	status, body := doJSON(t, f.B, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"bravo://muhasebe-2026.txt"},
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	bravoOpID := int64(body["op"].(map[string]any)["id"].(float64))

	status, body = doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
		"source": []string{"alpha://benim.txt"}, "target": "alpha://",
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	alphaOpID := int64(body["op"].(map[string]any)["id"].(float64))

	// The listing an alpha member sees must contain its own row and only that.
	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, raw, "muhasebe-2026.txt", "another tenant's file name must not appear in the queue listing")
	require.Contains(t, raw, "benim.txt", "a tenant must still see its own queue")

	// …and the per-row endpoint must not answer for a foreign id.
	status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+strconv.FormatInt(bravoOpID, 10))
	require.Equal(t, http.StatusNotFound, status, raw)

	status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+strconv.FormatInt(alphaOpID, 10))
	require.Equal(t, http.StatusOK, status, raw)

	// The supertenant keeps the instance-wide view it is there to have.
	status, raw = mtGet(t, f.Super, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, raw, "muhasebe-2026.txt")
	require.Contains(t, raw, "benim.txt")
}

// TestOpsTenantIsolation_SingleTenantInstallUnaffected — the honest form of the
// proof: with multi-tenant mode OFF, the identical crossing calls behave
// exactly as they did before any of this existed. This test passes on `main`.
func TestOpsTenantIsolation_SingleTenantInstallUnaffected(t *testing.T) {
	f := newMTFix(t, false)
	f.seedFile(t, f.StB, f.RootB, "ortak.txt", "shared instance file")

	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"bravo://ortak.txt"},
	})
	require.Equal(t, http.StatusAccepted, status,
		"a single-tenant install has one boundary, not two: every user reaches every storage: %v", body)

	f.drainOps(t)
	_, err := os.Stat(filepath.Join(f.RootB, "ortak.txt"))
	require.True(t, os.IsNotExist(err), "and the op really runs")

	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, raw, "ortak.txt", "the queue tray keeps showing every op")
}

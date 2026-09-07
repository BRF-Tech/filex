package handlers_test

// The trash listing across the tenant boundary.
//
// `GET /api/files/manager/trash` had a filter that LOOKED like the tenant
// boundary and was not one. The comment above it read
//
//	// Confinement: only surface trashed nodes whose original path is inside
//	// the caller's root, so a tenant never sees another tenant's deleted files.
//
// but the predicate was `confine.RootFrom(r.Context())` — the embedded-client
// root confinement, set by an `X-Filex-Root` header or a `root:`-scoped API
// token. A normal browser login has no confine root, so the block was skipped
// entirely and the listing was instance-wide. The claim in the comment is the
// thing that made this hard to see: the code says it is doing the check.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// trashOne soft-deletes a seeded node so it appears in the trash listing.
func (f *mtFix) trashOne(t *testing.T, nodeID int64) {
	t.Helper()
	require.NoError(t, f.Store.SoftDeleteNode(context.Background(), nodeID))
}

// TestTrashList_DoesNotShowAnotherTenantsDeletedFiles
//
// RED PROOF (unfixed code): with one soft-deleted file in each tenant, an alpha
// member's `GET /api/files/manager/trash` answered `200 OK` with
//
//	{"entries":[
//	   {"id":1,"storage_id":2,"name":"bravo-silinen.txt","path":"/bravo-silinen.txt",
//	    "size":27,"mime":"text/plain","storage_name":"bravo","ttl_days":30},
//	   {"id":2,"storage_id":1,"name":"alpha-silinen.txt",…}],
//	 "total":2,…}
//
// — including `storage_name:"bravo"`, so unlike the search leak this one names
// the tenant it came from.
func TestTrashList_DoesNotShowAnotherTenantsDeletedFiles(t *testing.T) {
	f := newMTFix(t, true)
	bravo := f.seedFile(t, f.StB, f.RootB, "bravo-silinen.txt", "bravo's deleted file")
	alpha := f.seedFile(t, f.StA, f.RootA, "alpha-silinen.txt", "alpha's deleted file")
	f.trashOne(t, bravo.ID)
	f.trashOne(t, alpha.ID)

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager/trash")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, "bravo-silinen.txt",
		"another tenant's deleted files must not be listed: %s", body)
	require.Contains(t, body, "alpha-silinen.txt", "the tenant still sees its own trash")
	require.Contains(t, body, `"total":1`, "the count must match what was actually returned: %s", body)
}

// TestTrashList_SupertenantStillSeesEverything — the operator's trash view is
// how a deleted-by-accident support ticket gets answered.
func TestTrashList_SupertenantStillSeesEverything(t *testing.T) {
	f := newMTFix(t, true)
	bravo := f.seedFile(t, f.StB, f.RootB, "bravo-silinen.txt", "bravo's deleted file")
	alpha := f.seedFile(t, f.StA, f.RootA, "alpha-silinen.txt", "alpha's deleted file")
	f.trashOne(t, bravo.ID)
	f.trashOne(t, alpha.ID)

	status, body := mtGet(t, f.Super, f.URL+"/api/files/manager/trash")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "bravo-silinen.txt")
	require.Contains(t, body, "alpha-silinen.txt")
}

// TestTrashList_SingleTenantInstallUnaffected — mode off: one instance, one
// trash can. Passes on `main`.
func TestTrashList_SingleTenantInstallUnaffected(t *testing.T) {
	f := newMTFix(t, false)
	bravo := f.seedFile(t, f.StB, f.RootB, "bravo-silinen.txt", "deleted over here")
	alpha := f.seedFile(t, f.StA, f.RootA, "alpha-silinen.txt", "deleted over there")
	f.trashOne(t, bravo.ID)
	f.trashOne(t, alpha.ID)

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager/trash")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "bravo-silinen.txt", "%s", body)
	require.Contains(t, body, "alpha-silinen.txt")
}

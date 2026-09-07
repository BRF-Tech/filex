package handlers_test

// The file manager across the tenant boundary.
//
// Four doors, and they fail for the same underlying reason: an id or a name
// that arrives from the client is used to address a storage or a node without
// anyone asking whose it is. `tenantstore` confines the LIST methods only
// (ListStorages / ListEnabledStorages / ListUsers), so every by-id lookup —
// `GetNode`, `StorageResolver(id)`, `ListNodesByParent(storageID, …)` — is
// unconfined by construction. The only gate on these routes is the ACL, and
// on an `rbac_enabled=false` storage (the migration default) `acl.Effective`
// answers `roleBase(role)` — Editor for a plain `user`. It stops nobody.
//
// ⚠ These are GETs, and `audit_middleware.go` filters GETs, so a crossing here
// leaves NO audit row. The assertions are on the response body and on the file
// bytes, not on the audit trail.

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManagerRead_ByStorageAndPath_CannotReachAnotherTenant
//
// The most severe of the four: this returns raw FILE BYTES.
//
// RED PROOF (unfixed code): a plain member of tenant `alpha` calling
//
//	GET /api/files/read?storage=3&path=/muhasebe.txt
//
// where storage 3 is tenant bravo's, answered `200 OK` with body
//
//	bravo's confidential ledger
//
// — the file's contents, verbatim, over one authenticated GET, with no audit
// row behind it.
func TestManagerRead_ByStorageAndPath_CannotReachAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "muhasebe.txt", "bravo's confidential ledger")

	status, body := mtGet(t, f.A,
		f.URL+"/api/files/read?storage="+strconv.FormatInt(f.StB.ID, 10)+"&path=/muhasebe.txt")
	require.Equal(t, http.StatusNotFound, status, body)
	require.NotContains(t, body, "confidential ledger", "not one byte of the other tenant's file may come back")
}

// TestManagerRead_ByNodeID_CannotReachAnotherTenant — the same bytes through
// the `?id=` door, which skips the storage parameter entirely and takes the
// storage from the node row it just read with an unconfined GetNode.
//
// RED PROOF (unfixed code): `GET /api/files/read?id=<bravo node>` → `200 OK`,
// body `bravo's confidential ledger`.
func TestManagerRead_ByNodeID_CannotReachAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StB, f.RootB, "muhasebe.txt", "bravo's confidential ledger")

	status, body := mtGet(t, f.A, f.URL+"/api/files/read?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusNotFound, status, body)
	require.NotContains(t, body, "confidential ledger")
}

// TestManagerStat_ByNodeID_CannotReachAnotherTenant — metadata rather than
// bytes, but the same unconfined GetNode: name, full path, size, mime, owner.
//
// RED PROOF (unfixed code): `GET /api/files/stat?id=<bravo node>` → `200 OK`
// with `{"id":…,"storage_id":3,"name":"muhasebe.txt","path":"/muhasebe.txt",…}`.
func TestManagerStat_ByNodeID_CannotReachAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StB, f.RootB, "muhasebe.txt", "bravo's confidential ledger")

	status, body := mtGet(t, f.A, f.URL+"/api/files/stat?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusNotFound, status, body)
	require.NotContains(t, body, "muhasebe.txt")
}

// TestManagerList_CannotListAnotherTenantsCatalogue — the native (admin SPA)
// listing shape, `?storage=<id>`, which never consults the confined storage
// list at all.
//
// RED PROOF (unfixed code): `GET /api/files/manager?storage=3` → `200 OK` with
// `{"nodes":[{"id":1,"storage_id":3,"name":"muhasebe.txt","path":"/muhasebe.txt",…}]}`
// — the other customer's whole catalogue: names, paths, sizes, node ids (and a
// node id is the key to the two `?id=` doors above).
func TestManagerList_CannotListAnotherTenantsCatalogue(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "muhasebe.txt", "bravo's confidential ledger")

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?storage="+strconv.FormatInt(f.StB.ID, 10))
	// ⚠ The refusal is an empty 200, not a 404, and on purpose: a storage id
	// that names NOTHING already answers `200 {"nodes":[]}` here, and so does
	// the handler's own RBAC refusal (`!set.StorageVisible()`). A 404 would be
	// the one answer only a real-but-foreign id produces, which turns the route
	// into a census of the platform's storages. See the comment at
	// manager.go:List.
	require.Equal(t, http.StatusOK, status, body)
	require.NotContains(t, body, "muhasebe.txt")
}

// TestManagerSearch_CrossStorageDoesNotLeakAnotherTenant
//
// The toolbar search. `vfSearch` computes
//
//	crossStorage := rel == "" && len(storageNames) > 1
//
// and its `keep()` then returns `crossStorage || n.StorageID == s.ID` — in
// cross-storage mode every hit is kept, whichever storage it came from, and the
// file contains no `tenant.FromContext` at all.
//
// ⚠ Two things about this one had to be measured rather than assumed.
//
//   - The branch is only entered when the CALLER can see more than one storage,
//     which is why the fixture gives tenant alpha two. With one storage per
//     tenant the branch is never taken and the test reports the hole as absent.
//
//   - The leaked row does NOT say which storage it came from. `projectFileNodes`
//     stamps every hit with the CURRENT adapter's name, so the other tenant's
//     file arrives labelled `alpha://…`. An assertion looking for the string
//     "bravo" in the body passes on the unfixed build — the first version of
//     this test did exactly that and reported safety. The assertion has to be
//     on the FILE NAME.
//
// RED PROOF (unfixed code): alpha member, `GET
// /api/files/manager?action=search&path=alpha://&filter=muhasebe` → `200 OK`
// with, among the files,
//
//	{"path":"alpha:///bravo-muhasebe-2026.txt","basename":"bravo-muhasebe-2026.txt",
//	 "type":"file","size":27,"mime_type":"text/plain","storage":"alpha"}
//
// — tenant bravo's file name and path, mislabelled as alpha's.
func TestManagerSearch_CrossStorageDoesNotLeakAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "bravo-muhasebe-2026.txt", "bravo's confidential ledger")
	f.seedFile(t, f.StA, f.RootA, "alpha-muhasebe-2026.txt", "alpha's own ledger")

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action=search&path=alpha://&filter=muhasebe")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, "bravo-muhasebe-2026.txt",
		"no hit from another tenant's storage: %s", body)
	require.Contains(t, body, "alpha-muhasebe-2026.txt", "the tenant still finds its own files")

	// The platform operator keeps the instance-wide view.
	status, body = mtGet(t, f.Super, f.URL+"/api/files/manager?action=search&path=alpha://&filter=muhasebe")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "bravo-muhasebe-2026.txt", body)
}

// TestManagerSearch_TagBranchDoesNotLeakAnotherTenant — the second entrance
// into the same `keep()`: a bare `tag:x` query lists tagged nodes directly and
// never touches the index. Tags live on node_meta and are SHARED across users
// by design, so the listing is a straight read of every node carrying the
// label — across tenants.
//
// RED PROOF (unfixed code): `…&filter=tag:gizli` → `200 OK` with both
// `bravo-muhasebe-2026.txt` and `alpha-muhasebe-2026.txt` in `files`.
func TestManagerSearch_TagBranchDoesNotLeakAnotherTenant(t *testing.T) {
	f := newMTFix(t, true)
	bravoNode := f.seedFile(t, f.StB, f.RootB, "bravo-muhasebe-2026.txt", "bravo's confidential ledger")
	alphaNode := f.seedFile(t, f.StA, f.RootA, "alpha-muhasebe-2026.txt", "alpha's own ledger")
	f.tag(t, bravoNode.ID, "gizli")
	f.tag(t, alphaNode.ID, "gizli")

	status, body := mtGet(t, f.A, f.URL+"/api/files/manager?action=search&path=alpha://&filter=tag:gizli")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, body, "bravo-muhasebe-2026.txt",
		"a shared tag must not carry a node across the tenant boundary: %s", body)
	require.Contains(t, body, "alpha-muhasebe-2026.txt")
}

// TestManagerTenantIsolation_OwnTenantUnaffected — every one of the four doors,
// used the way the product intends. A boundary that also blocks the tenant's
// own files is not a fix.
func TestManagerTenantIsolation_OwnTenantUnaffected(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StA, f.RootA, "kendi.txt", "alpha's own file")

	status, body := mtGet(t, f.A, f.URL+"/api/files/read?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "alpha's own file")

	status, body = mtGet(t, f.A,
		f.URL+"/api/files/read?storage="+strconv.FormatInt(f.StA.ID, 10)+"&path=/kendi.txt")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "alpha's own file")

	status, body = mtGet(t, f.A, f.URL+"/api/files/stat?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "kendi.txt")

	status, body = mtGet(t, f.A, f.URL+"/api/files/manager?storage="+strconv.FormatInt(f.StA.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "kendi.txt")
}

// TestManagerTenantIsolation_SupertenantReachesEverything — the platform
// operator answers support tickets about a tenant's files; locking them out
// would be a regression dressed up as a fix.
func TestManagerTenantIsolation_SupertenantReachesEverything(t *testing.T) {
	f := newMTFix(t, true)
	n := f.seedFile(t, f.StB, f.RootB, "muhasebe.txt", "bravo's confidential ledger")

	status, body := mtGet(t, f.Super, f.URL+"/api/files/read?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "confidential ledger")

	status, body = mtGet(t, f.Super, f.URL+"/api/files/stat?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status, body)

	status, body = mtGet(t, f.Super, f.URL+"/api/files/manager?storage="+strconv.FormatInt(f.StB.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "muhasebe.txt")
}

// TestManagerTenantIsolation_SingleTenantInstallUnaffected — the honest half of
// the proof: mode off, identical requests, opposite expectation. Passes on
// `main`.
func TestManagerTenantIsolation_SingleTenantInstallUnaffected(t *testing.T) {
	f := newMTFix(t, false)
	n := f.seedFile(t, f.StB, f.RootB, "ortak.txt", "one instance, one boundary")
	f.seedFile(t, f.StA, f.RootA, "ortak-alpha.txt", "alpha side")

	status, body := mtGet(t, f.A, f.URL+"/api/files/read?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "one instance, one boundary")

	status, body = mtGet(t, f.A,
		f.URL+"/api/files/read?storage="+strconv.FormatInt(f.StB.ID, 10)+"&path=/ortak.txt")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "one instance, one boundary")

	status, body = mtGet(t, f.A, f.URL+"/api/files/stat?id="+strconv.FormatInt(n.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "ortak.txt")

	status, body = mtGet(t, f.A, f.URL+"/api/files/manager?storage="+strconv.FormatInt(f.StB.ID, 10))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "ortak.txt")

	status, body = mtGet(t, f.A, f.URL+"/api/files/manager?action=search&path=alpha://&filter=ortak")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "ortak.txt", "cross-storage search still spans the whole instance: %s", body)
}

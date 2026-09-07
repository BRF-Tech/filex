package handlers_test

// Ownership, not supertenancy: the admin routes that take a raw id.
//
// `/api/admin` requires an admin, and in multi-tenant mode that means an admin
// of ANY tenant. `tenantstore` wraps exactly three list queries; every route
// that takes an `{id}` looks the row up directly. So the question here is not
// "may this tenant touch an instance-wide switch" — it is "does this tenant own
// the row it just named", and the answer, for the routes below, used to be that
// nobody asked.
//
// That makes this the worse half of the multi-tenancy audit. An over-broad
// instance switch is a control problem; reading another customer's share, or
// having their user's password handed back to you in the response body, is a
// cross-customer breach.
//
// ⚠ The refusal is 404, not 403, and deliberately so: a foreign id must be
// indistinguishable from one that never existed, or the endpoint becomes an
// oracle enumerating the platform's other customers. (The instance-wide gate in
// supertenant.go answers 403 because there the *surface* is being refused, not
// a row, and the operator needs to read why.)
//
// ⚠ Every case below is paired with a single-tenant assertion in
// TestOwnership_SingleTenantAdminUnaffected. On an install with multi-tenancy
// off no scope is attached at all, and the ordinary admin must keep reaching
// every row exactly as before — that is the case a careless ownership check
// breaks, and it passes on `main` too.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// ownershipServer is multiTenantServer with the Trash and Versioning services
// wired.
//
// ⚠ This matters for the measurement, not for the fix. The default test server
// leaves `Deps.Trash` and `Deps.Versions` nil, and both handlers answer 500
// "service not initialised" before they ever look at the id — so on the first
// run of this matrix `trash/{id}` and `versions/{id}` came back 500 and looked
// like they were refusing the crossing. They were not refusing anything; the
// handler had not been reached. A harness that cannot reach the code under test
// reports safety it never measured.
func ownershipServer(t *testing.T, multiTenant bool) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()
	return testutil.NewTestServerWith(t,
		func(c *config.Config) { c.MultiTenant = multiTenant },
		func(d *api.Deps) {
			d.Trash = trash.New(d.Store, d.StorageResolver, d.Quota)
			d.Versions = versioning.New(d.Store, d.StorageResolver)
			// Same reason as Trash/Versions: an unwired notify service makes
			// the webhook-config routes answer 503 "notifications offline"
			// before the tenancy gate is reached, which would have read as a
			// refusal it never made.
			d.Notify = notify.New(d.Store, notify.Config{})
		})
}

// ── fixtures ──────────────────────────────────────────────────────────────

// seedStorageFor creates a storage and links it to providerID, which is what
// makes it "that tenant's storage" as far as tenant.Scope.StorageIDs is
// concerned.
func seedStorageFor(t *testing.T, store db.Store, providerID int64, name string) *model.Storage {
	t.Helper()
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name:          name,
		Driver:        "local",
		MountPath:     "/" + name,
		ConfigJSON:    json.RawMessage(`{"root":"/tmp/` + name + `"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, providerID, st.ID))
	return st
}

// seedNodeIn creates a file node inside a storage.
func seedNodeIn(t *testing.T, store db.Store, storageID int64, path string) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: storageID,
		Name:      path,
		Path:      path,
		Type:      model.NodeTypeFile,
		Size:      42,
		Etag:      "etag-" + path,
		PathHash:  mutTestPathHash(storageID, path),
		SeenAt:    time.Now(),
	})
	require.NoError(t, err)
	return n
}

// ownedFixture is one tenant with everything the id-taking admin routes can
// name: a storage, a user, a node, a version, a share, a trashed node, a sync
// run, a grant and an API token.
type ownedFixture struct {
	providerID int64
	adminEmail string
	adminPass  string
	storage    *model.Storage
	user       int64
	node       *model.Node
	version    int64
	share      int64
	trashed    int64
	syncRun    int64
	grant      int64
	token      int64
}

func seedFullTenant(t *testing.T, store db.Store, slug string) *ownedFixture {
	t.Helper()
	ctx := context.Background()

	pid, email, pass := seedTenant(t, store, slug, "admin@"+slug+".test", false)
	f := &ownedFixture{providerID: pid, adminEmail: email, adminPass: pass}

	f.storage = seedStorageFor(t, store, pid, slug+"-store")
	f.user = seedUserIn(t, store, pid, "victim@"+slug+".test")
	f.node = seedNodeIn(t, store, f.storage.ID, slug+"-secret.txt")

	v, err := store.CreateNodeVersion(ctx, &model.NodeVersion{
		NodeID: f.node.ID, VersionN: 1, Size: 42, Etag: "v1",
	})
	require.NoError(t, err)
	f.version = v.ID

	sh, err := store.CreateShare(ctx, &model.Share{
		NodeID: f.node.ID, Token: slug + "-share-token", CreatedBy: &f.user,
		Kind: model.ShareKindDownload,
	})
	require.NoError(t, err)
	f.share = sh.ID

	trash := seedNodeIn(t, store, f.storage.ID, slug+"-deleted.txt")
	require.NoError(t, store.SoftDeleteNode(ctx, trash.ID))
	f.trashed = trash.ID

	run, err := store.CreateSyncRun(ctx, f.storage.ID, "")
	require.NoError(t, err)
	f.syncRun = run.ID

	g, err := store.CreateFileGrant(ctx, &model.FileGrant{
		StorageID: f.storage.ID, PathPrefix: "/", IsDir: true,
		UserID: f.user, Level: "read",
	})
	require.NoError(t, err)
	f.grant = g.ID

	tok, err := store.CreateAPIToken(ctx, &model.APIToken{
		UserID: f.user, Label: slug + "-agent",
		TokenHash: "hash-" + slug, Kind: model.TokenKindApp,
	})
	require.NoError(t, err)
	f.token = tok.ID

	return f
}

// ── the crossing matrix ───────────────────────────────────────────────────

// crossings is every id-taking admin route named in the audit, expressed as a
// request an admin of tenant A makes against a row belonging to tenant B.
//
// `mutates` marks the ones whose damage outlives the response, so the test can
// also measure the row afterwards rather than trusting the status code.
type crossing struct {
	name    string
	method  string
	path    func(*ownedFixture) string
	body    any
	mutates bool
}

var crossings = []crossing{
	// ⚠⚠ The worst one: the response body contains the new cleartext
	// password, so this is not "could disrupt" — it is account takeover of
	// another customer's user, in one request, with the credential returned.
	{"reset another tenant's user password", http.MethodPost,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/users/%d/reset-password", f.user) },
		nil, true},

	// Storage CRUD by raw id. DELETE cascades the node rows.
	{"read another tenant's storage", http.MethodGet,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/storages/%d", f.storage.ID) }, nil, false},
	{"repoint another tenant's storage", http.MethodPatch,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/storages/%d", f.storage.ID) },
		map[string]any{"name": "hijacked", "driver": "local", "config": map[string]any{"root": "/tmp/mine"}}, true},
	{"delete another tenant's storage", http.MethodDelete,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/storages/%d", f.storage.ID) }, nil, true},
	{"trigger sync on another tenant's storage", http.MethodPost,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/storages/%d/sync", f.storage.ID) }, nil, false},

	// Quota, both the nested and the flat legacy shape.
	{"read another tenant's user quota", http.MethodGet,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/quota/%d", f.user) }, nil, false},
	{"set another tenant's user quota", http.MethodPost,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/quota/%d", f.user) },
		map[string]any{"quota_bytes": 1}, true},
	{"read another tenant's user quota (nested)", http.MethodGet,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/users/%d/quota", f.user) }, nil, false},

	// Row-level destruction of another tenant's data.
	{"hard-delete another tenant's version", http.MethodDelete,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/versions/%d", f.version) }, nil, true},
	{"delete another tenant's grant", http.MethodDelete,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/grants/%d", f.grant) }, nil, true},
	{"revoke another tenant's share", http.MethodPost,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/shares/%d/revoke", f.share) }, nil, true},
	{"delete another tenant's share", http.MethodDelete,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/shares/%d", f.share) }, nil, true},
	{"purge another tenant's trash entry", http.MethodDelete,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/trash/%d", f.trashed) }, nil, true},

	// Reads that disclose another tenant's operational detail.
	{"read another tenant's sync run detail", http.MethodGet,
		func(f *ownedFixture) string { return fmt.Sprintf("/api/admin/sync-runs/%d", f.syncRun) }, nil, false},
}

// TestOwnership_ForeignIdsAreRefused is the red proof for the whole class.
func TestOwnership_ForeignIdsAreRefused(t *testing.T) {
	for _, c := range crossings {
		t.Run(c.name, func(t *testing.T) {
			srv, client, store := ownershipServer(t, true)
			// The attacker's own tenant, complete, so nothing fails merely
			// for want of a storage of one's own.
			mine := seedFullTenant(t, store, "diyetlif")
			theirs := seedFullTenant(t, store, "arasboya")
			testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

			status, body := doJSON(t, client, c.method, srv.URL+c.path(theirs), c.body)
			require.Equal(t, http.StatusNotFound, status,
				"a foreign id must be indistinguishable from one that does not exist: %v", body)
		})
	}
}

// TestOwnership_TheDamageDidNotHappen measures the rows rather than the status
// codes. A 404 that still wrote is not a fix.
func TestOwnership_TheDamageDidNotHappen(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	ctx := context.Background()
	mine := seedFullTenant(t, store, "diyetlif")
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	t.Run("the password was not reset and not disclosed", func(t *testing.T) {
		before, err := store.GetUser(ctx, theirs.user)
		require.NoError(t, err)

		status, body := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/reset-password", srv.URL, theirs.user), nil)
		require.Equal(t, http.StatusNotFound, status)
		assert.Empty(t, body["new_password"],
			"another tenant's user password came back in the response body")

		after, err := store.GetUser(ctx, theirs.user)
		require.NoError(t, err)
		assert.Equal(t, before.PasswordHash, after.PasswordHash,
			"the refused reset changed the password anyway")
	})

	t.Run("the storage still exists and still belongs to them", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, theirs.storage.ID), nil)
		require.Equal(t, http.StatusNotFound, status)

		st, err := store.GetStorage(ctx, theirs.storage.ID)
		require.NoError(t, err, "another tenant's storage was deleted")
		assert.Equal(t, theirs.storage.Name, st.Name)
	})

	t.Run("the share was not revoked", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/shares/%d/revoke", srv.URL, theirs.share), nil)
		require.Equal(t, http.StatusNotFound, status)

		sh, err := store.GetShareByID(ctx, theirs.share)
		require.NoError(t, err, "another tenant's share row was destroyed")
		assert.Nil(t, sh.ExpiresAt, "another tenant's share was revoked")
	})

	t.Run("the grant was not deleted", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/grants/%d", srv.URL, theirs.grant), nil)
		require.Equal(t, http.StatusNotFound, status)

		g, err := store.GetFileGrant(ctx, theirs.grant)
		require.NoError(t, err)
		require.NotNil(t, g, "another tenant's RBAC grant was revoked")
	})

	t.Run("the quota was not clamped", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/quota/%d", srv.URL, theirs.user),
			map[string]any{"quota_bytes": 1})
		require.Equal(t, http.StatusNotFound, status)

		_, limit, err := store.GetUserUsage(ctx, theirs.user)
		require.NoError(t, err)
		assert.NotEqual(t, int64(1), limit,
			"another tenant's user was clamped to one byte")
	})
}

// TestOwnership_OwnRowsStillWork is the other half: the ownership check must
// refuse the foreign row, not the route. Without this the test above would
// also pass against a handler that answered 404 to everybody.
func TestOwnership_OwnRowsStillWork(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	mine := seedFullTenant(t, store, "diyetlif")
	seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	for _, c := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"own storage read", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d", mine.storage.ID), nil},
		{"own storage sync-runs", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d/sync-runs", mine.storage.ID), nil},
		{"own storage drift", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d/drift", mine.storage.ID), nil},
		{"own user quota", http.MethodGet, fmt.Sprintf("/api/admin/quota/%d", mine.user), nil},
		{"own user quota (nested)", http.MethodGet, fmt.Sprintf("/api/admin/users/%d/quota", mine.user), nil},
		{"own sync run detail", http.MethodGet, fmt.Sprintf("/api/admin/sync-runs/%d", mine.syncRun), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, body := doJSON(t, client, c.method, srv.URL+c.path, c.body)
			assert.Equal(t, http.StatusOK, status,
				"a tenant admin was refused its OWN row: %v", body)
		})
	}

	t.Run("own user password reset still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/reset-password", srv.URL, mine.user), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.NotEmpty(t, body["new_password"])
	})

	t.Run("own share revoke still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/shares/%d/revoke", srv.URL, mine.share), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("own version delete still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/versions/%d", srv.URL, mine.version), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("own trash purge still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/trash/%d", srv.URL, mine.trashed), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("own grant delete still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/grants/%d", srv.URL, mine.grant), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})
}

// TestOwnership_SupertenantReachesEverything — the platform operator still
// administers every tenant's rows. An ownership check that also locked out the
// supertenant would make the product unoperable.
func TestOwnership_SupertenantReachesEverything(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	email, password := testutil.SeedAdmin(t, store) // provider 1 = supertenant
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, email, password)

	for _, path := range []string{
		fmt.Sprintf("/api/admin/storages/%d", theirs.storage.ID),
		fmt.Sprintf("/api/admin/storages/%d/sync-runs", theirs.storage.ID),
		fmt.Sprintf("/api/admin/quota/%d", theirs.user),
		fmt.Sprintf("/api/admin/sync-runs/%d", theirs.syncRun),
	} {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+path, nil)
		assert.Equal(t, http.StatusOK, status,
			"%s refused the platform operator: %v", path, body)
	}

	status, body := doJSON(t, client, http.MethodPost,
		fmt.Sprintf("%s/api/admin/users/%d/reset-password", srv.URL, theirs.user), nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.NotEmpty(t, body["new_password"],
		"the operator must still be able to recover a tenant's locked-out user")
}

// TestOwnership_SingleTenantAdminUnaffected — the case a careless ownership
// check breaks. Multi-tenancy off means no scope is attached at all, so there
// is no tenant to belong to and the ordinary admin keeps reaching every row.
// ⚠ This passes on `main` too, which is the point: it is the proof that the
// change is invisible to the vast majority of installs, not a new capability.
func TestOwnership_SingleTenantAdminUnaffected(t *testing.T) {
	srv, client, store := ownershipServer(t, false)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	// Rows created with no provider link at all — the shape a single-tenant
	// install actually has.
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "solo", Driver: "local", MountPath: "/solo",
		ConfigJSON: json.RawMessage(`{"root":"/tmp/solo"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	node := seedNodeIn(t, store, st.ID, "solo.txt")
	run, err := store.CreateSyncRun(ctx, st.ID, "")
	require.NoError(t, err)
	victim := seedUserIn(t, store, 1, "plain@solo.test")
	sh, err := store.CreateShare(ctx, &model.Share{
		NodeID: node.ID, Token: "solo-token", CreatedBy: &victim,
		Kind: model.ShareKindDownload,
	})
	require.NoError(t, err)

	for _, c := range []struct {
		name   string
		method string
		path   string
	}{
		{"storage read", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d", st.ID)},
		{"storage sync-runs", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d/sync-runs", st.ID)},
		{"storage drift", http.MethodGet, fmt.Sprintf("/api/admin/storages/%d/drift", st.ID)},
		{"sync run detail", http.MethodGet, fmt.Sprintf("/api/admin/sync-runs/%d", run.ID)},
		{"user quota", http.MethodGet, fmt.Sprintf("/api/admin/quota/%d", victim)},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, body := doJSON(t, client, c.method, srv.URL+c.path, nil)
			assert.Equal(t, http.StatusOK, status,
				"a single-tenant admin was refused: %v", body)
		})
	}

	t.Run("password reset", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/users/%d/reset-password", srv.URL, victim), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.NotEmpty(t, body["new_password"])
	})

	t.Run("share revoke", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost,
			fmt.Sprintf("%s/api/admin/shares/%d/revoke", srv.URL, sh.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("storage delete", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodDelete,
			fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, st.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})
}

// TestOwnership_RefusalIsIndistinguishableFromAMiss is the assertion the 404
// rule actually rests on, and it is easy to skip: a foreign id and an id that
// never existed must produce the SAME status AND the SAME body.
//
// ⚠ Getting the status right and the body wrong leaves the oracle wide open —
// `{"error":"storage not found"}` for a foreign id beside `{"error":"not
// found"}` for an unused one still tells the caller which ids are real.
//
// ⚠⚠ Where the gate runs BEFORE the handler's own lookup, the two answers are
// identical by construction: a confined caller fails `CanAccessStorage` for a
// foreign id and for an absent one alike, so the gate replies first in both
// cases and the wording cannot diverge. Those subtests can therefore never
// fail, and it would be dishonest to present them as proof.
//
// The cases that carry the weight are the ones where a lookup precedes the
// gate — `sync-runs/{id}`, `grants/{id}`, `shares/{id}`, `ai-tokens/{id}`,
// `versions/{id}` — because there the absent id is answered by the handler and
// the foreign id by the gate, and the two have to be made to agree by hand.
// Red-proved: making the sync-run refusal say "sync run not found" while its
// own miss says "not found" fails this test with exactly that diff.
func TestOwnership_RefusalIsIndistinguishableFromAMiss(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	mine := seedFullTenant(t, store, "diyetlif")
	theirs := seedFullTenant(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	// An id far beyond anything seeded: it names nothing, in any tenant.
	const unused = int64(999999)

	for _, c := range []struct {
		name    string
		method  string
		foreign string
		absent  string
	}{
		{"storage read", http.MethodGet,
			fmt.Sprintf("/api/admin/storages/%d", theirs.storage.ID),
			fmt.Sprintf("/api/admin/storages/%d", unused)},
		{"storage delete", http.MethodDelete,
			fmt.Sprintf("/api/admin/storages/%d", theirs.storage.ID),
			fmt.Sprintf("/api/admin/storages/%d", unused)},
		{"user password reset", http.MethodPost,
			fmt.Sprintf("/api/admin/users/%d/reset-password", theirs.user),
			fmt.Sprintf("/api/admin/users/%d/reset-password", unused)},
		{"sync run detail", http.MethodGet,
			fmt.Sprintf("/api/admin/sync-runs/%d", theirs.syncRun),
			fmt.Sprintf("/api/admin/sync-runs/%d", unused)},
		{"version hard delete", http.MethodDelete,
			fmt.Sprintf("/api/admin/versions/%d", theirs.version),
			fmt.Sprintf("/api/admin/versions/%d", unused)},
		{"trash purge", http.MethodDelete,
			fmt.Sprintf("/api/admin/trash/%d", theirs.trashed),
			fmt.Sprintf("/api/admin/trash/%d", unused)},
		{"grant delete", http.MethodDelete,
			fmt.Sprintf("/api/admin/grants/%d", theirs.grant),
			fmt.Sprintf("/api/admin/grants/%d", unused)},
		{"share revoke", http.MethodPost,
			fmt.Sprintf("/api/admin/shares/%d/revoke", theirs.share),
			fmt.Sprintf("/api/admin/shares/%d/revoke", unused)},
		{"share delete", http.MethodDelete,
			fmt.Sprintf("/api/admin/shares/%d", theirs.share),
			fmt.Sprintf("/api/admin/shares/%d", unused)},
		{"quota read", http.MethodGet,
			fmt.Sprintf("/api/admin/quota/%d", theirs.user),
			fmt.Sprintf("/api/admin/quota/%d", unused)},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs, fb := doJSON(t, client, c.method, srv.URL+c.foreign, nil)
			as, ab := doJSON(t, client, c.method, srv.URL+c.absent, nil)
			require.Equal(t, as, fs,
				"a foreign id and an absent id answer different statuses")
			require.Equal(t, ab["error"], fb["error"],
				"a foreign id and an absent id answer different bodies: %q vs %q",
				fb["error"], ab["error"])
		})
	}

	// The two list-shaped endpoints answer an empty 200 rather than a 404 —
	// there, a 404 would be the answer only a real-but-foreign id produces.
	for _, c := range []struct{ name, foreign, absent string }{
		{"storage sync-runs", fmt.Sprintf("/api/admin/storages/%d/sync-runs", theirs.storage.ID),
			fmt.Sprintf("/api/admin/storages/%d/sync-runs", unused)},
		{"storage drift", fmt.Sprintf("/api/admin/storages/%d/drift", theirs.storage.ID),
			fmt.Sprintf("/api/admin/storages/%d/drift", unused)},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs, fb := doJSON(t, client, http.MethodGet, srv.URL+c.foreign, nil)
			as, ab := doJSON(t, client, http.MethodGet, srv.URL+c.absent, nil)
			require.Equal(t, http.StatusOK, fs)
			require.Equal(t, as, fs)
			require.Equal(t, ab["entries"], fb["entries"],
				"a foreign storage's listing differs from an absent one's")
		})
	}
}

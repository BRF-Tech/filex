package handlers_test

// GET /api/files/manager/shared-with-me — the listing behind the navigation
// panel's "Shared with me" (issue #14).
//
// Three things have to be true at once, and each of them fails differently:
// a granted item must appear, an item the caller reaches by their own role
// must NOT (otherwise the list is just "all my files" with a different title),
// and an item in another tenant's storage must NOT (an unfiltered listing is
// the classic cross-tenant leak — see handlers/search.go for the same filter).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// sharedWithMe calls the endpoint and returns the decoded body.
func sharedWithMe(t *testing.T, client *http.Client, base string) struct {
	Files    []map[string]any `json:"files"`
	Storages []string         `json:"storages"`
	Total    int              `json:"total"`
} {
	t.Helper()
	st, raw := doReq(t, client, http.MethodGet, base+"/api/files/manager/shared-with-me", nil)
	require.Equal(t, http.StatusOK, st, "shared-with-me: %s", raw)
	var out struct {
		Files    []map[string]any `json:"files"`
		Storages []string         `json:"storages"`
		Total    int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// pathsOf pulls the adapter-qualified path off each row.
func pathsOf(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if p, ok := r["path"].(string); ok {
			out = append(out, p)
		}
	}
	return out
}

// seedStorage creates a storage straight through the store. Going through the
// admin API instead would drag the admin routes' own tenant gating into a test
// about the file API's gating.
func seedStorage(t *testing.T, store db.Store, name string, rbac bool) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:        name,
		Driver:      "local",
		MountPath:   "/" + name,
		ConfigJSON:  json.RawMessage(`{"root":"/tmp/filex-shared-test/` + name + `"}`),
		SyncMode:    model.SyncModeOnDemand,
		Enabled:     true,
		RBACEnabled: rbac,
	})
	require.NoError(t, err)
	return st
}

// seedNode indexes one node so the projection has something to enrich with.
func seedNode(t *testing.T, store db.Store, st *model.Storage, rel string, dir bool) *model.Node {
	t.Helper()
	typ := model.NodeTypeFile
	if dir {
		typ = model.NodeTypeDirectory
	}
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      rel,
		Path:      "/" + rel,
		PathHash:  pathkey.Hash(st.ID, "/"+rel),
		Type:      typ,
		Size:      7,
		Mime:      "text/plain",
	})
	require.NoError(t, err)
	return n
}

// seedUser creates a plain (non-admin) account and returns it.
func seedSharedUser(t *testing.T, store db.Store, email, pw string) *model.User {
	t.Helper()
	hash, err := authlocal.HashPassword(pw)
	require.NoError(t, err)
	u, err := store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	return u
}

func grant(t *testing.T, store db.Store, st *model.Storage, u *model.User, rel, level string, dir bool) {
	t.Helper()
	_, err := store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: rel, IsDir: dir, UserID: u.ID, Level: level,
	})
	require.NoError(t, err)
}

// A granted folder shows up; a file the caller reaches through their own role
// on an RBAC-off storage does not.
func TestSharedWithMe_GrantedAppears_OwnFilesDoNot(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)

	shared := seedStorage(t, store, "team", true)    // reachable only via grants
	personal := seedStorage(t, store, "mine", false) // reachable by role

	u := seedSharedUser(t, store, "u@test.local", "UserPass1!")
	seedNode(t, store, shared, "Projects", true)
	seedNode(t, store, personal, "notes.txt", false)
	grant(t, store, shared, u, "Projects", model.GrantEditor, true)
	// ⚠ A grant row on the RBAC-off storage too. Without it this case proves
	// nothing: "mine://notes.txt" would be absent simply because no grant names
	// it, and the RBAC-off rule could be deleted with the test still green. The
	// grant here is INERT — every authenticated user already reaches that
	// storage by role — so the item is the caller's own, not shared with them.
	grant(t, store, personal, u, "notes.txt", model.GrantEditor, false)

	client := freshClient(t)
	testutil.LoginAs(t, srv, client, "u@test.local", "UserPass1!")
	got := sharedWithMe(t, client, srv.URL)

	assert.Equal(t, []string{"team://Projects"}, pathsOf(got.Files))
	assert.Equal(t, 1, got.Total)
	assert.Equal(t, []string{"team"}, got.Storages,
		"a storage the caller only reaches through a grant is reported as a shared drive")

	row := got.Files[0]
	assert.Equal(t, "dir", row["type"], "a folder grant lists the folder, not its contents")
	assert.Equal(t, "editor", row["perm"], "the row carries the grant's level, not a re-derived one")
	assert.Equal(t, true, row["shared"])

	// The file on the RBAC-off storage is the caller's own by role. It must not
	// be here — otherwise "Shared with me" is "everything", relabelled.
	assert.NotContains(t, pathsOf(got.Files), "mine://notes.txt")
}

// A grant a user never received is not theirs to see, and a grant on a storage
// outside their tenant is invisible even though the grant row names them.
func TestSharedWithMe_TenantScopeAndOtherUsersGrants(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.MultiTenant = true })
	ctx := context.Background()

	mine := seedStorage(t, store, "tenant-a-drive", true)
	theirs := seedStorage(t, store, "tenant-b-drive", true)

	pa, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "tenant-a", Name: "A", Host: "a.test", AuthType: model.AuthTypeLocal, Enabled: true,
	})
	require.NoError(t, err)
	pb, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "tenant-b", Name: "B", Host: "b.test", AuthType: model.AuthTypeLocal, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, pa.ID, mine.ID))
	require.NoError(t, store.LinkProviderStorage(ctx, pb.ID, theirs.ID))

	u := seedSharedUser(t, store, "a@test.local", "UserPass1!")
	require.NoError(t, store.SetUserProvider(ctx, u.ID, pa.ID, ""))
	other := seedSharedUser(t, store, "b@test.local", "UserPass1!")

	seedNode(t, store, mine, "Ours", true)
	seedNode(t, store, theirs, "Theirs", true)
	// Same user, a grant in the OTHER tenant's storage: the row exists and
	// names them, so only the tenant filter can keep it out.
	grant(t, store, mine, u, "Ours", model.GrantViewer, true)
	grant(t, store, theirs, u, "Theirs", model.GrantOwner, true)
	// …and a grant that belongs to somebody else entirely.
	grant(t, store, mine, other, "NotMine", model.GrantOwner, true)

	client := freshClient(t)
	testutil.LoginAs(t, srv, client, "a@test.local", "UserPass1!")
	got := sharedWithMe(t, client, srv.URL)

	assert.Equal(t, []string{"tenant-a-drive://Ours"}, pathsOf(got.Files))
	assert.Equal(t, []string{"tenant-a-drive"}, got.Storages)
	assert.NotContains(t, pathsOf(got.Files), "tenant-b-drive://Theirs",
		"a grant in another tenant's storage must never be listed")
	assert.NotContains(t, pathsOf(got.Files), "tenant-a-drive://NotMine")
}

// A grant on a path the indexer has never walked still has to be listed —
// dropping it would make the panel quietly incomplete for exactly the folder
// somebody just shared.
func TestSharedWithMe_UnindexedGrantStillListed(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	shared := seedStorage(t, store, "team", true)
	u := seedSharedUser(t, store, "u@test.local", "UserPass1!")
	grant(t, store, shared, u, "Fresh Folder", model.GrantViewer, true)
	// A whole-storage grant is the shared DRIVE, not an item in the list.
	grant(t, store, shared, u, "", model.GrantViewer, true)

	client := freshClient(t)
	testutil.LoginAs(t, srv, client, "u@test.local", "UserPass1!")
	got := sharedWithMe(t, client, srv.URL)

	assert.Equal(t, []string{"team://Fresh Folder"}, pathsOf(got.Files))
	assert.Equal(t, "dir", got.Files[0]["type"])
	assert.Equal(t, "Fresh Folder", got.Files[0]["basename"])
}

// Empty must serialise as [] — `null` is a crash in every consumer, and "I
// have been shared nothing" is the state most accounts are in.
func TestSharedWithMe_EmptyIsArrayNotNull(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	_, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/files/manager/shared-with-me", nil)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, "[]", string(body["files"]), "got %s", raw)
	assert.Equal(t, "[]", string(body["storages"]), "got %s", raw)
}

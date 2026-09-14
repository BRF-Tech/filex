package handlers_test

// GET /api/files/quota/storages — "how full is this drive" for somebody who is
// not an administrator.
//
// The gate these tests hold is not "the endpoint returns a number". It is that
// the set of drives a person can MEASURE is exactly the set they can OPEN:
// anything else hands a non-admin a fact about a storage they were never shown.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type usageBody struct {
	Storages []struct {
		Name      string `json:"name"`
		UsedBytes int64  `json:"used_bytes"`
		FileCount int64  `json:"file_count"`
	} `json:"storages"`
}

func readUsage(t *testing.T, raw []byte) usageBody {
	t.Helper()
	var out usageBody
	require.NoError(t, json.Unmarshal(raw, &out), "body: %s", raw)
	return out
}

func (u usageBody) byName(name string) (int64, int64, bool) {
	for _, s := range u.Storages {
		if s.Name == name {
			return s.UsedBytes, s.FileCount, true
		}
	}
	return 0, 0, false
}

// mkUsageStorage creates an enabled storage through the admin API and returns
// its id.
func mkUsageStorage(t *testing.T, url string, admin *http.Client, name string, rbac bool) int64 {
	t.Helper()
	st, raw := doReq(t, admin, http.MethodPost, url+"/api/admin/storages", model.Storage{
		Name:          name,
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    json.RawMessage(`{"root":"/tmp/filex-usage-test"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
		RBACEnabled:   rbac,
	})
	require.Equal(t, http.StatusOK, st, "create storage %s: %s", name, raw)
	var row struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &row))
	require.NotZero(t, row.ID)
	return row.ID
}

// TestStorageUsage_RBACHidesTheDrivesYouCannotOpen is the whole point of the
// endpoint: a regular account holding one grant on one storage is told how big
// THAT storage is, and is not told that the other storage exists — not its
// name, not its size, not a zero.
//
// ⚠ A "0 bytes" row would fail this test on purpose. Reporting a storage with
// no figure still answers "there is a drive here called theirs", which is the
// census the RBAC filter exists to prevent.
func TestStorageUsage_RBACHidesTheDrivesYouCannotOpen(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	ctx := context.Background()
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)

	mineID := mkUsageStorage(t, srv.URL, adminClient, "mine", true)
	theirsID := mkUsageStorage(t, srv.URL, adminClient, "theirs", true)

	mkNode := func(storageID int64, p string, size int64) {
		_, err := store.CreateNode(ctx, &model.Node{
			StorageID: storageID,
			Name:      p[strings.LastIndex(p, "/")+1:],
			Path:      p,
			PathHash:  mutTestPathHash(storageID, p),
			Type:      model.NodeTypeFile,
			Size:      size,
		})
		require.NoError(t, err)
	}
	mkNode(mineID, "/alfa/doc.txt", 1200)
	mkNode(mineID, "/alfa/note.txt", 800)
	mkNode(theirsID, "/gizli/plan.txt", 999000)

	uid := createUser(t, srv.URL, adminClient, "usage-u@test.local", "UserPass1!", model.RoleUser)
	st, raw := doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "mine://alfa", "user_id": uid, "level": "viewer"})
	require.Equal(t, http.StatusOK, st, "grant viewer on mine://alfa: %s", raw)

	userClient := freshClient(t)
	testutil.LoginAs(t, srv, userClient, "usage-u@test.local", "UserPass1!")

	st, raw = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/quota/storages", nil)
	require.Equal(t, http.StatusOK, st, "body: %s", raw)
	body := readUsage(t, raw)

	bytes, files, ok := body.byName("mine")
	require.True(t, ok, "the drive the user holds a grant on must be reported: %s", raw)
	assert.Equal(t, int64(2000), bytes, "sum of the live file rows of that storage")
	assert.Equal(t, int64(2), files)

	_, _, leaked := body.byName("theirs")
	assert.False(t, leaked,
		"a storage the caller holds no grant on must not appear at all — not even as a 0: %s", raw)

	// And the same request as the admin sees both, so the test above is
	// measuring the RBAC filter and not an empty database.
	st, raw = doReq(t, adminClient, http.MethodGet, srv.URL+"/api/files/quota/storages", nil)
	require.Equal(t, http.StatusOK, st)
	adminBody := readUsage(t, raw)
	_, _, adminSeesMine := adminBody.byName("mine")
	otherBytes, _, adminSeesTheirs := adminBody.byName("theirs")
	assert.True(t, adminSeesMine)
	require.True(t, adminSeesTheirs, "admin must see both drives, else the test above proves nothing")
	assert.Equal(t, int64(999000), otherBytes)
}

// TestStorageUsage_SameListAsTheExplorerRoot pins the endpoint to the filter it
// borrows. The explorer's own root listing (GET /api/files/manager) answers a
// storages array; if the two ever disagree, one of them is showing a drive the
// other hides, and it does not matter which.
func TestStorageUsage_SameListAsTheExplorerRoot(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)

	mkUsageStorage(t, srv.URL, adminClient, "open", false)
	mkUsageStorage(t, srv.URL, adminClient, "closed", true)
	mkUsageStorage(t, srv.URL, adminClient, "granted", true)

	uid := createUser(t, srv.URL, adminClient, "usage-p@test.local", "UserPass1!", model.RoleUser)
	st, raw := doReq(t, adminClient, http.MethodPost, srv.URL+"/api/files/permissions",
		map[string]any{"path": "granted://", "user_id": uid, "level": "viewer"})
	require.Equal(t, http.StatusOK, st, "grant on granted root: %s", raw)

	userClient := freshClient(t)
	testutil.LoginAs(t, srv, userClient, "usage-p@test.local", "UserPass1!")

	st, raw = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/manager?action=index&path=", nil)
	require.Equal(t, http.StatusOK, st, "manager root: %s", raw)
	var root struct {
		Storages []string `json:"storages"`
	}
	require.NoError(t, json.Unmarshal(raw, &root))
	require.NotEmpty(t, root.Storages, "the explorer root must list something, else this proves nothing")

	st, raw = doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/quota/storages", nil)
	require.Equal(t, http.StatusOK, st)
	usage := readUsage(t, raw)

	got := make([]string, 0, len(usage.Storages))
	for _, s := range usage.Storages {
		got = append(got, s.Name)
	}
	assert.ElementsMatch(t, root.Storages, got,
		"the drives you can measure must be exactly the drives you can open")
}

// TestStorageUsage_RequiresAuth — the route lives in the authenticated group,
// so an anonymous caller is refused by the middleware rather than answered
// with a list of drive names and sizes.
func TestStorageUsage_RequiresAuth(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)
	st, raw := doReq(t, freshClient(t), http.MethodGet, srv.URL+"/api/files/quota/storages", nil)
	assert.Equal(t, http.StatusUnauthorized, st, "body: %s", raw)
}

// TestStorageUsage_RootConfinedTokenGetsNoWholeStorageTotal — a token confined
// to one sub-folder is looking at a folder, not a drive. The whole-storage
// total counts bytes it cannot list, so it is not reported.
func TestStorageUsage_RootConfinedTokenGetsNoWholeStorageTotal(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	ctx := context.Background()
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)

	sid := mkUsageStorage(t, srv.URL, adminClient, "confined", false)
	_, err := store.CreateNode(ctx, &model.Node{
		StorageID: sid,
		Name:      "big.bin",
		Path:      "/other/big.bin",
		PathHash:  mutTestPathHash(sid, "/other/big.bin"),
		Type:      model.NodeTypeFile,
		Size:      5000000,
	})
	require.NoError(t, err)

	adminUser, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	token := testutil.NewAPIToken(t, store, adminUser.ID, "root:confined://projects/p1")

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/files/quota/storages", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body usageBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Empty(t, body.Storages,
		"a sub-folder token must not be handed the whole storage total")
}

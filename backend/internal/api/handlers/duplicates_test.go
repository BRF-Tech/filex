package handlers_test

// GET /api/admin/duplicates — duplicate-file report (v0.2 "Bul" S2).
// Exercised through the real router so the admin auth chain is covered too.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// seedDupStorage creates a storage + a set of file nodes for the report.
func seedDupStorage(t *testing.T, store db.Store) int64 {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "dup-test", Driver: "local", MountPath: "/tmp/dup-test", Enabled: true,
	})
	require.NoError(t, err)
	return st.ID
}

func seedDupNode(t *testing.T, store db.Store, storageID int64, name string, size int64, etag string) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: storageID,
		Name:      name,
		Path:      "docs/" + name,
		PathHash:  "hash-" + name,
		Type:      model.NodeTypeFile,
		Size:      size,
		Etag:      etag,
		SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

type dupNodeJSON struct {
	ID        int64  `json:"id"`
	StorageID int64  `json:"storage_id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Etag      string `json:"etag"`
}

type dupGroupJSON struct {
	Key        string        `json:"key"`
	Size       int64         `json:"size"`
	Count      int           `json:"count"`
	TotalWaste int64         `json:"total_waste"`
	Nodes      []dupNodeJSON `json:"nodes"`
}

type dupReportJSON struct {
	Groups []dupGroupJSON `json:"groups"`
}

func TestAdminDuplicates_GroupsWasteAndOrder(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	stID := seedDupStorage(t, store)

	// Pair A: 2 × 100 bytes, etag "aaa" → waste 100.
	seedDupNode(t, store, stID, "a1.txt", 100, "aaa")
	seedDupNode(t, store, stID, "a2.txt", 100, "aaa")
	// A soft-deleted third copy must NOT count.
	ghost := seedDupNode(t, store, stID, "a3.txt", 100, "aaa")
	require.NoError(t, store.SoftDeleteNode(context.Background(), ghost.ID))

	// Triple B: 3 × 500 bytes, etag "bbb" → waste 1000 (sorts first).
	seedDupNode(t, store, stID, "b1.bin", 500, "bbb")
	seedDupNode(t, store, stID, "b2.bin", 500, "bbb")
	seedDupNode(t, store, stID, "b3.bin", 500, "bbb")

	// Singletons + noise that must be excluded:
	seedDupNode(t, store, stID, "c-single.txt", 100, "ccc") // unique etag
	seedDupNode(t, store, stID, "d1-noetag.txt", 100, "")   // empty etag pair —
	seedDupNode(t, store, stID, "d2-noetag.txt", 100, "")   // never grouped
	seedDupNode(t, store, stID, "e-samesize.txt", 500, "eee")

	resp, err := client.Get(srv.URL + "/api/admin/duplicates")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got dupReportJSON
	testutil.ReadJSON(t, resp, &got)
	require.Len(t, got.Groups, 2, "only the two real duplicate groups")

	// Order: total_waste desc → B (1000) before A (100).
	b := got.Groups[0]
	assert.Equal(t, "500-bbb", b.Key)
	assert.Equal(t, int64(500), b.Size)
	assert.Equal(t, 3, b.Count)
	assert.Equal(t, int64(1000), b.TotalWaste, "waste = (count-1)*size")
	require.Len(t, b.Nodes, 3)
	assert.Equal(t, stID, b.Nodes[0].StorageID)
	assert.Equal(t, "docs/b1.bin", b.Nodes[0].Path)
	assert.Equal(t, "b1.bin", b.Nodes[0].Name)
	assert.Equal(t, "bbb", b.Nodes[0].Etag)
	assert.NotZero(t, b.Nodes[0].ID)

	a := got.Groups[1]
	assert.Equal(t, "100-aaa", a.Key)
	assert.Equal(t, 2, a.Count, "soft-deleted copy must not count")
	assert.Equal(t, int64(100), a.TotalWaste)
	for _, n := range a.Nodes {
		assert.NotEqual(t, ghost.ID, n.ID, "soft-deleted node leaked into the report")
	}
}

func TestAdminDuplicates_MinSizeAndLimit(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	stID := seedDupStorage(t, store)
	seedDupNode(t, store, stID, "s1.txt", 10, "small")
	seedDupNode(t, store, stID, "s2.txt", 10, "small")
	seedDupNode(t, store, stID, "l1.bin", 9000, "large")
	seedDupNode(t, store, stID, "l2.bin", 9000, "large")

	// min_size filters the small pair out in SQL.
	resp, err := client.Get(srv.URL + "/api/admin/duplicates?min_size=100")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got dupReportJSON
	testutil.ReadJSON(t, resp, &got)
	require.Len(t, got.Groups, 1)
	assert.Equal(t, "9000-large", got.Groups[0].Key)

	// limit truncates to the top-waste group.
	resp2, err := client.Get(srv.URL + "/api/admin/duplicates?limit=1")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var got2 dupReportJSON
	testutil.ReadJSON(t, resp2, &got2)
	require.Len(t, got2.Groups, 1)
	assert.Equal(t, "9000-large", got2.Groups[0].Key, "highest total_waste survives the limit")
}

func TestAdminDuplicates_EmptyReportIsEmptyArray(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/duplicates")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]any
	testutil.ReadJSON(t, resp, &got)
	groups, ok := got["groups"].([]any)
	require.True(t, ok, `"groups" must be a JSON array (not null), got %T`, got["groups"])
	assert.Empty(t, groups)
}

func TestAdminDuplicates_AdminOnly(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	// Unauthenticated → 401.
	resp, err := client.Get(srv.URL + "/api/admin/duplicates")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Non-admin → 403.
	testutil.SeedRegularUser(t, store, "dupuser@test.local", "DupUserPass1!")
	testutil.LoginAs(t, srv, client, "dupuser@test.local", "DupUserPass1!")
	resp2, err := client.Get(srv.URL + "/api/admin/duplicates")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp2.StatusCode)
}

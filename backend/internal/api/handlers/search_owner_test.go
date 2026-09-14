package handlers_test

// /api/files/search — what a HIT says about itself beyond its own name.
//
// ⚠⚠ Both facts pinned here were MISSING from this endpoint while the same
// two were already answered by the folder listing next door, and both are the
// kind of gap that reads as a working response:
//
//   - a hit carried a numeric `storage_id` and no drive NAME, so a client in
//     multi-storage mode could not build the `name://path` it needs to open
//     one. That is the defect `model.Node.Storage` was added for (see its own
//     comment: the recently-opened tray listed files that did nothing when
//     clicked) and search was the surface left out of the fix;
//   - `owner_id` was on the wire (migrations 00004 + 00038) but the display
//     name was not hydrated and nothing said whether the row is YOURS. An id
//     the client cannot compare against anything is a number, not an owner —
//     and an Owner filter fed by it answers "System" for everybody, confidently.
//
// Measured through HTTP rather than against describeHits directly, because the
// serialization is the thing under test: a field the handler fills and the
// JSON tag omits is exactly as absent as one it never filled.

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// searchOwnerItem reads the four keys this file is about. Separate from
// `searchRespItem` next door on purpose: that one pins snippet/matched, and a
// shared struct would make either test's failure look like the other's.
type searchOwnerItem struct {
	Name          string `json:"name"`
	Storage       string `json:"storage"`
	OwnerID       *int64 `json:"owner_id"`
	OwnerName     string `json:"owner_name"`
	OwnerSelf     bool   `json:"owner_self"`
	LastActorName string `json:"last_actor_name"`
}

func doOwnerSearch(t *testing.T, base string, client *http.Client, q string) map[string]searchOwnerItem {
	t.Helper()
	resp, err := client.Get(base + "/api/files/search?scope=name&q=" + url.QueryEscape(q))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Results []searchOwnerItem `json:"results"`
	}
	testutil.ReadJSON(t, resp, &body)
	out := map[string]searchOwnerItem{}
	for _, r := range body.Results {
		out[r.Name] = r
	}
	return out
}

// seedOwnedSearch indexes three files that differ ONLY in who owns them:
// the signed-in admin, another account, and nobody at all.
func seedOwnedSearch(t *testing.T) (base string, client *http.Client, store db.Store) {
	t.Helper()

	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Index = idx
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	me, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserDisplayName(ctx, me.ID, "Ada Lovelace"))
	other, err := store.CreateUser(ctx, "grace@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserDisplayName(ctx, other.ID, "Grace Hopper"))

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "vault", Driver: "local", MountPath: "/data", Enabled: true,
	})
	require.NoError(t, err)

	mk := func(name string, owner *int64) {
		p := "/" + name
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID,
			Name:      name,
			Path:      p,
			PathHash:  searchTestPathHash(st.ID, p),
			Type:      model.NodeTypeFile,
			Mime:      "text/plain",
			Size:      12,
			OwnerID:   owner,
		})
		require.NoError(t, err)
		require.NoError(t, idx.IndexNode(ctx, n))
	}
	mk("ledger-mine.txt", &me.ID)
	mk("ledger-hers.txt", &other.ID)
	mk("ledger-nobody.txt", nil)

	return srv.URL, client, store
}

// TestSearchHits_CarryTheirDrive — a hit says which storage it came from.
func TestSearchHits_CarryTheirDrive(t *testing.T) {
	base, client, _ := seedOwnedSearch(t)
	got := doOwnerSearch(t, base, client, "ledger")
	require.Len(t, got, 3, "fixture did not come back: %+v", got)
	for name, hit := range got {
		assert.Equal(t, "vault", hit.Storage,
			"%s came back with no drive name — the client cannot build vault://%s from it", name, name)
	}
}

// TestSearchHits_CarryOwnership — who owns it, by name, and whether that is
// the caller. The three rows differ only in ownership, so a filter that reads
// these keys can tell them apart and one that cannot must fail here.
func TestSearchHits_CarryOwnership(t *testing.T) {
	base, client, _ := seedOwnedSearch(t)
	got := doOwnerSearch(t, base, client, "ledger")
	require.Len(t, got, 3, "fixture did not come back: %+v", got)

	mine := got["ledger-mine.txt"]
	require.NotNil(t, mine.OwnerID, "the caller's own file lost its owner_id")
	assert.True(t, mine.OwnerSelf, "the caller's own file must say owner_self")
	assert.Equal(t, "Ada Lovelace", mine.OwnerName,
		"the caller's own file must come back with a NAME, not just an id")

	hers := got["ledger-hers.txt"]
	require.NotNil(t, hers.OwnerID, "somebody else's file lost its owner_id")
	assert.False(t, hers.OwnerSelf, "somebody else's file must NOT say owner_self")
	assert.Equal(t, "Grace Hopper", hers.OwnerName)

	// ⚠ The honest word for ownerless is "no key at all" — the same thing the
	// listing projection means by it. A system row that arrived carrying
	// somebody's name would be the worst of the three outcomes.
	nobody := got["ledger-nobody.txt"]
	assert.Nil(t, nobody.OwnerID, "a system row must carry no owner_id")
	assert.Empty(t, nobody.OwnerName)
	assert.False(t, nobody.OwnerSelf)
}

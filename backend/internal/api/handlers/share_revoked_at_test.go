package handlers_test

// A revoked link reads as REVOKED, not as expired (00053).
//
// ⚠ QA, 2026-09-21: revoking set `expires_at` to now and nothing else, so My
// shares said "Süresi doldu" for a link its owner had revoked a minute
// earlier, and the admin Shares page listed it as "expires … 9 seconds ago"
// while its `revoked` badge waited for a field no server ever sent. Both
// revoke doors — the owner's (`DELETE /api/files/share/{id}`) and the
// administrator's (`POST /api/admin/shares/{id}/revoke`) — must leave the
// word behind, and both listings must carry it. A link that merely expired
// must NOT carry it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

type revokedRow struct {
	Share struct {
		ID        int64      `json:"id"`
		ExpiresAt *time.Time `json:"expires_at"`
		RevokedAt *time.Time `json:"revoked_at"`
	} `json:"share"`
}

func revokedRows(t *testing.T, c *http.Client, url string) map[int64]revokedRow {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", url, raw)
	var body struct {
		Items   []revokedRow `json:"items"`
		Entries []revokedRow `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	out := map[int64]revokedRow{}
	for _, r := range append(body.Items, body.Entries...) {
		out[r.Share.ID] = r
	}
	return out
}

func TestShareRevoke_ListingsSayRevokedNotExpired(t *testing.T) {
	f, ownerClient, as := newMineFixture(t, mineSecretKey)
	admin := as(f.adminEmail, f.adminPw)

	byOwner := mintShare(t, f, ownerClient, "111111")
	byAdmin := mintShare(t, f, ownerClient, "222222")

	// The owner revokes one from My shares…
	req, _ := http.NewRequest(http.MethodDelete, f.srv.URL+"/api/files/share/"+strconv.FormatInt(byOwner, 10), nil)
	resp, err := ownerClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// …the administrator another from Shares…
	resp, err = admin.Post(f.srv.URL+"/api/admin/shares/"+strconv.FormatInt(byAdmin, 10)+"/revoke", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// …and a third simply ran out (the API will not mint a link that is
	// already past its expiry, so it is written as the store would hold it).
	past := time.Now().Add(-time.Hour)
	owner := f.ownerID
	sh, err := f.store.CreateShare(context.Background(), &model.Share{
		NodeID: f.node.ID, Token: "ranout0000000000", ExpiresAt: &past, CreatedBy: &owner,
	})
	require.NoError(t, err)
	expired := sh.ID

	for name, rows := range map[string]map[int64]revokedRow{
		"My shares":    revokedRows(t, ownerClient, f.srv.URL+"/api/shares/"),
		"admin Shares": revokedRows(t, admin, f.srv.URL+"/api/admin/shares/"),
	} {
		for _, id := range []int64{byOwner, byAdmin} {
			r, ok := rows[id]
			require.True(t, ok, "%s: share %d missing from the listing", name, id)
			assert.NotNil(t, r.Share.RevokedAt, "%s: share %d was revoked and must say so (revoked_at)", name, id)
			assert.NotNil(t, r.Share.ExpiresAt, "%s: revoking still ends the link through its expiry", name)
		}
		r := rows[expired]
		assert.Nil(t, r.Share.RevokedAt, "%s: a link that only EXPIRED must not read as revoked", name)
	}
}

package handlers_test

// A person is named ONE way on every admin screen (model.PersonLabel: display
// name, else username, else e-mail).
//
// ⚠ QA, 2026-09-21: the same administrator was "admin2" in the explorer's
// Owner column and "admin@local" in the audit log, the Shares page and the
// dashboard — those three printed the e-mail because their rows carried
// nothing else. They carry the name now, the e-mail stays as the second line.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestAdminRows_NameThePersonTheWayTheExplorerDoes(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	ctx := context.Background()
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	require.NoError(t, store.SetUserUsername(ctx, admin.ID, "boss"))
	testutil.LoginAs(t, srv, client, email, pw)

	// The Owner column's own lookup is the yardstick.
	names, err := store.GetUserDisplayNames(ctx, []int64{admin.ID})
	require.NoError(t, err)
	require.Equal(t, "boss", names[admin.ID], "no display name: the username, not the address")

	require.NoError(t, store.InsertAuditEntry(ctx, &model.AuditEntry{UserID: &admin.ID, Action: "user.update", TargetType: "user"}))
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "labels", Driver: "local", MountPath: "/labels", ConfigJSON: json.RawMessage(`{"root":"/tmp/labels"}`), SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true})
	require.NoError(t, err)
	node := seedNodeIn(t, store, st.ID, "a.txt")
	_, err = store.CreateShare(ctx, &model.Share{NodeID: node.ID, Token: "label-token", CreatedBy: &admin.ID, Kind: model.ShareKindDownload})
	require.NoError(t, err)

	get := func(path string, out any) {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}

	var audit struct {
		Entries []struct {
			UserEmail string `json:"user_email"`
			UserName  string `json:"user_name"`
		} `json:"entries"`
	}
	get("/api/admin/audit?limit=50", &audit)
	require.NotEmpty(t, audit.Entries)
	for _, e := range audit.Entries {
		if e.UserEmail == email {
			assert.Equal(t, "boss", e.UserName, "audit")
		}
	}

	var shares struct {
		Items []struct {
			CreatorEmail string `json:"creator_email"`
			CreatorName  string `json:"creator_name"`
		} `json:"items"`
	}
	get("/api/admin/shares?limit=10&offset=0", &shares)
	require.Len(t, shares.Items, 1)
	assert.Equal(t, "boss", shares.Items[0].CreatorName, "shares")
	assert.Equal(t, email, shares.Items[0].CreatorEmail, "the address stays, as the second line")

	var dash struct {
		Recent []struct {
			UserName string `json:"user_name"`
		} `json:"recent_activity"`
	}
	get("/api/admin/dashboard", &dash)
	require.NotEmpty(t, dash.Recent)
	assert.Equal(t, "boss", dash.Recent[0].UserName, "dashboard")
}

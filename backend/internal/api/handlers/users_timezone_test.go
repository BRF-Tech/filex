package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An account an operator creates without naming a time zone has NO zone.
//
// It used to be stored as "UTC", and the web app honours the account's zone —
// so the new person's every date was drawn in UTC while the same explorer
// embedded on another page drew their browser's clock (measured 2026-09-14:
// 11:57 PM beside 4:57 PM for one file). An empty zone is what tells every
// client "use the viewer's device".
func TestCreateUser_WithoutTimezoneHasNone(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, created := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "no-zone@test.local", "password": "Whatever!1",
	})
	require.Equal(t, http.StatusOK, status)
	// ⚠ The literal, not model.TimezoneUnset: a test that compares against the
	// constant goes green again the day somebody sets the constant to "UTC".
	require.Equal(t, "", created["timezone"],
		"the create response names a zone nobody chose")
	u, err := store.GetUserByEmail(ctx, "no-zone@test.local")
	require.NoError(t, err)
	require.Equal(t, "", u.Timezone, "the stored account names a zone nobody chose")

	// A zone that IS given is kept exactly.
	status, created = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]string{
		"email": "tokyo@test.local", "password": "Whatever!1", "timezone": "Asia/Tokyo",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "Asia/Tokyo", created["timezone"])
}

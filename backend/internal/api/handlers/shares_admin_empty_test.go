package handlers_test

// GET /api/admin/shares must always hand back arrays.
//
// `ListAllShares` returns a nil slice when there is nothing to list, and a nil
// Go slice marshals to JSON `null`. Both envelopes this endpoint documents —
// `items` for the admin SPA's PaginatedResponse and `entries` for older
// consumers — are consumed with `.length` / `.map` / `v-for`, so a brand-new
// instance with no shares served a null and broke the very page that exists to
// say "no shares yet".
//
// Found by 77-share.spec.ts ("admin GET /api/admin/shares carries BOTH SPA +
// legacy envelopes"), which had been failing on `Array.isArray(body.items)`.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestAdminShares_EmptyListIsArrayNotNull(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/shares?limit=10&offset=0")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Assert on the WIRE bytes as well as the decoded value: `null` and `[]`
	// both decode into a nil-ish any, so a decode-only check would pass
	// against the bug this test exists for.
	assert.NotContains(t, string(raw), `"items":null`, "items must serialise as [] when empty")
	assert.NotContains(t, string(raw), `"entries":null`, "entries must serialise as [] when empty")

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &body))

	for _, key := range []string{"items", "entries"} {
		require.Contains(t, body, key)
		var arr []any
		require.NoError(t, json.Unmarshal(body[key], &arr), "%s must decode as an array", key)
		assert.Empty(t, arr)
		assert.Equal(t, "[]", string(body[key]), "%s must be [] on the wire", key)
	}
}

package handlers_test

// The per-user metadata lists must serialise as arrays even when empty.
//
// `ListNodesByUserMeta` / `ListNodesByTag` / `ListAllTags` all return a nil
// slice when there is nothing to return, and a nil Go slice marshals to JSON
// `null`. Every consumer of these endpoints treats them as lists — the star
// column, the recently-opened panel, the tag picker — so `null` is a crash,
// and it lands in the state where the list is MOST likely to be read: a fresh
// account that has starred nothing and opened nothing.
//
// Found by 83-meta-and-markdown.spec.ts ("recently-opened tracks the last
// POSTed node"), which was reading `{"limit":10,"nodes":null}`.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestMetaLists_EmptyAreArraysNotNull(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	for _, tc := range []struct {
		name string
		path string
		key  string
	}{
		{"starred", "/api/files/manager/star/list?limit=10", "nodes"},
		{"recent", "/api/files/manager/recent?limit=10", "nodes"},
		{"tagged", "/api/files/manager/tagged?tag=nothing-has-this", "nodes"},
		{"all tags", "/api/files/manager/tags/all", "tags"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.Get(srv.URL + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var body map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &body))
			require.Contains(t, body, tc.key)
			// Compare the raw bytes: `null` and `[]` both unmarshal into a
			// nil-ish any, so a decode-only assertion would pass on the bug.
			assert.Equal(t, "[]", string(body[tc.key]),
				"%s.%s must be [] on the wire, got %s", tc.path, tc.key, string(raw))
		})
	}
}

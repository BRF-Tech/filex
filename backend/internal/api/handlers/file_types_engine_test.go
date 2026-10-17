package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Default apps → Office engine (filex 0.57): the office engine's back ends
// (an app's engine, the Document Server) in the order they are asked, the
// administrator's order kept as a rule of the `engine` capability, audited
// like a kind's. RED before 0.57: GET had no `engines` and the routes did
// not exist.
func TestFileTypes_TheOfficeEngineRow(t *testing.T) {
	f := newAppFixture(t, nil)

	code, body := f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code, body)
	engines, ok := body["engines"].([]any)
	require.True(t, ok, "the answer lists the engines: %v", body)
	require.Len(t, engines, 1)
	row := engines[0].(map[string]any)
	assert.Equal(t, "office", row["engine"])
	assert.Empty(t, handlerIDs(row["on"]), "no app brings an engine and no Document Server is connected here")
	assert.Equal(t, false, row["custom"])
	_, hasStates := row["states"].(map[string]any)
	assert.True(t, hasStates, "how each back end stands, even when none is said")

	// Nothing to put first: refused in the reader's words, never stored.
	code, body = f.jsonReq(t, http.MethodPut, "/api/admin/file-types/engines/office", map[string]any{"order": []string{"onlyoffice"}})
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Equal(t, "invalid_rule", body["error"])

	// An engine filex does not have.
	code, _ = f.jsonReq(t, http.MethodPut, "/api/admin/file-types/engines/video", map[string]any{"order": []string{}})
	assert.Equal(t, http.StatusNotFound, code)

	// Back to the default: audited like a kind's reset, the engine as target.
	code, body = f.jsonReq(t, http.MethodDelete, "/api/admin/file-types/engines/office", nil)
	require.Equal(t, http.StatusOK, code, body)
	rows, err := f.store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == "file_association.reset" && r.TargetID == "office" {
			found = true
			assert.Equal(t, "office", r.Metadata["engine"])
		}
	}
	assert.True(t, found, "file_association.reset was written for the engine")
}

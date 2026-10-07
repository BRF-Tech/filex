package handlers_test

// "Shared with me" for a caller confined to a folder (filex Roadmap #185).
//
// The rows were held to the root, the storage names were not: `storages[]`
// was filled before the root's filter, so a token confined to one folder was
// told the name of every storage its account held a grant on. A storage is
// named now only when one of the grants on it survives the root - inside it,
// or covering it.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestSharedWithMe_AFolderConfinedCallerIsToldOnlyTheStoragesOfItsRoot(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	useProductionAuthChain(t, store)

	team := seedStorage(t, store, "team", true)
	other := seedStorage(t, store, "other", true)
	whole := seedStorage(t, store, "whole", true)
	u := seedSharedUser(t, store, "u@test.local", "UserPass1!")
	seedNode(t, store, team, "Projects", true)
	grant(t, store, team, u, "Projects", model.GrantEditor, true)
	grant(t, store, other, u, "Docs", model.GrantViewer, true)
	grant(t, store, whole, u, "", model.GrantViewer, true)

	confinedTo := func(root string) (files []string, storages []string) {
		t.Helper()
		tok := testutil.NewAPIToken(t, store, u.ID, "read,root:"+root)
		status, raw := fxJSON(t, http.MethodGet, srv.URL+"/api/files/manager/shared-with-me", tok, nil)
		require.Equal(t, http.StatusOK, status, raw)
		var got struct {
			Files    []map[string]any `json:"files"`
			Storages []string         `json:"storages"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &got))
		return pathsOf(got.Files), got.Storages
	}

	files, storages := confinedTo("team://Projects")
	assert.Equal(t, []string{"team://Projects"}, files)
	assert.Equal(t, []string{"team"}, storages,
		"a token confined to team://Projects is not told the names of the other storages its account holds grants on")

	// A folder of the same storage that no grant reaches into or covers: the
	// storage is not named either - the grant on Projects lies outside it.
	files, storages = confinedTo("team://Elsewhere")
	assert.Empty(t, files)
	assert.Empty(t, storages)

	// A grant on the whole storage covers any root on it: the shared drive is
	// named, and only that one.
	files, storages = confinedTo("whole://deep/folder")
	assert.Empty(t, files)
	assert.Equal(t, []string{"whole"}, storages)

	// Unconfined, every storage the account holds a grant on is named, as
	// before.
	client := freshClient(t)
	testutil.LoginAs(t, srv, client, "u@test.local", "UserPass1!")
	got := sharedWithMe(t, client, srv.URL)
	assert.ElementsMatch(t, []string{"team", "other", "whole"}, got.Storages)
	assert.ElementsMatch(t, []string{"team://Projects", "other://Docs"}, pathsOf(got.Files))
}

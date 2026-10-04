package handlers_test

// filex 0.51 (GitHub #81): ONLYOFFICE opens a .csv while it is configured, and
// it is a choice like any other - the person's "Always open with"
// (PUT /api/me/open-with/csv) and the administrator's Default apps
// (PUT /api/admin/file-types/csv) both take it; switched off, a choice of it
// falls to filex's table without an error.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
)

// Fails on 0.50: "onlyoffice" was not an open choice (400 bad_handler).
func TestOpenWith_KeepsOnlyOfficeForACSV(t *testing.T) {
	prefs, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)

	got := choicesOf(t, openWithAs(t, h, ada, "PUT", "/api/me/open-with/csv", `{"handler":"onlyoffice"}`))
	assert.Equal(t, map[string]string{"csv": "onlyoffice"}, got)
	assert.Equal(t, map[string]string{"csv": "onlyoffice"}, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")))
	assert.Equal(t, map[string]string{"csv": "onlyoffice"}, surfaceChoices(t, prefs, ada, "web"),
		"the explorer reads it with the rest of its preferences")
}

func TestOpenWith_OnlyOfficeIsNoChoiceForAKindItDoesNotOpen(t *testing.T) {
	_, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)
	for _, kind := range []string{"docx", "xlsx", "png", "tsv"} {
		rec := openWithAs(t, h, ada, "PUT", "/api/me/open-with/"+kind, `{"handler":"onlyoffice"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, kind)
	}
	assert.Empty(t, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")))
}

func TestFileTypes_CSVOpensInOnlyOfficeAndTheAdministratorMayReorderIt(t *testing.T) {
	f := newAppFixture(t, nil)
	configured := true
	f.assoc.SetOnlyOfficeOpen(func(string, string) bool { return configured })

	code, body := f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code, body)
	open := kindOf(t, body, "csv")["open"].(map[string]any)
	assert.Equal(t, []string{"onlyoffice", "builtin"}, handlerIDs(open["on"]), "the default: ONLYOFFICE first, filex's table second")

	code, body = f.jsonReq(t, http.MethodPut, "/api/admin/file-types/csv", map[string]any{
		"open": map[string]any{"order": []string{"builtin", "onlyoffice"}},
	})
	require.Equal(t, http.StatusOK, code, body)
	open = kindOf(t, body, "csv")["open"].(map[string]any)
	assert.Equal(t, []string{"builtin", "onlyoffice"}, handlerIDs(open["on"]))

	// The explorer is told.
	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/actions")
	require.NoError(t, err)
	var ans struct {
		OpenRules map[string]struct {
			Order []string `json:"order"`
		} `json:"open_rules"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&ans))
	res.Body.Close()
	assert.Equal(t, []string{"builtin", "onlyoffice"}, ans.OpenRules["csv"].Order)

	// OnlyOffice switched off: the rule stays, the table opens the file, and
	// nothing errors.
	configured = false
	code, body = f.jsonReq(t, http.MethodGet, "/api/admin/file-types", nil)
	require.Equal(t, http.StatusOK, code, body)
	open = kindOf(t, body, "csv")["open"].(map[string]any)
	assert.Equal(t, []string{"builtin"}, handlerIDs(open["on"]))
	assert.Equal(t, true, open["custom"])

	// ...and a new rule naming it is refused while it is not there.
	code, body = f.jsonReq(t, http.MethodPut, "/api/admin/file-types/csv", map[string]any{
		"open": map[string]any{"order": []string{"onlyoffice"}},
	})
	assert.Equal(t, http.StatusBadRequest, code, body)
	rules, err := f.store.ListFileAssociations(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, []string{"builtin", "onlyoffice"}, rules[0].Handlers, "the refused change left the rule as it was")
}

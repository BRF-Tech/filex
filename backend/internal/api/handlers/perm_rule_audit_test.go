package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// A custom role's audit row says what its own list was and became: before,
// only the name, the folder part and the limits were written, so taking a
// permission out of a role's list left no trace of what was taken.
func TestPermAdmin_ARolesOwnListIsInItsAuditRow(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Audited list", "enabled": true,
		"permissions": []string{string(perm.FilesDownload), string(perm.FilesCreate)},
	})
	require.Equal(t, http.StatusCreated, status, body)
	var created model.PermissionRule
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/"+idStr(created.ID), pf.adminTok, map[string]any{
		"name": "Audited list", "enabled": true, "permissions": []string{string(perm.FilesDownload)},
	})
	require.Equal(t, http.StatusOK, status, body)

	rows, _, err := pf.Store.ListAuditFiltered(ctx, nil, "", nil, nil, 50, 0)
	require.NoError(t, err)
	byAction := map[string]map[string]any{}
	for _, r := range rows {
		byAction[r.Entry.Action] = r.Entry.Metadata
	}
	rule, _ := byAction["permission_rule.create"]["rule"].(map[string]any)
	require.NotNil(t, rule)
	require.Contains(t, rule, "permissions", "a new role's row names its own list")
	assert.ElementsMatch(t, []any{"files.download", "files.create"}, rule["permissions"])
	upd := byAction["permission_rule.update"]
	require.NotNil(t, upd)
	before, _ := upd["before"].(map[string]any)
	after, _ := upd["after"].(map[string]any)
	require.Contains(t, before, "permissions", "the row names the list the role had")
	require.Contains(t, after, "permissions", "and the list it got")
	assert.ElementsMatch(t, []any{"files.download", "files.create"}, before["permissions"])
	assert.ElementsMatch(t, []any{"files.download"}, after["permissions"])
}

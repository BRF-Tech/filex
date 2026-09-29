package handlers_test

// A custom role in other languages (migration 00071): the admin API takes and
// returns the translations, refuses a language the server does not offer, and
// a refusal names the role in its reader's account language — while the
// audit log and the refusal's source keep the role's own name.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestRoleNames_AdminAPI(t *testing.T) {
	pf := newPermFix(t)
	noDelete := perm.Standard.Without(perm.FilesDelete).Strings()

	// Written the way the panel writes a role (#120: an administrator's
	// session, not a key — this role holds no admin right, but the pattern
	// is the panel's).
	status, body := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/roles", map[string]any{
		"name": "Contractors", "description": "Outside staff", "enabled": true, "permissions": noDelete,
		"names":        map[string]string{"TR": "  Yükleniciler ", "en": ""},
		"descriptions": map[string]string{"tr": "Dışarıdan çalışanlar"},
	})
	require.Equal(t, http.StatusCreated, status, body)
	rule := decode(t, body)
	ruleID := idStr(int64(rule["id"].(float64)))
	assert.Equal(t, map[string]any{"tr": "Yükleniciler"}, rule["names"], "trimmed, keyed lower-case, the blank one dropped")
	assert.Equal(t, map[string]any{"tr": "Dışarıdan çalışanlar"}, rule["descriptions"])
	assert.Equal(t, "Contractors", rule["name"], "the role's own name stays the name")

	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/roles", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	listed := decode(t, body)["rules"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"tr": "Yükleniciler"}, listed["names"], "the list carries them")

	for what, extra := range map[string]map[string]any{
		"a language nobody offers": {"names": map[string]string{"xx": "X"}},
		"a region nobody offers":   {"names": map[string]string{"tr-TR": "X"}},
		"a description in one":     {"descriptions": map[string]string{"de": "Externe"}},
		"a name longer than names": {"names": map[string]string{"tr": strings.Repeat("ğ", perm.MaxRuleNameLen+1)}},
	} {
		payload := map[string]any{"name": "Refused", "permissions": noDelete}
		for k, v := range extra {
			payload[k] = v
		}
		status, body = sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/roles", payload)
		assert.Equal(t, http.StatusBadRequest, status, "%s: %s", what, body)
		status, body = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/roles/"+ruleID, payload)
		assert.Equal(t, http.StatusBadRequest, status, "%s (update): %s", what, body)
	}

	status, body = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/roles/"+ruleID, map[string]any{
		"name": "Contractors (EU)", "enabled": true, "permissions": noDelete,
		"names": map[string]string{"tr": "Taşeronlar"},
	})
	require.Equal(t, http.StatusOK, status, body)
	updated := decode(t, body)
	assert.Equal(t, map[string]any{"tr": "Taşeronlar"}, updated["names"])
	assert.Nil(t, updated["descriptions"], "an update replaces the translations: none sent is none kept")

	// The audit log keeps the role's own name.
	entries, err := pf.Store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var named []string
	for _, e := range entries {
		if e.TargetID == ruleID {
			if n, ok := e.Metadata["target_name"].(string); ok {
				named = append(named, n)
			}
		}
	}
	assert.Contains(t, named, "Contractors (EU)")
	assert.NotContains(t, named, "Taşeronlar")
}

func TestRoleNames_RefusalSpeaksTheReadersLanguage(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	status, body := sessionJSON(t, pf.AdminA, "POST", pf.URL+"/api/admin/roles", map[string]any{
		"name": "Contractors", "enabled": true, "permissions": perm.Standard.Without(perm.FilesDelete).Strings(),
		"names": map[string]string{"tr": "Yükleniciler"},
	})
	require.Equal(t, http.StatusCreated, status, body)
	status, body = sessionJSON(t, pf.AdminA, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles",
		map[string]any{"role_id": decode(t, body)["id"]})
	require.Equal(t, http.StatusOK, status, body)
	perm.Invalidate()

	refusal := func(locale string) (string, string) {
		t.Helper()
		require.NoError(t, pf.Store.UpdateUserLocale(ctx, pf.UserA, locale, "UTC"))
		status, body := deleteItem(t, pf, pf.memberTok, "report.txt")
		got := requireDenied(t, "delete under the role ("+locale+")", status, body, perm.FilesDelete, perm.SourceRule)
		return got["message"].(string), got["source"].(map[string]any)["rule_name"].(string)
	}

	msg, src := refusal("tr")
	assert.Contains(t, msg, "Yükleniciler", "a Turkish account reads the role's Turkish name")
	assert.NotContains(t, msg, "Contractors")
	assert.Equal(t, "Contractors", src, "the source keeps the role's own name")

	msg, src = refusal("en")
	assert.Contains(t, msg, "Contractors", "an English account reads the role's own name")
	assert.NotContains(t, msg, "Yükleniciler")
	assert.Equal(t, "Contractors", src)
}

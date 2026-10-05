package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/perm"
)

type gapsAnswer struct {
	Gaps []struct {
		ID       string `json:"id"`
		Key      string `json:"key"`
		From     string `json:"from"`
		Role     string `json:"role"`
		RuleID   int64  `json:"rule_id"`
		RuleName string `json:"rule_name"`
	} `json:"gaps"`
}

func (a gapsAnswer) ids() []string {
	out := []string{}
	for _, g := range a.Gaps {
		out = append(out, g.ID)
	}
	return out
}

// readGaps reads a 200 gaps answer: readGaps(t)(fxJSON(…)).
func readGaps(t *testing.T) func(int, string) gapsAnswer {
	return func(status int, body string) gapsAnswer {
		t.Helper()
		require.Equal(t, http.StatusOK, status, body)
		var a gapsAnswer
		require.NoError(t, json.Unmarshal([]byte(body), &a))
		return a
	}
}

// as050 writes the built-in User role as 0.50's page writes it back after
// 0.51.0 gave it files.encrypt: everything it knows, files.encrypt gone.
func as050(t *testing.T, pf *permFix) {
	t.Helper()
	b, err := json.Marshal(perm.Standard.Without(perm.FilesEncrypt).Without(perm.AccessFTP).Strings())
	require.NoError(t, err)
	require.NoError(t, pf.Store.UpsertSetting(context.Background(), model.SettingPermissionDefaults, string(b)))
}

func gapAuditRows(t *testing.T, pf *permFix, action string) []map[string]any {
	t.Helper()
	rows, _, err := pf.Store.ListAuditFiltered(context.Background(), nil, action, nil, nil, 50, 0)
	require.NoError(t, err)
	var out []map[string]any
	for _, r := range rows {
		m := map[string]any{"target_id": r.Entry.TargetID}
		for k, v := range r.Entry.Metadata {
			m[k] = v
		}
		out = append(out, m)
	}
	return out
}

// The User role and a custom role's folder part as 0.50 left them: listed on
// Admin -> Roles, given the permission back with one click or marked as on
// purpose - each an audit row - and nothing given back by itself.
func TestPermissionGaps_ListRestoreDismiss(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	as050(t, pf)
	drop, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Drop box", Enabled: true, Permissions: []string{"files.download"},
		Effects:    map[string]string{"files.create": model.PermAllow},
		Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
	})
	require.NoError(t, err)
	ruleGap := "role:" + idStr(drop.ID) + ":files.encrypt"

	a := readGaps(t)(fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.adminTok, nil))
	require.Equal(t, []string{"builtin:user:files.encrypt", ruleGap}, a.ids())
	assert.Equal(t, "user", a.Gaps[0].Role)
	assert.Equal(t, "files.create", a.Gaps[0].From)
	assert.Equal(t, "Drop box", a.Gaps[1].RuleName)

	// A plain member does not reach the list.
	status, body := fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.memberTok, nil)
	require.Equal(t, http.StatusForbidden, status, body)

	// Give the User role files.encrypt back.
	a = readGaps(t)(fxJSON(t, "POST", pf.URL+"/api/admin/roles/gaps/restore", pf.adminTok, map[string]any{"id": "builtin:user:files.encrypt"}))
	require.Equal(t, []string{ruleGap}, a.ids())
	keys := builtinList(t, pf)
	assert.Contains(t, keys, "files.encrypt")
	assert.NotContains(t, keys, "access.ftp", "nothing else changes")
	rows := gapAuditRows(t, pf, "permission_gap.restore")
	require.Len(t, rows, 1)
	assert.Equal(t, "builtin:user:files.encrypt", rows[0]["target_id"])
	assert.Equal(t, "files.encrypt", rows[0]["key"])
	assert.Contains(t, rows[0]["after"], "files.encrypt")
	assert.NotContains(t, rows[0]["before"], "files.encrypt")

	// The drop box was on purpose.
	a = readGaps(t)(fxJSON(t, "POST", pf.URL+"/api/admin/roles/gaps/dismiss", pf.adminTok, map[string]any{"id": ruleGap}))
	require.Empty(t, a.ids())
	saved, err := pf.Store.GetPermissionRule(ctx, drop.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"files.create": model.PermAllow}, saved.Effects, "dismissing changes nothing in the role")
	rows = gapAuditRows(t, pf, "permission_gap.dismiss")
	require.Len(t, rows, 1)
	assert.Equal(t, ruleGap, rows[0]["target_id"])

	// A dismissed gap can still be given back; the folder part gets the Allow.
	a = readGaps(t)(fxJSON(t, "POST", pf.URL+"/api/admin/roles/gaps/restore", pf.adminTok, map[string]any{"id": ruleGap}))
	require.Empty(t, a.ids())
	saved, err = pf.Store.GetPermissionRule(ctx, drop.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"files.create": model.PermAllow, "files.encrypt": model.PermAllow}, saved.Effects)
	assert.Equal(t, []string{"Drop"}, saved.Conditions.Paths)

	// Nothing left to act on.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles/gaps/restore", pf.adminTok, map[string]any{"id": ruleGap})
	require.Equal(t, http.StatusNotFound, status, body)
	assert.Contains(t, body, "gap_gone")
}

// A save from this version's editor - which sent the permissions it showed -
// is a decision; a save through the API that did not show the key is pointed
// out like a rollback, and the administrators are told once in the bell.
func TestPermissionGaps_AnEditorsSaveIsADecision(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	without := perm.Standard.Without(perm.FilesEncrypt).Strings()
	var shown []string
	for _, d := range perm.All() {
		shown = append(shown, string(d.Key))
	}

	status, body := fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": without})
	require.Equal(t, http.StatusOK, status, body)
	a := readGaps(t)(fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.adminTok, nil))
	require.Equal(t, []string{"builtin:user:files.encrypt"}, a.ids(), "a client that never showed files.encrypt")

	rows, _, err := pf.Notif.List(ctx, nil, notify.AdminBell, false, 50, 0)
	require.NoError(t, err)
	n := 0
	for _, r := range rows {
		if r.Event == string(notify.EventPermissionGaps) {
			n++
			assert.Nil(t, r.UserID, "a broadcast")
		}
	}
	require.Equal(t, 1, n, "told once")

	readGaps(t)(fxJSON(t, "POST", pf.URL+"/api/admin/roles/gaps/restore", pf.adminTok, map[string]any{"id": "builtin:user:files.encrypt"}))
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/roles/builtin", pf.adminTok, map[string]any{"permissions": without, "shown": shown})
	require.Equal(t, http.StatusOK, status, body)
	a = readGaps(t)(fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.adminTok, nil))
	require.Empty(t, a.ids(), "the administrator saw Encrypt and left it off")

	// The same for a custom role's folder part.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Drop box", "enabled": true, "permissions": []string{"files.download"},
		"effects": map[string]string{"files.create": "allow"}, "conditions": map[string]any{"paths": []string{"Drop"}},
		"shown": shown,
	})
	require.Equal(t, http.StatusCreated, status, body)
	a = readGaps(t)(fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.adminTok, nil))
	require.Empty(t, a.ids())
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Drop box by a script", "enabled": true, "permissions": []string{"files.download"},
		"effects": map[string]string{"files.create": "allow"}, "conditions": map[string]any{"paths": []string{"Drop"}},
	})
	require.Equal(t, http.StatusCreated, status, body)
	a = readGaps(t)(fxJSON(t, "GET", pf.URL+"/api/admin/roles/gaps", pf.adminTok, nil))
	require.Len(t, a.ids(), 1)
	assert.Equal(t, "Drop box by a script", a.Gaps[0].RuleName)
}

// Who sees and acts on a gap is who may edit the list: the built-in role is
// the platform operator's, a custom role its tenant's administrators' too.
func TestPermissionGaps_TenantAdminsSeeTheirOwnRolesOnly(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	b, err := json.Marshal(perm.Standard.Without(perm.FilesEncrypt).Strings())
	require.NoError(t, err)
	require.NoError(t, f.Store.UpsertSetting(ctx, model.SettingPermissionDefaults, string(b)))
	part := func(name string, pid int64) *model.PermissionRule {
		r, err := f.Store.CreatePermissionRule(ctx, &model.PermissionRule{
			Name: name, Enabled: true, Permissions: []string{"files.download"}, ProviderID: &pid,
			Effects:    map[string]string{"files.create": model.PermAllow},
			Conditions: model.PermRuleConditions{Paths: []string{"Drop"}},
		})
		require.NoError(t, err)
		return r
	}
	alpha := part("Alpha drop", f.ProvA)
	bravo := part("Bravo drop", f.ProvB)
	alphaGap := "role:" + idStr(alpha.ID) + ":files.encrypt"
	bravoGap := "role:" + idStr(bravo.ID) + ":files.encrypt"

	a := readGaps(t)(sessionJSON(t, f.Super, "GET", f.URL+"/api/admin/roles/gaps", nil))
	assert.Equal(t, []string{"builtin:user:files.encrypt", alphaGap, bravoGap}, a.ids(), "the platform operator sees every one")
	a = readGaps(t)(sessionJSON(t, f.AdminA, "GET", f.URL+"/api/admin/roles/gaps", nil))
	assert.Equal(t, []string{alphaGap}, a.ids(), "alpha's administrator: alpha's role, not the built-in one")

	status, body := sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/roles/gaps/restore", map[string]any{"id": "builtin:user:files.encrypt"})
	require.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "supertenant_only")
	status, body = sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/roles/gaps/dismiss", map[string]any{"id": bravoGap})
	require.Equal(t, http.StatusNotFound, status, "another tenant's role is not there for alpha: %s", body)

	a = readGaps(t)(sessionJSON(t, f.AdminA, "POST", f.URL+"/api/admin/roles/gaps/restore", map[string]any{"id": alphaGap}))
	assert.Empty(t, a.ids())
	got, err := f.Store.GetPermissionRule(ctx, alpha.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PermAllow, got.Effects["files.encrypt"])
	other, err := f.Store.GetPermissionRule(ctx, bravo.ID)
	require.NoError(t, err)
	assert.NotContains(t, other.Effects, "files.encrypt", "bravo's role is untouched")
}

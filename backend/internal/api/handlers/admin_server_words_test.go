package handlers_test

// The admin pages show the SERVER's numbers and words (0.54 audit, task #208:
// D8 D9 A5 A13 A14 B8 B10). Each test here was red on the code before: the
// field it reads did not exist, or the rule it holds lived in the browser.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/update"
)

// ── D8: the duplicate report's totals are the whole report's ──────────────

func TestAdminDuplicates_TotalsAreTheWholeReportsNotThePagesSum(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	stID := seedDupStorage(t, store)
	// Three groups: 3 x 500 (waste 1000), 2 x 300 (waste 300), 2 x 100 (waste 100).
	for _, n := range []string{"b1", "b2", "b3"} {
		seedDupNode(t, store, stID, n+".bin", 500, "bbb")
	}
	for _, n := range []string{"c1", "c2"} {
		seedDupNode(t, store, stID, n+".bin", 300, "ccc")
	}
	for _, n := range []string{"a1", "a2"} {
		seedDupNode(t, store, stID, n+".bin", 100, "aaa")
	}

	code, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/duplicates?limit=1", nil)
	require.Equal(t, http.StatusOK, code)
	groups, _ := body["groups"].([]any)
	require.Len(t, groups, 1, "the list is cut to the limit")
	assert.EqualValues(t, 3, body["total_groups"], "every group, not the one on the page")
	assert.EqualValues(t, 2+1+1, body["total_copies"], "every extra copy")
	assert.EqualValues(t, 1000+300+100, body["total_waste"], "every group's waste")
	assert.Equal(t, true, body["truncated"])
}

// ── D9: the dashboard's numbers are the server's ──────────────────────────

func TestDashboard_TheRunningCountAndTheNewestScanAreTheServers(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	ctx := context.Background()

	mk := func(name string) int64 {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
			ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(t.TempDir()) + `"}`),
			SyncMode:   model.SyncModeOnDemand,
		})
		require.NoError(t, err)
		return st.ID
	}
	done, running := mk("bitti"), mk("suruyor")
	run, err := store.CreateSyncRun(ctx, done, "")
	require.NoError(t, err)
	require.NoError(t, store.FinishSyncRun(ctx, run.ID, "", 1, 0, 0, 0, "ok", ""))
	_, err = store.CreateSyncRun(ctx, running, "")
	require.NoError(t, err)

	code, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard", nil)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 1, body["active_syncs"], "the storage whose scan is still running")
	assert.NotEmpty(t, body["last_sync_at"], "the newest scan's start, worked out by the server")
}

// ── A14: every audit row in words, on the list and on the dashboard ──────

func TestAudit_EveryRowCarriesItsWordsAndTheFilterIsTheServers(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	require.NoError(t, store.InsertAuditEntry(context.Background(), &model.AuditEntry{
		Action: "user.delete", TargetType: "user", TargetID: "14",
		Metadata: map[string]any{"target_name": "gone@example.com"},
	}))

	code, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/audit?lang=tr", nil)
	require.Equal(t, http.StatusOK, code)
	entries, _ := body["entries"].([]any)
	var row map[string]any
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if inner, _ := m["entry"].(map[string]any); inner["action"] == "user.delete" {
			row = m
		}
	}
	require.NotNil(t, row, "the seeded row is listed")
	assert.Equal(t, "Kullanıcı: silindi", row["label"])
	assert.Equal(t, "Kullanıcı “gone@example.com”", row["target_label"])

	resources, _ := body["resources"].([]any)
	require.NotEmpty(t, resources, "the What filter comes with the list")
	found := false
	for _, r := range resources {
		m, _ := r.(map[string]any)
		if m["label"] == "Kullanıcı" {
			found = true
			assert.Contains(t, m["value"], "user.")
		}
	}
	assert.True(t, found, "the filter names the resource in the reader's language")

	code, dash := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard?lang=en", nil)
	require.Equal(t, http.StatusOK, code)
	recent, _ := dash["recent_activity"].([]any)
	labelled := false
	for _, e := range recent {
		m, _ := e.(map[string]any)
		if m["action"] == "user.delete" {
			labelled = true
			assert.Equal(t, "User: deleted", m["label"])
			assert.Equal(t, "User “gone@example.com”", m["target_label"])
		}
	}
	assert.True(t, labelled, "the dashboard says the same row in the same words")
}

// ── A5: the Updates page's policy words are the server's ──────────────────

func updateWords(t *testing.T, cfg update.Config, target string, acceptLang string) map[string]any {
	t.Helper()
	cfg.CurrentVersion = "v0.7.5"
	h := handlers.NewUpdate(update.New(cfg))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestUpdateStatus_ThePolicyIsSaidByTheServer(t *testing.T) {
	tr := func(key string, vars srvtext.Vars) string { return srvtext.Text("tr", key, vars) }
	patchTR := tr("server.update.policy.patch", nil)

	t.Run("Homebrew with policy patch announces only, and says who upgrades", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: true, Policy: update.PolicyPatch,
			Install: update.Install{Mode: update.ModePackage, Manager: update.ManagerHomebrew, Package: "filex", Cask: true}},
			"/api/admin/update?lang=tr", "")
		assert.Equal(t, patchTR, b["policy_name"])
		assert.Equal(t, tr("server.update.behavior.announce", nil), b["policy_badge"])
		assert.Equal(t, tr("server.update.policy_limit.package", srvtext.Vars{"policy": patchTR, "manager": "Homebrew"}), b["policy_note"])
		assert.NotContains(t, b["policy_note"], "{")
	})
	t.Run("a package install whose manager is unknown says your package manager", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: true, Policy: update.PolicyPatch,
			Install: update.Install{Mode: update.ModePackage}}, "/api/admin/update?lang=tr", "")
		assert.Equal(t, tr("server.update.policy_limit.package_unknown", srvtext.Vars{"policy": patchTR}), b["policy_note"])
	})
	t.Run("checking switched off overrides the policy", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: false, Policy: update.PolicyPatch,
			Install: update.Install{Mode: update.ModeBinary}}, "/api/admin/update?lang=tr", "")
		assert.Equal(t, tr("server.update.behavior.off", nil), b["policy_badge"])
		assert.Equal(t, tr("server.update.policy_limit.disabled", srvtext.Vars{"policy": patchTR}), b["policy_note"])
	})
	t.Run("policy minor on 0.x installs patches only", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: true, Policy: update.PolicyMinor,
			Install: update.Install{Mode: update.ModeBinary}}, "/api/admin/update?lang=tr", "")
		assert.Equal(t, tr("server.update.behavior.patch", nil), b["policy_badge"])
		assert.Equal(t, tr("server.update.policy_limit.zero_major", srvtext.Vars{"policy": tr("server.update.policy.minor", nil)}), b["policy_note"])
	})
	t.Run("a policy in force is named as the policy, with no note", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: true, Policy: update.PolicyPatch,
			Install: update.Install{Mode: update.ModeBinary}}, "/api/admin/update?lang=en", "")
		assert.Equal(t, "Policy: install patches", b["policy_badge"])
		_, has := b["policy_note"]
		assert.False(t, has, "no note when the policy is in force")
	})
	t.Run("the screen's language beats the browser's", func(t *testing.T) {
		b := updateWords(t, update.Config{Enabled: true, Policy: update.PolicyPatch,
			Install: update.Install{Mode: update.ModeBinary}}, "/api/admin/update?lang=en", "tr-TR,tr;q=0.9")
		assert.Equal(t, "install patches", b["policy_name"])
	})
}

// ── B8: the protection page's bounds and refusals are the server's ────────

func TestProtection_TheBoundsComeWithTheValuesAndARefusalIsASentence(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/protection", nil)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 1, body["trash_retention_days_min"])
	assert.EqualValues(t, 3650, body["trash_retention_days_max"])
	assert.EqualValues(t, 0, body["versions_keep_n_min"])
	assert.EqualValues(t, 1000, body["versions_keep_n_max"])
	assert.EqualValues(t, 0, body["share_max_ttl_days_min"])
	assert.EqualValues(t, 3650, body["share_max_ttl_days_max"])

	patch := func(payload string) (int, map[string]string) {
		req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/protection", bytes.NewReader([]byte(payload)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]string{}
		testutil.ReadJSON(t, resp, &out)
		return resp.StatusCode, out
	}
	// Over the TOP: the page only ever checked the bottom.
	for field, max := range map[string]string{"trash_retention_days": "3650", "versions_keep_n": "1000", "share_max_ttl_days": "3650"} {
		code, out := patch(`{"` + field + `": 999999}`)
		assert.Equal(t, http.StatusBadRequest, code, field)
		assert.Equal(t, field, out["field"])
		assert.Contains(t, out["message"], max, "the sentence names the bound")
		assert.NotContains(t, out["message"], "must be between", "the sentence is the reader's, not the API's English")
		assert.Contains(t, out["error"], "must be between", "a script still reads the English")
	}
	// An emptied number box, and a fraction.
	for _, payload := range []string{`{"trash_retention_days": null}`, `{"trash_retention_days": 2.5}`} {
		code, out := patch(payload)
		assert.Equal(t, http.StatusBadRequest, code, payload)
		// The seeded administrator's account language is English.
		assert.Equal(t, srvtext.Text("en", "server.protection.whole_number", nil), out["message"], payload)
	}
}

// ── B10: a webhook address is http(s) in any case, refused in words ───────

func TestWebhooks_TheSchemeIsCaseInsensitiveAndARefusalNamesItsBox(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, _ := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks",
		map[string]any{"name": "ops", "url": "HTTPS://hooks.example.test/filex"})
	assert.Equal(t, http.StatusOK, code, "a scheme in capitals is still https")

	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"name": "", "url": "https://hooks.example.test/x"}, "name"},
		{map[string]any{"name": "ops", "url": ""}, "url"},
		{map[string]any{"name": "ops", "url": "ftp://hooks.example.test/x"}, "url"},
	} {
		code, out := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", c.body)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, c.field, out["field"])
		msg, _ := out["message"].(string)
		assert.NotEmpty(t, msg, "the refusal is a sentence")
		assert.False(t, strings.HasPrefix(msg, "server."), "a catalogue key is never the sentence: %q", msg)
	}
}

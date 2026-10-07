package handlers_test

// The explorer's question — may I encrypt here? — and the capabilities'
// `e2e_policy` (handlers/e2e_policy_files.go Allowed, capabilities.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// e2eAllowed asks POST /api/files/e2e/allowed as c and answers the list.
func e2eAllowed(t *testing.T, c *http.Client, url string, paths ...string) []string {
	t.Helper()
	items := make([]map[string]string, 0, len(paths))
	for _, p := range paths {
		items = append(items, map[string]string{"path": p})
	}
	st, raw := doReq(t, c, http.MethodPost, url+"/api/files/e2e/allowed", map[string]any{"items": items})
	require.Equal(t, http.StatusOK, st, string(raw))
	var out struct {
		Encrypt []string `json:"encrypt"`
	}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	require.Len(t, out.Encrypt, len(paths), "one answer per item, in order")
	return out.Encrypt
}

// The answer is the rule's, per place and per person — and asking spends
// nothing.
func TestE2EAllowed_AnswersWhatTheRuleWouldSay(t *testing.T) {
	f := newE2EFix(t)
	ctx := context.Background()
	a := f.a.storage.Name + "://"

	got := e2eAllowed(t, f.a.member, f.srv.URL,
		a+"Proje", a+"Proje/rapor.pdf", f.b.storage.Name+"://Proje", a+"../Proje", "yok://Proje")
	assert.Equal(t, []string{"request", "request", "denied", "denied", "denied"}, got,
		"approval policy: a member may ask; another tenant's storage, `..` and a storage nobody has are not places")
	assert.Equal(t, []string{"allowed"}, e2eAllowed(t, f.a.admin, f.srv.URL, a+"Proje"),
		"an administrator needs no approval")

	id := f.askAndApprove(t, f.a, "Proje", model.E2ERequestFolder)
	for range 2 {
		assert.Equal(t, []string{"allowed", "request"}, e2eAllowed(t, f.a.member, f.srv.URL, a+"Proje", a+"Baska"),
			"the approval opens the folder it names, and asking does not spend it")
	}
	row, err := f.store.GetE2ERequest(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.E2ERequestApproved, row.Status)

	// A file's approval is its folder's, for files: the file is allowed, the
	// folder itself is not.
	seedNodeIn(t, f.store, f.a.storage.ID, "Belgeler/rapor.pdf")
	f.askAndApprove(t, f.a, "Belgeler/rapor.pdf", model.E2ERequestFile)
	assert.Equal(t, []string{"allowed", "request"}, e2eAllowed(t, f.a.member, f.srv.URL, a+"Belgeler/rapor.pdf", a+"Belgeler"))

	for _, c := range []struct {
		setting       model.ProviderE2E
		member, admin string
	}{
		{model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, "allowed", "allowed"},
		{model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyAdmins}, "denied", "allowed"},
		{model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyOff}, "denied", "denied"},
		{model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}, "denied", "denied"},
	} {
		require.NoError(t, f.store.SetProviderE2E(ctx, f.a.id, c.setting))
		assert.Equal(t, []string{c.member}, e2eAllowed(t, f.a.member, f.srv.URL, a+"Yeni"), "member, %+v", c.setting)
		assert.Equal(t, []string{c.admin}, e2eAllowed(t, f.a.admin, f.srv.URL, a+"Yeni"),
			"administrator, %+v — the policy and the ceiling bind administrators too", c.setting)
	}
}

// One question is bounded, like the permission question beside it.
func TestE2EAllowed_TooManyItemsIsRefused(t *testing.T) {
	f := newE2EFix(t)
	items := make([]map[string]string, 1001)
	for i := range items {
		items[i] = map[string]string{"path": fmt.Sprintf("%s://k%d", f.a.storage.Name, i)}
	}
	st, raw := doReq(t, f.a.member, http.MethodPost, f.url("/api/files/e2e/allowed"), map[string]any{"items": items})
	assert.Equal(t, http.StatusBadRequest, st, string(raw))
}

// A token confined to a folder (`root:`) hears of the rule only inside it. Its
// body is confined on the way in whatever its Content-Type (confine.Middleware
// refuses a path outside the root, the whole request; up to 0.52 only in a body
// labelled JSON). The handler reads the root itself too, before any rule is
// asked, so a door the middleware does not cover answers a place outside it as
// a place that cannot be placed does, "denied" - never the rule's "request",
// or "allowed" for a folder the person holds an approval for.
func TestE2EAllowed_ARootConfinedTokenHearsNothingOutsideItsRoot(t *testing.T) {
	f := newE2EFix(t)
	useProductionAuthChain(t, f.store)
	a := f.a.storage.Name + "://"
	f.askAndApprove(t, f.a, "Proje/Alt", model.E2ERequestFolder)
	f.askAndApprove(t, f.a, "Baska", model.E2ERequestFolder)
	tok := testutil.NewAPIToken(t, f.store, f.a.memberID, "read,root:"+a+"Proje")

	// ask sends the question carrying only the token, as a body of the given
	// Content-Type, and answers the status, the list and the raw body.
	ask := func(contentType string, paths ...string) (int, []string, string) {
		t.Helper()
		items := make([]map[string]string, 0, len(paths))
		for _, p := range paths {
			items = append(items, map[string]string{"path": p})
		}
		body, err := json.Marshal(map[string]any{"items": items})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, f.url("/api/files/e2e/allowed"), bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", contentType)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var out struct {
			Encrypt []string `json:"encrypt"`
		}
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out.Encrypt, string(raw)
	}

	// A sibling folder the person holds an approval for, and a name that only
	// starts like the root's: whatever the Content-Type, a path outside the
	// root refuses the whole request, and nothing of the rule is heard.
	for _, ct := range []string{"application/json", "text/plain", ""} {
		for _, outside := range []string{a + "Baska", a + "Proje2"} {
			code, _, raw := ask(ct, a+"Proje/Alt", outside)
			assert.Equal(t, http.StatusForbidden, code, "%s as %q: %s", outside, ct, raw)
			assert.Contains(t, raw, confine.ErrOutOfRoot.Error(), "%s as %q", outside, ct)
			assert.NotContains(t, raw, "allowed", "%s as %q", outside, ct)
		}
		// The root itself and a folder in it are answered as they are, and the
		// storage is read as the root folder.
		code, got, raw := ask(ct, a+"Proje", a+"Proje/Alt", a)
		require.Equal(t, http.StatusOK, code, "as %q: %s", ct, raw)
		assert.Equal(t, []string{"request", "allowed", "request"}, got, "as %q", ct)
	}
	paths := []string{a + "Proje", a + "Proje/Alt", a + "Baska", a + "Proje2", a}
	assert.Equal(t, []string{"request", "allowed", "allowed", "request", "request"}, e2eAllowed(t, f.a.member, f.srv.URL, paths...),
		"the same person, confined to nothing, is told what the rule says there")
}

// capabilitiesE2E answers the capabilities' `e2e_policy` as c sees it, and
// whether it was there at all.
func capabilitiesE2E(t *testing.T, c *http.Client, url string) (map[string]any, bool) {
	t.Helper()
	st, raw := doReq(t, c, http.MethodGet, url+"/api/files/capabilities", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	pol, ok := out["e2e_policy"].(map[string]any)
	return pol, ok
}

// A signed-in caller is told their tenant's ceiling and policy; an anonymous
// one (the login screen, a share page) is told nothing about it.
func TestCapabilities_E2EPolicy(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	_, ok := capabilitiesE2E(t, freshClient(t), srv.URL)
	assert.False(t, ok, "an anonymous caller learns nothing of the policy")

	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	pol, ok := capabilitiesE2E(t, client, srv.URL)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"available": true, "policy": model.E2EPolicyPermitted}, pol)
	require.NoError(t, store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyAdmins))
	pol, _ = capabilitiesE2E(t, client, srv.URL)
	assert.Equal(t, model.E2EPolicyAdmins, pol["policy"], "a single-tenant install answers its setting")

	f := newE2EFix(t)
	require.NoError(t, f.store.SetProviderE2E(context.Background(), f.b.id,
		model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}))
	pol, _ = capabilitiesE2E(t, f.a.member, f.srv.URL)
	assert.Equal(t, map[string]any{"available": true, "policy": model.E2EPolicyApproval}, pol, "the caller's own tenant")
	pol, _ = capabilitiesE2E(t, f.b.member, f.srv.URL)
	assert.Equal(t, map[string]any{"available": false, "policy": model.E2EPolicyPermitted}, pol)
}

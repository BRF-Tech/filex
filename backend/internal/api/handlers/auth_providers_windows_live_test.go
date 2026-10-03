package handlers_test

// The Windows sign-in provider on the Identity providers page, over HTTP: it is
// listed with its fields, it is switched on only by a test that PASSED — a real
// account signed in, sent on the request and never stored, logged or echoed —
// there is no "switch on anyway" for it, and the account that proved it works
// becomes the super administrator (the one exception to auto_create being off).
// The operating-system call is a fake (windows.SetLogonForTest), so this runs
// on any machine.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authwindows "github.com/brf-tech/filex/backend/internal/auth/drivers/windows"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

const (
	winPw     = "Wn-Pa55-S3ntinel"
	winPerson = "S-1-5-21-1000-2000-3000-1105"
	winAdmin  = "S-1-5-21-1000-2000-3000-500"
)

// windowsMachine is a machine with two people, the built-in Administrator and
// a locked account.
func windowsMachine(t *testing.T) {
	t.Helper()
	t.Cleanup(authwindows.SetLogonForTest(func(user, domain, password string) (*authwindows.Logon, error) {
		acc := map[string]struct {
			sid  string
			code uint32
		}{
			"ayse|.": {sid: winPerson}, "can|.": {sid: winPerson}, "administrator|.": {sid: winAdmin},
			"kilitli|.": {sid: winPerson, code: 1909},
		}
		a, ok := acc[strings.ToLower(user)+"|"+strings.ToLower(domain)]
		if !ok || password != winPw {
			return nil, &authwindows.LogonError{Code: 1326}
		}
		if a.code != 0 {
			return nil, &authwindows.LogonError{Code: a.code}
		}
		return &authwindows.Logon{SID: a.sid, Groups: []string{"Users"}}, nil
	}))
}

func testAccount(user, pw string) map[string]any {
	return map[string]any{"username": user, "password": pw}
}

func stepStatus(body map[string]any, id string) (string, map[string]any) {
	for _, c := range body["checks"].([]any) {
		m := c.(map[string]any)
		if m["id"] == id {
			p, _ := m["params"].(map[string]any)
			return m["status"].(string), p
		}
	}
	return "", nil
}

func TestWindowsProvider_IsOnThePageWithItsFields(t *testing.T) {
	windowsMachine(t)
	srv, client, _, _, _ := liveServer(t, nil)
	_, list := listProviders(t, client, srv.URL)
	w, ok := list["windows"]
	require.True(t, ok, "listed")
	assert.Equal(t, true, w["managed"])
	assert.Equal(t, true, w["testable"])
	assert.Equal(t, true, w["test_account_required"])
	assert.Equal(t, "off", w["state"])
	keys := map[string]any{}
	for _, f := range w["fields"].([]any) {
		m := f.(map[string]any)
		keys[m["key"].(string)] = m["default"]
	}
	assert.Equal(t, "false", keys["auto_create"], "closed by default")
	for _, k := range []string{"allowed_groups", "domain", "protocol_login"} {
		assert.Contains(t, keys, k)
	}
	assert.NotContains(t, keys, "test_account", "the test account is a request field, not a setting")
	// The other providers do not ask for a test account.
	assert.Nil(t, list["ldap"]["test_account_required"])
}

func TestWindowsProvider_ItIsNeverSwitchedOnWithoutAPassingTest(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)

	for name, body := range map[string]map[string]any{
		"no account given":   {"enabled": true, "config": map[string]any{}},
		"wrong password":     {"enabled": true, "config": map[string]any{}, "test_account": testAccount("ayse", "nope")},
		"locked account":     {"enabled": true, "config": map[string]any{}, "test_account": testAccount("kilitli", winPw)},
		"Administrator":      {"enabled": true, "config": map[string]any{}, "test_account": testAccount("Administrator", winPw)},
		"confirm is ignored": {"enabled": true, "confirm_failed_test": true, "config": map[string]any{}},
		"confirm, wrong pw":  {"enabled": true, "confirm_failed_test": true, "test_account": testAccount("ayse", "nope")},
	} {
		status, out := patchProvider(t, client, srv.URL, "windows", body)
		require.Equal(t, http.StatusConflict, status, "%s: %v", name, out)
		assert.Equal(t, "test_failed", out["error"], name)
		assert.Equal(t, false, out["confirm_allowed"], name)
		assert.NotEmpty(t, out["message"], name)
		assert.NotEmpty(t, out["failed"], name)
		raw, _ := json.Marshal(out)
		assert.NotContains(t, string(raw), winPw, "%s: a refusal never echoes the password", name)
	}

	_, list := listProviders(t, client, srv.URL)
	assert.Equal(t, "off", list["windows"]["state"], "nothing was saved")
	assert.Equal(t, false, list["windows"]["enabled"])
	_, gerr := store.GetUserByEmail(context.Background(), "ayse@local")
	assert.Error(t, gerr, "a failed test makes no administrator")
	for _, e := range []string{"administrator@local", "kilitli@local"} {
		_, gerr := store.GetUserByEmail(context.Background(), e)
		assert.Error(t, gerr, e)
	}
}

func TestWindowsProvider_APassingTestSwitchesItOnAndMakesTheTestAccountSuperAdmin(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)
	ctx := context.Background()

	status, out := patchProvider(t, client, srv.URL, "windows", map[string]any{
		"enabled": true, "config": map[string]any{}, "test_account": testAccount("Ayse", winPw),
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	assert.Equal(t, true, out["test_ok"])
	assert.Equal(t, true, out["super_admin"])
	for _, id := range []string{"platform", "test_account", "logon", "account"} {
		s, _ := stepStatus(out, id)
		assert.Equal(t, "ok", s, id)
	}

	// The account that signed in is the super administrator, with no password of its own.
	u, err := store.GetUserByEmail(ctx, "ayse@local")
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role)
	assert.Empty(t, u.PasswordHash)
	assert.True(t, u.Enabled)

	// Running, and on the login page's list, at once.
	_, list := listProviders(t, client, srv.URL)
	assert.Equal(t, "running", list["windows"]["state"])
	assert.Contains(t, capsDrivers(t, client, srv.URL), "windows")

	// The account signs in through the ordinary form: the password reaches Windows.
	c := freshClient(t)
	status, raw := doReq(t, c, http.MethodPost, srv.URL+"/api/auth/login", map[string]any{"email": "ayse", "password": winPw})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, c, http.MethodGet, srv.URL+"/api/auth/me", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), "ayse@local")

	// ⚠ auto_create is off: the exception was for the test account ONLY. Another
	// person of the machine gets nothing.
	c2 := freshClient(t)
	status, _ = doReq(t, c2, http.MethodPost, srv.URL+"/api/auth/login", map[string]any{"email": "can", "password": winPw})
	assert.Equal(t, http.StatusUnauthorized, status, "the same answer as a wrong password")
	_, gerr := store.GetUserByEmail(ctx, "can@local")
	assert.Error(t, gerr, "no account for anybody but the test account")

	// The Administrator of the machine is refused even with the right password.
	status, _ = doReq(t, freshClient(t), http.MethodPost, srv.URL+"/api/auth/login", map[string]any{"email": "Administrator", "password": winPw})
	assert.Equal(t, http.StatusUnauthorized, status)
	_, gerr = store.GetUserByEmail(ctx, "administrator@local")
	assert.Error(t, gerr)

	// The audit trail names the account and the fields, never the password.
	assertNoSecretAnywhere(t, store, srv.URL, client, winPw, out)
}

// The test account is used once: it is in no setting, no audit row, no log-like
// surface and no response.
func assertNoSecretAnywhere(t *testing.T, store db.Store, base string, client *http.Client, secret string, responses ...any) {
	t.Helper()
	ctx := context.Background()
	settings, err := store.ListSettings(ctx)
	require.NoError(t, err)
	raw, _ := json.Marshal(settings)
	assert.NotContains(t, string(raw), secret, "settings")
	rows, err := store.ListAuditRecent(ctx, 200)
	require.NoError(t, err)
	raw, _ = json.Marshal(rows)
	assert.NotContains(t, string(raw), secret, "audit")
	require.NotEmpty(t, rows)
	for _, r := range responses {
		raw, _ = json.Marshal(r)
		assert.NotContains(t, string(raw), secret, "response")
	}
	status, list := doReq(t, client, http.MethodGet, base+"/api/admin/auth-providers", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(list), secret, "the providers list")
}

func TestWindowsProvider_TheTestEndpointSignsTheAccountInAndChangesNothing(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)

	status, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/windows/test",
		map[string]any{"config": map[string]any{}, "test_account": testAccount("can", winPw)})
	require.Equal(t, http.StatusOK, status, string(raw))
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, true, out["ok"], string(raw))
	assert.NotContains(t, string(raw), winPw)

	// Flat form (the older MCP shape) reads the same.
	status, raw = doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/windows/test",
		map[string]any{"test_account": testAccount("can", "wrong")})
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, false, out["ok"])
	s, p := stepStatus(out, "logon")
	assert.Equal(t, "fail", s)
	assert.Equal(t, "credentials", p["reason"])

	// No account at all: a failed step, not an error.
	status, raw = doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/windows/test", map[string]any{})
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, false, out["ok"])
	s, p = stepStatus(out, "test_account")
	assert.Equal(t, "fail", s)
	assert.Equal(t, "missing", p["reason"])

	// A test alone makes nobody an administrator.
	_, gerr := store.GetUserByEmail(context.Background(), "can@local")
	assert.Error(t, gerr)
	_, list := listProviders(t, client, srv.URL)
	assert.Equal(t, "off", list["windows"]["state"])
}

func TestWindowsProvider_AnExistingAccountIsPromotedNotDuplicated(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)
	ctx := context.Background()
	existing, err := store.CreateUser(ctx, "can@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	status, out := patchProvider(t, client, srv.URL, "windows", map[string]any{
		"enabled": true, "config": map[string]any{}, "test_account": testAccount("can", winPw),
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	u, err := store.GetUserByEmail(ctx, "can@local")
	require.NoError(t, err)
	assert.Equal(t, existing.ID, u.ID)
	assert.Equal(t, model.RoleAdmin, u.Role)
}

// A test must not switch a disabled account back on: the save does not happen
// either, so nothing is half done.
func TestWindowsProvider_ADisabledTestAccountIsNotPromotedAndNothingIsSaved(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)
	ctx := context.Background()
	off, err := store.CreateUser(ctx, "can@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserEnabled(ctx, off.ID, false))

	status, out := patchProvider(t, client, srv.URL, "windows", map[string]any{
		"enabled": true, "config": map[string]any{}, "test_account": testAccount("can", winPw),
	})
	require.Equal(t, http.StatusConflict, status, "%v", out)
	assert.Equal(t, "test_account_disabled", out["error"])
	assert.NotEmpty(t, out["message"])
	u, _ := store.GetUser(ctx, off.ID)
	assert.Equal(t, model.RoleUser, u.Role)
	assert.False(t, u.Enabled)
	_, list := listProviders(t, client, srv.URL)
	assert.Equal(t, "off", list["windows"]["state"])
}

// Switching OFF needs no test (it runs nothing), and saving a disabled provider
// with settings is allowed without an account.
func TestWindowsProvider_SwitchingItOffNeedsNoTest(t *testing.T) {
	windowsMachine(t)
	srv, client, _, _, _ := liveServer(t, nil)
	status, out := patchProvider(t, client, srv.URL, "windows", map[string]any{
		"enabled": false, "config": map[string]any{"domain": "corp.example", "allowed_groups": "Editors"},
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	_, list := listProviders(t, client, srv.URL)
	assert.Equal(t, "off", list["windows"]["state"])
	cfg := list["windows"]["config_redacted"].(map[string]any)
	assert.Equal(t, "corp.example", cfg["domain"])

	status, out = patchProvider(t, client, srv.URL, "windows", map[string]any{
		"enabled": true, "config": map[string]any{"domain": ""}, "test_account": testAccount("ayse", winPw),
	})
	require.Equal(t, http.StatusOK, status, "%v", out)

	// Switch off again: no test, and the administrator has other ways in.
	status, out = patchProvider(t, client, srv.URL, "windows", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", out)
	_, list = listProviders(t, client, srv.URL)
	assert.Equal(t, "off", list["windows"]["state"])
}

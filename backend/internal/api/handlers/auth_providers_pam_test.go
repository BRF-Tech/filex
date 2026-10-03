//go:build linux

package handlers_test

// The Linux PAM sign-in on the Identity providers page. The machine is a fake
// (pam.SwapForTest), the page, the provider set and the sign-in are real:
//
//   - it cannot be switched on without its test passing — and
//     `confirm_failed_test` does not open that door;
//   - the test's last step is a real sign-in with the test account, whose
//     password lives on that one request and appears in no answer, no audit
//     row and no list;
//   - a passing test makes the account a super administrator;
//   - then people sign in with their machine login, in the browser.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/pam"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

const pamPW = "machine-login-pw-91"

// pamMachine plays sudo, pamtester, getent and id.
func pamMachine(sudoDown *bool) func(argv []string, stdin string) (int, string, string, error) {
	var run func(argv []string, stdin string) (int, string, string, error)
	run = func(argv []string, stdin string) (int, string, string, error) {
		switch filepath.Base(argv[0]) {
		case "getent":
			if argv[2] == "alice" {
				return 0, "alice:x:1001:1001::/home/alice:/bin/bash\n", "", nil
			}
			return 2, "", "", nil
		case "id":
			return 0, "alice staff\n", "", nil
		case "sudo":
			if sudoDown != nil && *sudoDown {
				return 1, "", "sudo: a password is required\n", nil
			}
			if argv[2] == "-l" {
				return 0, argv[3] + "\n", "", nil
			}
			return run(argv[2:], stdin)
		case "pamtester":
			if argv[2] == "alice" && strings.TrimSuffix(stdin, "\n") == pamPW {
				return 0, "", "", nil
			}
			return 1, "", "Password: pamtester: Authentication failure\n", nil
		}
		return 127, "", "", nil
	}
	return run
}

func pamFixtures(t *testing.T, sudoDown *bool) map[string]any {
	t.Helper()
	dir := t.TempDir()
	pamd := filepath.Join(dir, "pam.d")
	require.NoError(t, os.MkdirAll(pamd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pamd, "filex"), []byte("x\n"), 0o644))
	for _, n := range []string{"pamtester", "sudo"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755))
	}
	t.Cleanup(pam.SwapForTest(pamMachine(sudoDown), pamd))
	return map[string]any{
		"pamtester_path": filepath.Join(dir, "pamtester"),
		"sudo_path":      filepath.Join(dir, "sudo"),
	}
}

func checkIDs(body map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	cs, _ := body["checks"].([]any)
	for _, c := range cs {
		m := c.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func auditDump(t *testing.T, store db.Store) string {
	t.Helper()
	rows, err := store.ListAuditRecent(t.Context(), 200)
	require.NoError(t, err)
	b, _ := json.Marshal(rows)
	return string(b)
}

func TestPAMProvider_CannotBeSwitchedOnWithoutItsTest(t *testing.T) {
	cfg := pamFixtures(t, nil)
	srv, client, store, _, _ := liveServer(t, nil)

	// No test account: the last step fails, and nothing is saved.
	status, body := patchProvider(t, client, srv.URL, "pam", map[string]any{"enabled": true, "config": cfg})
	assert.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "test_failed", body["error"])
	assert.Equal(t, true, body["strict"])
	// The sentence is the catalogue's (one key for every operating-system
	// provider), read from it rather than copied here.
	assert.Equal(t, srvtext.Text("en", "server.auth_provider.test_required", nil), body["message"])
	ids := checkIDs(body)
	require.Contains(t, ids, "test_account")
	assert.Equal(t, "fail", ids["test_account"]["status"])
	params := ids["test_account"]["params"].(map[string]any)
	assert.Equal(t, "missing", params["reason"])
	assert.NotEmpty(t, params["hint"])

	// `confirm_failed_test` is not an answer for this provider.
	status, body = patchProvider(t, client, srv.URL, "pam", map[string]any{"enabled": true, "config": cfg, "confirm_failed_test": true})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, true, body["strict"])
	_, provs := listProviders(t, client, srv.URL)
	assert.Equal(t, false, provs["pam"]["enabled"])
	assert.Equal(t, "off", provs["pam"]["state"])

	// A wrong password for the test account: same.
	status, body = patchProvider(t, client, srv.URL, "pam", map[string]any{
		"enabled": true, "config": cfg, "confirm_failed_test": true,
		"test_account": map[string]any{"username": "alice", "password": "not-the-password"},
	})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "wrong_password", checkIDs(body)["test_account"]["params"].(map[string]any)["reason"])

	// A setup that is not done: the earlier step is what is named.
	down := true
	cfg2 := pamFixtures(t, &down)
	status, body = patchProvider(t, client, srv.URL, "pam", map[string]any{
		"enabled": true, "config": cfg2, "confirm_failed_test": true,
		"test_account": map[string]any{"username": "alice", "password": pamPW},
	})
	assert.Equal(t, http.StatusConflict, status)
	ids = checkIDs(body)
	assert.Equal(t, "password_required", ids["sudo"]["params"].(map[string]any)["reason"])
	assert.NotContains(t, ids, "test_account", "no sign-in is attempted on a broken setup")
	assert.Contains(t, ids["sudo"]["params"].(map[string]any)["hint"], "NOPASSWD")

	// Nothing was created along the way.
	_, err := store.GetUserByEmail(t.Context(), "alice@local")
	assert.Error(t, err)
	assert.NotContains(t, auditDump(t, store), "not-the-password")
	assert.NotContains(t, auditDump(t, store), pamPW)
}

func TestPAMProvider_TestAccountBecomesSuperAdminAndPeopleSignIn(t *testing.T) {
	cfg := pamFixtures(t, nil)
	srv, client, store, _, _ := liveServer(t, nil)

	// "Test now" first: same steps, nothing saved, nobody created.
	status, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/pam/test", map[string]any{
		"config": cfg, "test_account": map[string]any{"username": "alice", "password": pamPW},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	var tested map[string]any
	require.NoError(t, json.Unmarshal(raw, &tested))
	assert.Equal(t, true, tested["ok"], string(raw))
	assert.NotContains(t, string(raw), pamPW)
	_, err := store.GetUserByEmail(t.Context(), "alice@local")
	assert.Error(t, err, "testing creates nothing")

	// Switch it on.
	status, body := patchProvider(t, client, srv.URL, "pam", map[string]any{
		"enabled": true, "config": cfg,
		"test_account": map[string]any{"username": "alice", "password": pamPW},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["test_ok"])
	raw, _ = json.Marshal(body)
	assert.NotContains(t, string(raw), pamPW, "the password is in no answer")

	alice, err := store.GetUserByEmail(t.Context(), "alice@local")
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, alice.Role, "the tested account is a super administrator")
	super, err := store.GetSupertenant(t.Context())
	require.NoError(t, err)
	require.NotNil(t, alice.ProviderID)
	assert.Equal(t, super.ID, *alice.ProviderID)

	dump := auditDump(t, store)
	assert.NotContains(t, dump, pamPW, "the password is in no audit row")
	assert.Contains(t, dump, "auth.os_admin_granted")
	assert.Contains(t, dump, "test_account_user")

	listRaw, provs := listProviders(t, client, srv.URL)
	assert.NotContains(t, listRaw, pamPW)
	assert.Equal(t, "running", provs["pam"]["state"])
	assert.Equal(t, true, provs["pam"]["testable"])
	assert.Contains(t, capsDrivers(t, client, srv.URL), "pam")

	// The person signs in with the login of the machine, in the browser.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	login := func(id, pw string) int {
		b, _ := json.Marshal(map[string]string{"email": id, "password": pw})
		resp, err := c.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(b))
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusUnauthorized, login("alice", "wrong"))
	assert.Equal(t, http.StatusOK, login("alice", pamPW))
	// A machine login with no account, and auto_create off (the default): no.
	assert.Equal(t, http.StatusUnauthorized, login("carol", pamPW))
	// root, whatever it is called on the machine, never.
	assert.Equal(t, http.StatusUnauthorized, login("root", pamPW))
	_, err = store.GetUserByEmail(t.Context(), "root@local")
	assert.Error(t, err)

	// The file protocols judge the same login (protocol_login is on by default):
	// WebDAV here, with the machine login as a Basic credential — by name, and
	// the resolver's own hand-over of the account's e-mail.
	dav := func(user, pw string) int {
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/dav/main/from-pam.txt", strings.NewReader("hello"))
		require.NoError(t, err)
		req.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusUnauthorized, dav("alice", "wrong"))
	// Past the door: this server has no storage called "main", so what comes
	// back is a 404 — not the 401 of a refused credential.
	assert.NotContains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, dav("alice", pamPW))
	assert.Equal(t, http.StatusUnauthorized, dav("root", pamPW))

	// Switching it off again is allowed (the bootstrap administrator's password
	// is still a way in) and a disabled provider saves without a test.
	status, body = patchProvider(t, client, srv.URL, "pam", map[string]any{"enabled": false})
	assert.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, http.StatusUnauthorized, login("alice", pamPW))
}

func TestPAMProvider_BrokenSetupAtStartKeepsOnlyItOut(t *testing.T) {
	// Provider saved and enabled while the machine was fine; the machine then
	// breaks; the next reload leaves pam out and everything else running.
	down := false
	cfg := pamFixtures(t, &down)
	srv, client, store, _, _ := liveServer(t, nil)
	status, body := patchProvider(t, client, srv.URL, "pam", map[string]any{
		"enabled": true, "config": cfg,
		"test_account": map[string]any{"username": "alice", "password": pamPW},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)

	down = true
	// A start-up of a new process reads the settings again and builds pam from
	// scratch; here that is a save that switches it off (the set forgets it) and
	// the stored switch turned back on, as after a restore from backup — then
	// any reload builds pam against the machine as it is now.
	status, body = patchProvider(t, client, srv.URL, "pam", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.NoError(t, store.UpsertSetting(t.Context(), "auth.pam.enabled", "true"))
	status, _ = patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status)

	_, provs := listProviders(t, client, srv.URL)
	assert.Equal(t, "failed", provs["pam"]["state"], "%v", provs["pam"])
	assert.Contains(t, provs["pam"]["error"], "sudo")
	assert.NotEmpty(t, provs["pam"]["error"])
	assert.Contains(t, auditDump(t, store), "auth.provider_unavailable")

	// The administrator's own way in is untouched.
	status, _ = doReq(t, client, http.MethodGet, srv.URL+"/api/auth/me", nil)
	assert.Equal(t, http.StatusOK, status)
}

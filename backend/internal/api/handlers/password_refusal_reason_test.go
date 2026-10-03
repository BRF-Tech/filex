package handlers_test

// A password provider's refusal AFTER a right password (0.50, docs/LDAP.md
// "The first sign-in rule"): by default it is the answer a wrong password gets,
// byte for byte; a provider whose operator switched show_refusal_reason on
// answers 403 with the reason code, and the change is in the audit trail. The
// Windows provider stands in for the password providers here: its
// operating-system call is a fake (windows.SetLogonForTest), so this runs on
// any machine; LDAP and PAM take the same setting through the same helper
// (auth.RefusedAfterPassword, their own package tests).
//
// Reason codes are written out as strings: they are the wire contract with
// web/src/lib/ssoRefusal.ts, and the tests compile against the code before
// them (red proof).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// windowsOn switches the Windows provider on (ayse proves it and becomes the
// super administrator) with the given extra settings.
func windowsOn(t *testing.T, base string, client *http.Client, config map[string]any) {
	t.Helper()
	if config == nil {
		config = map[string]any{}
	}
	status, out := patchProvider(t, client, base, "windows", map[string]any{
		"enabled": true, "config": config, "test_account": testAccount("ayse", winPw),
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
}

func signIn(t *testing.T, base, who, pw string) (int, map[string]any) {
	t.Helper()
	status, raw := doReq(t, freshClient(t), http.MethodPost, base+"/api/auth/login", map[string]any{"email": who, "password": pw})
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	return status, body
}

// ⚠⚠ The regression guard. Off - the default - a person whose password is
// right but whom the first-login rule refuses gets EXACTLY the answer a wrong
// password gets: same status, same body (each asked first on a server of its
// own, so the attempt counters agree).
func TestPasswordRefusal_OffItIsTheWrongPasswordAnswer(t *testing.T) {
	windowsMachine(t)
	srvA, clientA, _, _, _ := liveServer(t, nil)
	windowsOn(t, srvA.URL, clientA, nil)
	srvB, clientB, _, _, _ := liveServer(t, nil)
	windowsOn(t, srvB.URL, clientB, nil)

	refusedStatus, refused := signIn(t, srvA.URL, "can", winPw)
	wrongStatus, wrong := signIn(t, srvB.URL, "can", "not-the-password")
	assert.Equal(t, http.StatusUnauthorized, refusedStatus)
	assert.Equal(t, wrongStatus, refusedStatus)
	assert.Equal(t, wrong, refused, "the right password must not be told apart")
	assert.NotContains(t, refused, "reason")
}

// On, the form is told why - and only after a right password.
func TestPasswordRefusal_OnTheFormIsToldWhy(t *testing.T) {
	windowsMachine(t)
	srv, client, store, _, _ := liveServer(t, nil)
	windowsOn(t, srv.URL, client, map[string]any{"show_refusal_reason": true})
	ctx := context.Background()

	status, body := signIn(t, srv.URL, "can", winPw)
	assert.Equal(t, http.StatusForbidden, status, "%v", body)
	assert.Equal(t, "auto_create_off", body["reason"], "%v", body)
	_, gerr := store.GetUserByEmail(ctx, "can@local")
	assert.Error(t, gerr, "still no account")

	// A wrong password, an unknown account and an account refused by NAME
	// before any password is tried say nothing more than ever.
	for _, c := range [][2]string{{"can", "not-the-password"}, {"nobody", winPw}, {"Administrator", winPw}} {
		status, body = signIn(t, srv.URL, c[0], c[1])
		assert.Equal(t, http.StatusUnauthorized, status, c[0])
		assert.NotContains(t, body, "reason", c[0])
	}

	// The setting is the operator's, on the page's audit trail.
	rows, err := store.ListAuditRecent(ctx, 100)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		raw, _ := json.Marshal(r.Metadata["changed_fields"])
		if strings.Contains(string(raw), "show_refusal_reason") {
			found = true
		}
	}
	assert.True(t, found, "the change of show_refusal_reason is audited")
}

// The page lists the setting for the three password providers, off by default.
func TestPasswordRefusal_TheSettingIsOnThePageOffByDefault(t *testing.T) {
	srv, client, _, _, _ := liveServer(t, nil)
	_, list := listProviders(t, client, srv.URL)
	for _, name := range []string{"ldap", "pam", "windows"} {
		var def any = "missing"
		for _, f := range list[name]["fields"].([]any) {
			if m := f.(map[string]any); m["key"] == "show_refusal_reason" {
				def = m["default"]
			}
		}
		assert.Equal(t, "false", def, name)
	}
}

// The gates after the password say which, on the form as on the SSO page.
func TestPasswordRefusal_TheAccountGatesCarryTheirCode(t *testing.T) {
	srv, _, store, _, _ := liveServer(t, nil)
	ctx := context.Background()
	testutil.SeedRegularUser(t, store, "off@example.test", "Off-Pa55-word")
	u, err := store.GetUserByEmail(ctx, "off@example.test")
	require.NoError(t, err)
	require.NoError(t, store.SetUserEnabled(ctx, u.ID, false))
	status, body := signIn(t, srv.URL, "off@example.test", "Off-Pa55-word")
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "account_disabled", body["reason"], "%v", body)
	assert.Equal(t, true, body["disabled"], "the field API clients read stays")
}

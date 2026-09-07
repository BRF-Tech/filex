package handlers_test

// What a visitor to the public demo can reach, measured through HTTP.
//
// ⚠ Written to compile and run on the commit BEFORE the fix as well, so it is
// a red proof rather than a description of the new code: it names no new
// symbol, only routes and status codes. On the old build the demo half fails
// (every one of these answered 200/201/204/400) and the ordinary half passes.
//
// The exposure, measured on a local instance in demo mode on 2026-09-07,
// signed in with the published credentials:
//
//	POST   /api/admin/users/{id}/reset-password   200   ← locks every other reader out
//	DELETE /api/admin/users/{id}                  200
//	PATCH  /api/admin/settings                    200
//	POST   /api/auth/password                     200   ← same lockout, no admin needed
//	POST   /api/auth/totp/enroll                  200   ← same lockout, harder to undo
//	POST   /api/admin/ai-tokens                   201   ← mints an admin token, and then
//	PATCH  /api/ai/admin/settings                 200   ← the whole admin surface again
//
// …and 101 admin routes in total, none of which answered 403.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// demoWrite is one state-changing request a visitor can try.
type demoWrite struct {
	method string
	path   string
	body   string
	why    string
}

// demoWritesThatMustBeRefused is the list this change is about. Each row is a
// door that a stranger reading the published credentials could open.
var demoWritesThatMustBeRefused = []demoWrite{
	{"POST", "/api/admin/users/{victim}/reset-password", `{}`,
		"takes the shared account away from every other reader"},
	{"DELETE", "/api/admin/users/{victim}", ``,
		"deletes accounts"},
	{"POST", "/api/admin/users", `{"email":"x@y.z","password":"aaaaaaaaaa","role":"admin"}`,
		"creates accounts"},
	{"PATCH", "/api/admin/settings", `{"site_name":"visitor was here"}`,
		"rewrites the instance's own settings"},
	{"PUT", "/api/admin/settings/site_name", `{"value":"visitor was here"}`,
		"the same, one key at a time"},
	{"POST", "/api/admin/settings/smtp-test", `{}`,
		"makes the server connect where a visitor points it"},
	{"PATCH", "/api/admin/external/onlyoffice", `{"enabled":true,"url":"http://169.254.169.254/"}`,
		"repoints an external service at an address of the visitor's choosing"},
	{"PATCH", "/api/admin/auth-providers/oidc", `{"enabled":false}`,
		"decides who may sign in at all"},
	{"POST", "/api/admin/webhooks", `{"url":"http://127.0.0.1:1/"}`,
		"makes the server call out on every event"},
	{"POST", "/api/admin/update/apply", `{}`,
		"replaces the running binary"},
	{"POST", "/api/admin/trash/empty", `{}`,
		"destroys what other visitors put in the trash"},
	{"PATCH", "/api/admin/storages/1", `{"mount_path":"/etc"}`,
		"repoints an EXISTING storage — the create-side guard never covered this"},
	{"DELETE", "/api/admin/storages/1", ``,
		"deletes the storage the demo is demonstrating"},
	{"POST", "/api/admin/ai-tokens", `{"label":"x","user_id":1,"scopes":"admin"}`,
		"mints an admin-scoped token, which is the whole admin surface again"},
	// The identity surface. No admin role needed for any of these — they are
	// the demo account's own settings, and the account is shared.
	{"POST", "/api/auth/password", `{"current_password":"x","new_password":"aaaaaaaaaa"}`,
		"changes the published password"},
	{"PATCH", "/api/auth/profile", `{"email":"visitor@example.com"}`,
		"changes the e-mail the published credentials sign in with"},
	{"POST", "/api/auth/totp/enroll", `{}`,
		"puts a second factor on the shared account"},
}

// demoReadsThatMustStillWork is the other half of the decision. A demo exists
// to show the product INCLUDING the operator surfaces; a guard that blanks the
// admin area would be a cure worse than the disease.
var demoReadsThatMustStillWork = []string{
	"/api/admin/dashboard",
	"/api/admin/users",
	"/api/admin/settings",
	"/api/admin/storages",
	"/api/admin/audit",
	"/api/admin/shares",
	"/api/admin/duplicates",
	"/api/admin/external",
	"/api/admin/auth-providers",
}

func demoDo(t *testing.T, base string, client *http.Client, w demoWrite, victimID string) int {
	t.Helper()
	var body *bytes.Reader
	if w.body == "" {
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader([]byte(w.body))
	}
	req, err := http.NewRequest(w.method, base+strings.ReplaceAll(w.path, "{victim}", victimID), body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// startDemo brings up an instance in demo mode with an admin signed in — which
// is what the published credentials give a visitor.
func startDemo(t *testing.T, demo bool) (string, *http.Client, db.Store) {
	t.Helper()
	srv, client, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.Demo.Mode = demo
	}, nil)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return srv.URL, client, store
}

// seedVictim creates a second account and returns its id as a path segment.
//
// ⚠ The reset-password and delete rows MUST target somebody other than the
// caller. Aimed at the caller they succeed and then invalidate the session, so
// every later row answers 401 — which passes a "not 403" assertion while
// measuring nothing. That is exactly how the first run of the ordinary-install
// test went green without exercising a single door after the first one.
func seedVictim(t *testing.T, store db.Store) string {
	t.Helper()
	testutil.SeedRegularUser(t, store, "victim@test.local", "TestUserPass!1")
	u, err := store.GetUserByEmail(context.Background(), "victim@test.local")
	require.NoError(t, err)
	return strconv.FormatInt(u.ID, 10)
}

// TestDemo_WritesAreRefused — the fix, stated as behaviour.
func TestDemo_WritesAreRefused(t *testing.T) {
	base, client, store := startDemo(t, true)
	victim := seedVictim(t, store)

	for _, w := range demoWritesThatMustBeRefused {
		got := demoDo(t, base, client, w, victim)
		t.Logf("demo    %-6s %-42s -> %d", w.method, w.path, got)
		require.Equal(t, http.StatusForbidden, got,
			"%s %s must be refused on a demo: it %s", w.method, w.path, w.why)
	}
}

// TestDemo_ReadsStillWork — the demo is still a demo.
func TestDemo_ReadsStillWork(t *testing.T) {
	base, client, _ := startDemo(t, true)

	for _, p := range demoReadsThatMustStillWork {
		req, err := http.NewRequest(http.MethodGet, base+p, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		t.Logf("demo    GET    %-42s -> %d", p, resp.StatusCode)
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"GET %s must keep working: a demo that hides the admin panel is not demonstrating it", p)
	}
}

// TestOrdinaryInstall_WritesAreUnaffected is the promise to everybody who is
// NOT running a demo, and the reason this file compiles on the old build: it
// asserts the ordinary behaviour, so it passes there too.
//
// It looks only at whether the request was AUTHORIZED — a 400 from a
// deliberately thin body still proves the guard let it through, while 403 is
// the one answer that would mean an ordinary operator lost a door.
func TestOrdinaryInstall_WritesAreUnaffected(t *testing.T) {
	base, client, store := startDemo(t, false)
	victim := seedVictim(t, store)

	for _, w := range demoWritesThatMustBeRefused {
		if strings.HasPrefix(w.path, "/api/admin/update/apply") {
			// The one row with a side effect worth avoiding in a unit test.
			continue
		}
		got := demoDo(t, base, client, w, victim)
		t.Logf("ordinary %-6s %-42s -> %d", w.method, w.path, got)
		require.NotEqual(t, http.StatusForbidden, got,
			"%s %s must stay available on a normal install", w.method, w.path)
	}
}

// TestDemo_AuditIPsAreHidden — item 3. The audit page stays readable (it is
// one of the operator surfaces a demo exists to show) but the addresses in it
// are the other visitors'.
//
// ⚠ The row is inserted through the store rather than produced by a request,
// for two reasons: the audit middleware only records MUTATIONS, which is
// precisely what a demo now refuses, and the rows a visitor actually reads on
// demo.filex.sh came from the operator, frozen into the nightly golden copy.
// A test that waited for its own request to be audited would measure an empty
// list and pass on any build.
func TestDemo_AuditIPsAreHidden(t *testing.T) {
	const visitorIP = "203.0.113.44"

	for _, tc := range []struct {
		name   string
		demo   bool
		hidden bool
	}{
		{"demo hides it", true, true},
		{"an ordinary install still shows it", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, client, store := startDemo(t, tc.demo)
			require.NoError(t, store.InsertAuditEntry(context.Background(), &model.AuditEntry{
				Action: "user.login",
				IP:     visitorIP,
			}))

			// Both surfaces carry the same rows: the audit page and the
			// `recent_activity` block on the FIRST page of the admin panel.
			require.Equal(t, tc.hidden, !strings.Contains(getBody(t, base, client, "/api/admin/audit"), visitorIP),
				"/api/admin/audit")
			require.Equal(t, tc.hidden, !strings.Contains(getBody(t, base, client, "/api/admin/dashboard"), visitorIP),
				"/api/admin/dashboard recent_activity")
		})
	}
}

// TestDemo_AnonymousCapabilitiesHideTheOperatorsHosts — item 2, and NOT
// demo-specific: every install published whatever its operator configured.
//
// Measured on demo.filex.sh (2026-09-07): GET /api/files/capabilities with no
// credential at all answered 200 carrying "url":"https://docs.example.com".
func TestDemo_AnonymousCapabilitiesHideTheOperatorsHosts(t *testing.T) {
	const host = "https://docs.internal.example"

	srv, client, store := testutil.NewTestServerWith(t, nil, nil)
	require.NoError(t, store.UpsertExternalService(context.Background(),
		"onlyoffice", true, host, "", "{}", time.Now(), "ok"))

	// Anonymous: a stranger learns THAT the capability is on, never where.
	anon := &http.Client{}
	body := getBody(t, srv.URL, anon, "/api/files/capabilities")
	require.NotContains(t, body, host,
		"an anonymous caller must not be told the operator's internal host")
	require.Contains(t, body, `"onlyoffice"`,
		"…but must still be told the capability exists, or embedders cannot probe")

	// Signed in: unchanged, because the drawio iframe and the convert modal
	// need a real address and every caller of theirs is authenticated.
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	require.Contains(t, getBody(t, srv.URL, client, "/api/files/capabilities"), host,
		"an authenticated caller still gets the host it has to talk to")
}

func getBody(t *testing.T, base string, client *http.Client, path string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", path)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// TestDemo_AuthProviderSecretsAreMasked — found while enumerating what a demo
// visitor can READ, which is the half a guard on writes does not cover.
//
// GET /api/admin/auth-providers returns the stored provider config under a
// field called `config_redacted`. The redaction is by leaf NAME, and the admin
// UI saves the whole provider config as ONE leaf called `config` holding a
// JSON document — so the name check saw nothing secret about "config" and the
// document went out with `client_secret` in clear, to whoever read the demo
// credentials off the landing page.
//
// Masked on a demo only: on an ordinary install the operator is entitled to
// read back what they configured, and blanking it would leave the provider
// form unable to show its own settings.
func TestDemo_AuthProviderSecretsAreMasked(t *testing.T) {
	const secret = "OIDC-Cli3nt-Secret"

	for _, tc := range []struct {
		name   string
		demo   bool
		masked bool
	}{
		{"demo masks it", true, true},
		{"an ordinary install still shows it", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, client, store := startDemo(t, tc.demo)
			require.NoError(t, store.UpsertSetting(context.Background(), "auth.oidc.config",
				`{"issuer":"https://auth.example","client_id":"filex","client_secret":"`+secret+`"}`))

			body := getBody(t, base, client, "/api/admin/auth-providers")
			require.Equal(t, tc.masked, !strings.Contains(body, secret),
				"/api/admin/auth-providers")
		})
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
)

// On a public demo the admin pages are readable by whoever read the published
// credentials - and the addresses in the sign-in pages belong to the OTHER
// visitors (and the allow-list to the operator). Every surface masks them.
//
// ⚠ Measured before the fix (2026-10-01), signed in with the demo's
// credentials: GET /api/admin/login-security/attempts listed every visitor's
// address, /locks named each locked address as its `subject` and every
// counter's `last_ip`, an address lock's audit rows named the address as
// their target on the audit page and the dashboard, the Sign-in security
// settings showed the operator's allow-list - and so did /api/ai/admin and the
// admin_* MCP tools, the same handlers behind a token.

// The addresses a visitor must never read on a demo.
var (
	visitorLocked1  = "203.0.113.41"
	visitorLocked2  = "203.0.113.42"
	visitorCounting = "203.0.113.43"
	visitorUnlocked = "203.0.113.44"
	visitorV6       = "2001:db8::45"
	typedAsName     = "203.0.113.47"
	adminAddress    = "203.0.113.48"
	allowAddress    = "192.0.2.77"
	allowNetwork    = "198.51.100.0/24"
	proxyAddress    = "10.9.8.7"
	// Names typed at the sign-in form that are no account of the instance:
	// a visitor's own e-mail, typed instead of the demo's, is theirs.
	strangerNames     = []string{"stranger.one@example.org", "strangertwo", "acme/stranger.three@example.org"}
	strangerAddresses = []string{"203.0.113.50", "203.0.113.51", "203.0.113.52"}
	demoSecrets       = append([]string{visitorLocked1, visitorLocked2, visitorCounting, visitorUnlocked, visitorV6,
		typedAsName, adminAddress, allowAddress, "198.51.100.0", proxyAddress,
		"visitora0@example.com", "stranger.three@example.org"}, append(strangerNames, strangerAddresses...)...)
)

// seedSignInHistory leaves what a demo's golden copy holds: addresses locked,
// an account being counted, settings the operator saved with demo mode off,
// and the audit rows of an administrator who lifted an address lock and
// changed the allow-list before the demo went public.
func seedSignInHistory(t *testing.T, e *secEnv) {
	t.Helper()
	ctx := context.Background()
	for k, v := range map[string]string{
		loginguard.KeyIPMax:        "3",
		loginguard.KeyIPAllowlist:  allowAddress + ", " + allowNetwork,
		loginguard.KeyTrustedProxy: "loopback, " + proxyAddress,
	} {
		require.NoError(t, e.store.UpsertSetting(ctx, k, v))
	}
	e.guard.Invalidate()
	for i, ip := range []string{visitorLocked1, visitorLocked2} {
		for j := 0; j < 3; j++ {
			e.wrong(t, ip, "visitor"+string(rune('a'+i))+string(rune('0'+j))+"@example.com", 1)
		}
	}
	// ada has an account here (a golden-copy account a demo shows); the
	// "visitor…" names and the strangers below name nobody.
	seedUser(t, e.store, "ada@example.com", "AdaPass!1")
	e.wrong(t, visitorCounting, "ada@example.com", 1)
	for i, name := range strangerNames {
		e.wrong(t, strangerAddresses[i], name, 1)
	}
	e.wrong(t, visitorV6, "six@example.com", 1)
	e.wrong(t, visitorCounting, typedAsName, 1)

	require.NoError(t, e.store.InsertAuditEntry(ctx, &model.AuditEntry{
		Action: loginguard.ActionUnlocked, TargetType: "login", TargetID: visitorUnlocked, IP: adminAddress,
		Metadata:  map[string]any{"scope": "ip", "subject": visitorUnlocked, "target_name": visitorUnlocked, "reason": "admin"},
		CreatedAt: time.Now().Add(-time.Hour),
	}))
	require.NoError(t, e.store.InsertAuditEntry(ctx, &model.AuditEntry{
		Action: loginguard.ActionSettingsUpdate, TargetType: "login_security", IP: adminAddress,
		Metadata: map[string]any{
			"ip_allowlist": allowAddress + ", " + allowNetwork, "trusted_proxies": "loopback, " + proxyAddress,
			"changed_fields": []string{"ip_allowlist", "trusted_proxies"},
		},
		CreatedAt: time.Now().Add(-time.Hour),
	}))
}

// get reads one route as the signed-in administrator - the session for the
// panel's routes, the API key for /api/ai/* - and answers status and body.
func (e *secEnv) get(t *testing.T, tok, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	require.NoError(t, err)
	c := e.admin
	if strings.HasPrefix(path, "/api/ai/") {
		req.Header.Set("X-Filex-Token", tok)
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, string(b)
}

func leaked(body string) []string {
	var out []string
	for _, s := range demoSecrets {
		if strings.Contains(body, s) {
			out = append(out, s)
		}
	}
	return out
}

var readSurfaces = []string{
	"/api/admin/audit?limit=500",
	"/api/admin/dashboard",
	"/api/admin/settings",
	"/api/admin/login-security",
	"/api/admin/login-security/locks",
	"/api/admin/login-security/attempts?limit=500",
	"/api/ai/admin/audit?limit=500",
	"/api/ai/admin/dashboard",
	"/api/ai/admin/settings",
	"/api/ai/admin/login-security",
	"/api/ai/admin/login-security/locks",
	"/api/ai/admin/login-security/attempts?limit=500",
}

var readTools = []string{
	"admin_dashboard", "admin_audit_list", "admin_settings_get",
	"admin_login_security_get", "admin_login_security_locks", "admin_login_security_attempts",
}

func newMaskEnv(t *testing.T, demo bool) (*secEnv, string) {
	t.Helper()
	e := newSecEnv(t, func(c *config.Config) { c.Demo.Mode = demo })
	admin, err := e.store.GetUserByEmail(context.Background(), e.email)
	require.NoError(t, err)
	tok := issueToken(t, e.store, admin.ID, "read,mcp,admin", nil)
	seedSignInHistory(t, e)
	return e, tok
}

func TestDemo_SignInAddressesAreHiddenOnEverySurface(t *testing.T) {
	e, tok := newMaskEnv(t, true)

	for _, p := range readSurfaces {
		status, body := e.get(t, tok, p)
		require.Equal(t, http.StatusOK, status, p)
		require.Empty(t, leaked(body), "GET %s shows an address on a demo", p)
		require.Contains(t, body, "hidden on the demo", "GET %s says it hid something", p)
	}
	for _, tool := range readTools {
		body := mcpTool(t, e.srv.URL, tok, tool, `{}`)
		require.Empty(t, leaked(body), "MCP %s shows an address on a demo", tool)
	}

	// What is not a visitor's is still there: the trail is a trail.
	_, body := e.get(t, tok, "/api/admin/login-security/attempts?limit=500")
	require.Contains(t, body, "ada@example.com")
	require.Contains(t, body, `"protocol":"web"`)

	// Every GET route of the instance, walked from the router - a surface
	// nobody listed above is a surface all the same.
	routes, ok := e.srv.Config.Handler.(chi.Routes)
	require.True(t, ok)
	param := regexp.MustCompile(`\{[^}]*\}`)
	walked := 0
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet || !strings.HasPrefix(route, "/api/") || route == "/api/ws" {
			return nil
		}
		p := strings.TrimSuffix(param.ReplaceAllString(route, "1"), "/*")
		status, body := e.get(t, tok, p)
		if status == 0 {
			return nil
		}
		walked++
		require.Empty(t, leaked(body), "GET %s (%d) shows an address on a demo", p, status)
		return nil
	}))
	require.Greater(t, walked, 100, "the walk reached the routes")
}

// The locks list keeps working when the subjects look alike: every row has an
// id of its own, and a masked address cannot be used to act.
func TestDemo_MaskedLocksStayDistinctAndCannotBeActedOn(t *testing.T) {
	e, tok := newMaskEnv(t, true)
	_, body := e.get(t, tok, "/api/admin/login-security/locks")
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Scope   string `json:"scope"`
			Subject string `json:"subject"`
			LastIP  string `json:"last_ip"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	ids := map[string]bool{}
	masked := 0
	for _, it := range out.Items {
		require.NotEmpty(t, it.ID)
		require.False(t, ids[it.ID], "row ids are unique: %s", it.ID)
		ids[it.ID] = true
		if it.Scope == "ip" {
			require.Equal(t, "hidden on the demo", it.Subject)
			masked++
		}
		if it.LastIP != "" {
			require.Equal(t, "hidden on the demo", it.LastIP)
		}
	}
	require.GreaterOrEqual(t, masked, 2, "two address locks that look alike")

	status, _ := e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "ip", "subject": "hidden on the demo"})
	require.Equal(t, http.StatusForbidden, status)
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "ip", "subject": visitorLocked1})
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, http.StatusForbidden, tokenCall(t, e.srv.URL, tok, http.MethodPost, "/api/ai/admin/login-security/unlock",
		`{"scope":"ip","subject":"`+visitorLocked1+`"}`))

	// The MCP admin tools reach the same handlers in-process: a demo refuses
	// their writes exactly as it refuses the routes'.
	for _, c := range []struct{ tool, args string }{
		{"admin_login_security_unlock", `{"body":{"scope":"ip","subject":"` + visitorLocked1 + `"}}`},
		{"admin_login_security_unlock", `{"body":{"all":true}}`},
		{"admin_login_security_update", `{"body":{"ip_max_fails":50}}`},
		{"admin_settings_set", `{"key":"site_name","body":{"value":"visitor was here"}}`},
	} {
		code, body := mcpPost(t, http.DefaultClient, e.srv.URL+"/api/ai/mcp", tok,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+c.tool+`","arguments":`+c.args+`}}`)
		require.Equal(t, http.StatusOK, code, body)
		require.Contains(t, body, `"isError":true`, "%s must be refused on a demo: %s", c.tool, body)
		require.Contains(t, body, "403", c.tool)
		require.Contains(t, body, "public demo", c.tool)
	}
	_, locks := e.do(t, http.MethodGet, "/locks?locked=1", nil)
	require.GreaterOrEqual(t, len(locks["items"].([]any)), 2, "nothing was unlocked")
	v, err := e.store.GetSetting(context.Background(), loginguard.KeyIPMax)
	require.NoError(t, err)
	require.Equal(t, "3", v, "nothing was changed")
}

// An ordinary install shows the operator every address: the masking runs only
// on a demo, and this is what proves the surfaces above had something to hide.
func TestOrdinaryInstall_SignInAddressesAreShown(t *testing.T) {
	e, tok := newMaskEnv(t, false)
	want := map[string][]string{
		"/api/admin/login-security/attempts?limit=500": {visitorLocked1, visitorV6, typedAsName, visitorUnlocked},
		"/api/admin/login-security/locks":              {visitorLocked1, visitorLocked2, visitorCounting},
		"/api/admin/audit?limit=500":                   {visitorUnlocked, adminAddress, allowAddress, proxyAddress},
		"/api/admin/login-security":                    {allowAddress, allowNetwork, proxyAddress},
		"/api/admin/settings":                          {allowAddress, proxyAddress},
		"/api/ai/admin/login-security/attempts":        {visitorLocked1},
	}
	for p, secrets := range want {
		status, body := e.get(t, tok, p)
		require.Equal(t, http.StatusOK, status, p)
		for _, s := range secrets {
			require.Contains(t, body, s, "GET %s on an ordinary install", p)
		}
		require.NotContains(t, body, "hidden on the demo", p)
	}
	body := mcpTool(t, e.srv.URL, tok, "admin_login_security_locks", `{}`)
	require.Contains(t, body, visitorLocked1)
	// The MCP admin writes work on an ordinary install.
	mcpTool(t, e.srv.URL, tok, "admin_login_security_unlock", `{"body":{"scope":"ip","subject":"`+visitorLocked1+`"}}`)
}

// A name typed at the sign-in form that is no account of the instance is a
// visitor's - their own e-mail, typed instead of the demo's - and a demo hides
// it like an address. The names of the instance's own accounts stay readable,
// typed bare, as a username or with a realm in front.
func TestDemo_TypedNamesOfNoAccountAreHidden(t *testing.T) {
	for _, demo := range []bool{true, false} {
		e, tok := newMaskEnv(t, demo)
		known := seedUser(t, e.store, "known@example.com", "KnownPass!1")
		known, err := e.store.GetUser(context.Background(), known.ID)
		require.NoError(t, err)
		require.NotEmpty(t, known.Username)
		for _, name := range []string{"known@example.com", known.Username, "acme/known@example.com"} {
			e.wrong(t, "192.0.2.10", name, 1)
		}

		for _, p := range []string{
			"/api/admin/login-security/attempts?limit=500",
			"/api/admin/login-security/locks",
			"/api/admin/audit?limit=500",
			"/api/ai/admin/login-security/attempts?limit=500",
			"/api/ai/admin/login-security/locks",
			"/api/ai/admin/audit?limit=500",
		} {
			_, body := e.get(t, tok, p)
			for _, s := range strangerNames {
				if demo {
					require.NotContains(t, body, s, "GET %s on a demo", p)
				} else {
					require.Contains(t, body, s, "GET %s on an ordinary install shows every name", p)
				}
			}
			for _, s := range []string{"known@example.com", known.Username, "acme/known@example.com"} {
				require.Contains(t, body, s, "GET %s: an account of the instance stays readable (demo=%v)", p, demo)
			}
		}
		body := mcpTool(t, e.srv.URL, tok, "admin_login_security_attempts", `{"filters":{"limit":500}}`)
		require.Equal(t, demo, !strings.Contains(body, "stranger.one@example.org"), "MCP, demo=%v", demo)
		require.Contains(t, body, "known@example.com")
	}
}

package handlers_test

// Signing in to a tenant by its realm on a multi-tenant install
// (docs/MULTI-TENANCY.md, Realms; #128). The owner's rules, each measured
// here:
//
//   - the platform's page: an empty realm is the platform's own tenant, a realm
//     is that tenant — and the lookup never leaves it;
//   - a tenant's own page: its realm, locked; a body that names another is
//     refused;
//   - a realm nobody has is answered exactly as a wrong password;
//   - a tenant with an address of its own is handed there with a one-use,
//     short-lived ticket honoured on that address only;
//   - a single-tenant install is unchanged: no realm field, a realm in the body
//     is ignored.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const realmPass = "RealmPass!1"

type realmFixture struct {
	srv                   *httptest.Server
	store                 db.Store
	acme, beta            *model.Provider
	acmeAlex, betaAlex    *model.User
	mainAlex              *model.User
	acmeHost, platformURL string
}

// realmServer builds a multi-tenant server with three people called alex —
// one in the platform's own tenant, one in acme (which has an address of its
// own) and one in beta (which has none) — each with its own local password.
func realmServer(t *testing.T) *realmFixture {
	t.Helper()
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.MultiTenant = true })
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "files.acme.test", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	require.Equal(t, "acme", acme.Realm)

	mk := func(email string, p *model.Provider) *model.User {
		hash, err := authlocal.HashPassword(realmPass)
		require.NoError(t, err)
		u, err := store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		if p != nil {
			require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, ""))
		}
		u, err = store.GetUser(ctx, u.ID)
		require.NoError(t, err)
		return u
	}
	f := &realmFixture{srv: srv, store: store, acme: acme, beta: beta, acmeHost: "files.acme.test", platformURL: srv.URL}
	f.mainAlex = mk("alex@local", nil)
	f.acmeAlex = mk("alex@acme.local", acme)
	f.betaAlex = mk("alex@beta.local", beta)
	return f
}

// post sends a JSON body with the given Host header and returns status,
// decoded body and the session cookie it set ("" when none).
func (f *realmFixture) post(t *testing.T, host, path string, body any) (int, map[string]any, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+path, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	cookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == authlocal.SessionCookieName && c.Value != "" {
			cookie = c.Value
		}
	}
	return resp.StatusCode, out, cookie
}

func (f *realmFixture) login(t *testing.T, host, realm, id string) (int, map[string]any, string) {
	t.Helper()
	body := map[string]any{"email": id, "password": realmPass}
	if realm != "" {
		body["realm"] = realm
	}
	return f.post(t, host, "/api/auth/login", body)
}

func userID(t *testing.T, body map[string]any) int64 {
	t.Helper()
	u, ok := body["user"].(map[string]any)
	require.True(t, ok, "no user in %v", body)
	return int64(u["id"].(float64))
}

func TestRealmLogin_PlatformPage(t *testing.T) {
	f := realmServer(t)

	// No realm: the platform's own alex, and nobody else's.
	code, body, cookie := f.login(t, "", "", "alex")
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, f.mainAlex.ID, userID(t, body))
	require.NotEmpty(t, cookie)

	// ⚠ The tenant's account is NOT reached from the platform's own realm —
	// not by its address, not by its username.
	code, body, _ = f.login(t, "", "", "alex@acme.local")
	require.Equal(t, http.StatusUnauthorized, code)
	require.Equal(t, "invalid credentials", body["error"])
	code, _, _ = f.login(t, "", "", f.acmeAlex.Username)
	require.Equal(t, http.StatusUnauthorized, code)

	// realm beta (no address of its own): signed in right here.
	code, body, cookie = f.login(t, "", "beta", "alex")
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, f.betaAlex.ID, userID(t, body))
	require.NotEmpty(t, cookie)

	// ⚠ Across tenants, by any name: refused. (`alex` itself is beta's own
	// alex in realm beta — the derived address comes first.)
	for _, id := range []string{"alex@acme.local", f.acmeAlex.Username, "alex@local"} {
		code, _, _ = f.login(t, "", "beta", id)
		require.Equal(t, http.StatusUnauthorized, code, "beta realm, %q", id)
	}
}

func TestRealmLogin_UnknownRealmIsAWrongPassword(t *testing.T) {
	f := realmServer(t)
	codeA, unknown, _ := f.login(t, "", "nobody", "alex")
	wrong := map[string]any{"email": "alex", "password": "not-it", "realm": "beta"}
	codeB, bad, _ := f.post(t, "", "/api/auth/login", wrong)
	require.Equal(t, http.StatusUnauthorized, codeA)
	require.Equal(t, codeB, codeA)
	require.Equal(t, keysOf(bad), keysOf(unknown), "the same shape as a wrong password")
	require.Equal(t, bad["error"], unknown["error"])
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestRealmLogin_TenantPage(t *testing.T) {
	f := realmServer(t)

	// On acme's own address the address says the realm: alex is acme's.
	code, body, cookie := f.login(t, f.acmeHost, "", "alex")
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, f.acmeAlex.ID, userID(t, body))
	require.NotEmpty(t, cookie)
	require.Nil(t, body["handoff"], "already on the tenant's address: no handoff")

	// The locked field sends the same realm back: fine.
	code, _, _ = f.login(t, f.acmeHost, "ACME", "alex")
	require.Equal(t, http.StatusOK, code)

	// ⚠ A body that names another tenant than the address: refused.
	code, body, _ = f.login(t, f.acmeHost, "beta", "alex")
	require.Equal(t, http.StatusUnauthorized, code)
	require.Equal(t, "invalid credentials", body["error"])
	// …and the platform's account is not reached from a tenant's address.
	code, _, _ = f.login(t, f.acmeHost, "", "alex@local")
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestRealmLogin_HandoffToTheTenantsAddress(t *testing.T) {
	f := realmServer(t)

	code, body, cookie := f.login(t, "", "acme", "alex")
	require.Equal(t, http.StatusOK, code, body)
	require.Empty(t, cookie, "no session on the platform's address")
	require.Nil(t, body["token"])
	ho, ok := body["handoff"].(map[string]any)
	require.True(t, ok, "a handoff: %v", body)
	require.Equal(t, "http://files.acme.test", ho["origin"])
	ticket, _ := ho["code"].(string)
	require.GreaterOrEqual(t, len(ticket), 32)

	// ⚠ Presented on another host: refused — and spent.
	code, _, _ = f.post(t, "", "/api/auth/handoff", map[string]any{"code": ticket})
	require.Equal(t, http.StatusUnauthorized, code)
	code, _, _ = f.post(t, f.acmeHost, "/api/auth/handoff", map[string]any{"code": ticket})
	require.Equal(t, http.StatusUnauthorized, code, "a ticket shown on the wrong host is gone")

	// A fresh one, on the tenant's address: the session is opened there, once.
	_, body, _ = f.login(t, "", "acme", "alex")
	ticket = body["handoff"].(map[string]any)["code"].(string)
	code, body, cookie = f.post(t, f.acmeHost, "/api/auth/handoff", map[string]any{"code": ticket})
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, f.acmeAlex.ID, userID(t, body))
	require.NotEmpty(t, cookie)
	code, _, _ = f.post(t, f.acmeHost, "/api/auth/handoff", map[string]any{"code": ticket})
	require.Equal(t, http.StatusUnauthorized, code, "a ticket is spent by its first use")

	// Nonsense and nothing.
	code, _, _ = f.post(t, f.acmeHost, "/api/auth/handoff", map[string]any{"code": strings.Repeat("0", 64)})
	require.Equal(t, http.StatusUnauthorized, code)
	code, _, _ = f.post(t, f.acmeHost, "/api/auth/handoff", map[string]any{"code": ""})
	require.Equal(t, http.StatusUnauthorized, code)
}

// The handoff ticket, redeemed on the handler: its life, its host, its
// purpose, the account's tenant and the actor/subject rule are each enough to
// refuse it, and every refusal of a real ticket, like its use, is audited —
// never with the code.
func TestHandoff_TicketRules(t *testing.T) {
	_, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.MultiTenant = true })
	ctx := context.Background()
	p, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Host: "files.acme.test", Enabled: true})
	require.NoError(t, err)
	hash, _ := authlocal.HashPassword(realmPass)
	u, err := store.CreateUser(ctx, "x@acme.local", hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, ""))

	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	hs := auth.NewHandoffStore()
	hs.Now = func() time.Time { return now }
	h := &handlers.Auth{Store: store, MultiTenant: true, Handoffs: hs}
	redeem := func(code string) int {
		b, _ := json.Marshal(map[string]string{"code": code})
		r := httptest.NewRequest(http.MethodPost, "http://files.acme.test/api/auth/handoff", bytes.NewReader(b))
		w := httptest.NewRecorder()
		h.Handoff(w, r)
		return w.Code
	}
	issue := func(tk auth.HandoffTicket, ttl time.Duration) string {
		t.Helper()
		code, err := hs.Issue(tk, ttl)
		require.NoError(t, err)
		return code
	}
	good := auth.HandoffTicket{Purpose: auth.HandoffLogin, ActorID: u.ID, SubjectID: u.ID, Host: "files.acme.test", ProviderID: p.ID}

	// Expired: a minute passes.
	stale := issue(good, time.Minute)
	now = now.Add(61 * time.Second)
	require.Equal(t, http.StatusUnauthorized, redeem(stale), "expired")

	// Bound to its host on its own: minted for another address, refused here
	// although the account is this host's tenant's.
	elsewhere := good
	elsewhere.Host = "files.elsewhere.test"
	require.Equal(t, http.StatusUnauthorized, redeem(issue(elsewhere, time.Minute)), "minted for another host")

	// Bound to its purpose: a ticket for anything else is not a sign-in.
	other := good
	other.Purpose = "impersonate"
	require.Equal(t, http.StatusUnauthorized, redeem(issue(other, time.Minute)), "another purpose")

	// A sign-in handoff is one account's: actor and subject must agree.
	mixed := good
	mixed.ActorID = u.ID + 1000
	require.Equal(t, http.StatusUnauthorized, redeem(issue(mixed, time.Minute)), "actor is not subject")

	// Bound to the tenant the host names: an account of another tenant is
	// refused even on the right host.
	stranger, err := store.CreateUser(ctx, "y@elsewhere.local", hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	foreign := good
	foreign.ActorID, foreign.SubjectID = stranger.ID, stranger.ID
	require.Equal(t, http.StatusUnauthorized, redeem(issue(foreign, time.Minute)), "another tenant's account")

	// The good one, once.
	code := issue(good, time.Minute)
	require.Equal(t, http.StatusOK, redeem(code))
	require.Equal(t, http.StatusUnauthorized, redeem(code), "spent")

	// The trail: one row per refusal of a real ticket, one for the use, with
	// the reason — and never the code.
	rows, err := store.ListAuditRecent(ctx, 100)
	require.NoError(t, err)
	reasons := map[string]int{}
	used := 0
	for _, r := range rows {
		raw, _ := json.Marshal(r)
		require.NotContains(t, string(raw), code, "the code reached the audit log")
		switch r.Action {
		case auth.AuditHandoffRefused:
			reasons[fmt.Sprint(r.Metadata["reason"])]++
		case auth.AuditHandoffUsed:
			used++
			require.Equal(t, "login_handoff", r.Metadata["purpose"])
		}
	}
	require.Equal(t, 1, used)
	for _, want := range []string{auth.HandoffRefuseExpired, auth.HandoffRefuseHost, auth.HandoffRefusePurpose, auth.HandoffRefuseActorPolicy, auth.HandoffRefuseTenant} {
		require.Equal(t, 1, reasons[want], "refusal %q audited once: %v", want, reasons)
	}
	require.Zero(t, reasons[auth.HandoffRefuseUnknown], "a spent or unknown code is not audited (anonymous flood)")
}

// A real sign-in handoff leaves the issue in the audit trail, with its actor
// and subject.
func TestHandoff_IssueIsAudited(t *testing.T) {
	f := realmServer(t)
	code, body, _ := f.login(t, "", "acme", "alex")
	require.Equal(t, http.StatusOK, code, body)
	rows, err := f.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == auth.AuditHandoffIssued {
			found = true
			require.Equal(t, "files.acme.test", r.Metadata["host"])
			require.EqualValues(t, f.acmeAlex.ID, r.Metadata["subject_id"])
			require.EqualValues(t, f.acmeAlex.ID, r.Metadata["actor_id"])
		}
	}
	require.True(t, found, "no auth.handoff_issued row")
}

func TestRealmCapabilities(t *testing.T) {
	f := realmServer(t)
	get := func(host string) map[string]any {
		req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/api/capabilities", nil)
		require.NoError(t, err)
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}
	plat, ok := get("")["realm"].(map[string]any)
	require.True(t, ok, "a multi-tenant install has a realm field")
	require.Equal(t, true, plat["enabled"])
	require.Nil(t, plat["locked_realm"], "empty and free on the platform's page")

	acme := get(f.acmeHost)["realm"].(map[string]any)
	require.Equal(t, "acme", acme["locked_realm"], "filled and locked on the tenant's own page")

	// Single-tenant: no realm at all.
	single, _, _ := testutil.NewTestServer(t)
	resp, err := http.Get(single.URL + "/api/capabilities")
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	_, has := out["realm"]
	require.False(t, has, "a single-tenant answer carries no realm")
}

// A single-tenant install does not read the realm at all.
func TestRealmLogin_SingleTenantIgnoresTheRealm(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	b, _ := json.Marshal(map[string]string{"email": email, "password": pass, "realm": "whatever"})
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// /api/auth/me names a tenant account's realm (the connection guides print
// it); the platform's accounts have none.
func TestRealmInMe(t *testing.T) {
	f := realmServer(t)
	me := func(cookie string) map[string]any {
		req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/auth/me", nil)
		req.AddCookie(&http.Cookie{Name: authlocal.SessionCookieName, Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}
	_, _, c := f.login(t, "", "beta", "alex")
	require.Equal(t, "beta", me(c)["realm"])
	// …and the SFTP/FTPS login name the connection guide prints carries it:
	// SSH never says which address the client dialled.
	require.Equal(t, "beta/"+f.betaAlex.Username, sshLogin(t, f, c))
	_, _, c = f.login(t, "", "", "alex")
	_, has := me(c)["realm"]
	require.False(t, has)
	require.Equal(t, f.mainAlex.Username, sshLogin(t, f, c))
}

func sshLogin(t *testing.T, f *realmFixture, cookie string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/auth/ssh-keys", nil)
	req.AddCookie(&http.Cookie{Name: authlocal.SessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	login, _ := out["login"].(string)
	return login
}

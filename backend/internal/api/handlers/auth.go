package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

// Auth handles login/logout/oidc routes.
type Auth struct {
	Store       db.Store
	LocalAuth   auth.LoginDriver
	OIDCAuth    auth.OIDCDriver
	PublicURL   string
	MultiTenant bool
	// CookieDomain (FILEX_COOKIE_DOMAIN) is the GLOBAL session-cookie Domain
	// attribute. In multi-tenant mode it is only the last-resort fallback —
	// see cookieDomain() for the per-provider resolution. Applied on clear
	// too — a logout without the matching Domain leaves the old cookie
	// behind.
	CookieDomain string
	// Tenants resolves the per-request origin for absolute URLs. See
	// internal/tenanturl — it is the one implementation of this rule, and
	// redirectBase below is now just its caller.
	Tenants tenanturl.Resolver
	// OIDCLocalLogout (FILEX_OIDC_LOGOUT=local) keeps sign-out inside filex:
	// the IdP's session is left open, as it was before RP-initiated logout.
	OIDCLocalLogout bool
	// Guard limits wrong sign-in attempts per account identifier and per
	// address (internal/loginguard). Nil = no limit, which is what a handler
	// built without one (a test, an embedder) had before.
	Guard *loginguard.Guard
	// EmailToken is the installation's e-mail token (FILEX_OS_LOGIN_EMAIL_TOKEN,
	// identity.EmailToken): a realm's derived addresses end in it
	// (`alex@acme.local`). "" = the default.
	EmailToken string
	// Handoffs holds the one-use tickets that carry a sign-in from the
	// platform's address to a tenant's own (see handOff; auth.HandoffStore).
	// Nil = no handoff: the session is opened where the person signed in, as
	// before.
	Handoffs *auth.HandoffStore
	// Live is the running set of sign-in providers: the sign-in page asks it
	// which methods a realm has (Methods). Nil on a harness that has none;
	// Methods then answers from LocalAuth / OIDCAuth alone.
	Live *authsetup.Live
}

// handoffTTL is how long a sign-in handed to a tenant's address may take to
// arrive there: one browser navigation. Short on purpose — the ticket is a
// session in all but name until it is spent.
const handoffTTL = 60 * time.Second

// NewAuth constructs an Auth handler.
func NewAuth(store db.Store, local auth.LoginDriver, oidc auth.OIDCDriver, publicURL string, multiTenant bool, cookieDomain string) *Auth {
	return &Auth{
		Store: store, LocalAuth: local, OIDCAuth: oidc,
		PublicURL: publicURL, MultiTenant: multiTenant, CookieDomain: cookieDomain,
		Tenants: tenanturl.New(store, publicURL, multiTenant),
	}
}

// available reports whether a sign-in path is there to use. The server hands
// these handlers the running set's proxies (authsetup.Live), which are never
// nil and answer through Available instead, so a provider switched on or off
// on the Identity providers page takes effect without re-wiring anything.
func available(d any) bool {
	if d == nil {
		return false
	}
	if a, ok := d.(interface{ Available() bool }); ok {
		return a.Available()
	}
	return true
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// TOTP is the second-factor code. The SPA's Login.vue sends it under
	// this key; it is only consulted when the resolved user has TOTP
	// enabled.
	TOTP string `json:"totp"`
	// Realm is the tenant realm the person typed (docs/MULTI-TENANCY.md,
	// Realms). Read on a multi-tenant install only — the form shows the field
	// only there; a single-tenant install ignores it. Empty = the tenant the
	// address names, else the platform's own.
	Realm string `json:"realm"`
}

// Login authenticates email + password and sets the session cookie.
//
// When the resolved user has TOTP enabled, a valid second-factor code is
// mandatory: password success alone does NOT grant a session. The session
// is minted by LocalAuth.Login (which owns the password check), so on a
// missing/invalid TOTP code we revoke that just-created session before
// returning — no usable cookie is ever handed out and no orphan session
// lingers.
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !available(h.LocalAuth) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "local login disabled"})
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// ⚠ Stamp the Host onto the context before the login chain runs. A driver
	// that provisions accounts just-in-time (LDAP) sees only a ctx —
	// auth.LoginDriver.Login takes no *http.Request — and without the host it
	// has no way to tell which tenant the new account belongs to, so it homed
	// every one of them in the confine-exempt supertenant. See
	// auth.WithLoginHost / auth.ProvisionUser.
	ctx := auth.WithRequestLoginHost(r.Context(), r)

	// Which tenant this sign-in is for (multi-tenant only; nil otherwise): the
	// realm typed, the tenant's own address, else the platform's own tenant.
	// Every account lookup below stays inside it (auth.ResolveAccount).
	lr, realmErr := h.loginRealm(ctx, r, req.Realm)
	ctx = auth.WithLoginRealm(ctx, lr)

	// ⚠ The limit sits ABOVE the driver chain: it is asked before any password
	// is compared (so a locked account refuses even the right one) and it is
	// keyed by what was TYPED, not by an account row — an identifier that names
	// nobody is counted and answered exactly like one that does. It does not
	// know which driver (local, LDAP, recovery…) will judge the password. The
	// realm is part of the key: `acme/alex` and `beta/alex` are two people.
	att := loginguard.Attempt{Identifier: req.Email, Realm: lr.CounterRealm(), IP: clientIP(r), Protocol: loginguard.ProtoWeb}
	if h.Guard != nil {
		if v := h.Guard.Check(r.Context(), att); v.Blocked {
			h.writeLocked(w, v.Scope, v.RetryAfter, v.Message(requestLang(r)))
			return
		}
	}
	if realmErr != nil {
		if !errors.Is(realmErr, auth.ErrUnknownRealm) && !errors.Is(realmErr, auth.ErrRealmConflict) {
			slog.Error("login: could not tell which tenant the sign-in is for", slog.String("err", realmErr.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sign-in could not be checked"})
			return
		}
		// ⚠ A realm nobody has, or one that is not this address's tenant, is
		// answered — and counted — exactly as a wrong password: the form must
		// not tell a stranger which realms exist.
		slog.Debug("login refused", slog.String("reason", realmErr.Error()),
			slog.String("realm", lr.CounterRealm()), slog.String("identifier", req.Email))
		h.loginFailed(w, r, att, loginguard.ReasonCredentials, "invalid credentials", false)
		return
	}
	user, token, err := h.LocalAuth.Login(ctx, req.Email, req.Password)
	if errors.Is(err, auth.ErrBusy) {
		// Not a judgement of the password: the provider is at its limit of
		// simultaneous attempts. Try again — and it does not count against the
		// person.
		w.Header().Set("Retry-After", "3")
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "busy", "message": srvtext.Text(requestLang(r), "server.login.busy", nil),
		})
		return
	}
	if err != nil {
		// A provider whose operator switched show_refusal_reason on refused a
		// person whose password was RIGHT, and may say why
		// (auth.RefusedAfterPassword): the first-login rule, a system account.
		// A code, never words; not counted against the person, whose password
		// was not wrong. With the setting off the provider answered plain
		// ErrUnauthorized and this is never reached.
		if reason := auth.SSOReason(err); reason != "" && errors.Is(err, auth.ErrUnauthorized) {
			slog.Info("login refused after the password; the provider tells why",
				slog.String("reason", reason), slog.String("identifier", req.Email))
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "sign-in refused", "reason": reason})
			return
		}
		// ⚠ One answer for every failure, on purpose — see the comment on
		// local.Driver.Login. The driver has already logged WHICH failure it
		// was; what an anonymous caller learns must not depend on that.
		h.loginFailed(w, r, att, loginguard.ReasonCredentials, "invalid credentials", false)
		return
	}
	// ⚠ The last word on the tenant boundary: whatever a provider returned, an
	// account of another tenant is not signed in to through this realm. The
	// lookups already stay inside it; this is the check that does not depend
	// on every provider having got that right.
	if !lr.Admits(user) {
		_ = h.Store.DeleteSession(r.Context(), token)
		slog.Warn("login refused: the account is not in the realm the sign-in was for",
			slog.Int64("user_id", user.ID), slog.String("realm", lr.CounterRealm()))
		h.loginFailed(w, r, att, loginguard.ReasonCredentials, "invalid credentials", false)
		return
	}
	// A disabled account is refused on its own terms. Folding it into the
	// maintenance branch below would tell the user the whole platform is
	// locked down when in fact only their account is.
	//
	// Both answers carry the gate as a reason code too (auth.LoginBlockReason),
	// so the form says it in the reader's language, as the SSO page does.
	if !user.Enabled {
		_ = h.Store.DeleteSession(r.Context(), token)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":    "this account is disabled",
			"disabled": true,
			"reason":   auth.SSOReasonAccountDisabled,
		})
		return
	}
	if reason := auth.LoginBlockReason(r.Context(), h.Store, h.MultiTenant, user); reason != "" {
		// Maintenance mode: multi-tenant is off but tenants exist — only the
		// supertenant may sign in; or the account's tenant is suspended. See
		// docs/MULTI-TENANCY.md.
		_ = h.Store.DeleteSession(r.Context(), token)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":       "sign-in is temporarily limited to the platform operator",
			"maintenance": true,
			"reason":      reason,
		})
		return
	}
	if user.TOTPEnabled {
		// These two refusals are the third case a 401 can mean: the password
		// was RIGHT and the second factor was not. The body says so (the SPA
		// needs `totp_required` to show the code field), but an operator
		// reading the log for a login that "just fails" needs the same fact —
		// a client that never learned to send `totp` is otherwise
		// indistinguishable from a wrong password. Debug: still a caller
		// getting it wrong, on an unauthenticated endpoint.
		if strings.TrimSpace(req.TOTP) == "" {
			_ = h.Store.DeleteSession(r.Context(), token)
			slog.Debug("login refused",
				slog.String("reason", "two-factor code required"),
				slog.String("identifier", req.Email))
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "two-factor code required",
				"totp_required": true,
			})
			return
		}
		if !verifyTOTP(user.TOTPSecret, req.TOTP) {
			_ = h.Store.DeleteSession(r.Context(), token)
			slog.Debug("login refused",
				slog.String("reason", "invalid two-factor code"),
				slog.String("identifier", req.Email))
			// A wrong second factor is a wrong attempt like any other: it
			// counts against the same account and address. (A MISSING code,
			// above, does not — that is the form asking for its second step.)
			h.loginFailed(w, r, att, loginguard.ReasonTOTP, "invalid two-factor code", true)
			return
		}
	}
	if h.Guard != nil {
		h.Guard.Succeeded(r.Context(), att)
	}
	if h.handOff(w, r, lr, user, token) {
		return
	}
	h.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  user,
		"token": token,
	})
}

// loginRealm resolves the tenant a sign-in is for, on a multi-tenant install;
// (nil, nil) on a single-tenant one, where the realm field does not exist.
func (h *Auth) loginRealm(ctx context.Context, r *http.Request, typed string) (*auth.LoginRealm, error) {
	if !h.MultiTenant || h.Store == nil {
		return nil, nil
	}
	t := tenant.NormalizeRealm(typed)
	return auth.ResolveLoginRealm(ctx, h.Store, t, t != "", requestHost(r), h.EmailToken)
}

// handOff finishes a sign-in typed on the platform's address for a tenant that
// has an address of its own: the session belongs THERE — the cookie is
// host-bound, and the tenant's page is the one with its branding, its
// sign-out and its links — so instead of a cookie here the answer is a
// handoff ticket (auth.HandoffStore, purpose auth.HandoffLogin) the browser
// carries to the tenant's address (POST /api/auth/handoff).
//
// The ticket's actor and subject are the ONE account that just signed in; it
// lives handoffTTL, is spent by its first use, and is honoured on the tenant's
// host only. It travels in the URL fragment of the tenant's sign-in page, which
// a browser never sends to a server — so no access log, proxy or Referer ever
// sees it — and it is never logged or audited, only its issue, use and refusal.
//
// A tenant with no address of its own is signed in to right here: the session
// is scoped by the account's tenant, not by the host (auth.TenantResolver).
func (h *Auth) handOff(w http.ResponseWriter, r *http.Request, lr *auth.LoginRealm, u *model.User, session string) bool {
	if h.Handoffs == nil || lr == nil || !lr.Named || lr.Tenant == nil || lr.Tenant.IsSupertenant || u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(lr.Tenant.Host))
	if host == "" || host == requestHost(r) {
		return false
	}
	// Another address of the same tenant (its platform subdomain, an own
	// domain: docs/TENANT-ADMIN.md) is the tenant's page too: the session
	// stays where the person is.
	if here, err := h.Store.GetProviderByHost(r.Context(), requestHost(r)); err == nil && here != nil && here.ID == lr.Tenant.ID {
		return false
	}
	origin := h.Tenants.ForProvider(r.Context(), lr.Tenant.ID)
	if origin == "" || origin == h.Tenants.Fallback() {
		return false
	}
	t := auth.HandoffTicket{Purpose: auth.HandoffLogin, ActorID: u.ID, SubjectID: u.ID, Host: host, ProviderID: lr.Tenant.ID}
	code, err := h.Handoffs.Issue(t, handoffTTL)
	if err != nil {
		slog.Warn("login: could not hand the sign-in to the tenant's address; it stays here", slog.String("err", err.Error()))
		return false
	}
	auth.AuditHandoff(r.Context(), h.Store, auth.AuditHandoffIssued, &t, requestHost(r), "")
	// The session the provider opened on THIS host is not the one the person
	// will use: the tenant's address mints its own when the ticket arrives.
	_ = h.Store.DeleteSession(r.Context(), session)
	writeJSON(w, http.StatusOK, map[string]any{
		"handoff": map[string]string{"origin": origin, "code": code},
	})
	return true
}

// Handoff completes a sign-in handed over by handOff: POST /api/auth/handoff
// {"code"}, on the tenant's own address. Answers like Login — the session
// cookie, and {user, token} — or 401 for a ticket that is unknown, spent,
// expired, issued for another purpose, presented on another host (it is spent
// all the same), or whose account may no longer sign in here
// (auth.CheckHandoffTarget). Every refusal of a real ticket is audited.
func (h *Auth) Handoff(w http.ResponseWriter, r *http.Request) {
	if h.Handoffs == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ctx := r.Context()
	host := requestHost(r)
	refuse := func(t *auth.HandoffTicket, err error) {
		reason := auth.HandoffRefuseUnknown
		var hr *auth.HandoffRefusal
		if errors.As(err, &hr) {
			reason = hr.Reason
		}
		auth.AuditHandoff(ctx, h.Store, auth.AuditHandoffRefused, t, host, reason)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired handoff"})
	}
	t, err := h.Handoffs.Redeem(req.Code, auth.HandoffLogin, host)
	if err != nil {
		refuse(t, err)
		return
	}
	u, err := auth.CheckHandoffTarget(ctx, h.Store, h.MultiTenant, t, host)
	if err != nil {
		refuse(t, err)
		return
	}
	token, err := authlocal.IssueSession(ctx, h.Store, u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not open a session"})
		return
	}
	auth.AuditHandoff(ctx, h.Store, auth.AuditHandoffUsed, t, host, "")
	h.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "token": token})
}

// oidcHandOff is handOff for an OIDC callback: when the account's tenant has
// an address of its own and the callback arrived elsewhere, it issues the
// handoff ticket and answers the tenant's sign-in page to bounce to, with the
// ticket in the fragment; "" to open the session here.
func (h *Auth) oidcHandOff(r *http.Request, u *model.User, session string) string {
	if h.Handoffs == nil || !h.MultiTenant || u == nil || u.ProviderID == nil {
		return ""
	}
	p, err := h.Store.GetProvider(r.Context(), *u.ProviderID)
	if err != nil || p == nil || p.IsSupertenant || strings.TrimSpace(p.Host) == "" {
		return ""
	}
	here := requestHost(r)
	if here == "" || strings.EqualFold(here, strings.TrimSpace(p.Host)) {
		return ""
	}
	// Any address of the tenant (an own domain, its platform subdomain) is
	// already the tenant's: the session stays where it was made.
	if at, err := h.Store.GetProviderByHost(r.Context(), here); err == nil && at != nil && at.ID == p.ID {
		return ""
	}
	origin := h.Tenants.ForProvider(r.Context(), p.ID)
	if origin == "" || origin == h.Tenants.Fallback() {
		return ""
	}
	t := auth.HandoffTicket{Purpose: auth.HandoffLogin, ActorID: u.ID, SubjectID: u.ID, Host: strings.ToLower(strings.TrimSpace(p.Host)), ProviderID: p.ID}
	code, err := h.Handoffs.Issue(t, handoffTTL)
	if err != nil {
		slog.Warn("oidc: could not hand the sign-in to the tenant's address; it stays here", slog.String("err", err.Error()))
		return ""
	}
	auth.AuditHandoff(r.Context(), h.Store, auth.AuditHandoffIssued, &t, here, "")
	_ = h.Store.DeleteSession(r.Context(), session)
	return origin + "/admin/login#handoff=" + code
}

// loginFailed answers a wrong attempt: records it and says what is left.
//
// The body keeps `error` (the SPA and API clients match on it) and adds what
// the person needs — `message` in their language, `remaining` tries and the
// `limit` at which the lock falls. When this attempt tripped a lock the answer
// is 429 with Retry-After instead.
func (h *Auth) loginFailed(w http.ResponseWriter, r *http.Request, att loginguard.Attempt, reason, errText string, totp bool) {
	lang := requestLang(r)
	body := map[string]any{"error": errText}
	if totp {
		body["totp_required"] = true
	}
	if h.Guard == nil {
		writeJSON(w, http.StatusUnauthorized, body)
		return
	}
	o := h.Guard.Failed(r.Context(), att, reason)
	body["message"] = o.Message(lang)
	switch {
	case o.Locked:
		h.writeLocked(w, o.Scope, o.RetryAfter, body["message"].(string))
	case o.Unlimited:
		writeJSON(w, http.StatusUnauthorized, body)
	default:
		body["remaining"] = o.Remaining
		body["limit"] = o.Limit
		body["scope"] = o.Scope
		writeJSON(w, http.StatusUnauthorized, body)
	}
}

// writeLocked answers a refused attempt: 429, Retry-After, and the sentence.
func (h *Auth) writeLocked(w http.ResponseWriter, scope string, wait time.Duration, message string) {
	secs := loginguard.RetryAfterSeconds(wait)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":       "too many attempts",
		"message":     message,
		"locked":      true,
		"scope":       scope,
		"retry_after": secs,
	})
}

// Logout clears the cookie + revokes the server-side session — and, for an
// OIDC session, answers with the IdP's end-session URL as `logout_url` so the
// web app can end the IdP's session too (see idpLogoutURL).
//
// Optional body: {"return_to": "/admin/login" | "/drive/login"} — the sign-in
// page of the front door the person was using, where the IdP sends the browser
// back once it is done.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"ok": true}
	if c, err := r.Cookie(authlocal.SessionCookieName); err == nil && c.Value != "" {
		// Before the delete: the id_token lives on the session row.
		if u := h.idpLogoutURL(w, r, c.Value); u != "" {
			out["logout_url"] = u
		}
		_ = h.Store.DeleteSession(r.Context(), c.Value)
	}
	h.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, out)
}

// signedOutPages are the only places the IdP may send the browser back to
// after sign-out: the sign-in page of either front door (router/index.ts).
// `signed_out` tells the page not to start SSO again on its own.
var signedOutPages = map[string]string{
	"/admin/login": "/admin/login?signed_out=1",
	"/drive/login": "/drive/login?signed_out=1",
}

// idpLogoutURL is the IdP half of signing out (OpenID Connect RP-Initiated
// Logout 1.0), or "" to keep sign-out local.
//
// ⚠ Without it signing out was not signing out. filex dropped its own session
// and nothing else, so in SSO-first mode (FILEX_OIDC_AUTO_REDIRECT) the login
// page went straight back to the IdP, whose session was still open: a new code
// without a form, the same account signed in again ~0.5 s later (measured on
// Keycloak 26), and on a shared computer the next person got the previous
// one's files.
//
// Local when: the operator chose FILEX_OIDC_LOGOUT=local, the session kept no
// id_token (password/LDAP sign-in, or one from before this version), or the
// driver/IdP cannot end sessions. The post-logout address is picked from
// signedOutPages — never taken from the request — so sign-out cannot be turned
// into an open redirect; the IdP must list it among its allowed post-logout
// redirect URIs (docs/SSO.md).
func (h *Auth) idpLogoutURL(w http.ResponseWriter, r *http.Request, session string) string {
	if h.OIDCLocalLogout {
		return ""
	}
	lo, ok := h.OIDCAuth.(auth.OIDCLogoutDriver)
	if !ok {
		return ""
	}
	idToken, err := h.Store.GetSessionIDToken(r.Context(), session)
	if err != nil || idToken == "" {
		return ""
	}
	var body struct {
		ReturnTo string `json:"return_to"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body)
	page, ok := signedOutPages[body.ReturnTo]
	if !ok {
		page = signedOutPages["/admin/login"]
	}
	return lo.EndSessionURL(r, idToken, h.redirectBase(r)+page)
}

// OIDCStart redirects to the IdP: `?instance=<slug>&realm=<realm>` start one
// OIDC of the tenant the address or the realm names (docs/TENANT-ADMIN.md).
//
// A start the tenant has no such OIDC for, or a realm nobody has, goes back
// to the sign-in page (`?error=oidc`), like a failed callback: the start is a
// browser navigation, and a JSON body would dead-end the person. No reason
// code for those two: "this realm has no such SSO" against "no such realm"
// would tell a stranger which realms exist. Any other failure to start goes
// back the same way, with a code only when the person can act on it (too many
// sign-ins in flight: try again shortly); its words stay in the log.
func (h *Auth) OIDCStart(w http.ResponseWriter, r *http.Request) {
	if !available(h.OIDCAuth) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC not configured"})
		return
	}
	if err := h.OIDCAuth.StartFlow(w, r); err != nil {
		if errors.Is(err, authsetup.ErrNoSSOForTenant) {
			slog.Debug("oidc start refused", slog.String("reason", err.Error()))
			http.Redirect(w, r, h.redirectBase(r)+oidcFailedPage(""), http.StatusFound)
			return
		}
		reason := auth.SSOReason(err)
		slog.Warn("oidc start failed", slog.String("err", err.Error()), slog.String("reason", reason))
		http.Redirect(w, r, h.redirectBase(r)+oidcFailedPage(reason), http.StatusFound)
	}
}

// OIDCCallback completes the OIDC flow.
func (h *Auth) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !available(h.OIDCAuth) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC not configured"})
		return
	}
	base := h.redirectBase(r)
	usr, token, err := h.OIDCAuth.HandleCallback(w, r)
	if err != nil {
		// The callback is a browser navigation (the IdP redirected here), so
		// a JSON body would dead-end the user on a raw error page. Send them
		// back to the login form instead; the SPA shows a friendly message and,
		// critically, suppresses OIDC auto-redirect so a broken IdP can't
		// cause a redirect loop.
		//
		// The marker carries a reason CODE when the person can act on it
		// (auth.SSOReason: no account opened at a first sign-in, not in an
		// allowed group, the IdP refused...), never the error's words. A
		// failure with no code (an account in another tenant among them) gets
		// the plain marker, byte for byte (auth/sso_refusal.go). The log keeps
		// the whole error either way.
		reason := auth.SSOReason(err)
		slog.Warn("oidc callback failed", slog.String("err", err.Error()), slog.String("reason", reason))
		http.Redirect(w, r, base+oidcFailedPage(reason), http.StatusFound)
		return
	}
	if reason := auth.LoginBlockReason(r.Context(), h.Store, h.MultiTenant, usr); reason != "" {
		// The account may not open a session (docs/MULTI-TENANCY.md): it is
		// disabled, its tenant is suspended, or the install is in maintenance
		// mode. The page says which; it used to say nothing at all.
		_ = h.Store.DeleteSession(r.Context(), token)
		slog.Warn("oidc sign-in refused", slog.String("reason", reason), slog.Int64("user_id", usr.ID))
		page := "/admin/login?maintenance=1&reason=" + reason
		if reason == auth.SSOReasonAccountDisabled || reason == auth.SSOReasonAccountPending {
			page = oidcFailedPage(reason)
		}
		http.Redirect(w, r, base+page, http.StatusFound)
		return
	}
	// A tenant with an address of its own whose person signed in through an
	// identity provider on another address (a tenant with no address of its
	// own that gained one, a shared OIDC started on the platform's page with
	// the realm): the session belongs on the tenant's address, so it goes
	// there in a handoff ticket, as a password sign-in does.
	if target := h.oidcHandOff(r, usr, token); target != "" {
		writeOIDCBounce(w, r, target)
		return
	}
	h.setSessionCookie(w, r, token)
	// Land on the panel via a 200 HTML bounce rather than a 302. A
	// TLS-terminating CDN (Cloudflare, measured live) strips a Domain-scoped
	// Set-Cookie from a 3xx response but keeps it on a 200 — so a 302 here
	// silently loses the just-minted session cookie and the SPA loops on
	// /api/auth/me 401. The session cookie is written above (unchanged
	// Domain/Secure/SameSite logic); the body just forwards the browser. The
	// target is a fixed relative path so it stays on the tenant host that
	// served this callback (v0.1.66's host fix) with zero open-redirect
	// surface. Under a base path (FILEX_BASE_PATH) the path carries it — still a
	// fixed path, still on the host that served the callback.
	writeOIDCBounce(w, r, basepath.Path(r.Context(), "/admin/"))
}

// oidcFailedPage is where a failed SSO sign-in sends the browser: the sign-in
// page with the generic marker, and the reason code when there is one. The
// code is one of auth's SSOReason* constants (plain ASCII words, nothing to
// escape); "" gives exactly `/admin/login?error=oidc`, the answer every
// unclassified failure gets.
func oidcFailedPage(reason string) string {
	if reason == "" {
		return "/admin/login?error=oidc"
	}
	return "/admin/login?error=oidc&reason=" + url.QueryEscape(reason)
}

// oidcBounceTmpl is the 200 "signing in…" page that carries the session
// Set-Cookie past a CDN that strips it from 3xx responses. html/template
// context-escapes the target in the meta/JS/href sinks; the caller only ever
// passes a fixed relative path. Its two phrases are the server catalogue's
// (`server.public.signing_in` / `continue`), in the browser's language: the
// person is not signed in yet, so there is no account to read one from.
var oidcBounceTmpl = template.Must(template.New("oidcbounce").Parse(
	`<!doctype html><html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8">` +
		`<meta http-equiv="refresh" content="0;url={{.Target}}">` +
		`<title>{{.SigningIn}}</title></head>` +
		`<body><script>location.replace("{{.Target}}")</script>` +
		`<noscript><a href="{{.Target}}">{{.Continue}}</a></noscript>` +
		`{{.SigningIn}}</body></html>`))

func writeOIDCBounce(w http.ResponseWriter, r *http.Request, target string) {
	lang := publicLocale(r, "")
	t := publicT(lang)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = oidcBounceTmpl.Execute(w, map[string]string{
		"Lang": lang, "Dir": pageDir(lang), "Target": target, "SigningIn": t["signing_in"], "Continue": t["continue"],
	})
}

// redirectBase returns the origin that OIDCCallback redirects should target.
//
// Single-tenant: the configured PublicURL (which IS the install's host).
// Multi-tenant: the callback arrives on the TENANT's own host (each realm's
// redirect_uri points there — see multioidc.driverFor), so bouncing the user
// to h.PublicURL would strand them on the operator/supertenant host where
// they have no session and no files.
//
// The rule itself lives in tenanturl.Resolver, which every other absolute-URL
// builder now shares; this method stays only so the OIDC code reads the way it
// always did.
func (h *Auth) redirectBase(r *http.Request) string {
	return h.Tenants.FromRequest(r)
}

// cookieDomain returns the Domain attribute for the session cookie on this
// request. Single-tenant: the global FILEX_COOKIE_DOMAIN (may be empty =
// host-only). Multi-tenant, when the request host resolves to a provider:
//  1. the provider's explicit cookie_domain (operator-set, always wins);
//  2. else derived from the provider host by dropping its first label
//     (files.example.com → .example.com) — skipped when the remainder has
//     no dot left (files.localhost);
//  3. else the global FILEX_COOKIE_DOMAIN;
//  4. and whichever it is, none (host-only) when the request arrived on
//     another address of the tenant (its platform subdomain, an own domain)
//     that is not under it.
//
// ⚠ The derived form assumes the host is a subdomain OF the tenant apex
// (files.<apex>, the documented layout). A tenant served on its bare apex —
// or one whose derivation would land on a public suffix (globex.com.tr →
// .com.tr, which browsers REJECT) — must set cookie_domain explicitly.
func (h *Auth) cookieDomain(r *http.Request) string {
	if !h.MultiTenant {
		return h.CookieDomain
	}
	host := requestHost(r)
	if host == "" {
		return h.CookieDomain
	}
	p, err := h.Store.GetProviderByHost(r.Context(), host)
	if err != nil || p == nil {
		return h.CookieDomain
	}
	d := h.CookieDomain
	if p.CookieDomain != "" {
		d = p.CookieDomain
	} else if p.Host != "" {
		if i := strings.Index(p.Host, "."); i > 0 && strings.Contains(p.Host[i+1:], ".") {
			d = p.Host[i:]
		}
	}
	// A tenant also answers on its platform subdomain and its own domains
	// (docs/TENANT-ADMIN.md). There a Domain the request's host is not under
	// is one the browser refuses outright, and with it the whole sign-in: the
	// cookie is that address's alone. (On the tenant's own `host` the
	// operator's setting stands, as before.)
	if d != "" && !strings.EqualFold(bareHost(p.Host), host) && !hostUnder(host, d) {
		return ""
	}
	return d
}

// bareHost is a provider's host without a port.
func bareHost(h string) string {
	h = strings.TrimSpace(h)
	if x, _, err := net.SplitHostPort(h); err == nil {
		return x
	}
	return h
}

// hostUnder reports whether host is the cookie domain d or a name under it.
func hostUnder(host, d string) bool {
	d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
	return d != "" && (host == d || strings.HasSuffix(host, "."+d))
}

// requestHost extracts the bare lowercase hostname (no port) the client asked
// for. One definition, in tenanturl, shared with the origin resolver.
func requestHost(r *http.Request) string { return tenanturl.RequestHost(r) }

// WhoAmI returns the current user (or null).
func (h *Auth) WhoAmI(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

func (h *Auth) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    token,
		Path:     basepath.CookiePath(r.Context()),
		Domain:   h.cookieDomain(r),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(authlocal.SessionTTL),
	})
}

func (h *Auth) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    "",
		Path:     basepath.CookiePath(r.Context()),
		Domain:   h.cookieDomain(r),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		MaxAge:   -1,
	})
}

// requestIsHTTPS reports whether the client reached filex over TLS — either
// directly (r.TLS) or, far more commonly, through a TLS-terminating reverse
// proxy that forwards X-Forwarded-Proto. Behind such a proxy r.TLS is nil, so
// without the header check the session cookie would never be marked Secure on
// an HTTPS site. A Secure session cookie is both correct hardening and, on a
// cross-subdomain (Domain-scoped) cookie, what keeps Chrome's schemeful
// same-site rules from dropping it during the OIDC redirect chain.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

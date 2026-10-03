// Package oidc implements OpenID Connect authentication.
//
// Uses coreos/go-oidc + golang.org/x/oauth2 to talk to any spec-compliant IdP
// (Keycloak, Auth0, Authentik, Dex, Okta, Google, …). On successful callback
// the user is upserted into the local users table, then a normal local
// session token is minted so the rest of the request lifecycle can stay
// driver-agnostic.
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

func init() {
	auth.Register("oidc", func() auth.Driver { return &Driver{} })
}

const stateCookieName = "filex_oidc_state"

var _ auth.OIDCLogoutDriver = (*Driver)(nil)

// Driver is the OIDC auth driver.
type Driver struct {
	store       db.Store
	mu          sync.RWMutex
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       *oauth2.Config
	issuer      string
	endSession  string // IdP's end_session_endpoint from discovery; "" = none, sign-out stays local
	roleClaim   string // metadata field name containing role
	adminGroup  string // group/role string that elevates user to admin
	defaultRole string
	// tenant is the tenant this driver signs people in to when a sign-in says
	// no other (Flow.Tenant): the one it was built for. A tenant's own OIDC is
	// pinned to its tenant (SetProviderID, or TenantConfigKey at Init) and
	// serves no other; every other instance is the platform's own tenant's
	// until a flow names the tenant it was started for. Never "every tenant":
	// docs/SSO.md, "Which account an SSO sign-in opens".
	tenant Tenant
	pinned bool
	// trustEmail: the operator says this identity provider only ever sends
	// addresses its people own (`trust_email`, default off). Off, an address
	// the provider does not mark `email_verified: true` opens no existing
	// account and a new account only switched off.
	trustEmail bool
	// firstLogin is what happens to a person with no account yet: auto_create
	// and allowed_groups (auth.ProvisionFirstLogin). The zero value would refuse
	// everybody, so Init always sets it (default: open an account).
	firstLogin auth.FirstLoginPolicy
	// client is the HTTP client every call to the identity provider goes
	// through (discovery, keys, the code exchange); nil = the default one. A
	// tenant's own OIDC (`guarded`, docs/TENANT-ADMIN.md) gets one that dials
	// through internal/netguard: no loopback, private, link-local or overlay
	// address, after DNS and on every redirect.
	client *http.Client
}

// Flow is what a dispatcher decided for ONE sign-in through a driver it shares
// between tenants (a sign-in provider bound to several tenants,
// docs/TENANT-ADMIN.md): the redirect URI of the address the flow started on
// (the identity provider sends the browser back there, and the code exchange
// must name the same one) and the tenant the account is looked up and created
// in. The zero value changes nothing: the driver's own redirect URL and
// tenant stand.
type Flow struct {
	RedirectURL string
	Tenant      Tenant
}

// Tenant is the tenant an SSO sign-in's account belongs to: it is looked up
// there, created there, and an account of any other tenant is refused (the
// meaning auth.LoginRealm.Admits gives a password sign-in).
type Tenant struct {
	// ProviderID is the tenant's row. 0 with Main: the platform's own tenant,
	// read at the callback (the supertenant row).
	ProviderID int64
	// Main: the platform's own tenant, which also owns an account with no
	// tenant at all (identity.Tenant.Owns).
	Main bool
}

// IsSet reports whether t names a tenant at all.
func (t Tenant) IsSet() bool { return t.ProviderID != 0 || t.Main }

// MainTenant is the platform's own tenant, its row read at the callback.
func MainTenant() Tenant { return Tenant{Main: true} }

// TenantConfigKey is the Init key that pins a driver to one tenant (int64):
// what a tenant's own OIDC is built with (authsetup), so it serves its tenant
// and no other from the moment it exists. Never a page field.
const TenantConfigKey = "tenant_provider_id"

// TrustEmailKey is the provider setting that takes every address the identity
// provider sends as verified (bool, default off).
const TrustEmailKey = "trust_email"

type flowKey struct{}

// WithFlow stamps a flow on the request context StartFlow and HandleCallback
// are given.
func WithFlow(ctx context.Context, f Flow) context.Context {
	return context.WithValue(ctx, flowKey{}, f)
}

func flowFrom(ctx context.Context) Flow {
	f, _ := ctx.Value(flowKey{}).(Flow)
	return f
}

// oauthFor is the OAuth configuration of one flow: the driver's, with the
// flow's redirect URI when it has one. Never the shared value itself.
func oauthFor(base *oauth2.Config, f Flow) *oauth2.Config {
	if base == nil || f.RedirectURL == "" {
		return base
	}
	c := *base
	c.RedirectURL = f.RedirectURL
	return &c
}

// SetProviderID pins this driver instance to a tenant: it signs people in to
// that tenant only, and a flow started for another is refused. Called by the
// multi-provider dispatcher right after Init.
func (d *Driver) SetProviderID(id int64) {
	d.mu.Lock()
	d.tenant, d.pinned = Tenant{ProviderID: id}, id != 0
	d.mu.Unlock()
}

// BoundTenant is the tenant the driver signs people in to when a sign-in
// names no other, and whether it is pinned there (serves that tenant only).
func (d *Driver) BoundTenant() (Tenant, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tenant, d.pinned
}

// New constructs an empty OIDC driver - Init must be called. It signs people
// in to the platform's own tenant until it is pinned or a flow names another.
func New(store db.Store) *Driver {
	return &Driver{store: store, defaultRole: model.RoleUser, tenant: MainTenant()}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "oidc" }

// Init configures the driver. Required keys: issuer, client_id,
// client_secret, redirect_url. Optional: scopes, role_claim, admin_group.
func (d *Driver) Init(ctx context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("oidc: nil store")
	}
	issuer, _ := cfg["issuer"].(string)
	clientID, _ := cfg["client_id"].(string)
	clientSecret, _ := cfg["client_secret"].(string)
	redirect, _ := cfg["redirect_url"].(string)
	if issuer == "" || clientID == "" || redirect == "" {
		return errors.New("oidc: issuer, client_id, redirect_url required")
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if extra, ok := cfg["scopes"].([]string); ok {
		scopes = append(scopes, extra...)
	}
	var client *http.Client
	if auth.CfgBool(cfg, "guarded") {
		client = &http.Client{Transport: netguard.Transport(15 * time.Second), Timeout: 30 * time.Second}
		ctx = oidc.ClientContext(ctx, client)
	}

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("oidc: discover provider: %w", err)
	}
	// RP-initiated logout (see EndSessionURL). Optional in discovery: an IdP
	// without it simply keeps sign-out local, as it always was.
	var logout struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = provider.Claims(&logout)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.provider = provider
	d.verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	d.oauth = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirect,
		Scopes:       scopes,
	}
	d.issuer = issuer
	d.client = client
	d.endSession = logout.EndSessionEndpoint
	d.roleClaim, _ = cfg["role_claim"].(string)
	d.adminGroup, _ = cfg["admin_group"].(string)
	d.firstLogin = auth.FirstLoginPolicyFrom(cfg)
	d.trustEmail = auth.CfgBool(cfg, TrustEmailKey)
	if id, _ := cfg[TenantConfigKey].(int64); id != 0 {
		d.tenant, d.pinned = Tenant{ProviderID: id}, true
	} else if !d.tenant.IsSet() {
		d.tenant = MainTenant()
	}
	return nil
}

// Capabilities implements auth.Driver.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{
		SignIn:         true,
		Logout:         true,
		ChangePassword: false,
		Register:       false,
	}
}

// Authenticate falls back to the local driver's session token contract —
// once the OIDC callback completed, downstream requests carry the same
// session cookie that the local driver knows how to validate.
func (d *Driver) Authenticate(r *http.Request) (*model.User, error) {
	// OIDC's only authoritative moment is the callback. After that we
	// rely on the session token established by HandleCallback.
	return nil, auth.ErrUnauthorized
}

// StartFlow redirects the browser to the IdP authorization endpoint.
func (d *Driver) StartFlow(w http.ResponseWriter, r *http.Request) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.oauth == nil {
		return errors.New("oidc: not initialized")
	}
	state, err := randString(24)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     basepath.CookiePath(r.Context()),
		HttpOnly: true,
		// Behind a TLS-terminating proxy r.TLS is nil; trust X-Forwarded-Proto
		// so the state cookie is still marked Secure on an HTTPS site (matches
		// the session cookie — see handlers.requestIsHTTPS).
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	url := oauthFor(d.oauth, flowFrom(r.Context())).AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusFound)
	return nil
}

// HandleCallback processes ?code= and ?state= from the IdP.
// It returns the upserted local user plus the local session token.
func (d *Driver) HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	d.mu.RLock()
	flow := flowFrom(r.Context())
	oauthCfg := oauthFor(d.oauth, flow)
	verifier := d.verifier
	roleClaim := d.roleClaim
	adminGroup := d.adminGroup
	tenant, pinned := d.tenant, d.pinned
	trustEmail := d.trustEmail
	endSession := d.endSession
	firstLogin := d.firstLogin
	client := d.client
	d.mu.RUnlock()
	if oauthCfg == nil {
		return nil, "", errors.New("oidc: not initialized")
	}
	// The tenant this sign-in is for: the flow's (a provider bound to several
	// tenants is told which one it was started for), else the driver's own.
	// ⚠ A pinned driver (a tenant's own OIDC) serves its tenant only. No
	// reason code for either refusal: the page answers them as any failure.
	if flow.Tenant.IsSet() {
		if pinned && flow.Tenant.ProviderID != tenant.ProviderID {
			return nil, "", fmt.Errorf("oidc: this provider belongs to tenant %d, the sign-in was started for tenant %d", tenant.ProviderID, flow.Tenant.ProviderID)
		}
		tenant = flow.Tenant
	}
	if !tenant.IsSet() {
		return nil, "", errors.New("oidc: this provider is bound to no tenant; it signs nobody in")
	}

	// ⚠ Every refusal below that the person can act on carries a reason code
	// for the sign-in page (auth.SSORefused, auth.SSOReason); the wrapped error
	// is the log line, unchanged. An account in another tenant carries none
	// (see auth/sso_refusal.go).
	c, err := r.Cookie(stateCookieName)
	if err != nil || c.Value == "" || c.Value != r.URL.Query().Get("state") {
		return nil, "", auth.SSORefused(auth.SSOReasonExpired, errors.New("oidc: state mismatch"))
	}
	// The identity provider answered with an error instead of a code (the
	// person cancelled, or the IdP refused them: RFC 6749 4.1.2.1). Its words
	// go to the log only; the page gets the code.
	if idpErr := r.URL.Query().Get("error"); idpErr != "" {
		return nil, "", auth.SSORefused(auth.SSOReasonIdPDenied, fmt.Errorf("oidc: the identity provider answered %q: %s",
			clip(idpErr, 64), clip(r.URL.Query().Get("error_description"), 200)))
	}
	exCtx := r.Context()
	if client != nil {
		exCtx = context.WithValue(exCtx, oauth2.HTTPClient, client)
	}
	tok, err := oauthCfg.Exchange(exCtx, r.URL.Query().Get("code"))
	if err != nil {
		return nil, "", auth.SSORefused(auth.SSOReasonIdPError, fmt.Errorf("oidc: code exchange: %w", err))
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, "", auth.SSORefused(auth.SSOReasonIdPError, errors.New("oidc: missing id_token"))
	}
	idTok, err := verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		return nil, "", auth.SSORefused(auth.SSOReasonIdPError, fmt.Errorf("oidc: verify id_token: %w", err))
	}

	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		return nil, "", auth.SSORefused(auth.SSOReasonIdPError, err)
	}
	email, _ := claims["email"].(string)
	if email == "" {
		return nil, "", auth.SSORefused(auth.SSOReasonNoEmail, errors.New("oidc: id_token missing email claim"))
	}
	// Roles/groups may live in the id_token OR the access_token, under a flat
	// or dotted claim path. Keycloak, for instance, puts realm roles at
	// "realm_access.roles" in the ACCESS token, not the id_token — so check
	// both tokens and traverse the dotted path.
	role := d.defaultRole
	mapping := roleClaim != "" && adminGroup != ""
	claimAdmin := false
	claimSets := []map[string]any{claims}
	if at, _ := tok.Extra("access_token").(string); at != "" {
		if ac := parseJWTClaims(at); ac != nil {
			claimSets = append(claimSets, ac)
		}
	}
	if mapping {
		for _, cs := range claimSets {
			if claimContains(cs, roleClaim, adminGroup) {
				role = model.RoleAdmin
				claimAdmin = true
				break
			}
		}
	}

	ctx := r.Context()
	lower := strings.ToLower(email)
	// The groups this login carried, for permission rules that target an SSO
	// group (package perm) and for the first-login rule's allowed_groups. Read
	// from the same claim the admin mapping uses, in both tokens, and REPLACED
	// at every login: the IdP is the authority, so somebody removed from a
	// group there leaves it here on their next sign-in. No claim configured, or
	// none in the tokens, is no groups.
	var groups []string
	if roleClaim != "" {
		for _, cs := range claimSets {
			groups = append(groups, claimValues(cs, roleClaim)...)
		}
	}
	scope, err := d.scopeOf(ctx, tenant)
	if err != nil {
		return nil, "", err
	}
	id := identity{issuer: idTok.Issuer, subject: idTok.Subject, email: lower,
		verified: trustEmail || emailVerified(claims["email_verified"])}
	// A person with no account yet goes through the first-login rule
	// (auth.ProvisionFirstLogin) — the ONE creation path, whichever tenant mode.
	first := auth.FirstLogin{
		Driver: "oidc", Identifier: lower, Email: lower, Role: role, Groups: groups,
		Policy: firstLogin, ProviderID: scope.ProviderID, OIDCSubject: idTok.Subject,
	}
	user, created, err := d.account(ctx, scope, id, first)
	if err != nil {
		return nil, "", err
	}
	// ⚠⚠ The last word on the tenant boundary, as auth.LoginRealm.Admits is for
	// a password sign-in: whatever the lookups above returned, an account of
	// another tenant is not signed in to. No reason code: the page answers it
	// as any failure, so nobody learns that the address has an account there.
	if !scope.owns(user) {
		return nil, "", fmt.Errorf("oidc: account %d is not in tenant %d, the one this sign-in is for", user.ID, scope.ProviderID)
	}
	if created && !id.verified {
		return nil, "", d.holdForApproval(ctx, user, groups)
	}
	if mapping && !created {
		d.syncMappedRole(ctx, user, claimAdmin)
	}
	// The sign-in's groups (auth.RecordSignInGroups, one rule for every
	// provider): recorded and REPLACED — the IdP is the authority; a NEW
	// account that is not an administrator starts with the role they name
	// (perm.StartingRole), once; the filex groups linked to them are joined
	// and left, members added by hand staying; and the level a group's role
	// sets is checked at every sign-in — the admin mapping above may just
	// have demoted an administrator.
	user = auth.RecordSignInGroups(ctx, d.store, "oidc", user, groups, created)
	_ = d.store.TouchLastLogin(ctx, user.ID)

	// Mint a local session.
	sessionToken, err := randString(32)
	if err != nil {
		return nil, "", err
	}
	if _, err := d.store.CreateSession(ctx, user.ID, sessionToken, time.Now().Add(12*time.Hour), "", ""); err != nil {
		return nil, "", err
	}
	// Kept only where sign-out can use it (EndSessionURL). Losing it costs the
	// IdP half of sign-out, not the sign-in, so a failure here is a warning.
	if endSession != "" {
		if err := d.store.SetSessionIDToken(ctx, sessionToken, rawIDToken); err != nil {
			slog.Warn("oidc: could not keep the id_token for sign-out; this session will sign out of filex only",
				slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
		}
	}
	return user, sessionToken, nil
}

// EndSessionURL implements auth.OIDCLogoutDriver: where to send the browser so
// the IdP ends its own session too (OpenID Connect RP-Initiated Logout 1.0).
//
// ⚠ Without this, signing out of filex was not signing out. The IdP's session
// stayed open, so in SSO-first mode (FILEX_OIDC_AUTO_REDIRECT) the login page
// went straight back to the IdP, which issued a new code without a form: the
// same account was signed in again ~0.5 s later (measured on Keycloak 26), and
// on a shared computer the next person got the previous one's files.
//
// id_token_hint is what lets the IdP end the session without first asking
// "Do you want to log out?" (Keycloak) — or at all (IdPs that require it).
// An expired token is fine: the spec has the IdP accept it, and by sign-out
// time it usually has expired.
//
// "" — keep sign-out local — when the IdP advertises no end_session_endpoint,
// no id_token was kept for the session, or the one kept was issued by another
// issuer (a provider re-pointed between sign-in and sign-out): one IdP is
// never handed a token another one issued.
func (d *Driver) EndSessionURL(_ *http.Request, idToken, postLogoutRedirect string) string {
	d.mu.RLock()
	endSession, issuer := d.endSession, d.issuer
	clientID := ""
	if d.oauth != nil {
		clientID = d.oauth.ClientID
	}
	d.mu.RUnlock()
	if endSession == "" || idToken == "" {
		return ""
	}
	if iss, _ := parseJWTClaims(idToken)["iss"].(string); iss != issuer {
		return ""
	}
	u, err := url.Parse(endSession)
	if err != nil {
		return ""
	}
	q := u.Query() // an endpoint may already carry parameters of its own
	q.Set("id_token_hint", idToken)
	q.Set("client_id", clientID)
	if postLogoutRedirect != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// mappedRoleOnLogin is the role an EXISTING account holds after an SSO sign-in
// while an admin mapping (role claim + admin group) is configured.
//
// ⚠⚠ The mapping used to be read once, at account creation. Adding someone to
// the admin group after their first filex login did nothing, and removing
// someone from it did nothing either — an ex-admin in the IdP kept
// administering filex. The mapping owns exactly one fact, "is this account an
// admin", so that is all it changes: a role set by hand below admin (viewer)
// is left alone unless the group now grants admin.
//
// `protected` names the two accounts it never demotes, because demoting either
// can lock every administrator out: the account filex was set up with (the
// recovery login's account, local.BootstrapAdminSetting) and the last admin.
func mappedRoleOnLogin(current string, claimAdmin bool, defaultRole string, protected bool) string {
	switch {
	case claimAdmin && current != model.RoleAdmin:
		return model.RoleAdmin
	case !claimAdmin && current == model.RoleAdmin && !protected:
		return defaultRole
	}
	return current
}

// syncMappedRole applies mappedRoleOnLogin to a signed-in account and records
// the change in the log, since it is a privilege change nobody clicked.
func (d *Driver) syncMappedRole(ctx context.Context, user *model.User, claimAdmin bool) {
	protected := false
	if !claimAdmin && user.Role == model.RoleAdmin {
		protected = d.isProtectedAdmin(ctx, user.ID)
	}
	next := mappedRoleOnLogin(user.Role, claimAdmin, d.defaultRole, protected)
	if next == user.Role {
		if protected {
			slog.Warn("oidc: account is not in the admin group but keeps its admin role (setup account or last admin)",
				slog.Int64("user_id", user.ID), slog.String("email", user.Email))
		}
		return
	}
	if err := d.store.UpdateUserRole(ctx, user.ID, next); err != nil {
		slog.Warn("oidc: could not apply the mapped role",
			slog.Int64("user_id", user.ID), slog.String("role", next), slog.String("err", err.Error()))
		return
	}
	slog.Info("oidc: role changed by the IdP admin mapping",
		slog.Int64("user_id", user.ID), slog.String("email", user.Email),
		slog.String("from", user.Role), slog.String("to", next))
	user.Role = next
}

// isProtectedAdmin reports whether demoting this admin could leave filex with
// nobody able to administer it. An error while checking counts as protected:
// refusing a demotion is recoverable, a lockout is not.
func (d *Driver) isProtectedAdmin(ctx context.Context, userID int64) bool {
	if id, err := authlocal.BootstrapAdminID(ctx, d.store); err != nil || id == userID {
		return true
	}
	users, err := d.store.ListUsers(ctx)
	if err != nil {
		return true
	}
	admins := 0
	for _, u := range users {
		if u.IsAdmin() {
			admins++
		}
	}
	return admins <= 1
}

// parseJWTClaims decodes a JWT payload WITHOUT verifying the signature. It is
// used only to read role/group claims from an access_token that filex already
// obtained over TLS from the token endpoint in the code exchange (the id_token
// is separately signature-verified). Returns nil if the token is not a
// parseable JWT (e.g. an opaque access token).
func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return nil
	}
	return m
}

// claimContains traverses a possibly-dotted claim path (e.g.
// "realm_access.roles") and reports whether the value equals want (a string
// claim) or contains want (an array-of-strings claim).
func claimContains(claims map[string]any, path, want string) bool {
	for _, v := range claimValues(claims, path) {
		if v == want {
			return true
		}
	}
	return false
}

// claimValues returns the string value(s) at a dotted claim path: one for a
// string claim, every string element for an array one, none otherwise.
func claimValues(claims map[string]any, path string) []string {
	var cur any = claims
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = m[seg]; !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// clip shortens what the identity provider wrote for the log line.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

func randString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

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
	"github.com/brf-tech/filex/backend/internal/group"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
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
	// providerID scopes this driver instance to one tenant (multi-tenant mode;
	// docs/MULTI-TENANCY.md): the JIT upsert then looks users up WITHIN that
	// provider and stamps new users with it. 0 = single-tenant behaviour.
	providerID int64
}

// SetProviderID pins this driver instance to a tenant. Called by the
// multi-provider dispatcher right after Init.
func (d *Driver) SetProviderID(id int64) {
	d.mu.Lock()
	d.providerID = id
	d.mu.Unlock()
}

// New constructs an empty OIDC driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store, defaultRole: model.RoleUser}
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
	d.endSession = logout.EndSessionEndpoint
	d.roleClaim, _ = cfg["role_claim"].(string)
	d.adminGroup, _ = cfg["admin_group"].(string)
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
	url := d.oauth.AuthCodeURL(state)
	http.Redirect(w, r, url, http.StatusFound)
	return nil
}

// HandleCallback processes ?code= and ?state= from the IdP.
// It returns the upserted local user plus the local session token.
func (d *Driver) HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	d.mu.RLock()
	oauthCfg := d.oauth
	verifier := d.verifier
	roleClaim := d.roleClaim
	adminGroup := d.adminGroup
	providerID := d.providerID
	endSession := d.endSession
	d.mu.RUnlock()
	if oauthCfg == nil {
		return nil, "", errors.New("oidc: not initialized")
	}

	c, err := r.Cookie(stateCookieName)
	if err != nil || c.Value == "" || c.Value != r.URL.Query().Get("state") {
		return nil, "", errors.New("oidc: state mismatch")
	}
	tok, err := oauthCfg.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return nil, "", fmt.Errorf("oidc: code exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, "", errors.New("oidc: missing id_token")
	}
	idTok, err := verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		return nil, "", fmt.Errorf("oidc: verify id_token: %w", err)
	}

	var claims map[string]any
	if err := idTok.Claims(&claims); err != nil {
		return nil, "", err
	}
	email, _ := claims["email"].(string)
	if email == "" {
		return nil, "", errors.New("oidc: id_token missing email claim")
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

	// Upsert user. In multi-tenant mode (providerID set) the lookup is scoped to
	// THIS tenant, a new user is JIT-stamped with it, and the tag is immutable —
	// an email already registered to another tenant cannot hop realms (the JIT
	// create then fails on the unique email and the login is refused).
	ctx := r.Context()
	lower := strings.ToLower(email)
	var user *model.User
	created := false
	if providerID != 0 {
		user, err = d.store.GetUserByProviderEmail(ctx, providerID, lower)
		if err != nil {
			return nil, "", fmt.Errorf("oidc: lookup user: %w", err)
		}
		if user == nil {
			user, err = d.store.CreateUser(ctx, lower, "", role, "en", model.TimezoneUnset)
			if err != nil {
				return nil, "", fmt.Errorf("oidc: this email is registered to another tenant: %w", err)
			}
			created = true
			if err := d.store.SetUserProvider(ctx, user.ID, providerID, idTok.Subject); err != nil {
				return nil, "", fmt.Errorf("oidc: stamp tenant: %w", err)
			}
			if u2, err := d.store.GetUser(ctx, user.ID); err == nil {
				user = u2
			}
		} else if user.OIDCSubject == "" {
			// Backfill the subject for a pre-existing tenant user.
			_ = d.store.SetUserProvider(ctx, user.ID, providerID, idTok.Subject)
		}
	} else {
		user, err = d.store.GetUserByEmail(ctx, lower)
		if err != nil {
			user, err = d.store.CreateUser(ctx, lower, "", role, "en", model.TimezoneUnset)
			if err != nil {
				return nil, "", fmt.Errorf("oidc: upsert user: %w", err)
			}
			created = true
		}
	}
	if mapping && !created {
		d.syncMappedRole(ctx, user, claimAdmin)
	}
	// The groups this login carried, for permission rules that target an SSO
	// group (package perm). Read from the same claim the admin mapping uses,
	// in both tokens, and REPLACED at every login: the IdP is the authority,
	// so somebody removed from a group there leaves it here on their next
	// sign-in. No claim configured, or none in the tokens, is no groups.
	var groups []string
	if roleClaim != "" {
		for _, cs := range claimSets {
			groups = append(groups, claimValues(cs, roleClaim)...)
		}
	}
	if err := d.store.SetUserSSOGroups(ctx, user.ID, groups); err != nil {
		// Not fatal to the sign-in, but loud: a rule that should bind this
		// account through a group will not until the next successful write.
		slog.Warn("oidc: could not record the sign-in's SSO groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	// A NEW account whose groups name a role's SSO group starts with that
	// role (perm.StartingRole). Only at creation: afterwards the role is the
	// person's, changed on their own page like anyone else's. An account the
	// admin mapping made an administrator is bound by no role.
	if created && !user.IsAdmin() && len(groups) > 0 {
		d.giveStartingRole(ctx, user, groups)
	}
	// filex groups linked to these SSO groups (internal/group): the account
	// joins the ones it is now in and leaves the ones it was in only through
	// SSO — every sign-in, the IdP being the authority. Members added by hand
	// stay. A group can give a role, so the level underneath may move too.
	if _, err := group.SyncLinked(ctx, d.store, user, model.GroupLinkSSO, groups); err != nil {
		slog.Warn("oidc: could not update the account's SSO-linked groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	// The level a group's role sets is checked at every sign-in, not only
	// when a membership moved: the admin mapping above may just have demoted
	// an administrator, whose level no group's role was allowed to set while
	// they were one.
	if err := group.SyncLevels(ctx, d.store, []int64{user.ID}); err != nil {
		slog.Warn("oidc: could not bring the account's level in step with its groups",
			slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
	}
	if u2, err := d.store.GetUser(ctx, user.ID); err == nil && u2 != nil {
		user = u2
	}
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

func randString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// giveStartingRole gives a new account the custom role its SSO groups name,
// and the built-in role that role implies (perm.HolderRole). Failures are
// logged, not fatal: the account then simply starts with its default role.
func (d *Driver) giveStartingRole(ctx context.Context, user *model.User, groups []string) {
	rules, err := d.store.ListPermissionRules(ctx)
	if err != nil {
		slog.Warn("oidc: starting role: list roles", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
		return
	}
	role := perm.StartingRole(rules, groups, user.ProviderID)
	if role == nil {
		return
	}
	want := perm.RoleHolder(role)
	if user.Role != want {
		if err := d.store.UpdateUserRole(ctx, user.ID, want); err != nil {
			slog.Warn("oidc: starting role: set built-in role", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
			return
		}
		user.Role = want
	}
	if err := d.store.SetUserCustomRole(ctx, user.ID, role.ID); err != nil {
		slog.Warn("oidc: starting role: give role", slog.Int64("user_id", user.ID), slog.String("err", err.Error()))
		return
	}
	perm.Invalidate()
}

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
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func init() {
	auth.Register("oidc", func() auth.Driver { return &Driver{} })
}

const stateCookieName = "filex_oidc_state"

// Driver is the OIDC auth driver.
type Driver struct {
	store       db.Store
	mu          sync.RWMutex
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       *oauth2.Config
	issuer      string
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
		Path:     "/",
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
	if mapping {
		claimSets := []map[string]any{claims}
		if at, _ := tok.Extra("access_token").(string); at != "" {
			if ac := parseJWTClaims(at); ac != nil {
				claimSets = append(claimSets, ac)
			}
		}
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
	_ = d.store.TouchLastLogin(ctx, user.ID)

	// Mint a local session.
	sessionToken, err := randString(32)
	if err != nil {
		return nil, "", err
	}
	if _, err := d.store.CreateSession(ctx, user.ID, sessionToken, time.Now().Add(12*time.Hour), "", ""); err != nil {
		return nil, "", err
	}
	return user, sessionToken, nil
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
	var cur any = claims
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[seg]; !ok {
			return false
		}
	}
	switch v := cur.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if s, _ := x.(string); s == want {
				return true
			}
		}
	}
	return false
}

func randString(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

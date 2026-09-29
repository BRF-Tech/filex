package auth

import (
	"context"
	"net/http"
	"sync"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// Using an API token at all is a per-user permission (package perm):
// access.desktop for a token the desktop pairing minted, access.api for every
// other. Minting one is gated at /api/tokens and /api/auth/desktop/complete;
// this is the other half — a token minted BEFORE an administrator took the
// permission away stops working at its next request, not whenever its owner
// gets round to deleting it.
//
// It lives here, in front of both token doors (APITokenMiddleware for
// /api/ai, the apitoken driver for everything behind Middleware), rather than
// in a route gate, because a token is refused as a credential: no route
// behind it should be reachable.

// TokenPermission is the permission using tok needs.
func TokenPermission(tok *model.APIToken) perm.Perm {
	if tok != nil && tok.Source == model.TokenSourceDesktop {
		return perm.AccessDesktop
	}
	return perm.AccessAPI
}

// loaders keeps one perm.Loader per store so the install-wide cache is shared
// across requests (a Loader built per request would re-read the rules every
// time).
var loaders sync.Map // db.Store → *perm.Loader

func loaderFor(store db.Store) *perm.Loader {
	if l, ok := loaders.Load(store); ok {
		return l.(*perm.Loader)
	}
	l, _ := loaders.LoadOrStore(store, perm.NewLoader(store))
	return l.(*perm.Loader)
}

// TokenMayBeUsed reports whether u may authenticate with tok. A resolution
// error refuses — a credential that cannot be judged is not accepted.
func TokenMayBeUsed(ctx context.Context, store db.Store, u *model.User, tok *model.APIToken) bool {
	if store == nil {
		return true
	}
	res, err := loaderFor(store).Load(ctx, u)
	if err != nil {
		return false
	}
	return res.Can(TokenPermission(tok))
}

// ── Require 2FA (permission rule setting) ──────────────────────────────────
//
// A rule can require two-factor authentication. An account it binds that has
// not enrolled TOTP keeps a session — it has to, or it could never reach the
// enrolment screen — but that session answers 403 {"error":"2fa_required"}
// everywhere except the few routes enrolment needs (TwoFactorPendingAllowed).
// API tokens are separate credentials and are not affected; protocol PASSWORD
// logins are refused (protocolauth), the same way an enrolled account's are.
//
// SSO accounts (an OIDC subject) are exempt: their identity provider is where
// their second factor lives, and filex's TOTP is for local passwords.

var permStore struct {
	sync.RWMutex
	store db.Store
}

// SetPermissionStore gives the session middleware the store it resolves
// permission-rule settings from. Unset (tests), Require 2FA is not enforced
// at the session layer.
func SetPermissionStore(store db.Store) {
	permStore.Lock()
	permStore.store = store
	permStore.Unlock()
}

// TwoFactorPending reports whether u is bound by a Require 2FA rule and has
// not enrolled.
func TwoFactorPending(ctx context.Context, store db.Store, u *model.User) bool {
	if store == nil || u == nil || u.TOTPEnabled || u.OIDCSubject != "" || u.Role == model.RoleAdmin {
		return false
	}
	res, err := loaderFor(store).Load(ctx, u)
	if err != nil {
		// Unreadable rules: do not lock the account out of its own session
		// over a settings read; every permission check behind it fails
		// closed on the same error anyway.
		return false
	}
	return res.Settings.Require2FA
}

// twoFactorPendingAllowed lists what a session waiting on enrolment may still
// reach: who am I, what may I do, enrol, and leave — plus the read-only boot
// data the web app needs to draw the enrolment screen at all.
var twoFactorPendingAllowed = map[string]bool{
	"/api/auth/me":             true,
	"/api/auth/me/permissions": true,
	"/api/auth/logout":         true,
	"/api/auth/totp/enroll":    true,
	"/api/auth/totp/verify":    true,
	"/api/capabilities":        true,
	"/api/appearance":          true,
	"/api/branding":            true,
	"/api/me/prefs/":           true,
	"/api/me/custom-css":       true,
}

// TwoFactorPendingAllowed reports whether a pending session may reach path.
func TwoFactorPendingAllowed(path string) bool { return twoFactorPendingAllowed[path] }

// refuseTwoFactorPending writes the 403 for a pending session and reports
// true when it did.
func refuseTwoFactorPending(w http.ResponseWriter, r *http.Request, u *model.User) bool {
	permStore.RLock()
	store := permStore.store
	permStore.RUnlock()
	if TwoFactorPendingAllowed(r.URL.Path) || !TwoFactorPending(r.Context(), store, u) {
		return false
	}
	writeAuthErr(w, http.StatusForbidden, "2fa_required")
	return true
}

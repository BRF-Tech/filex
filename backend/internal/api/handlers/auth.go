package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
)

// Auth handles login/logout/oidc routes.
type Auth struct {
	Store       db.Store
	LocalAuth   auth.LoginDriver
	OIDCAuth    auth.OIDCDriver
	PublicURL   string
	MultiTenant bool
	// CookieDomain, when non-empty (FILEX_COOKIE_DOMAIN), is stamped as the
	// Domain attribute on the session cookie so subdomains share it. It must
	// be applied on clear too — a logout without the matching Domain leaves
	// the old cookie behind.
	CookieDomain string
}

// NewAuth constructs an Auth handler.
func NewAuth(store db.Store, local auth.LoginDriver, oidc auth.OIDCDriver, publicURL string, multiTenant bool, cookieDomain string) *Auth {
	return &Auth{Store: store, LocalAuth: local, OIDCAuth: oidc, PublicURL: publicURL, MultiTenant: multiTenant, CookieDomain: cookieDomain}
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// TOTP is the second-factor code. The SPA's Login.vue sends it under
	// this key; it is only consulted when the resolved user has TOTP
	// enabled.
	TOTP string `json:"totp"`
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
	if h.LocalAuth == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "local login disabled"})
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	user, token, err := h.LocalAuth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if !auth.LoginAllowed(r.Context(), h.Store, h.MultiTenant, user) {
		// Maintenance mode: multi-tenant is off but tenants exist — only the
		// supertenant may sign in. See docs/MULTI-TENANCY.md.
		_ = h.Store.DeleteSession(r.Context(), token)
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":       "sign-in is temporarily limited to the platform operator",
			"maintenance": true,
		})
		return
	}
	if user.TOTPEnabled {
		if strings.TrimSpace(req.TOTP) == "" {
			_ = h.Store.DeleteSession(r.Context(), token)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "two-factor code required",
				"totp_required": true,
			})
			return
		}
		if !verifyTOTP(user.TOTPSecret, req.TOTP) {
			_ = h.Store.DeleteSession(r.Context(), token)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "invalid two-factor code",
				"totp_required": true,
			})
			return
		}
	}
	h.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":  user,
		"token": token,
	})
}

// Logout clears the cookie + revokes the server-side session.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(authlocal.SessionCookieName); err == nil && c.Value != "" {
		_ = h.Store.DeleteSession(r.Context(), c.Value)
	}
	h.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// OIDCStart redirects to the IdP.
func (h *Auth) OIDCStart(w http.ResponseWriter, r *http.Request) {
	if h.OIDCAuth == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC not configured"})
		return
	}
	if err := h.OIDCAuth.StartFlow(w, r); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// OIDCCallback completes the OIDC flow.
func (h *Auth) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.OIDCAuth == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC not configured"})
		return
	}
	usr, token, err := h.OIDCAuth.HandleCallback(w, r)
	if err != nil {
		// The callback is a browser navigation (the IdP redirected here), so
		// a JSON body would dead-end the user on a raw error page. Send them
		// back to the login form with a generic marker instead; the SPA shows
		// a friendly message and — critically — suppresses OIDC auto-redirect
		// so a broken IdP can't cause a redirect loop.
		slog.Warn("oidc callback failed", slog.String("err", err.Error()))
		http.Redirect(w, r, h.PublicURL+"/admin/login?error=oidc", http.StatusFound)
		return
	}
	if !auth.LoginAllowed(r.Context(), h.Store, h.MultiTenant, usr) {
		// Maintenance mode (see docs/MULTI-TENANCY.md): tenant locked out.
		_ = h.Store.DeleteSession(r.Context(), token)
		http.Redirect(w, r, h.PublicURL+"/admin/login?maintenance=1", http.StatusFound)
		return
	}
	h.setSessionCookie(w, token)
	// After successful callback redirect to admin (or wherever the SPA wants).
	http.Redirect(w, r, h.PublicURL+"/admin/", http.StatusFound)
}

// WhoAmI returns the current user (or null).
func (h *Auth) WhoAmI(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

func (h *Auth) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    token,
		Path:     "/",
		Domain:   h.CookieDomain,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(authlocal.SessionTTL),
	})
}

func (h *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   h.CookieDomain,
		HttpOnly: true,
		MaxAge:   -1,
	})
}

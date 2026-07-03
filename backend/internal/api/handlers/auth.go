package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
)

// Auth handles login/logout/oidc routes.
type Auth struct {
	Store     db.Store
	LocalAuth auth.LoginDriver
	OIDCAuth  auth.OIDCDriver
	PublicURL string
}

// NewAuth constructs an Auth handler.
func NewAuth(store db.Store, local auth.LoginDriver, oidc auth.OIDCDriver, publicURL string) *Auth {
	return &Auth{Store: store, LocalAuth: local, OIDCAuth: oidc, PublicURL: publicURL}
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
	setSessionCookie(w, token)
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
	clearSessionCookie(w)
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
	_, token, err := h.OIDCAuth.HandleCallback(w, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	setSessionCookie(w, token)
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

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(authlocal.SessionTTL),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authlocal.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// Package handlers — auth_self.go
//
// Self-service auth endpoints (current user). All routes require an
// authenticated session and act on the principal in the request context.
//
//	GET    /api/auth/me              — current user
//	PATCH  /api/auth/profile         — update email/locale/timezone
//	POST   /api/auth/password        — change password (requires old)
//	POST   /api/auth/totp/enroll     — start TOTP enrollment
//	POST   /api/auth/totp/verify     — confirm TOTP enrollment with code
//	POST   /api/auth/totp/disable    — turn TOTP off (password + code)
package handlers

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"github.com/pquerna/otp/totp"

	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
)

// AuthSelf wraps the self-service profile/password/TOTP routes.
type AuthSelf struct {
	Store db.Store
}

// NewAuthSelf constructs the handler.
func NewAuthSelf(store db.Store) *AuthSelf { return &AuthSelf{Store: store} }

// Me returns the authenticated user.
//
// Wire shape mirrors LoginResponse: `{user: {…}}`. The frontend
// auth store reads `me.user`; without the wrapper user.value
// stayed undefined and TopNav fell back to "? —" forever.
func (h *AuthSelf) Me(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

type profileReq struct {
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

// UpdateProfile patches the current user's profile fields.
func (h *AuthSelf) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req profileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Email != nil && *req.Email != "" {
		_ = h.Store.UpdateUserEmail(r.Context(), u.ID, strings.ToLower(strings.TrimSpace(*req.Email)))
	}
	if req.DisplayName != nil {
		_ = h.Store.UpdateUserDisplayName(r.Context(), u.ID, strings.TrimSpace(*req.DisplayName))
	}
	if req.Locale != nil || req.Timezone != nil {
		l := u.Locale
		tz := u.Timezone
		if req.Locale != nil {
			l = *req.Locale
		}
		if req.Timezone != nil {
			tz = *req.Timezone
		}
		_ = h.Store.UpdateUserLocale(r.Context(), u.ID, l, tz)
	}
	updated, _ := h.Store.GetUser(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, updated)
}

type passwordReq struct {
	// OldPassword is the documented field. CurrentPassword is a defensive
	// alias — different frontends (and earlier builds of this SPA) posted
	// `current_password`; accept either so a field-name mismatch can never
	// silently turn the old-password check into a no-op.
	OldPassword     string `json:"old_password"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword verifies the old password then writes a new bcrypt hash
// and revokes other sessions to force re-login.
func (h *AuthSelf) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req passwordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password too short (min 8)"})
		return
	}
	oldPassword := req.OldPassword
	if oldPassword == "" {
		oldPassword = req.CurrentPassword
	}
	cur, err := h.Store.GetUser(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte(oldPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "old password incorrect"})
		return
	}
	hash, err := authlocal.HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpdateUserPassword(r.Context(), u.ID, hash); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Revoke other sessions; keep current.
	if c, err := r.Cookie(authlocal.SessionCookieName); err == nil {
		_ = h.Store.DeleteSessionsForUser(r.Context(), u.ID, c.Value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// TotpEnroll generates a new pending secret + QR SVG + recovery codes.
//
// The user must call /totp/verify with a valid code from their authenticator
// app to actually activate it; this endpoint does NOT enable TOTP yet.
func (h *AuthSelf) TotpEnroll(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	secret, err := generateTotpSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	codes := generateRecoveryCodes(10)
	if err := h.Store.SetTotpPendingSecret(r.Context(), u.ID, secret, codes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	otpURL := fmt.Sprintf(
		"otpauth://totp/filex:%s?secret=%s&issuer=filex&algorithm=SHA1&digits=6&period=30",
		u.Email, secret,
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"secret":         secret,
		"otpauth_url":    otpURL,
		"qr_svg":         renderQRSVG(otpURL),
		"recovery_codes": codes,
	})
}

type totpVerifyReq struct {
	Code string `json:"code"`
}

// TotpVerify activates TOTP if the given code matches the pending secret.
func (h *AuthSelf) TotpVerify(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req totpVerifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, err := h.Store.GetUser(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if cur.TOTPPendingSecret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no pending TOTP enrollment"})
		return
	}
	if !verifyTOTP(cur.TOTPPendingSecret, req.Code) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
		return
	}
	if err := h.Store.ActivateTotp(r.Context(), u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "totp_enabled": true})
}

type totpDisableReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// TotpDisable clears the user's TOTP secret if both password + code match.
func (h *AuthSelf) TotpDisable(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req totpDisableReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur, err := h.Store.GetUser(r.Context(), u.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "password incorrect"})
		return
	}
	if !cur.TOTPEnabled || !verifyTOTP(cur.TOTPSecret, req.Code) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
		return
	}
	if err := h.Store.ClearTotp(r.Context(), u.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "totp_enabled": false})
}

// generateTotpSecret produces a base32-encoded 20-byte secret (RFC 4648).
func generateTotpSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// generateRecoveryCodes produces n random 10-char alphanumeric codes.
func generateRecoveryCodes(n int) []string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]string, n)
	for i := range out {
		buf := make([]byte, 10)
		_, _ = rand.Read(buf)
		s := make([]byte, 10)
		for j := range s {
			s[j] = alphabet[int(buf[j])%len(alphabet)]
		}
		out[i] = string(s[:5]) + "-" + string(s[5:])
	}
	return out
}

// verifyTOTP validates a user-supplied one-time code against the stored
// base32 secret using RFC 6238 (SHA1, 6 digits, 30s period). pquerna's
// totp.Validate applies a ±1 period skew to tolerate clock drift and
// decodes no-padding base32 secrets (matching generateTotpSecret above).
func verifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}

// renderQRSVG renders the otpauth:// URI as a self-contained SVG QR code so
// the admin SPA (which v-html's the response) can display it without an
// extra request. Modules are drawn as 1×1 rects in a viewBox sized to the
// matrix; the SVG scales crisply to any width. On encode failure we fall
// back to a tiny notice SVG rather than failing enrollment outright — the
// caller also returns secret + otpauth_url so the user can still proceed.
func renderQRSVG(payload string) string {
	qr, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="180" height="180" viewBox="0 0 180 180">` +
			`<rect width="180" height="180" fill="#fff"/>` +
			`<text x="90" y="92" text-anchor="middle" font-family="monospace" font-size="9" fill="#900">QR encode error</text></svg>`
	}
	bitmap := qr.Bitmap()
	n := len(bitmap)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200" shape-rendering="crispEdges" viewBox="0 0 %d %d">`, n, n)
	b.WriteString(`<rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y, row := range bitmap {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

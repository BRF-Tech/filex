// Package handlers — auth_self.go
//
// Self-service auth endpoints (current user). All routes require an
// authenticated session and act on the principal in the request context.
//
//	GET    /api/auth/me              — current user
//	PATCH  /api/auth/profile         — update email/username/locale/timezone
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

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// AuthSelf wraps the self-service profile/password/TOTP routes.
type AuthSelf struct {
	Store db.Store
	// ACL resolves the caller's per-user permissions for Me (package perm).
	// Nil (tests) leaves them out of the answer.
	ACL *acl.Resolver
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
	out := map[string]any{"user": u}
	// What the account may do, so the web app can shape itself — which
	// admin pages to offer, which file actions to show — without a second
	// round trip. The stores/auth `permissions` field has always read this;
	// the server had never sent it. Account-wide: a rule limited to paths
	// is decided per path by the server (GET /api/auth/me/permissions lists
	// those rules).
	if h.ACL != nil {
		if res, err := h.ACL.Perms(r.Context(), u); err == nil && res != nil {
			out["permissions"] = res.Allowed.Strings()
			// …and what the role allows only in some folders: the web app
			// shows those actions, and the server decides per path.
			out["permissions_in_folders"] = res.AllowedInFolders().Strings()
			// …and every action whose answer differs from folder to folder:
			// the file browser asks POST /api/files/manager?action=allowed
			// about the selection before offering these.
			out["permissions_by_folder"] = res.VariesByFolder().Strings()
			out["permission_settings"] = res.Settings
			out["two_factor_required"] = res.Settings.Require2FA && !u.TOTPEnabled && u.OIDCSubject == "" && !u.IsAdmin()
		}
	}
	// The account's realm, on a multi-tenant install and for a tenant's
	// account only: what it writes in front of its name where no address says
	// which tenant it is (`acme/alex` over SFTP, the sign-in form's Realm field
	// on the platform's page). The connection guides print it. Absent on a
	// single-tenant install (no tenant scope) and for the platform's own
	// accounts.
	if realm := callerRealm(r, h.Store); realm != "" {
		out["realm"] = realm
	}
	writeJSON(w, http.StatusOK, out)
}

// callerRealm is the realm of the signed-in account's tenant on a multi-tenant
// install — what it writes in front of its name where no address names the
// tenant — or "" (single-tenant: no scope; the platform's own accounts: none).
func callerRealm(r *http.Request, store db.Store) string {
	sc, ok := tenant.FromContext(r.Context())
	if !ok || sc == nil || sc.IsSupertenant || sc.ProviderID == 0 || store == nil {
		return ""
	}
	p, err := store.GetProvider(r.Context(), sc.ProviderID)
	if err != nil {
		return ""
	}
	return p.LoginRealm()
}

type profileReq struct {
	Email *string `json:"email,omitempty"`
	// Username is the short login name used by the connection protocols
	// (migration 00025). Unlike the fields around it this one is REJECTED
	// rather than silently ignored when invalid or taken: it is a name other
	// people can see is unavailable, and a form that appears to save a name
	// the account did not get is worse than an error.
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
	// AvatarURL is the profile picture — a small data:image/… URI (what the
	// profile page's file picker produces) or an http(s)/site-relative URL.
	// An explicit "" removes it. Absent = leave the current one alone.
	AvatarURL *string `json:"avatar_url,omitempty"`
}

// avatarMaxBytes caps an inline profile picture. Far below the branding logo's
// 256 KB on purpose: the avatar rides inside every presence frame the live
// collaboration socket broadcasts, so a heavy one is paid for again on every
// join, leave and focus change — not once per page like a logo. The profile
// page downscales to 160px before encoding, which lands comfortably under this.
const avatarMaxBytes = 48 * 1024

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
	// ⚠⚠ Everything is CHECKED before anything is written. The e-mail used to
	// be written first and its error thrown away (`_ = UpdateUserEmail`):
	// "bu-bir-eposta-degil" was saved and answered "Profil kaydedildi" (and
	// e-mail login then failed with 401), another account's address answered
	// 200 and changed nothing, and a username refused afterwards left the
	// e-mail half-saved. account_rules.go says why, and in which words.
	var email, name string
	emailChanges, nameChanges := false, false
	if req.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*req.Email))
		// An account that has no address (some SSO ones) may keep having
		// none; one that has an address cannot be emptied from here.
		if email != "" || u.Email != "" {
			if p := emailProblem(r.Context(), h.Store, email, u.ID); p != nil {
				p.write(w, r)
				return
			}
			emailChanges = email != u.Email
		}
	}
	// ⚠ Keeping the name you already hold is not claiming it. The first
	// administrator holds the RESERVED "admin" (identity.ClaimBootstrap), and
	// the settings form sends the username with every save — checking an
	// unchanged name refused that account's every profile save.
	if req.Username != nil && identity.Normalize(*req.Username) != u.Username {
		name = identity.Normalize(*req.Username)
		// Check before writing so the common case gets a clear 409 instead of
		// a driver-specific unique-constraint string. The index is still the
		// real guard: a racing claim fails the UPDATE below and is reported.
		if p := usernameProblem(r.Context(), h.Store, name, u.ID); p != nil {
			p.write(w, r)
			return
		}
		nameChanges = true
	}
	if emailChanges {
		if err := h.Store.UpdateUserEmail(r.Context(), u.ID, email); err != nil {
			if isUniqueViolation(err) {
				emailTaken(email).write(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if nameChanges {
		if err := h.Store.SetUserUsername(r.Context(), u.ID, name); err != nil {
			if isUniqueViolation(err) {
				usernameTaken(name).write(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.DisplayName != nil {
		_ = h.Store.UpdateUserDisplayName(r.Context(), u.ID, strings.TrimSpace(*req.DisplayName))
	}
	if req.AvatarURL != nil {
		avatar := strings.TrimSpace(*req.AvatarURL)
		// Reject loudly rather than silently dropping the picture: the user is
		// looking at an upload they believe worked.
		if err := validateImageRef("avatar_url", avatar, avatarMaxBytes); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := h.Store.UpdateUserAvatar(r.Context(), u.ID, avatar); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
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
		// One language per person, whichever surface reads it (#191).
		if req.Locale != nil {
			syncSurfaceLocales(r.Context(), h.Store, u.ID, l)
		}
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
	// The one password rule (authlocal.CheckPassword), said in the reader's
	// language; an administrator setting a password asks the same.
	if p := passwordProblem(req.NewPassword); p != nil {
		p.write(w, r)
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

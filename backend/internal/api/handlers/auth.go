package handlers

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net"
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
	// CookieDomain (FILEX_COOKIE_DOMAIN) is the GLOBAL session-cookie Domain
	// attribute. In multi-tenant mode it is only the last-resort fallback —
	// see cookieDomain() for the per-provider resolution. Applied on clear
	// too — a logout without the matching Domain leaves the old cookie
	// behind.
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
	h.setSessionCookie(w, r, token)
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
	h.clearSessionCookie(w, r)
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
	base := h.redirectBase(r)
	usr, token, err := h.OIDCAuth.HandleCallback(w, r)
	if err != nil {
		// The callback is a browser navigation (the IdP redirected here), so
		// a JSON body would dead-end the user on a raw error page. Send them
		// back to the login form with a generic marker instead; the SPA shows
		// a friendly message and — critically — suppresses OIDC auto-redirect
		// so a broken IdP can't cause a redirect loop.
		slog.Warn("oidc callback failed", slog.String("err", err.Error()))
		http.Redirect(w, r, base+"/admin/login?error=oidc", http.StatusFound)
		return
	}
	if !auth.LoginAllowed(r.Context(), h.Store, h.MultiTenant, usr) {
		// Maintenance mode (see docs/MULTI-TENANCY.md): tenant locked out.
		_ = h.Store.DeleteSession(r.Context(), token)
		http.Redirect(w, r, base+"/admin/login?maintenance=1", http.StatusFound)
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
	// surface.
	writeOIDCBounce(w, "/admin/")
}

// oidcBounceTmpl is the 200 "signing in…" page that carries the session
// Set-Cookie past a CDN that strips it from 3xx responses. html/template
// context-escapes the target in the meta/JS/href sinks; the caller only ever
// passes a fixed relative path.
var oidcBounceTmpl = template.Must(template.New("oidcbounce").Parse(
	`<!doctype html><html><head><meta charset="utf-8">` +
		`<meta http-equiv="refresh" content="0;url={{.}}">` +
		`<title>Signing in…</title></head>` +
		`<body><script>location.replace("{{.}}")</script>` +
		`<noscript><a href="{{.}}">Continue</a></noscript>` +
		`Signing in…</body></html>`))

func writeOIDCBounce(w http.ResponseWriter, target string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = oidcBounceTmpl.Execute(w, target)
}

// redirectBase returns the origin that OIDCCallback redirects should target.
//
// Single-tenant: the configured PublicURL (which IS the install's host).
// Multi-tenant: the callback arrives on the TENANT's own host (each realm's
// redirect_uri points there — see multioidc.driverFor), so bouncing the user
// to h.PublicURL would strand them on the operator/supertenant host where
// they have no session and no files. Derive the base from the request host
// instead — but only when that host resolves to an enabled provider row (the
// same trusted-host model as tenant resolution, docs/MULTI-TENANCY.md §13);
// anything else falls back to PublicURL. Scheme is https (multi-tenant hosts
// sit behind the TLS-terminating proxy, same assumption as the per-tenant
// OIDC redirect default) unless the trusted proxy says X-Forwarded-Proto:
// http (TLS-less test setups).
func (h *Auth) redirectBase(r *http.Request) string {
	if !h.MultiTenant {
		return h.PublicURL
	}
	host := requestHost(r)
	if host == "" {
		return h.PublicURL
	}
	p, err := h.Store.GetProviderByHost(r.Context(), host)
	if err != nil || p == nil {
		return h.PublicURL
	}
	scheme := "https"
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
		scheme = "http"
	}
	return scheme + "://" + host
}

// cookieDomain returns the Domain attribute for the session cookie on this
// request. Single-tenant: the global FILEX_COOKIE_DOMAIN (may be empty =
// host-only). Multi-tenant, when the request host resolves to a provider:
//  1. the provider's explicit cookie_domain (operator-set, always wins);
//  2. else derived from the provider host by dropping its first label
//     (files.example.com → .example.com) — skipped when the remainder has
//     no dot left (files.localhost);
//  3. else the global FILEX_COOKIE_DOMAIN.
//
// ⚠ The derived form assumes the host is a subdomain OF the tenant apex
// (files.<apex>, the documented layout). A tenant served on its bare apex —
// or one whose derivation would land on a public suffix (diyetlif.com.tr →
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
	if p.CookieDomain != "" {
		return p.CookieDomain
	}
	if p.Host != "" {
		if i := strings.Index(p.Host, "."); i > 0 && strings.Contains(p.Host[i+1:], ".") {
			return p.Host[i:]
		}
	}
	return h.CookieDomain
}

// requestHost extracts the bare lowercase hostname (no port) the client asked
// for. Behind the reverse proxy filex trusts the proxied Host header — the
// proxy is the only reachable path in the documented deployments (§13).
// Mirrors multioidc.RequestHost.
func requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

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
		Path:     "/",
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
		Path:     "/",
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

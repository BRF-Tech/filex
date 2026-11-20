package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

const mcpOAuthCookie = "filex_mcp_oauth"
const mcpOAuthPath = "/api/oauth"

// MCPOAuth brokers an existing browser sign-in into a narrowly scoped MCP
// credential. Upstream OIDC tokens never become filex API credentials.
type MCPOAuth struct {
	store            db.Store
	scopes           *SelfTokens
	acl              *acl.Resolver
	issuer, resource string
	unavailable      string
	registrationMu   sync.Mutex
	registrations    map[string]mcpRegistrationWindow
}

type mcpRegistrationWindow struct {
	minute int64
	count  int
}

type mcpOAuthClient struct {
	ID        string   `json:"client_id"`
	Name      string   `json:"client_name"`
	Redirects []string `json:"redirect_uris"`
	Method    string   `json:"token_endpoint_auth_method"`
}

// The persisted form includes the hash; the registration response never does.
type mcpOAuthClientRecord struct {
	Client     mcpOAuthClient `json:"client"`
	SecretHash string         `json:"secret_hash,omitempty"`
}

type mcpOAuthRequest struct {
	ClientID    string
	ClientName  string
	RedirectURI string
	Challenge   string
	Resource    string
	Scope       string
	State       string
	UserID      int64
	CSRFHash    string
}

func NewMCPOAuth(store db.Store, resolver *acl.Resolver, publicURL string, multiTenant bool) *MCPOAuth {
	issuer := strings.TrimRight(publicURL, "/")
	u, err := url.Parse(issuer)
	unavailable := ""
	if err != nil || !validOAuthRedirect(issuer) || u.RawQuery != "" {
		issuer = ""
		unavailable = "Configure an HTTPS FILEX_PUBLIC_URL first"
	}
	if multiTenant {
		issuer = ""
		unavailable = "MCP OAuth currently supports single-tenant mode only; existing API keys remain available"
	}
	return &MCPOAuth{store: store, scopes: NewSelfTokens(store, resolver, nil), acl: resolver, issuer: issuer, resource: issuer + "/api/ai/mcp", unavailable: unavailable, registrations: map[string]mcpRegistrationWindow{}}
}

func (h *MCPOAuth) ready(w http.ResponseWriter) bool {
	if h.unavailable == "" {
		return true
	}
	oauthError(w, 503, "temporarily_unavailable", h.unavailable)
	return false
}

// Public registration is bounded in memory and per caller, independently of
// the persistent client quota. Existing grants never compete for these slots.
func (h *MCPOAuth) registrationAllowed(r *http.Request) bool {
	h.registrationMu.Lock()
	defer h.registrationMu.Unlock()
	minute := time.Now().Unix() / 60
	for ip, v := range h.registrations {
		if v.minute != minute {
			delete(h.registrations, ip)
		}
	}
	ip := clientip.FromRequest(r)
	v, ok := h.registrations[ip]
	if (!ok && len(h.registrations) >= 1024) || v.count >= 20 {
		return false
	}
	v.minute = minute
	v.count++
	h.registrations[ip] = v
	return true
}

func (h *MCPOAuth) ResourceMetadataURL() string {
	if h.issuer == "" {
		return ""
	}
	u, _ := url.Parse(h.issuer)
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + u.Path + "/api/ai/mcp"
}

// Discovery intercepts only the RFC 8414/9728 well-known paths. They precede
// the issuer's path, so sub-path installs need them outside basepath.Middleware.
func (h *MCPOAuth) Discovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.issuer != "" && (r.Method == "GET" || r.Method == "OPTIONS") {
			u, _ := url.Parse(h.issuer)
			resourcePath := "/.well-known/oauth-protected-resource" + u.Path + "/api/ai/mcp"
			serverPath := "/.well-known/oauth-authorization-server" + u.Path
			if r.URL.Path == resourcePath || r.URL.Path == serverPath || (u.Path == "" && r.URL.Path == "/.well-known/oauth-protected-resource") {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				if r.Method == "OPTIONS" {
					w.WriteHeader(204)
					return
				}
				if r.URL.Path == serverPath {
					h.Metadata(w, r)
				} else {
					writeJSON(w, 200, map[string]any{"resource": h.resource, "authorization_servers": []string{h.issuer}, "scopes_supported": []string{"read", "write", "delete", "mcp"}, "bearer_methods_supported": []string{"header"}})
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *MCPOAuth) Metadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"issuer": h.issuer, "authorization_endpoint": h.issuer + mcpOAuthPath + "/authorize",
		"token_endpoint": h.issuer + mcpOAuthPath + "/token", "registration_endpoint": h.issuer + mcpOAuthPath + "/register",
		"revocation_endpoint":      h.issuer + mcpOAuthPath + "/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported": []string{"read", "write", "delete", "mcp"}, "authorization_response_iss_parameter_supported": true,
	})
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func validOAuthRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

func (h *MCPOAuth) save(ctx context.Context, kind, key string, value any, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return h.store.CreateMCPOAuthRecord(ctx, kind, key, string(b), time.Now().Add(ttl).Unix())
}

func (h *MCPOAuth) read(ctx context.Context, kind, key string, value any, take bool) error {
	var b string
	var err error
	if take {
		b, err = h.store.TakeMCPOAuthRecord(ctx, kind, key, time.Now().Unix())
	} else {
		b, err = h.store.GetMCPOAuthRecord(ctx, kind, key, time.Now().Unix())
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(b), value)
}

func (h *MCPOAuth) Register(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if !h.registrationAllowed(r) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, 429, "slow_down", "Too many client registrations; retry in one minute")
		return
	}
	var c struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
		Method    string   `json:"token_endpoint_auth_method"`
		Grants    []string `json:"grant_types"`
		Responses []string `json:"response_types"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&c); err != nil {
		oauthError(w, 400, "invalid_client_metadata", "Invalid registration JSON")
		return
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		oauthError(w, 400, "invalid_client_metadata", "Expected one registration JSON object")
		return
	}
	if len(c.Redirects) == 0 || len(c.Redirects) > 10 || len(c.Name) > 200 {
		oauthError(w, 400, "invalid_client_metadata", "Provide 1–10 callback URLs and a client name up to 200 characters")
		return
	}
	for _, v := range c.Redirects {
		if !validOAuthRedirect(v) {
			oauthError(w, 400, "invalid_redirect_uri", "Callbacks require HTTPS, or HTTP on a literal loopback address, with no credentials or fragment")
			return
		}
	}
	if c.Method == "" {
		c.Method = "client_secret_basic"
	}
	if c.Method != "none" && c.Method != "client_secret_post" && c.Method != "client_secret_basic" {
		oauthError(w, 400, "invalid_client_metadata", "Unsupported client authentication method")
		return
	}
	for _, v := range c.Grants {
		if v != "authorization_code" && v != "refresh_token" {
			oauthError(w, 400, "invalid_client_metadata", "Unsupported grant type")
			return
		}
	}
	for _, v := range c.Responses {
		if v != "code" {
			oauthError(w, 400, "invalid_client_metadata", "Only the code response type is supported")
			return
		}
	}
	id, err := randHex(32)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not create client")
		return
	}
	rec := mcpOAuthClientRecord{Client: mcpOAuthClient{ID: id, Name: c.Name, Redirects: c.Redirects, Method: c.Method}}
	secret := ""
	if c.Method != "none" {
		secret, err = randHex(32)
		if err != nil {
			oauthError(w, 500, "server_error", "Could not create secret")
			return
		}
		rec.SecretHash = apitoken.HashToken(secret)
	}
	if err = h.save(r.Context(), "client", id, rec, 365*24*time.Hour); err != nil {
		oauthError(w, 503, "temporarily_unavailable", "Client registration is unavailable")
		return
	}
	out := map[string]any{"client_id": id, "client_name": c.Name, "redirect_uris": c.Redirects, "token_endpoint_auth_method": c.Method, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "client_id_issued_at": time.Now().Unix()}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, out)
}

func (h *MCPOAuth) client(ctx context.Context, id string) (*mcpOAuthClientRecord, error) {
	var c mcpOAuthClientRecord
	if len(id) != 64 {
		return nil, errors.New("invalid client")
	}
	if err := h.read(ctx, "client", id, &c, false); err != nil {
		return nil, err
	}
	return &c, nil
}

func oauthScope(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "read mcp"
	}
	parts := strings.Fields(raw)
	hasMCP := false
	for _, p := range parts {
		if p == "mcp" {
			hasMCP = true
		}
		if p != "mcp" && p != "read" && p != "write" && p != "delete" && !strings.HasPrefix(p, "root:") {
			return "", errors.New("Only read, write, delete, mcp and root scopes are supported")
		}
	}
	if !hasMCP {
		parts = append(parts, "mcp")
	}
	return strings.Join(parts, ","), nil
}

func (h *MCPOAuth) parseRequest(r *http.Request) (*mcpOAuthRequest, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	for _, v := range q {
		if len(v) != 1 {
			return nil, errors.New("Duplicate authorization parameter")
		}
	}
	c, err := h.client(r.Context(), q.Get("client_id"))
	if err != nil {
		return nil, errors.New("Unknown client")
	}
	redirect := q.Get("redirect_uri")
	valid := false
	for _, v := range c.Client.Redirects {
		if v == redirect {
			valid = true
		}
	}
	if !valid {
		return nil, errors.New("Callback is not registered for this client")
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !oauthChallengeValid(q.Get("code_challenge")) {
		return nil, errors.New("Authorization Code with S256 PKCE is required")
	}
	resource := q.Get("resource")
	if resource == "" {
		resource = h.resource
	}
	if resource != h.resource {
		return nil, errors.New("Unknown resource")
	}
	scope, err := oauthScope(q.Get("scope"))
	if err != nil {
		return nil, err
	}
	if len(scope) > 2048 || len(q.Get("state")) > 1024 {
		return nil, errors.New("Authorization parameters too long")
	}
	return &mcpOAuthRequest{ClientID: c.Client.ID, ClientName: c.Client.Name, RedirectURI: redirect, Challenge: q.Get("code_challenge"), Scope: scope, State: q.Get("state"), Resource: resource}, nil
}

func oauthNonce(raw string) bool {
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && len(raw) == 64
}

func mcpOAuthResumeTarget(r *http.Request) string {
	c, err := r.Cookie(mcpOAuthCookie)
	if err != nil || !oauthNonce(c.Value) {
		return ""
	}
	return basepath.Path(r.Context(), mcpOAuthPath+"/authorize?request=") + c.Value
}

func (h *MCPOAuth) cookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{Name: mcpOAuthCookie, Value: value, Path: basepath.CookiePath(r.Context()), MaxAge: 600, HttpOnly: true, Secure: strings.HasPrefix(h.issuer, "https://"), SameSite: http.SameSiteLaxMode})
}

func (h *MCPOAuth) browser(r *http.Request) *model.User {
	// OAuth consent never accepts a header credential, even if it represents
	// a real user: a confined integration cannot authorize a broader client.
	if auth.TokenFrom(r.Context()) != nil || r.Header.Get("Authorization") != "" || r.Header.Get("X-Filex-Token") != "" {
		return nil
	}
	c, err := r.Cookie(local.SessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		return nil
	}
	session, err := h.store.GetSessionByToken(r.Context(), c.Value)
	if err != nil || session == nil || session.UserID != u.ID || !session.ExpiresAt.After(time.Now()) {
		return nil
	}
	return u
}

func (h *MCPOAuth) Authorize(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if r.Method == "POST" {
		h.consent(w, r)
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		oauthError(w, 400, "invalid_request", "Authorization request is too long")
		return
	}
	var req *mcpOAuthRequest
	id := r.URL.Query().Get("request")
	if id != "" {
		c, err := r.Cookie(mcpOAuthCookie)
		if err != nil || !oauthNonce(id) || subtle.ConstantTimeCompare([]byte(c.Value), []byte(id)) != 1 {
			oauthError(w, 400, "invalid_request", "Authorization browser does not match")
			return
		}
		req = &mcpOAuthRequest{}
		if err := h.read(r.Context(), "request", apitoken.HashToken(id), req, true); err != nil {
			oauthError(w, 400, "invalid_request", "Authorization expired; reconnect from your MCP client")
			return
		}
	} else {
		if !h.registrationAllowed(r) {
			w.Header().Set("Retry-After", "60")
			oauthError(w, 429, "slow_down", "Too many authorization requests; retry in one minute")
			return
		}
		var err error
		req, err = h.parseRequest(r)
		if err != nil {
			oauthError(w, 400, "invalid_request", err.Error())
			return
		}
	}
	u := h.browser(r)
	id, err := randHex(32)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not start authorization")
		return
	}
	if u == nil {
		if err = h.save(r.Context(), "request", apitoken.HashToken(id), req, 10*time.Minute); err != nil {
			oauthError(w, 503, "temporarily_unavailable", "Could not start authorization")
			return
		}
		h.cookie(w, r, id)
		target := basepath.Path(r.Context(), "/admin/login") + "?redirect=" + url.QueryEscape(basepath.Path(r.Context(), mcpOAuthPath+"/authorize?request=")+id)
		http.Redirect(w, r, target, 303)
		return
	}
	scoped, err := h.scopes.cappedScopes(r.Context(), u, req.Scope)
	if err != nil || !callerCan(r.Context(), h.acl, perm.AccessAPI) || !callerCan(r.Context(), h.acl, perm.AIUse) {
		oauthError(w, 403, "access_denied", "Your account cannot authorize the requested file access")
		return
	}
	req.Scope = scoped
	req.UserID = u.ID
	csrf, err := randHex(32)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not create consent")
		return
	}
	req.CSRFHash = apitoken.HashToken(csrf)
	if err = h.save(r.Context(), "consent", apitoken.HashToken(id), req, 10*time.Minute); err != nil {
		oauthError(w, 503, "temporarily_unavailable", "Could not create consent")
		return
	}
	h.cookie(w, r, id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_ = mcpConsentTemplate.Execute(w, map[string]any{"Name": req.ClientName, "Callback": req.RedirectURI, "Email": u.Email, "Scopes": strings.Split(req.Scope, ","), "Request": id, "CSRF": csrf, "Action": basepath.Path(r.Context(), mcpOAuthPath+"/authorize")})
}

func (h *MCPOAuth) consent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", "Invalid consent form")
		return
	}
	id := r.PostForm.Get("request")
	c, err := r.Cookie(mcpOAuthCookie)
	u := h.browser(r)
	if u == nil || err != nil || !oauthNonce(id) || subtle.ConstantTimeCompare([]byte(c.Value), []byte(id)) != 1 {
		oauthError(w, 403, "access_denied", "A signed-in browser must approve this request")
		return
	}
	var req mcpOAuthRequest
	if err = h.read(r.Context(), "consent", apitoken.HashToken(id), &req, false); err != nil || req.UserID != u.ID || subtle.ConstantTimeCompare([]byte(req.CSRFHash), []byte(apitoken.HashToken(r.PostForm.Get("csrf")))) != 1 {
		oauthError(w, 403, "access_denied", "Invalid or expired consent")
		return
	}
	if !callerCan(r.Context(), h.acl, perm.AccessAPI) || !callerCan(r.Context(), h.acl, perm.AIUse) {
		oauthError(w, 403, "access_denied", "Your account cannot authorize MCP access")
		return
	}
	scope, err := h.scopes.cappedScopes(r.Context(), u, req.Scope)
	if err != nil {
		oauthError(w, 403, "access_denied", "Requested access is no longer available")
		return
	}
	req.Scope = scope
	decision := r.PostForm.Get("decision")
	if decision != "approve" && decision != "deny" {
		oauthError(w, 400, "invalid_request", "Choose Allow access or Deny")
		return
	}
	code, err := randHex(32)
	if err != nil {
		oauthError(w, 500, "server_error", "Could not authorize client")
		return
	}
	err = h.store.WithTx(r.Context(), func(ctx context.Context) error {
		var consumed mcpOAuthRequest
		if err := h.read(ctx, "consent", apitoken.HashToken(id), &consumed, true); err != nil {
			return err
		}
		if decision == "approve" {
			return h.save(ctx, "code", apitoken.HashToken(code), req, time.Minute)
		}
		return nil
	})
	if err != nil {
		oauthError(w, 400, "invalid_request", "Consent expired or was already used")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: mcpOAuthCookie, Path: basepath.CookiePath(r.Context()), MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(h.issuer, "https://"), SameSite: http.SameSiteLaxMode})
	target, _ := url.Parse(req.RedirectURI)
	q := target.Query()
	q.Set("state", req.State)
	q.Set("iss", h.issuer)
	if decision == "approve" {
		q.Set("code", code)
	} else {
		q.Set("error", "access_denied")
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), 303)
}

var mcpConsentTemplate = template.Must(template.New("mcp-consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorize MCP access</title>
<style>html{color-scheme:light dark;font-family:system-ui;line-height:1.5}body{max-width:36rem;margin:3rem auto;padding:1rem}button{font:inherit;padding:.75rem 1rem;min-height:3rem;margin:.5rem .5rem 0 0}button:focus-visible{outline:3px solid Highlight;outline-offset:3px}code,p{overflow-wrap:anywhere}</style></head>
<body><main><h1>Authorize MCP access</h1><p><strong>{{if .Name}}{{.Name}}{{else}}An MCP client{{end}}</strong> requests access as {{.Email}}.</p>
<p>This client will receive access to files your account can reach, limited to these scopes:</p><ul>{{range .Scopes}}<li><code>{{.}}</code></li>{{end}}</ul>
<p>Only continue if you started this connection. The client returns to <code>{{.Callback}}</code>.</p>
<form action="{{.Action}}" method="post"><input type="hidden" name="request" value="{{.Request}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit" name="decision" value="deny">Deny</button><button type="submit" name="decision" value="approve">Allow access</button></form>
<p>You can revoke this connection from API / MCP in filex.</p></main></body></html>`))

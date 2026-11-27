package handlers

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

const mcpOAuthCookie = "filex_mcp_oauth"
const mcpOAuthPath = "/api/oauth"

// The kinds of record the authorization server keeps (db.MCPOAuthSQL). The
// two a caller can make without signing in - "client" and "request" - share
// one ceiling of their own there (db.MCPOAuthAnonymousKind), so a stranger
// who fills them leaves the signed-in grants alone.
const (
	// A registration nobody has used yet. It lives an hour: a client that
	// registers goes on to authorize at once, and one that never does is
	// forgotten instead of holding a slot for a year.
	oauthKindClient = "client"
	// A registration that obtained a token: oauthClientLife after its last
	// authorization, so it outlives every grant it made.
	oauthKindClientUsed = "client_used"
	// An authorization waiting for its sign-in (no account yet).
	oauthKindRequest = "request"
	// The consent page that was shown, bound to its account and CSRF value.
	oauthKindConsent = "consent"
	// An authorization code: one use, one minute.
	oauthKindCode = "code"
	// A used code's grant, so a code that comes back revokes what it made
	// (RFC 6749 section 4.1.2).
	oauthKindSpentCode = "spent_code"
	// One authorization: its access token, scopes, address and the hash of
	// its current refresh token. One row per grant whatever the number of
	// refreshes.
	oauthKindGrant = "grant"
)

const (
	oauthPendingClientLife = time.Hour
	oauthClientLife        = 90 * 24 * time.Hour
	oauthRequestLife       = 10 * time.Minute
	oauthCodeLife          = time.Minute
	oauthSpentCodeLife     = 10 * time.Minute
	oauthGrantLife         = 30 * 24 * time.Hour
	oauthAccessLife        = time.Hour
)

// MCPOAuth brokers an existing browser sign-in into a narrowly scoped MCP
// credential. Upstream OIDC tokens never become filex API credentials.
//
// Every address filex answers on as an OAuth authorization server is a site
// of its own (oauthSite): its issuer is that address, its protected resource
// that address's MCP endpoint. A single-tenant install has one, its public
// URL. A multi-tenant one has the platform's public URL and every address a
// tenant owns (its host, an active own domain, its platform subdomain). A
// client registered at one site, a code issued at one, a grant and an access
// token are bound to that site and are refused at every other.
type MCPOAuth struct {
	store       db.Store
	scopes      *SelfTokens
	acl         *acl.Resolver
	tenants     tenanturl.Resolver
	multiTenant bool
	// public is the public URL's site; its Issuer is "" when that URL cannot
	// be an issuer (unset, not HTTPS, a query).
	public     oauthSite
	publicHost string
	limiter    oauthLimiter
}

// oauthSite is one address's authorization server and protected resource.
type oauthSite struct {
	Issuer   string
	Resource string
	// TenantID is the tenant whose own address this is, 0 for the platform's
	// address (and a single-tenant install), where every account that signs
	// in is at home.
	TenantID int64
}

func (s oauthSite) host() string {
	u, err := url.Parse(s.Issuer)
	if err != nil {
		return s.Issuer
	}
	return u.Host
}

// metadataURL is the site's protected-resource metadata (RFC 9728 section
// 3.1: the well-known segment goes between the host and the resource's path).
func (s oauthSite) metadataURL() string {
	u, err := url.Parse(s.Issuer)
	if err != nil || s.Issuer == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + u.Path + auth.MCPResourcePath
}

type mcpOAuthClient struct {
	ID        string   `json:"client_id"`
	Name      string   `json:"client_name"`
	Redirects []string `json:"redirect_uris"`
	Method    string   `json:"token_endpoint_auth_method"`
	// Issuer is the site the client registered at; it is unknown at every
	// other.
	Issuer string `json:"issuer"`
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
	// The site the authorization was asked of. The browser may finish it on
	// another address (a sign-in handed to the account's tenant), but the
	// code, the grant and the token are this site's.
	Issuer   string
	TenantID int64
}

// NewMCPOAuth builds the authorization server for an install.
func NewMCPOAuth(store db.Store, resolver *acl.Resolver, publicURL string, multiTenant bool) *MCPOAuth {
	issuer := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	h := &MCPOAuth{store: store, scopes: NewSelfTokens(store, resolver, nil), acl: resolver,
		tenants: tenanturl.New(store, publicURL, multiTenant), multiTenant: multiTenant}
	if oauthIssuerValid(issuer) {
		h.public = oauthSite{Issuer: issuer, Resource: issuer + auth.MCPResourcePath}
		u, _ := url.Parse(issuer)
		h.publicHost = strings.ToLower(u.Hostname())
	}
	return h
}

// oauthIssuerValid: an issuer is an https URL with no query, fragment or
// credentials (RFC 8414 section 2), or http on this machine for a test
// install (a loopback address, localhost or a name under .localhost, which
// browsers resolve to the machine itself).
func oauthIssuerValid(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return oauthLoopbackHost(u.Hostname())
	}
	return false
}

// oauthLoopbackHost: a literal loopback address or localhost (RFC 8252
// section 7.3, and the MCP specification's "localhost URLs").
func oauthLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// site is the authorization server of the address r arrived at; false when
// OAuth is not offered there. On a multi-tenant install the address must be
// the public URL's or one a tenant owns - an address nobody provisioned gets
// no issuer, so a forged Host header never becomes one.
func (h *MCPOAuth) site(r *http.Request) (oauthSite, bool) {
	if !h.multiTenant {
		return h.public, h.public.Issuer != ""
	}
	host := tenanturl.RequestHost(r)
	if host != "" && host == h.publicHost {
		return h.public, h.public.Issuer != ""
	}
	origin, p := h.tenants.Arrived(r)
	if p == nil || !oauthIssuerValid(origin) {
		return oauthSite{}, false
	}
	s := oauthSite{Issuer: origin, Resource: origin + auth.MCPResourcePath}
	// The platform's own tenant at an address of its own is the platform.
	if !p.IsSupertenant {
		s.TenantID = p.ID
	}
	return s, true
}

// MCPResource is auth.MCPOAuthDoor's question: the protected resource the
// request arrived at and its metadata URL, "" where OAuth is not offered.
func (h *MCPOAuth) MCPResource(r *http.Request) (string, string) {
	s, ok := h.site(r)
	if !ok {
		return "", ""
	}
	return s.Resource, s.metadataURL()
}

// AdmitsOAuthOwner is auth.MCPOAuthDoor's other question: whether u may use a
// grant of the address r arrived at.
func (h *MCPOAuth) AdmitsOAuthOwner(r *http.Request, u *model.User) bool {
	s, ok := h.site(r)
	return ok && h.admits(r.Context(), s.TenantID, u)
}

// admits is the tenant rule of a grant: on a tenant's own address only that
// tenant's accounts, and the platform's own (whose accounts may sign in on a
// tenant's address); on the platform's address (tenantID 0) every account,
// as an API key of any tenant's account opens the platform's MCP endpoint.
// It is asked at consent, at every token issued and at every MCP request, so
// an address that passes to another tenant (an own domain moved) stops
// opening the grants made there.
func (h *MCPOAuth) admits(ctx context.Context, tenantID int64, u *model.User) bool {
	if u == nil {
		return false
	}
	if tenantID == 0 {
		return true
	}
	if u.ProviderID == nil {
		return false
	}
	if *u.ProviderID == tenantID {
		return true
	}
	p, err := h.store.GetProvider(ctx, *u.ProviderID)
	return err == nil && p != nil && p.IsSupertenant
}

// ready answers a machine endpoint (register, token, revoke) that is not
// offered on this address.
func (h *MCPOAuth) ready(w http.ResponseWriter, r *http.Request) (oauthSite, bool) {
	s, ok := h.site(r)
	if !ok {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", h.unavailableReason())
	}
	return s, ok
}

func (h *MCPOAuth) unavailableReason() string {
	if h.public.Issuer == "" {
		return "Configure an HTTPS FILEX_PUBLIC_URL first"
	}
	return "MCP OAuth is not offered on this address"
}

// Discovery intercepts only the RFC 8414/9728 well-known paths. They precede
// the issuer's path, so sub-path installs need them outside basepath.Middleware.
func (h *MCPOAuth) Discovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodOptions) || !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-") {
			next.ServeHTTP(w, r)
			return
		}
		s, ok := h.site(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		u, _ := url.Parse(s.Issuer)
		resourcePath := "/.well-known/oauth-protected-resource" + u.Path + auth.MCPResourcePath
		serverPath := "/.well-known/oauth-authorization-server" + u.Path
		if r.URL.Path != resourcePath && r.URL.Path != serverPath && (u.Path != "" || r.URL.Path != "/.well-known/oauth-protected-resource") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == serverPath {
			h.metadata(w, s)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"resource": s.Resource, "authorization_servers": []string{s.Issuer},
			"scopes_supported": oauthScopesSupported, "bearer_methods_supported": []string{"header"}, "resource_name": "filex"})
	})
}

var oauthScopesSupported = []string{"read", "write", "delete", "mcp"}

func (h *MCPOAuth) metadata(w http.ResponseWriter, s oauthSite) {
	auths := []string{"none", "client_secret_post", "client_secret_basic"}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer": s.Issuer, "authorization_endpoint": s.Issuer + mcpOAuthPath + "/authorize",
		"token_endpoint": s.Issuer + mcpOAuthPath + "/token", "registration_endpoint": s.Issuer + mcpOAuthPath + "/register",
		"revocation_endpoint":      s.Issuer + mcpOAuthPath + "/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": auths,
		"revocation_endpoint_auth_methods_supported": auths,
		"scopes_supported":                           oauthScopesSupported, "authorization_response_iss_parameter_supported": true,
	})
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// validOAuthRedirect: a callback a client may register. HTTPS anywhere, or
// http on this machine (a loopback address or localhost, where a native
// client listens: RFC 8252 section 7.3, the MCP specification). No
// credentials, no fragment, and a host of plain letters, digits, dots and
// dashes (or an IP literal), so the origin can stand in a CSP header.
func validOAuthRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.Contains(raw, "#") {
		return false
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		for _, c := range host {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
				return false
			}
		}
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return oauthLoopbackHost(host)
	}
	return false
}

// redirectMatches compares the callback an authorization asks for with one
// the client registered: the very same string, except that on a loopback
// callback the port may differ (RFC 8252 section 7.3: a native client picks
// a free port when it listens). Never a prefix, never a pattern.
func redirectMatches(registered, asked string) bool {
	if registered == asked {
		return true
	}
	if !validOAuthRedirect(asked) {
		return false
	}
	ru, err1 := url.Parse(registered)
	au, err2 := url.Parse(asked)
	if err1 != nil || err2 != nil || ru.Scheme != "http" || au.Scheme != "http" {
		return false
	}
	return oauthLoopbackHost(ru.Hostname()) && strings.EqualFold(ru.Hostname(), au.Hostname()) &&
		ru.EscapedPath() == au.EscapedPath() && ru.RawQuery == au.RawQuery
}

// oauthClientName is the name a client gave itself, as the consent page and
// the API key's label show it: control and formatting characters (a
// right-to-left override that turns a name around) are dropped, spaces
// collapsed.
func oauthClientName(raw string) string {
	var b strings.Builder
	for _, c := range raw {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			continue
		}
		b.WriteRune(c)
	}
	return strings.Join(strings.Fields(b.String()), " ")
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
	s, ok := h.ready(w, r)
	if !ok {
		return
	}
	if !h.limiter.allow(r) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, http.StatusTooManyRequests, "slow_down", "Too many client registrations; retry in one minute")
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
	c.Name = oauthClientName(c.Name)
	if len(c.Redirects) == 0 || len(c.Redirects) > 10 || len(c.Name) > 200 {
		oauthError(w, 400, "invalid_client_metadata", "Provide 1 to 10 callback URLs and a client name up to 200 characters")
		return
	}
	for _, v := range c.Redirects {
		if !validOAuthRedirect(v) {
			oauthError(w, 400, "invalid_redirect_uri", "Callbacks require HTTPS, or HTTP on a loopback address or localhost, with no credentials or fragment")
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
	rec := mcpOAuthClientRecord{Client: mcpOAuthClient{ID: id, Name: c.Name, Redirects: c.Redirects, Method: c.Method, Issuer: s.Issuer}}
	secret := ""
	if c.Method != "none" {
		secret, err = randHex(32)
		if err != nil {
			oauthError(w, 500, "server_error", "Could not create secret")
			return
		}
		rec.SecretHash = apitoken.HashToken(secret)
	}
	if err = h.save(r.Context(), oauthKindClient, id, rec, oauthPendingClientLife); err != nil {
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

// client finds a registration at site s: one waiting for its first token,
// else a used one. The kind it was found under is returned with it.
//
// ⚠ In this order: a registration only ever moves from waiting to used
// (confirmClient, in one transaction), so a lookup that misses the waiting
// one is sure to find the used one. The other order missed both when another
// exchange of the same client moved it between the two reads.
func (h *MCPOAuth) client(ctx context.Context, s oauthSite, id string) (*mcpOAuthClientRecord, string, error) {
	var c mcpOAuthClientRecord
	if len(id) != 64 {
		return nil, "", errors.New("invalid client")
	}
	kind := oauthKindClient
	err := h.read(ctx, kind, id, &c, false)
	if errors.Is(err, sql.ErrNoRows) {
		kind = oauthKindClientUsed
		err = h.read(ctx, kind, id, &c, false)
	}
	if err != nil {
		return nil, "", err
	}
	if c.Client.Issuer == "" || c.Client.Issuer != s.Issuer {
		return nil, "", errors.New("client of another address")
	}
	return &c, kind, nil
}

// confirmClient keeps a client that has just obtained a token for
// oauthClientLife from now: a registration waiting for its first token
// becomes a used one, a used one starts its life again.
func (h *MCPOAuth) confirmClient(ctx context.Context, rec *mcpOAuthClientRecord, kind string) error {
	var taken mcpOAuthClientRecord
	if err := h.read(ctx, kind, rec.Client.ID, &taken, true); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Another exchange of this client confirmed it a moment ago.
			return nil
		}
		return err
	}
	return h.save(ctx, oauthKindClientUsed, rec.Client.ID, taken, oauthClientLife)
}

// oauthScope reads the scope an authorization asks for: space-separated
// read, write, delete, mcp and at most one root:<storage>://<path>, and
// nothing else (RFC 6749 section 3.3). It answers the stored form, the
// entries joined with commas - so a comma inside a requested scope is
// refused: it would smuggle a second entry (a permission level such as
// comments:rw) past this list.
func oauthScope(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "read mcp"
	}
	var parts []string
	seen := map[string]bool{}
	roots := 0
	for _, p := range strings.Fields(raw) {
		if seen[p] {
			continue
		}
		seen[p] = true
		switch {
		case strings.ContainsAny(p, ",\""):
			return "", errors.New("Only read, write, delete, mcp and root scopes are supported")
		case p == "mcp" || p == "read" || p == "write" || p == "delete":
		case strings.HasPrefix(p, apitoken.ScopeRootPrefix) && strings.Contains(p, "://"):
			roots++
		default:
			return "", errors.New("Only read, write, delete, mcp and root scopes are supported")
		}
		parts = append(parts, p)
	}
	if roots > 1 {
		return "", errors.New("Ask for one root scope at most")
	}
	if !seen["mcp"] {
		parts = append(parts, "mcp")
	}
	return strings.Join(parts, ","), nil
}

func (h *MCPOAuth) parseRequest(r *http.Request, s oauthSite) (*mcpOAuthRequest, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	for _, v := range q {
		if len(v) != 1 {
			return nil, errors.New("Duplicate authorization parameter")
		}
	}
	c, _, err := h.client(r.Context(), s, q.Get("client_id"))
	if err != nil {
		return nil, errors.New("Unknown client")
	}
	redirect := q.Get("redirect_uri")
	valid := false
	for _, v := range c.Client.Redirects {
		if redirectMatches(v, redirect) {
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
		resource = s.Resource
	}
	if resource != s.Resource {
		return nil, errors.New("Unknown resource")
	}
	scope, err := oauthScope(q.Get("scope"))
	if err != nil {
		return nil, err
	}
	if len(scope) > 2048 || len(q.Get("state")) > 1024 {
		return nil, errors.New("Authorization parameters too long")
	}
	return &mcpOAuthRequest{ClientID: c.Client.ID, ClientName: c.Client.Name, RedirectURI: redirect, Challenge: q.Get("code_challenge"),
		Scope: scope, State: q.Get("state"), Resource: resource, Issuer: s.Issuer, TenantID: s.TenantID}, nil
}

func oauthNonce(raw string) bool {
	b, err := hex.DecodeString(raw)
	return err == nil && len(b) == 32 && len(raw) == 64
}

// mcpOAuthResumeTarget is where a sign-in on this address continues when
// this browser has an authorization waiting here: the request's nonce from
// its HttpOnly cookie, validated, on a fixed path - never an address the
// request carried.
func mcpOAuthResumeTarget(r *http.Request) string {
	c, err := r.Cookie(mcpOAuthCookie)
	if err != nil || !oauthNonce(c.Value) {
		return ""
	}
	return basepath.Path(r.Context(), mcpOAuthPath+"/authorize?request=") + c.Value
}

// pendingKey is the key of the authorization this browser is signing in for
// on this address, "" for none: what a sign-in handed to another address
// carries in its ticket (auth.HandoffTicket.OAuthRequest).
func (h *MCPOAuth) pendingKey(r *http.Request) string {
	if h == nil {
		return ""
	}
	c, err := r.Cookie(mcpOAuthCookie)
	if err != nil || !oauthNonce(c.Value) {
		return ""
	}
	return apitoken.HashToken(c.Value)
}

// adopt continues, on the address a sign-in was handed to, the authorization
// the person started elsewhere: the request moves to a fresh nonce bound to
// THIS browser by this address's cookie, and the path to continue at is
// answered ("" when it is gone). The handoff ticket is the proof that this
// browser is the one that signed in where the request waits: it is one use,
// a minute long, carried in a URL fragment and honoured on its host only.
// The request keeps its own site: the code goes back to the client from the
// issuer it asked.
func (h *MCPOAuth) adopt(w http.ResponseWriter, r *http.Request, key string) string {
	if h == nil || key == "" {
		return ""
	}
	ctx := r.Context()
	var req mcpOAuthRequest
	if err := h.read(ctx, oauthKindRequest, key, &req, true); err != nil {
		return ""
	}
	id, err := randHex(32)
	if err != nil {
		return ""
	}
	if err := h.save(ctx, oauthKindRequest, apitoken.HashToken(id), req, oauthRequestLife); err != nil {
		return ""
	}
	h.cookie(w, r, id)
	return basepath.Path(ctx, mcpOAuthPath+"/authorize?request=") + id
}

func (h *MCPOAuth) cookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{Name: mcpOAuthCookie, Value: value, Path: basepath.CookiePath(r.Context()), MaxAge: int(oauthRequestLife / time.Second), HttpOnly: true, Secure: h.secureCookie(r), SameSite: http.SameSiteLaxMode})
}

func (h *MCPOAuth) dropCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: mcpOAuthCookie, Path: basepath.CookiePath(r.Context()), MaxAge: -1, HttpOnly: true, Secure: h.secureCookie(r), SameSite: http.SameSiteLaxMode})
}

// secureCookie: the browser reached filex over TLS (directly or through the
// proxy's X-Forwarded-Proto), the rule the session cookie follows - a Secure
// cookie set on a plain-HTTP answer is never sent back, and the consent would
// fail for want of it.
func (h *MCPOAuth) secureCookie(r *http.Request) bool {
	return requestIsHTTPS(r)
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
	if r.Method == http.MethodPost {
		h.consent(w, r)
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		h.page(w, r, http.StatusBadRequest, oauthPageInvalid, "Authorization request is too long")
		return
	}
	var req *mcpOAuthRequest
	id := r.URL.Query().Get("request")
	if id != "" {
		c, err := r.Cookie(mcpOAuthCookie)
		if err != nil || !oauthNonce(id) || subtle.ConstantTimeCompare([]byte(c.Value), []byte(id)) != 1 {
			h.page(w, r, http.StatusBadRequest, oauthPageExpired, "")
			return
		}
		req = &mcpOAuthRequest{}
		if err := h.read(r.Context(), oauthKindRequest, apitoken.HashToken(id), req, true); err != nil {
			h.page(w, r, http.StatusBadRequest, oauthPageExpired, "")
			return
		}
	} else {
		s, ok := h.site(r)
		if !ok {
			h.page(w, r, http.StatusServiceUnavailable, oauthPageUnavailable, "")
			return
		}
		if !h.limiter.allow(r) {
			w.Header().Set("Retry-After", "60")
			h.page(w, r, http.StatusTooManyRequests, oauthPageBusy, "")
			return
		}
		var err error
		req, err = h.parseRequest(r, s)
		if err != nil {
			h.page(w, r, http.StatusBadRequest, oauthPageInvalid, err.Error())
			return
		}
	}
	u := h.browser(r)
	id, err := randHex(32)
	if err != nil {
		h.page(w, r, http.StatusInternalServerError, oauthPageInvalid, "Could not start authorization")
		return
	}
	if u == nil {
		if err = h.save(r.Context(), oauthKindRequest, apitoken.HashToken(id), req, oauthRequestLife); err != nil {
			h.page(w, r, http.StatusServiceUnavailable, oauthPageBusy, "")
			return
		}
		h.cookie(w, r, id)
		target := basepath.Path(r.Context(), "/admin/login") + "?redirect=" + url.QueryEscape(basepath.Path(r.Context(), mcpOAuthPath+"/authorize?request=")+id)
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if !h.mayConsent(w, r, u, req) {
		return
	}
	scoped, err := h.scopes.cappedScopes(r.Context(), u, req.Scope)
	if err != nil {
		h.page(w, r, http.StatusForbidden, oauthPageScope, err.Error())
		return
	}
	req.Scope = scoped
	req.UserID = u.ID
	csrf, err := randHex(32)
	if err != nil {
		h.page(w, r, http.StatusInternalServerError, oauthPageInvalid, "Could not create consent")
		return
	}
	req.CSRFHash = apitoken.HashToken(csrf)
	if err = h.save(r.Context(), oauthKindConsent, apitoken.HashToken(id), req, oauthRequestLife); err != nil {
		h.page(w, r, http.StatusServiceUnavailable, oauthPageBusy, "")
		return
	}
	h.cookie(w, r, id)
	h.consentPage(w, r, u, req, id, csrf)
}

// mayConsent is everything an account must be to approve req, asked when
// the page is shown and again when it is answered: it may use API keys and
// the AI surface, it is not waiting to enrol a second factor, and the
// request's address is one its tenant may hold grants of (admits). On a
// refusal it has written the page.
func (h *MCPOAuth) mayConsent(w http.ResponseWriter, r *http.Request, u *model.User, req *mcpOAuthRequest) bool {
	ctx := r.Context()
	if !callerCan(ctx, h.acl, perm.AccessAPI) || !callerCan(ctx, h.acl, perm.AIUse) || auth.TwoFactorPending(ctx, h.store, u) {
		h.page(w, r, http.StatusForbidden, oauthPageDenied, "")
		return false
	}
	if !h.admits(ctx, req.TenantID, u) {
		h.tenantPage(w, r, u, req)
		return false
	}
	return true
}

func (h *MCPOAuth) consent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		h.page(w, r, http.StatusBadRequest, oauthPageInvalid, "Invalid consent form")
		return
	}
	id := r.PostForm.Get("request")
	c, err := r.Cookie(mcpOAuthCookie)
	u := h.browser(r)
	if u == nil || err != nil || !oauthNonce(id) || subtle.ConstantTimeCompare([]byte(c.Value), []byte(id)) != 1 {
		h.page(w, r, http.StatusForbidden, oauthPageExpired, "")
		return
	}
	var req mcpOAuthRequest
	if err = h.read(r.Context(), oauthKindConsent, apitoken.HashToken(id), &req, false); err != nil || req.UserID != u.ID || subtle.ConstantTimeCompare([]byte(req.CSRFHash), []byte(apitoken.HashToken(r.PostForm.Get("csrf")))) != 1 {
		h.page(w, r, http.StatusForbidden, oauthPageExpired, "")
		return
	}
	if !h.mayConsent(w, r, u, &req) {
		return
	}
	scope, err := h.scopes.cappedScopes(r.Context(), u, req.Scope)
	if err != nil {
		h.page(w, r, http.StatusForbidden, oauthPageScope, err.Error())
		return
	}
	req.Scope = scope
	decision := r.PostForm.Get("decision")
	if decision != "approve" && decision != "deny" {
		h.page(w, r, http.StatusBadRequest, oauthPageInvalid, "Choose Allow access or Deny")
		return
	}
	code, err := randHex(32)
	if err != nil {
		h.page(w, r, http.StatusInternalServerError, oauthPageInvalid, "Could not authorize client")
		return
	}
	err = h.store.WithTx(r.Context(), func(ctx context.Context) error {
		var consumed mcpOAuthRequest
		if err := h.read(ctx, oauthKindConsent, apitoken.HashToken(id), &consumed, true); err != nil {
			return err
		}
		if decision == "approve" {
			return h.save(ctx, oauthKindCode, apitoken.HashToken(code), req, oauthCodeLife)
		}
		return nil
	})
	if err != nil {
		h.page(w, r, http.StatusBadRequest, oauthPageExpired, "")
		return
	}
	h.dropCookie(w, r)
	target, _ := url.Parse(req.RedirectURI)
	q := target.Query()
	if req.State != "" {
		q.Set("state", req.State)
	}
	q.Set("iss", req.Issuer)
	if decision == "approve" {
		q.Set("code", code)
	} else {
		q.Set("error", "access_denied")
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

// Package fakeidp is a minimal OpenID Connect provider for tests: discovery,
// a JWKS, an authorization endpoint that signs the current identity in at once,
// a token endpoint that answers with an RS256-signed id_token for that
// identity, and an end-session endpoint (RP-initiated logout) that records who
// asked.
//
// Standard library only, on purpose: internal/testutil pulls in the whole API
// (and with it every auth driver), so a driver's own tests could not import a
// provider living there without an import cycle.
//
// ⚠ The ONE fake IdP. PR #40 (Berk Başarır) brought it for RP-initiated
// logout; the identity-providers page's tests (handlers/auth_providers_live_
// test.go) had a second one of their own with a real client check and a
// browser-shaped authorize step. Both live here now: WithClient makes the
// token endpoint judge the client the way a real IdP does, WithIssuerPath
// gives the issuer a realm path.
package fakeidp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const keyID = "fakeidp-1"

// IdP is one running provider.
type IdP struct {
	srv          *httptest.Server
	key          *rsa.PrivateKey
	noEndSession bool
	path         string // issuer path, "" or "/realms/x"

	// A confidential client the token endpoint insists on (WithClient).
	// Unset, any client and any code are accepted.
	clientID, clientSecret string

	mu          sync.Mutex
	email, sub  string
	lastIDToken string
	codes       map[string]bool
	logouts     []url.Values
}

// Option configures New.
type Option func(*IdP)

// WithoutEndSession leaves end_session_endpoint out of discovery — an IdP
// that offers no RP-initiated logout (GitHub-style OAuth bridges, some
// hosted providers).
func WithoutEndSession() Option { return func(p *IdP) { p.noEndSession = true } }

// WithClient makes the token endpoint behave like a real IdP's for one
// confidential client: wrong credentials are `401 invalid_client`, a request
// that is not an authorization-code exchange of a code /auth issued is
// `400 unauthorized_client` (what filex's provider probe reads as "the client
// authenticated").
func WithClient(id, secret string) Option {
	return func(p *IdP) { p.clientID, p.clientSecret = id, secret }
}

// WithIssuerPath puts the issuer under a path, e.g. "/realms/test".
func WithIssuerPath(path string) Option { return func(p *IdP) { p.path = path } }

// New starts a provider for the lifetime of the test.
func New(t *testing.T, opts ...Option) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("fakeidp: key: %v", err)
	}
	p := &IdP{key: key, email: "user@example.test", sub: "sub-1", codes: map[string]bool{}}
	for _, o := range opts {
		o(p)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(p.path+"/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/keys", p.jwks)
	mux.HandleFunc("/auth", p.authorize)
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/logout", p.logout)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// Issuer is the provider's issuer URL (what filex is configured with).
func (p *IdP) Issuer() string { return p.srv.URL + p.path }

// EndSessionEndpoint is where RP-initiated logout goes, or "" when the
// provider was started WithoutEndSession.
func (p *IdP) EndSessionEndpoint() string {
	if p.noEndSession {
		return ""
	}
	return p.srv.URL + "/logout"
}

// SignIn sets the identity the next code exchange signs in.
func (p *IdP) SignIn(email, sub string) {
	p.mu.Lock()
	p.email, p.sub = email, sub
	p.mu.Unlock()
}

// LastIDToken is the raw id_token the token endpoint handed out last.
func (p *IdP) LastIDToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastIDToken
}

// Logouts are the query strings the end-session endpoint was called with.
func (p *IdP) Logouts() []url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]url.Values(nil), p.logouts...)
}

// IDToken signs an id_token with arbitrary claims — for tests that need one
// the token endpoint would never issue (another issuer, say).
func (p *IdP) IDToken(claims map[string]any) string {
	header := b64(mustJSON(map[string]string{"alg": "RS256", "kid": keyID, "typ": "JWT"}))
	input := header + "." + b64(mustJSON(claims))
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		panic("fakeidp: sign: " + err.Error())
	}
	return input + "." + b64(sig)
}

func (p *IdP) discovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                p.Issuer(),
		"authorization_endpoint":                p.srv.URL + "/auth",
		"token_endpoint":                        p.srv.URL + "/token",
		"jwks_uri":                              p.srv.URL + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	if !p.noEndSession {
		doc["end_session_endpoint"] = p.srv.URL + "/logout"
	}
	writeJSON(w, doc)
}

func (p *IdP) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": keyID,
		"n": b64(p.key.N.Bytes()),
		"e": b64(big.NewInt(int64(p.key.E)).Bytes()),
	}}})
}

// authorize signs the current identity in without a form: it issues a code
// and sends the browser straight back to redirect_uri — what an IdP does for
// somebody who already has a session there.
func (p *IdP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || back.Scheme == "" {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	code := "code-" + q.Get("state")
	p.mu.Lock()
	p.codes[code] = true
	p.mu.Unlock()
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// token answers a code for the current SignIn identity. The client is
// whoever authenticated the exchange (basic auth or form, as oauth2 picks);
// WithClient makes it judge that client.
func (p *IdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	clientID, secret, ok := r.BasicAuth()
	if ok {
		clientID, _ = url.QueryUnescape(clientID)
		secret, _ = url.QueryUnescape(secret)
	} else {
		clientID, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if p.clientID != "" {
		if clientID != p.clientID || secret != p.clientSecret {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		p.mu.Lock()
		known := p.codes[r.PostForm.Get("code")]
		p.mu.Unlock()
		if r.PostForm.Get("grant_type") != "authorization_code" || !known {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized_client"})
			return
		}
	}
	p.mu.Lock()
	now := time.Now()
	idt := p.IDToken(map[string]any{
		"iss": p.Issuer(), "aud": clientID, "sub": p.sub, "email": p.email, "email_verified": true,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "sid": "sid-" + p.sub,
	})
	p.lastIDToken = idt
	p.mu.Unlock()
	writeJSON(w, map[string]any{
		"access_token": "at-" + p.sub, "token_type": "Bearer", "expires_in": 300, "id_token": idt,
	})
}

// logout is the end-session endpoint: it records the request and, like a real
// IdP, sends the browser on to post_logout_redirect_uri when there is one.
func (p *IdP) logout(w http.ResponseWriter, r *http.Request) {
	if p.noEndSession {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	p.mu.Lock()
	p.logouts = append(p.logouts, q)
	p.mu.Unlock()
	if back := q.Get("post_logout_redirect_uri"); back != "" {
		http.Redirect(w, r, back, http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

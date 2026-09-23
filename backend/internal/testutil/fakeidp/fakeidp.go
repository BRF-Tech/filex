// Package fakeidp is a minimal OpenID Connect provider for tests: discovery,
// a JWKS, and a token endpoint that answers any authorization code with an
// RS256-signed id_token for the identity set by SignIn.
//
// Standard library only, on purpose: internal/testutil pulls in the whole API
// (and with it every auth driver), so a driver's own tests could not import a
// provider living there without an import cycle.
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
	"sync"
	"testing"
	"time"
)

const keyID = "fakeidp-1"

// IdP is one running provider. Its issuer is the httptest server's URL.
type IdP struct {
	srv          *httptest.Server
	key          *rsa.PrivateKey
	noEndSession bool

	mu          sync.Mutex
	email, sub  string
	lastIDToken string
}

// Option configures New.
type Option func(*IdP)

// WithoutEndSession leaves end_session_endpoint out of discovery — an IdP
// that offers no RP-initiated logout (GitHub-style OAuth bridges, some
// hosted providers).
func WithoutEndSession() Option { return func(p *IdP) { p.noEndSession = true } }

// New starts a provider for the lifetime of the test.
func New(t *testing.T, opts ...Option) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("fakeidp: key: %v", err)
	}
	p := &IdP{key: key, email: "user@example.test", sub: "sub-1"}
	for _, o := range opts {
		o(p)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/keys", p.jwks)
	mux.HandleFunc("/token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// Issuer is the provider's issuer URL (what filex is configured with).
func (p *IdP) Issuer() string { return p.srv.URL }

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
		"issuer":                                p.srv.URL,
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

// token answers every code for the current SignIn identity. The client is
// whoever authenticated the exchange (basic auth or form, as oauth2 picks).
func (p *IdP) token(w http.ResponseWriter, r *http.Request) {
	clientID, _, ok := r.BasicAuth()
	if !ok {
		_ = r.ParseForm()
		clientID = r.PostForm.Get("client_id")
	}
	p.mu.Lock()
	now := time.Now()
	idt := p.IDToken(map[string]any{
		"iss": p.srv.URL, "aud": clientID, "sub": p.sub, "email": p.email,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "sid": "sid-" + p.sub,
	})
	p.lastIDToken = idt
	p.mu.Unlock()
	writeJSON(w, map[string]any{
		"access_token": "at-" + p.sub, "token_type": "Bearer", "expires_in": 300, "id_token": idt,
	})
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

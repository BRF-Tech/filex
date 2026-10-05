// Package storetest is a fake app store for tests: the `/v1/` paths a filex
// reads (docs/APP-PLUGINS-API.md → The store contract), signing exactly as a
// store must - ed25519 over the lower-hex sha256 of the canonical payload -
// and every answer adjustable mid-test (a tampered signature, a 410, a key
// that changes, a store that cannot be reached).
//
// It signs with appstore.Canonical, so it can only prove that filex agrees
// with ITSELF; the independent proof that the canonical form is what a store
// in another language writes is the test vector (appstore/testdata,
// produced by Node's crypto, docs/APP-PLUGINS-API.md).
package storetest

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
)

// Completion is one `POST /v1/install/{token}/complete` the store received.
type Completion struct {
	Token      string `json:"-"`
	InstanceID string `json:"instance_id"`
	Result     string `json:"result"`
}

// IntentEntry is how the store answers one token.
type IntentEntry struct {
	Payload map[string]any
	KeyID   string
	// Status: 0/200 answers the signed intent; 404 / 410 as such.
	Status int
	// Tamper changes the payload after signing it.
	Tamper bool
}

// DefaultFilexOrigin is the filex a link of New's store is made for unless
// FilexOrigin says otherwise.
const DefaultFilexOrigin = "http://test.local"

// Store is the fake.
type Store struct {
	Srv *httptest.Server
	// FilexOrigin is the filex_origin IntentPayload writes.
	FilexOrigin string

	mu          sync.Mutex
	keys        []appstore.Key
	priv        map[string]ed25519.PrivateKey
	intents     map[string]*IntentEntry
	completions []Completion
	verifies    []appstore.LicenseRequest
	// License answers a license check: the payload to sign and the key to
	// sign it with ("" = the first license key). nil answers 404.
	License func(req appstore.LicenseRequest) (payload map[string]any, keyID string)
	down    bool
	// KeysHits counts the keys.json reads; IntentHits the install-link reads.
	KeysHits   int
	IntentHits int
}

// New starts a fake store on a loopback address (plain http: filex admits it
// with loopback sources on).
func New() *Store {
	s := &Store{priv: map[string]ed25519.PrivateKey{}, intents: map[string]*IntentEntry{}, FilexOrigin: DefaultFilexOrigin}
	s.Srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the server.
func (s *Store) Close() { s.Srv.Close() }

// Origin is the store's origin, as filex names it.
func (s *Store) Origin() string { return s.Srv.URL }

// AddKey publishes a fresh key; its private half stays here.
func (s *Store) AddKey(id, use, status string) ed25519.PublicKey {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, appstore.Key{ID: id, Use: use, Ed25519: hex.EncodeToString(pub), Status: status})
	s.priv[id] = priv
	return pub
}

// SetKeyStatus moves a published key's status (rotation, retirement).
func (s *Store) SetKeyStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			s.keys[i].Status = status
		}
	}
}

// ReplaceKey publishes new material under an existing id (what a store whose
// key was swapped looks like).
func (s *Store) ReplaceKey(id string) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			s.keys[i].Ed25519 = hex.EncodeToString(pub)
		}
	}
	s.priv[id] = priv
}

// Keys answers the published keys.
func (s *Store) Keys() []appstore.Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]appstore.Key(nil), s.keys...)
}

// Fingerprints are what an administrator approves for the keys published now.
func (s *Store) Fingerprints() []string {
	ks := appstore.KeySet{Keys: s.Keys()}
	return appstore.Fingerprints(ks.TrustedKeys())
}

// SetDown makes every request fail at the connection (unreachable).
func (s *Store) SetDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
}

// Sign builds an envelope over payload with key id.
func (s *Store) Sign(keyID string, payload map[string]any) appstore.Envelope {
	s.mu.Lock()
	priv := s.priv[keyID]
	s.mu.Unlock()
	return SignWith(priv, keyID, payload)
}

// SignWith signs payload with priv: ed25519(lower-hex sha256(canonical)).
// The payload is carried INDENTED and in Go's map order, not canonical: a
// filex must canonicalise what it received before it verifies.
func SignWith(priv ed25519.PrivateKey, keyID string, payload map[string]any) appstore.Envelope {
	raw, _ := json.MarshalIndent(payload, "", "  ")
	c, err := appstore.Canonical(raw)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(c)
	sig := ed25519.Sign(priv, []byte(hex.EncodeToString(sum[:])))
	return appstore.Envelope{Payload: raw, KeyID: keyID, Signature: hex.EncodeToString(sig)}
}

// PutIntent publishes an install link.
func (s *Store) PutIntent(token string, e *IntentEntry) {
	s.mu.Lock()
	s.intents[token] = e
	s.mu.Unlock()
}

// Intent answers the entry for token (to change it mid-test).
func (s *Store) Intent(token string) *IntentEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intents[token]
}

// KeyReads is how many times keys.json was read; IntentReads how many times
// an install link was. Read under the store's lock (the fields are written
// by the server's goroutines: read bare, -race reports it).
func (s *Store) KeyReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.KeysHits
}

// IntentReads: see KeyReads.
func (s *Store) IntentReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.IntentHits
}

// Completions answers the completions received.
func (s *Store) Completions() []Completion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Completion(nil), s.completions...)
}

// Verifies answers the license checks received.
func (s *Store) Verifies() []appstore.LicenseRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]appstore.LicenseRequest(nil), s.verifies...)
}

// IntentPayload is a well-formed intent payload for this store.
func (s *Store) IntentPayload(tokenID, app, version, repo, ref string, expires time.Time) map[string]any {
	return map[string]any{
		"store": s.Origin(), "token_id": tokenID, "app": app, "kind": "app", "version": version,
		"repo": repo, "ref": ref, "commit": strings.Repeat("a", 40), "permissions": []string{},
		"filex_range": ">=0.52.0", "paid": false, "expires_at": expires.UTC().Format(time.RFC3339),
		"filex_origin": s.FilexOrigin,
	}
}

func (s *Store) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down {
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/keys.json":
		s.mu.Lock()
		s.KeysHits++
		body, _ := json.Marshal(map[string]any{"keys": s.keys})
		s.mu.Unlock()
		_, _ = w.Write(body)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/install/") && strings.HasSuffix(r.URL.Path, "/complete"):
		tok := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/install/"), "/complete")
		var c Completion
		_ = json.NewDecoder(r.Body).Decode(&c)
		c.Token = tok
		s.mu.Lock()
		s.completions = append(s.completions, c)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/install/"):
		tok := strings.TrimPrefix(r.URL.Path, "/v1/install/")
		s.mu.Lock()
		s.IntentHits++
		e := s.intents[tok]
		s.mu.Unlock()
		if e == nil || e.Status == http.StatusNotFound {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		if e.Status == http.StatusGone {
			http.Error(w, `{"error":"gone"}`, http.StatusGone)
			return
		}
		env := s.Sign(e.KeyID, e.Payload)
		if e.Tamper {
			var m map[string]any
			_ = json.Unmarshal(env.Payload, &m)
			m["version"] = "9.9.9"
			env.Payload, _ = json.Marshal(m)
		}
		_ = json.NewEncoder(w).Encode(env)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/licenses/verify":
		var req appstore.LicenseRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		s.verifies = append(s.verifies, req)
		fn := s.License
		s.mu.Unlock()
		if fn == nil {
			http.NotFound(w, r)
			return
		}
		payload, keyID := fn(req)
		if keyID == "" {
			for _, k := range s.Keys() {
				if k.Use == appstore.UseLicense {
					keyID = k.ID
					break
				}
			}
		}
		_ = json.NewEncoder(w).Encode(s.Sign(keyID, payload))
	default:
		http.NotFound(w, r)
	}
}

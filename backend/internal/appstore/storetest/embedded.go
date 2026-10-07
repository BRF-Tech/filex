package storetest

// The embedded store's half of the fake (#162): connection codes, a
// connected filex's signed requests held to the store's rules (the key, the
// five headers, the text it signs, a 5-minute window, a nonce seen once),
// the signed index and its icons.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/appstore"
)

// DefaultInstanceID is the store instance a connection binds to.
const DefaultInstanceID = "0f1e2d3c-4b5a-4968-8776-655443322110"

// IntentAsk is one signed intent request the store received.
type IntentAsk struct {
	App        string `json:"app"`
	Version    string `json:"version"`
	LicenseKey string `json:"license_key"`
}

// embedded is the fake's state for the embedded store.
type embedded struct {
	mu sync.Mutex
	// codes are the connection codes the store hands out (unused ones).
	codes map[string]bool
	// key is the connected filex's public key (nil: not connected).
	key        ed25519.PublicKey
	instanceID string
	// connOrigin is the filex_origin the last connection named.
	connOrigin string
	nonces     map[string]bool
	asks       []IntentAsk
	refused    []string
	deletes    int
	// OnIntent answers a signed intent request: the token to hand out and
	// the status (0 = 201). nil answers 404.
	onIntent func(ask IntentAsk) (token, tokenID string, status int)
	index    []byte
	indexSig string
	media    map[string][]byte
	idxHits  int
}

func (s *Store) emb() *embedded {
	s.embOnce.Do(func() {
		s.e = &embedded{codes: map[string]bool{}, nonces: map[string]bool{}, media: map[string][]byte{}, instanceID: DefaultInstanceID}
	})
	return s.e
}

// AddConnectCode makes code a connection code the store takes once.
func (s *Store) AddConnectCode(code string) {
	e := s.emb()
	e.mu.Lock()
	e.codes[code] = true
	e.mu.Unlock()
}

// ConnectedKey is the public key a filex connected with (nil: none).
func (s *Store) ConnectedKey() ed25519.PublicKey {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.key
}

// ConnectedOrigin is the filex_origin the last connection named.
func (s *Store) ConnectedOrigin() string {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.connOrigin
}

// Disconnects counts the signed DELETEs of the connection.
func (s *Store) Disconnects() int {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deletes
}

// OnIntent sets how the store answers a connected filex's intent request.
func (s *Store) OnIntent(fn func(ask IntentAsk) (token, tokenID string, status int)) {
	e := s.emb()
	e.mu.Lock()
	e.onIntent = fn
	e.mu.Unlock()
}

// IntentAsks are the signed intent requests the store took.
func (s *Store) IntentAsks() []IntentAsk {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]IntentAsk(nil), e.asks...)
}

// Refused are the reasons the store refused signed requests with.
func (s *Store) Refused() []string {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.refused...)
}

// SetIndex publishes doc as the store's index, signed with key id (the
// bytes as served are what is signed). tamper changes the bytes after.
func (s *Store) SetIndex(keyID string, doc map[string]any, tamper bool) {
	b, _ := json.MarshalIndent(doc, "", "  ")
	b = append(b, '\n')
	s.mu.Lock()
	priv := s.priv[keyID]
	s.mu.Unlock()
	sum := sha256.Sum256(b)
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(hex.EncodeToString(sum[:]))))
	if tamper {
		b = append(b[:len(b)-1], ' ', '\n')
	}
	e := s.emb()
	e.mu.Lock()
	e.index, e.indexSig = b, sig+"\n"
	e.mu.Unlock()
}

// IndexReads counts the index reads.
func (s *Store) IndexReads() int {
	e := s.emb()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.idxHits
}

// PutMedia serves b at /v1/media/<name>.
func (s *Store) PutMedia(name string, b []byte) {
	e := s.emb()
	e.mu.Lock()
	e.media[name] = b
	e.mu.Unlock()
}

// IndexDoc is a well-formed schema-1 index of this store with apps (each
// {name, version, permissions?}), expiring in a day.
func (s *Store) IndexDoc(apps ...map[string]any) map[string]any {
	list := []any{}
	for _, a := range apps {
		name, _ := a["name"].(string)
		version, _ := a["version"].(string)
		kind, _ := a["kind"].(string)
		if kind == "" {
			kind = "app"
		}
		perms, _ := a["permissions"].([]string)
		if perms == nil {
			perms = []string{}
		}
		app := map[string]any{
			"name": name, "kind": kind, "publisher": "acme", "repo": "Owner/" + name, "categories": []string{},
			"label": map[string]string{"en": "App " + name, "tr": "Uygulama " + name}, "latest": version, "revoked": nil,
			"screenshots": []any{},
			"versions": []any{map[string]any{
				"version": version, "ref": "v" + version, "commit": strings.Repeat("a", 40), "filex": ">=0.52.0",
				"published_at": "2026-10-01", "manifest": map[string]any{"url": "https://x/m.json", "sha256": strings.Repeat("b", 64), "sig": "00"},
				"wasm": nil, "ui": nil, "permissions": perms, "languages": []any{}, "engines": []any{}, "security": false, "yanked": nil,
			}},
		}
		if icon, ok := a["icon"].(string); ok {
			app["icon"] = map[string]any{"url": s.Origin() + "/v1/media/" + icon, "sha256": strings.SplitN(icon, ".", 2)[0]}
		}
		if y, ok := a["yanked"].(bool); ok && y {
			app["versions"].([]any)[0].(map[string]any)["yanked"] = map[string]any{"reason": "broken", "at": "2026-10-02T00:00:00Z"}
		}
		if rv, ok := a["revoked"].(bool); ok && rv {
			app["revoked"] = map[string]any{"reason": "harmful", "at": "2026-10-02T00:00:00Z"}
		}
		list = append(list, app)
	}
	return map[string]any{
		"schema": 1, "serial": 7, "generated_at": time.Now().UTC().Format(time.RFC3339),
		"expires_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		"keys": []any{}, "publishers": []any{map[string]any{"id": "acme", "name": "Acme", "github": "acme", "verified": true, "official": false}},
		"apps": list,
	}
}

// serveEmbedded answers the embedded store's paths; false when the path is
// not one of them.
func (s *Store) serveEmbedded(w http.ResponseWriter, r *http.Request) bool {
	e := s.emb()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v1/index.json":
		e.mu.Lock()
		e.idxHits++
		b := e.index
		e.mu.Unlock()
		if b == nil {
			http.NotFound(w, r)
			return true
		}
		_, _ = w.Write(b)
	case r.Method == http.MethodGet && path == "/v1/index.json.sig":
		e.mu.Lock()
		sig := e.indexSig
		e.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, sig)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/media/"):
		e.mu.Lock()
		b, ok := e.media[strings.TrimPrefix(path, "/v1/media/")]
		e.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)
	case r.Method == http.MethodPost && path == "/v1/instances/connect":
		s.serveConnect(w, r)
	case strings.HasPrefix(path, "/v1/instances/"):
		s.serveSigned(w, r)
	default:
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": code})
}

func (s *Store) serveConnect(w http.ResponseWriter, r *http.Request) {
	e := s.emb()
	var in struct {
		Code            string `json:"code"`
		PublicKey       string `json:"public_key"`
		FilexOrigin     string `json:"filex_origin"`
		FilexInstanceID string `json:"filex_instance_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	e.mu.Lock()
	ok := e.codes[in.Code]
	e.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	if in.FilexOrigin != s.FilexOrigin {
		writeErr(w, http.StatusConflict, "wrong_instance")
		return
	}
	pub, err := hex.DecodeString(in.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		writeErr(w, http.StatusBadRequest, "invalid_field")
		return
	}
	sum := sha256.Sum256(pub)
	e.mu.Lock()
	delete(e.codes, in.Code)
	e.key, e.connOrigin, e.nonces = ed25519.PublicKey(pub), in.FilexOrigin, map[string]bool{}
	id := e.instanceID
	e.mu.Unlock()
	keyID := ""
	for _, k := range s.Keys() {
		if k.Use == appstore.UseIndex {
			keyID = k.ID
			break
		}
	}
	_ = json.NewEncoder(w).Encode(s.Sign(keyID, map[string]any{
		"store": s.Origin(), "instance_id": id, "filex_origin": s.FilexOrigin,
		"key_fingerprint": hex.EncodeToString(sum[:]), "connected_at": time.Now().UTC().Format(time.RFC3339),
	}))
}

// serveSigned holds a connected filex's request to the store's rules.
func (s *Store) serveSigned(w http.ResponseWriter, r *http.Request) {
	e := s.emb()
	body, _ := io.ReadAll(r.Body)
	refuse := func(why string) {
		e.mu.Lock()
		e.refused = append(e.refused, why)
		e.mu.Unlock()
		writeErr(w, http.StatusUnauthorized, "instance_unauthorized")
	}
	e.mu.Lock()
	key, id := e.key, e.instanceID
	e.mu.Unlock()
	h := r.Header
	if key == nil {
		refuse("not connected")
		return
	}
	sum := sha256.Sum256(key)
	fpr := hex.EncodeToString(sum[:])
	if h.Get(appstore.HeaderInstance) != id || h.Get(appstore.HeaderKey) != fpr || !strings.HasPrefix(r.URL.Path, "/v1/instances/"+id+"/") {
		refuse("instance or key")
		return
	}
	ts, err := strconv.ParseInt(h.Get(appstore.HeaderTimestamp), 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > 5*time.Minute {
		refuse("timestamp")
		return
	}
	text := appstore.RequestText(r.Method, r.URL.EscapedPath(), id, fpr, h.Get(appstore.HeaderTimestamp), h.Get(appstore.HeaderNonce), body)
	tsum := sha256.Sum256([]byte(text))
	sig, err := hex.DecodeString(h.Get(appstore.HeaderSignature))
	if err != nil || !ed25519.Verify(key, []byte(hex.EncodeToString(tsum[:])), sig) {
		refuse("signature")
		return
	}
	nonce := h.Get(appstore.HeaderNonce)
	e.mu.Lock()
	seen := e.nonces[nonce]
	e.nonces[nonce] = true
	e.mu.Unlock()
	if seen {
		refuse("replay")
		return
	}
	switch {
	case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/connection"):
		e.mu.Lock()
		e.key = nil
		e.deletes++
		e.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/intents"):
		var ask IntentAsk
		_ = json.Unmarshal(body, &ask)
		e.mu.Lock()
		e.asks = append(e.asks, ask)
		fn := e.onIntent
		e.mu.Unlock()
		if fn == nil {
			writeErr(w, http.StatusNotFound, "not_found")
			return
		}
		token, tokenID, status := fn(ask)
		if status != 0 && status != http.StatusCreated {
			writeErr(w, status, "refused")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "token_id": tokenID, "app": ask.App, "version": ask.Version,
			"expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)})
	default:
		http.NotFound(w, r)
	}
}

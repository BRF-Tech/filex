package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// DesktopAuth authorizes the desktop app through the BROWSER instead of a
// native form.
//
// Why not a native login form: it can only ever do username+password. An
// install that authenticates with Keycloak/OIDC — passkeys, MFA, corporate SSO
// — cannot be signed into that way at all. The browser already knows how to do
// all of it (and usually already has the session), so the desktop app sends the
// user there and waits to be handed a credential.
//
// The flow, and why each piece exists:
//
//	desktop  → picks verifier + state, opens the browser at
//	           <server>/admin/login?desktop_state=…&desktop_challenge=sha256(verifier)
//	browser  → user signs in HOWEVER this server does it (local, OIDC, passkey)
//	SPA      → POST /api/auth/desktop/complete   (session-authenticated)
//	           server mints a durable token, parks it behind a ONE-TIME code
//	SPA      → navigates to filex://auth?state=…&code=…
//	desktop  → POST /api/auth/desktop/exchange {state, code, verifier}
//	           server checks sha256(verifier) == challenge, returns the token
//
// The deep link carries only a one-time code, never the token: a custom-scheme
// URL is handed around by the OS and lands in shell histories and logs. The
// verifier never leaves the desktop process, so a code someone else captures is
// worthless to them (PKCE, RFC 7636 in spirit).
type DesktopAuth struct {
	store db.Store

	mu      sync.Mutex
	pending map[string]*desktopPending
}

type desktopPending struct {
	challenge string
	code      string
	token     string
	email     string
	expires   time.Time
}

// desktopTTL bounds how long a half-finished authorization can sit around. Long
// enough to sign in through an IdP with MFA, short enough that an abandoned
// attempt is not a standing credential.
const desktopTTL = 10 * time.Minute

func NewDesktopAuth(store db.Store) *DesktopAuth {
	return &DesktopAuth{store: store, pending: map[string]*desktopPending{}}
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sweep drops expired entries. Called on every write so an idle server does not
// accumulate abandoned attempts.
func (h *DesktopAuth) sweep(now time.Time) {
	for k, v := range h.pending {
		if now.After(v.expires) {
			delete(h.pending, k)
		}
	}
}

type desktopCompleteReq struct {
	State     string `json:"state"`
	Challenge string `json:"challenge"`
	Label     string `json:"label"`
}

// Complete is called BY THE SPA once the browser session is authenticated, so
// it runs with whatever identity the server established — password, OIDC, it
// does not matter here. It mints the desktop's durable token and parks it
// behind a one-time code.
//
//	POST /api/auth/desktop/complete
func (h *DesktopAuth) Complete(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req desktopCompleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.State = strings.TrimSpace(req.State)
	req.Challenge = strings.TrimSpace(req.Challenge)
	if req.State == "" || req.Challenge == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "state and challenge required"})
		return
	}

	plain, err := generateToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "filex desktop"
	}
	if _, err := h.store.CreateAPIToken(r.Context(), &model.APIToken{
		UserID:    u.ID,
		Label:     label,
		TokenHash: apitoken.HashToken(plain),
		// Full-capability token: the desktop app IS the user's client, and the
		// sync engine has to read and write. Revocation is per-token from the
		// server's token screen, which is what makes handing one out safe.
		Scopes: "read,write,delete",
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	code, err := randHex(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.mu.Lock()
	h.sweep(time.Now())
	h.pending[req.State] = &desktopPending{
		challenge: req.Challenge,
		code:      code,
		token:     plain,
		email:     u.Email,
		expires:   time.Now().Add(desktopTTL),
	}
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"code": code, "email": u.Email})
}

type desktopExchangeReq struct {
	State    string `json:"state"`
	Code     string `json:"code"`
	Verifier string `json:"verifier"`
}

// Exchange trades the one-time code for the token. Unauthenticated on purpose —
// the desktop app has no session yet; possession of the verifier IS the proof.
//
//	POST /api/auth/desktop/exchange
func (h *DesktopAuth) Exchange(w http.ResponseWriter, r *http.Request) {
	var req desktopExchangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	h.mu.Lock()
	h.sweep(time.Now())
	p := h.pending[req.State]
	// One-time: remove it whatever the outcome, so a stolen code cannot be
	// replayed and a wrong verifier cannot be brute-forced by repeating.
	delete(h.pending, req.State)
	h.mu.Unlock()

	if p == nil || time.Now().After(p.expires) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or expired authorization"})
		return
	}
	sum := sha256.Sum256([]byte(req.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	// Constant-time on both: neither the code nor the challenge should leak
	// through timing.
	okCode := subtle.ConstantTimeCompare([]byte(req.Code), []byte(p.code)) == 1
	okChal := subtle.ConstantTimeCompare([]byte(want), []byte(p.challenge)) == 1
	if !okCode || !okChal {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "code or verifier mismatch"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"token": p.token, "email": p.email})
}

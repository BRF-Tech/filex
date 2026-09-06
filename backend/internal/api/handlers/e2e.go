package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

/* wiring:e2 — escrow use, and telling the owner about it.
 *
 * The server cannot decrypt anything and is not asked to. What it can do is
 * notice that somebody used the operator's escrow key, and say so.
 *
 * The naive version of this endpoint — "the client POSTs 'I used escrow'" —
 * is a string anybody can send, so it is worth nothing. Instead the client
 * proves possession first:
 *
 *   1. POST .../escrow/challenge {path}
 *      → the server mints a random nonce, encrypts it TO THE ESCROW PUBLIC
 *        KEY, and returns the ciphertext. Only the holder of the private
 *        half can read it.
 *   2. POST .../escrow/used {path, id, nonce}
 *      → the server compares in constant time. A match is evidence that the
 *        escrow private key was present in that browser, and only then does
 *        the notification go to the folder's owner.
 *
 * ⚠⚠ This makes the notification HONEST, not ENFORCED. An operator holding
 * the escrow private key can read the marker and the ciphertext straight off
 * the disk and decrypt offline, in a script, without ever touching filex.
 * No notification, no audit row, and nothing the server could do about it.
 * The alarm is on the front door of a house whose walls the operator owns.
 * docs/E2E-ENCRYPTION.md says exactly this, and must keep saying it.
 */

// escrowChallengeTTL is how long a minted nonce stays redeemable. Long
// enough for a human to paste a key, short enough that a captured challenge
// is not a durable artifact.
const escrowChallengeTTL = 5 * time.Minute

// escrowNonceLen is the nonce size. 32 bytes is far below the RSA-OAEP
// plaintext ceiling for a 2048-bit key (190 bytes with SHA-256), so the
// smallest key we accept still fits.
const escrowNonceLen = 32

type escrowChallenge struct {
	nonce   []byte
	storage int64
	rel     string
	expires time.Time
}

// escrowChallenges is an in-memory, TTL'd nonce store. Deliberately not
// persisted: a challenge lost to a restart costs one retry, whereas a table
// of them would be one more thing to migrate and prune for no gain.
type escrowChallenges struct {
	mu sync.Mutex
	m  map[string]escrowChallenge
}

func newEscrowChallenges() *escrowChallenges {
	return &escrowChallenges{m: map[string]escrowChallenge{}}
}

func (c *escrowChallenges) put(id string, ch escrowChallenge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	c.m[id] = ch
}

// take removes and returns a challenge — single use, so a replayed nonce
// cannot mint a second notification (or suppress one by being consumed
// early).
func (c *escrowChallenges) take(id string) (escrowChallenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.m[id]
	if !ok {
		return escrowChallenge{}, false
	}
	delete(c.m, id)
	if time.Now().After(ch.expires) {
		return escrowChallenge{}, false
	}
	return ch, true
}

// E2E serves the escrow challenge/report pair. Everything else about
// end-to-end encryption happens in the browser.
type E2E struct {
	Store  db.Store
	ACL    *acl.Resolver
	Escrow *e2e.EscrowKey // nil when escrow is disabled for this installation

	challenges *escrowChallenges
}

// NewE2E constructs the handler. escrow may be nil.
func NewE2E(store db.Store, escrow *e2e.EscrowKey) *E2E {
	return &E2E{Store: store, Escrow: escrow, challenges: newEscrowChallenges()}
}

// AttachACL wires the RBAC resolver.
func (h *E2E) AttachACL(r *acl.Resolver) { h.ACL = r }

type e2eEscrowChallengeReq struct {
	Path string `json:"path"` // wire path: <adapter>://<rel>
}

type e2eEscrowUsedReq struct {
	Path  string `json:"path"`
	ID    string `json:"id"`
	Nonce string `json:"nonce"` // base64 of the decrypted challenge
}

// resolveDir turns a wire path into (storage, relative dir) and enforces
// that the caller may at least SEE it. Returns nil after writing the error.
func (h *E2E) resolveDir(w http.ResponseWriter, r *http.Request, wire string) (*model.Storage, string) {
	adapter, rel := splitAdapterPath(wire)
	if adapter == "" {
		storages, err := h.Store.ListEnabledStorages(r.Context())
		if err != nil || len(storages) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no storages"})
			return nil, ""
		}
		adapter = storages[0].Name
	}
	st, err := h.Store.GetStorageByName(r.Context(), adapter)
	if err != nil || st == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, ""
	}
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return nil, ""
	}
	if !aclAllowID(r.Context(), h.ACL, h.Store, st.ID, rel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return nil, ""
	}
	return st, rel
}

// EscrowChallenge mints a proof-of-possession challenge for an encrypted
// folder.
//
//	POST /api/files/e2e/escrow/challenge {path} → {id, challenge}
//
// `challenge` is base64 RSA-OAEP(escrow public key, nonce). A caller without
// the private half learns nothing from it.
func (h *E2E) EscrowChallenge(w http.ResponseWriter, r *http.Request) {
	if h.Escrow == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow is not enabled on this installation"})
		return
	}
	var req e2eEscrowChallengeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	st, rel := h.resolveDir(w, r, req.Path)
	if st == nil {
		return
	}
	root, ok := e2e.FindRoot(r.Context(), h.Store, st.ID, rel)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not an encrypted folder"})
		return
	}
	nonce := make([]byte, escrowNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rng unavailable"})
		return
	}
	sealed, err := h.Escrow.SealNonce(nonce)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rng unavailable"})
		return
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	h.challenges.put(id, escrowChallenge{
		nonce:   nonce,
		storage: st.ID,
		rel:     root,
		expires: time.Now().Add(escrowChallengeTTL),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        id,
		"challenge": base64.StdEncoding.EncodeToString(sealed),
		"kid":       h.Escrow.KID,
	})
}

// EscrowUsed redeems the challenge and notifies the folder's owner.
//
//	POST /api/files/e2e/escrow/used {path, id, nonce} → {ok, notified}
//
// `notified` reports whether an owner could be identified. When the folder
// has no recorded owner the event still goes out — as an admin-visible
// broadcast rather than to nobody.
func (h *E2E) EscrowUsed(w http.ResponseWriter, r *http.Request) {
	if h.Escrow == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "escrow is not enabled on this installation"})
		return
	}
	var req e2eEscrowUsedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	ch, ok := h.challenges.take(req.ID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown or expired challenge"})
		return
	}
	got, err := base64.StdEncoding.DecodeString(req.Nonce)
	if err != nil || subtle.ConstantTimeCompare(got, ch.nonce) != 1 {
		// Not proof of possession. Say so plainly and do NOT notify — a
		// notification that fires on a wrong answer is a spam vector.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "challenge response did not match"})
		return
	}

	st, rel := h.resolveDir(w, r, req.Path)
	if st == nil {
		return
	}
	if st.ID != ch.storage {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "challenge was issued for another storage"})
		return
	}
	// The challenge names the encrypted ROOT it was minted for. Re-derive the
	// root of the path being reported and require them to match, so a
	// challenge taken for one folder cannot be spent announcing another.
	root, ok := e2e.FindRoot(r.Context(), h.Store, st.ID, rel)
	if !ok || root != ch.rel {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "challenge was issued for another folder"})
		return
	}

	actor := auth.UserFrom(r.Context())
	ownerID := h.folderOwner(r, st.ID, root)

	name := path.Base(root)
	if name == "." || name == "/" {
		name = root
	}
	ev := notify.Event{
		Event:    notify.EventE2EEscrowUsed,
		Severity: notify.SeverityWarning,
		Title:    "Encrypted folder opened with the escrow key",
		Body:     root,
		Node:     &notify.NodeRef{StorageID: st.ID, Path: root, Name: name},
		Meta: map[string]any{
			"escrow_kid": h.Escrow.KID,
			"storage":    st.Name,
			"folder":     root,
		},
	}
	if actor != nil {
		ev.Meta["actor_email"] = actor.Email
	}
	if ownerID != nil {
		ev.UserID = ownerID
	} else {
		// No recorded owner: emitFileEvent would otherwise scope the row to
		// the person who just used the escrow key, i.e. tell them about
		// themselves. Broadcast to admins instead.
		ev.UserID = nil
	}
	emitEscrowEvent(r.Context(), ev, ownerID != nil)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notified": ownerID != nil})
}

// folderOwner returns the recorded owner of the encrypted folder's own node,
// or nil when there is none.
func (h *E2E) folderOwner(r *http.Request, storageID int64, rel string) *int64 {
	if h.Store == nil || rel == "" {
		return nil
	}
	n, err := h.Store.GetNodeByPath(r.Context(), storageID, pathkey.Hash(storageID, rel))
	if err != nil || n == nil {
		return nil
	}
	owner, err := h.Store.GetNodeOwner(r.Context(), n.ID)
	if err != nil {
		return nil
	}
	return owner
}

// emitEscrowEvent sends the escrow-use event without the "scope it to the
// acting user" default that emitFileEvent applies. That default is right for
// routine file activity and exactly wrong here: the acting user IS the person
// holding the escrow key, so inheriting it would deliver the warning to them
// and to nobody else.
func emitEscrowEvent(ctx context.Context, e notify.Event, scoped bool) {
	if notifySink == nil {
		return
	}
	if e.Actor == nil {
		e.Actor = eventActor(ctx)
	}
	if !scoped {
		e.UserID = nil // admin-visible broadcast
	}
	c := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("notify: escrow event panic", slog.Any("recover", rec))
			}
		}()
		if _, err := notifySink.Send(c, e); err != nil {
			slog.Warn("notify: escrow event send", slog.String("err", err.Error()))
		}
	}()
}

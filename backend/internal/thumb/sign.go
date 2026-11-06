package thumb

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// ── Signed thumbnail URLs ───────────────────────────────────────────────────
//
// A thumbnail is fetched by `<img src=…>`, which carries no Authorization
// header and — because the session cookie is SameSite=Lax — carries no cookie
// either when the <img> lives in a third-party page (an embedded
// @brftech/filex explorer inside work.example.com or the fishapp PWA). So "require
// a session" alone cannot be the whole answer: it would blank every embedded
// explorer that renders `thumb_url` directly.
//
// The answer is to let the URL itself carry the proof. A listing is only ever
// produced for a caller who passed tenancy + ACL for those nodes, so the
// listing stamps each `thumb_url` with a short-lived HMAC over (node id,
// expiry). The image then proves it was handed out by an authorized listing,
// exactly the way a share link proves it was handed out by a share.
//
// ⚠ This is a capability, not an identity: anyone holding the URL can fetch
// that ONE node's thumbnail until it expires. That is the same trade a share
// link makes, and it is what makes a header-less <img> possible at all. What
// it replaces is far worse — before this, `sig` was optional and the key was
// never seeded, so `GET /api/files/thumb/{id}` served every rendered preview
// on the instance to anybody who could count.
//
// ⚠⚠ The signature is a FALLBACK, not the primary path. Every in-repo consumer
// (the admin SPA, the desktop app, the embedded explorer) fetches thumbnails
// through useThumbs, i.e. `fetch()` with credentials + auth headers, and is
// authorized per-request against the node's tenancy and ACL. The signature
// exists for the one consumer that cannot do that: a bare <img> pointed at the
// URL the server itself emitted.

// SigningKeySetting is the settings row holding the HMAC key. Generated on
// first use and then stable — rotating it only invalidates outstanding URLs,
// it never locks anybody out (authenticated callers do not need one).
const SigningKeySetting = "thumb_signing_key"

// DefaultURLTTL is how long a stamped thumbnail URL stays valid.
//
// ⚠ It matches the `Cache-Control: private, max-age=86400` the endpoint sends:
// a signature that expired before the browser cache did would produce a
// thumbnail that renders from cache on one machine and 403s on another, which
// is the kind of difference nobody can reproduce.
const DefaultURLTTL = 24 * time.Hour

// SignStore is the slice of db.Store the signer needs. Narrow on purpose so
// the signer is testable without a database.
type SignStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

// Signer mints and verifies thumbnail URL signatures.
//
// The zero value is not usable; a nil *Signer is, and it is the fail-CLOSED
// state: Query returns no signature and Verify refuses everything, so an
// install that never wired one simply requires an authenticated caller.
type Signer struct {
	store SignStore
	ttl   time.Duration

	mu  sync.Mutex
	key []byte

	// stamps reads the tenant a storage is in (db.StorageStamp), when the
	// store can: a URL carries it, and is served only while that tenant
	// still has the storage (db.StampStands, asked by the endpoint). nil: no
	// URL carries a tenant (a signer over a settings-only store, in a test).
	stamps   db.StampReader
	stampsMu sync.Mutex
	stampAt  map[int64]stampEntry
}

// stampEntry is one storage's tenant as a listing read it, for stampTTL: a
// listing signs every row of a folder, and the tenant of the folder's
// storage is one question, not one per row. A storage moved in the meantime
// is caught where it matters, at the fetch, which reads the rows fresh.
type stampEntry struct {
	tenant int64
	at     time.Time
}

const stampTTL = 30 * time.Second

// NewSigner returns a signer over store. ttl <= 0 means DefaultURLTTL.
func NewSigner(store SignStore, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = DefaultURLTTL
	}
	s := &Signer{store: store, ttl: ttl, stampAt: map[int64]stampEntry{}}
	if st, ok := store.(db.StampReader); ok {
		s.stamps = st
	}
	return s
}

// TTL reports the configured lifetime of a stamped URL.
func (s *Signer) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.ttl
}

// Query returns the `exp=…&sig=…` fragment for node id, or "" when no
// signature can be produced (nil signer, or the key could not be loaded —
// callers must then fall back to authenticated fetching rather than emit an
// unsigned URL that would only 401).
//
// ⚠ The expiry is QUANTIZED to a bucket rather than being `now + ttl`. An
// expiry that moved on every listing would change the URL string on every
// listing, and the URL string is the cache key both for the browser's HTTP
// cache and for useThumbs' object-URL cache — every folder refresh would
// re-download every thumbnail. Quantizing makes the URL stable within a
// window at the cost of up to one bucket of extra lifetime.
func (s *Signer) Query(id int64) string {
	return s.query(id, 0)
}

// QueryFor is Query for a node on storageID: the stamp also carries the
// tenant the storage is in (`ten`, signed with the rest), and the endpoint
// serves it only while that tenant still has the storage. A storage the
// platform operator moved to another tenant stops answering its old URLs at
// once, not when they expire a day later. When the tenant cannot be read no
// signature is minted (the caller's URL degrades to authenticated fetching,
// as for a signer with no key).
//
// The tenant is read with a background context and a short timeout, for the
// reason currentKey says: a listing's cancelled request must not leave its
// thumbnails unsigned.
func (s *Signer) QueryFor(id, storageID int64) string {
	if s == nil {
		return ""
	}
	tenant, ok := s.stampOf(storageID)
	if !ok {
		return ""
	}
	return s.query(id, tenant)
}

func (s *Signer) query(id, tenant int64) string {
	if s == nil {
		return ""
	}
	key := s.currentKey()
	if len(key) == 0 {
		return ""
	}
	exp := s.expiryFor(time.Now())
	q := "exp=" + strconv.FormatInt(exp, 10)
	if tenant > 0 {
		q += "&ten=" + strconv.FormatInt(tenant, 10)
	}
	return q + "&sig=" + sign(key, id, exp, tenant)
}

// stampOf is storageID's tenant (db.StorageStamp), cached for stampTTL.
func (s *Signer) stampOf(storageID int64) (int64, bool) {
	if s.stamps == nil || storageID <= 0 {
		return 0, true
	}
	s.stampsMu.Lock()
	e, hit := s.stampAt[storageID]
	s.stampsMu.Unlock()
	if hit && time.Since(e.at) < stampTTL {
		return e.tenant, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tenant, err := db.StorageStamp(ctx, s.stamps, storageID)
	if err != nil {
		return 0, false
	}
	s.stampsMu.Lock()
	if len(s.stampAt) > 4096 {
		s.stampAt = map[int64]stampEntry{}
	}
	s.stampAt[storageID] = stampEntry{tenant: tenant, at: time.Now()}
	s.stampsMu.Unlock()
	return tenant, true
}

// Verify reports whether (exp, sig) is a live signature for node id.
//
// A mismatch triggers ONE reload of the key from the settings row before the
// refusal: another process (a second replica, or an operator rotating the row)
// may have changed it since this process cached it, and a stale cache would
// otherwise reject signatures the deployment is currently minting.
func (s *Signer) Verify(id int64, exp, sig string) bool {
	return s.VerifyIn(id, exp, "", sig)
}

// VerifyIn is Verify for a stamp that may carry a tenant (`ten`, "" for
// none): the tenant is part of what is signed. Whether that tenant still has
// the node's storage is the endpoint's to ask (db.StampStands); it has the
// node.
func (s *Signer) VerifyIn(id int64, exp, ten, sig string) bool {
	if s == nil || sig == "" || exp == "" {
		return false
	}
	var tenant int64
	if ten != "" {
		t, err := strconv.ParseInt(ten, 10, 64)
		if err != nil || t <= 0 {
			return false
		}
		tenant = t
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expUnix {
		return false
	}
	if key := s.currentKey(); len(key) > 0 && hmac.Equal([]byte(sign(key, id, expUnix, tenant)), []byte(sig)) {
		return true
	}
	key := s.reloadKey()
	return len(key) > 0 && hmac.Equal([]byte(sign(key, id, expUnix, tenant)), []byte(sig))
}

// expiryFor is Query's quantizer. See the note on Query.
func (s *Signer) expiryFor(now time.Time) int64 {
	bucket := time.Hour
	if s.ttl < 2*time.Hour {
		bucket = s.ttl / 2
	}
	if bucket < time.Second {
		bucket = time.Second
	}
	return now.Add(s.ttl).Truncate(bucket).Add(bucket).Unix()
}

// sign is the HMAC itself. The node id and the expiry are joined by a
// character that cannot occur in either, so no two (id, exp) pairs can produce
// the same signing input.
//
// A stamp with a tenant appends it the same way; one without signs what it
// always did, so a single-tenant install's URLs are the same bytes as before.
func sign(key []byte, id, exp, tenant int64) string {
	mac := hmac.New(sha256.New, key)
	msg := strconv.FormatInt(id, 10) + "." + strconv.FormatInt(exp, 10)
	if tenant > 0 {
		msg += "." + strconv.FormatInt(tenant, 10)
	}
	_, _ = mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// currentKey returns the cached key, loading (and seeding, once) if needed.
//
// ⚠ context.Background with a short timeout, not a request context: the key is
// process-wide state read at most once, and hanging a listing's response on a
// settings lookup that a cancelled request could poison would make thumbnail
// URLs intermittently unsigned.
func (s *Signer) currentKey() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.key) > 0 {
		return s.key
	}
	s.key = s.loadOrSeedLocked()
	return s.key
}

// reloadKey drops the cache and re-reads the settings row.
func (s *Signer) reloadKey() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = nil
	s.key = s.loadOrSeedLocked()
	return s.key
}

func (s *Signer) loadOrSeedLocked() []byte {
	if s.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if v, err := s.store.GetSetting(ctx, SigningKeySetting); err == nil && v != "" {
		return []byte(v)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		slog.Error("thumb: could not generate a URL signing key; thumbnails will require an authenticated caller",
			slog.String("err", err.Error()))
		return nil
	}
	v := hex.EncodeToString(raw)
	if err := s.store.UpsertSetting(ctx, SigningKeySetting, v); err != nil {
		slog.Warn("thumb: could not persist the URL signing key; stamped URLs will not survive a restart",
			slog.String("err", err.Error()))
		return []byte(v)
	}
	// Re-read: two processes seeding at once both wrote, and the row is the
	// only arbiter of which value won.
	if got, err := s.store.GetSetting(ctx, SigningKeySetting); err == nil && got != "" {
		return []byte(got)
	}
	return []byte(v)
}

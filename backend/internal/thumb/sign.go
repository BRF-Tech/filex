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
}

// NewSigner returns a signer over store. ttl <= 0 means DefaultURLTTL.
func NewSigner(store SignStore, ttl time.Duration) *Signer {
	if ttl <= 0 {
		ttl = DefaultURLTTL
	}
	return &Signer{store: store, ttl: ttl}
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
	if s == nil {
		return ""
	}
	key := s.currentKey()
	if len(key) == 0 {
		return ""
	}
	exp := s.expiryFor(time.Now())
	return "exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sign(key, id, exp)
}

// Verify reports whether (exp, sig) is a live signature for node id.
//
// A mismatch triggers ONE reload of the key from the settings row before the
// refusal: another process (a second replica, or an operator rotating the row)
// may have changed it since this process cached it, and a stale cache would
// otherwise reject signatures the deployment is currently minting.
func (s *Signer) Verify(id int64, exp, sig string) bool {
	if s == nil || sig == "" || exp == "" {
		return false
	}
	expUnix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expUnix {
		return false
	}
	if key := s.currentKey(); len(key) > 0 && hmac.Equal([]byte(sign(key, id, expUnix)), []byte(sig)) {
		return true
	}
	key := s.reloadKey()
	return len(key) > 0 && hmac.Equal([]byte(sign(key, id, expUnix)), []byte(sig))
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
func sign(key []byte, id, exp int64) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strconv.FormatInt(id, 10) + "." + strconv.FormatInt(exp, 10)))
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

package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Ticket is a short-lived, single-use grant that authenticates one WebSocket
// upgrade. Embedded consumers (the vendored webcomponent inside work.example.com /
// fishapp) can't set an Authorization header on a native WebSocket and connect
// cross-origin to fm.example.com, so instead they fetch a ticket through the host's
// existing HTTP proxy (which injects the real token server-side) and open
// `wss://fm.example.com/api/ws?ticket=<t>`. The durable token never reaches the
// browser; only this 60-second, one-shot ticket does.
type Ticket struct {
	UserID int64
	Name   string
	// Avatar is the presence picture for this connection (a small data: URI or
	// a URL). Resolved at mint time from the account the connection
	// authenticates as, so every client of that account — browser session,
	// desktop app, any API key minted under it — shows the same face. Empty
	// when the account has no picture, or when a proxy re-identified the
	// connection as a different end user without supplying one (drawing the
	// account owner's face for somebody else would be worse than initials).
	Avatar string
	// PresenceKey distinguishes different END users sharing one filex account.
	// Embedded hosts (work/fishapp) authenticate every end user with a single
	// proxy token — the same filex UserID — so presence would collapse them all
	// into one entry. The host proxy stamps a per-end-user key (e.g. "work-7")
	// via X-Filex-Presence-Key and presence identity becomes (UserID, key).
	// Empty = identity is the filex user alone (native sessions).
	PresenceKey string
	// Confinement captured from the minting request (a root-confined token or
	// X-Filex-Root header). Empty adapter = unconfined.
	ConfineAdapter string
	ConfineRel     string
}

// OneShot is an in-memory store of short-lived, single-use tickets that
// carry a T. A ticket is consumed on first use and expires after its ttl, so
// the map stays tiny; a lazy sweep on mint drops anything stale. The zero
// value is ready to use. TicketStore (this package's socket) and the
// editing-together socket's tickets (handlers/coedit.go) are both one.
type OneShot[T any] struct {
	mu sync.Mutex
	m  map[string]oneShotEntry[T]
}

type oneShotEntry[T any] struct {
	v      T
	expiry time.Time
}

// Mint stores v for ttl and returns its opaque token. A best-effort sweep of
// expired tickets runs first so the map can't grow without bound.
func (s *OneShot[T]) Mint(v T, ttl time.Duration) (string, error) {
	tok, err := randToken()
	if err != nil {
		return "", err
	}
	e := oneShotEntry[T]{v: v, expiry: time.Now().Add(ttl)}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]oneShotEntry[T])
	}
	now := time.Now()
	for k, old := range s.m {
		if now.After(old.expiry) {
			delete(s.m, k)
		}
	}
	s.m[tok] = e
	return tok, nil
}

// Consume removes and returns what tok carries. ok is false if the token is
// unknown or expired (single-use: a second Consume of the same token fails).
func (s *OneShot[T]) Consume(tok string) (T, bool) {
	var zero T
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[tok]
	if !ok {
		return zero, false
	}
	delete(s.m, tok)
	if time.Now().After(e.expiry) {
		return zero, false
	}
	return e.v, true
}

// TicketStore is the realtime socket's store of live tickets (OneShot).
type TicketStore struct {
	s OneShot[Ticket]
}

// NewTicketStore builds an empty store.
func NewTicketStore() *TicketStore {
	return &TicketStore{}
}

// Mint stores t for ttl and returns its opaque token.
func (s *TicketStore) Mint(t Ticket, ttl time.Duration) (string, error) {
	return s.s.Mint(t, ttl)
}

// Consume removes and returns the ticket for tok. ok is false if the token is
// unknown or expired (single-use: a second Consume of the same token fails).
func (s *TicketStore) Consume(tok string) (Ticket, bool) {
	return s.s.Consume(tok)
}

// randToken returns 32 bytes of crypto-random data as hex.
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

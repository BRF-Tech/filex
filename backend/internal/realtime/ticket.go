package realtime

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Ticket is a short-lived, single-use grant that authenticates one WebSocket
// upgrade. Embedded consumers (the vendored webcomponent inside work.brf.sh /
// fishapp) can't set an Authorization header on a native WebSocket and connect
// cross-origin to fm.brf.sh, so instead they fetch a ticket through the host's
// existing HTTP proxy (which injects the real token server-side) and open
// `wss://fm.brf.sh/api/ws?ticket=<t>`. The durable token never reaches the
// browser; only this 60-second, one-shot ticket does.
type Ticket struct {
	UserID int64
	Name   string
	// Confinement captured from the minting request (a root-confined token or
	// X-Filex-Root header). Empty adapter = unconfined.
	ConfineAdapter string
	ConfineRel     string

	expiry time.Time
}

// TicketStore is an in-memory store of live tickets. Tickets are consumed on
// first use and expire quickly, so the map stays tiny; a lazy sweep on mint
// drops anything stale.
type TicketStore struct {
	mu sync.Mutex
	m  map[string]Ticket
}

// NewTicketStore builds an empty store.
func NewTicketStore() *TicketStore {
	return &TicketStore{m: make(map[string]Ticket)}
}

// Mint stores t for ttl and returns its opaque token. A best-effort sweep of
// expired tickets runs first so the map can't grow without bound.
func (s *TicketStore) Mint(t Ticket, ttl time.Duration) (string, error) {
	tok, err := randToken()
	if err != nil {
		return "", err
	}
	t.expiry = time.Now().Add(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expiry) {
			delete(s.m, k)
		}
	}
	s.m[tok] = t
	return tok, nil
}

// Consume removes and returns the ticket for tok. ok is false if the token is
// unknown or expired (single-use: a second Consume of the same token fails).
func (s *TicketStore) Consume(tok string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[tok]
	if !ok {
		return Ticket{}, false
	}
	delete(s.m, tok)
	if time.Now().After(t.expiry) {
		return Ticket{}, false
	}
	return t, true
}

// randToken returns 32 bytes of crypto-random data as hex.
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

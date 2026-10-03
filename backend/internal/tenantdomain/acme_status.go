package tenantdomain

import (
	"strings"
	"sync"
	"time"
)

// ── What filex's own ACME last did for an address ──────────────────────────
//
// The owner's decision (2026-10-02, after the real-authority measurement):
// the screen does not say "By filex (ACME)" over an address whose certificate
// could not be obtained. filex remembers, per address, the last thing its own
// ACME (FILEX_TLS_MODE=acme) did: a certificate obtained (until when), or
// none obtained (the authority's reason, when), and nothing at all until the
// first TLS handshake for the address asked for one.
//
// ⚠ In memory, this process only. A restart starts every address at
// "nothing yet" (the next handshake fills it in), and behind several replicas
// each one knows only the handshakes it served (docs/TENANT-ADMIN.md).

// The states of ACMEResult.State.
const (
	// ACMENone: no certificate asked for yet, in this process.
	ACMENone = "none"
	// ACMEObtained: the handshake was served a certificate the authority
	// issued (NotAfter: until when).
	ACMEObtained = "obtained"
	// ACMEFailed: the last attempt got none (Reason: what the authority said,
	// else what went wrong on the way to it; At: when).
	ACMEFailed = "failed"
)

// ACMEResult is the last thing filex's own ACME did for one address.
type ACMEResult struct {
	State    string     `json:"state"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	// Reason is in English: the authority's own words ("DNS problem: NXDOMAIN
	// looking up A for ..."), quoted on the screen.
	Reason string     `json:"reason,omitempty"`
	At     *time.Time `json:"at,omitempty"`
}

// problemFresh is how long an authority's reason stands for the failure the
// ACME client reports after it: one attempt (the orders of one handshake).
const problemFresh = 10 * time.Minute

type acmeProblem struct {
	reason string
	at     time.Time
}

// ACMEStatus remembers ACMEResult per address. The zero value is ready.
type ACMEStatus struct {
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	mu       sync.Mutex
	last     map[string]ACMEResult
	problems map[string]acmeProblem
}

func (s *ACMEStatus) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func acmeKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

// Obtained records that a handshake for name was served an issued
// certificate valid until notAfter.
func (s *ACMEStatus) Obtained(name string, notAfter time.Time) {
	k := acmeKey(name)
	if s == nil || k == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.last[k]; ok && r.State == ACMEObtained && r.NotAfter != nil && r.NotAfter.Equal(notAfter) {
		return
	}
	if s.last == nil {
		s.last = map[string]ACMEResult{}
	}
	at, na := s.now(), notAfter.UTC()
	s.last[k] = ACMEResult{State: ACMEObtained, NotAfter: &na, At: &at}
	delete(s.problems, k)
}

// Problem keeps what the authority said about an address it could not
// validate, for the failure the ACME client reports next (Failed).
func (s *ACMEStatus) Problem(name, reason string) {
	k := acmeKey(name)
	if s == nil || k == "" || strings.TrimSpace(reason) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problems == nil {
		s.problems = map[string]acmeProblem{}
	}
	s.problems[k] = acmeProblem{reason: strings.TrimSpace(reason), at: s.now()}
}

// Failed records that the last attempt for name got no certificate. The
// reason is the authority's own (Problem) when it gave one for this attempt,
// else the error the attempt ended on.
func (s *ACMEStatus) Failed(name string, err error) {
	k := acmeKey(name)
	if s == nil || k == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now()
	reason := ""
	if p, ok := s.problems[k]; ok && at.Sub(p.at) < problemFresh {
		reason = p.reason
	} else if err != nil {
		reason = err.Error()
	}
	if s.last == nil {
		s.last = map[string]ACMEResult{}
	}
	s.last[k] = ACMEResult{State: ACMEFailed, Reason: reason, At: &at}
}

// For is the last result for name; State ACMENone when there is none.
func (s *ACMEStatus) For(name string) ACMEResult {
	if s == nil {
		return ACMEResult{State: ACMENone}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.last[acmeKey(name)]; ok {
		return r
	}
	return ACMEResult{State: ACMENone}
}

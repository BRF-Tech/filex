package onlyoffice

// What filex answered the document server, per document (issue #80).
//
// "Download failed" is the editor's one sentence for two different failures:
// the document server could not download the document from filex, or the
// browser could not load the converted copy back from the document server.
// filex knows which, because it saw (or did not see) the document server's
// request: whether it came, when, and what filex answered. That used to end
// up in the log and nowhere else, so the person looking at the editor had to
// find an operator, and the operator had to grep.
//
// So the fetch endpoint remembers its last answer per document, the editor
// configuration endpoint remembers when it last opened one, and a person who
// may open the document can ask (GET /api/files/onlyoffice/diagnose). The
// editor asks when it reports "Download failed" and says which case it is.
//
// ⚠ This is one process's memory. It is bounded (fetchLogSize documents, the
// least recently touched one goes first), it is gone after a restart, and with
// several replicas behind a load balancer each one knows only the requests it
// served: the document server's download may have landed on another replica
// than the editor's question. The answer says so (Scope), and the docs do.
//
// ⚠ The fetch endpoint is public. A request whose signature checks out is the
// document server's and may add a document to the log; one that does not (a
// bad or expired link) only annotates a document filex itself opened. A
// stranger therefore cannot fill the log with node ids or push real documents
// out of it.

import (
	"sync"
	"time"
)

// fetchLogSize bounds how many documents the log remembers.
const fetchLogSize = 512

// Reason codes for a refused fetch (FetchOutcome.Code). The editor words them
// in the reader's language; Reason is the English the log line carries.
const (
	FetchBadLink            = "bad_link"
	FetchSignatureExpired   = "signature_expired"
	FetchSignatureBad       = "signature_bad"
	FetchNotFound           = "not_found"
	FetchStorageUnavailable = "storage_unavailable"
	FetchBodyUnavailable    = "body_unavailable"
	FetchObjectMissing      = "object_missing"
	FetchReadFailed         = "read_failed"
)

// Verdicts (Diagnosis.Verdict).
const (
	// VerdictServed: the document server downloaded the document and filex
	// served it. A "Download failed" after that is the browser's leg: the
	// converted copy could not be loaded from the document server.
	VerdictServed = "served"
	// VerdictNotRequested: the document server has not asked filex for the
	// document since filex last opened it (or ever). If the document was
	// opened before, the document server may be using its own converted copy.
	VerdictNotRequested = "not_requested"
	// VerdictRefused: the document server asked and filex refused; the
	// outcome's Code and Reason say why.
	VerdictRefused = "refused"
)

// ScopeThisProcess is the only Diagnosis.Scope there is: what this filex
// process saw.
const ScopeThisProcess = "this_process"

// FetchOutcome is what filex answered one request of the document server's.
type FetchOutcome struct {
	At     time.Time `json:"at"`
	Status int       `json:"status"`
	Code   string    `json:"reason_code,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// Diagnosis is the answer to "the editor said Download failed: what did filex
// see?".
type Diagnosis struct {
	Verdict string `json:"verdict"`
	// OpenedAt is when filex last handed out an editor configuration for the
	// document.
	OpenedAt *time.Time `json:"opened_at,omitempty"`
	// Fetch is the last answer filex gave the document server for it.
	Fetch *FetchOutcome `json:"fetch,omitempty"`
	Scope string        `json:"scope"`
}

type fetchEntry struct {
	opened  time.Time
	last    *FetchOutcome
	touched time.Time
}

type fetchLog struct {
	mu      sync.Mutex
	entries map[int64]*fetchEntry
	max     int
}

// entry returns the document's row, adding it when create is set (and pushing
// the least recently touched one out when the log is full).
func (l *fetchLog) entry(id int64, create bool) *fetchEntry {
	if e := l.entries[id]; e != nil || !create {
		return e
	}
	if l.entries == nil {
		l.entries = map[int64]*fetchEntry{}
	}
	if l.max > 0 && len(l.entries) >= l.max {
		var oldest int64
		var at time.Time
		first := true
		for k, e := range l.entries {
			if first || e.touched.Before(at) {
				oldest, at, first = k, e.touched, false
			}
		}
		delete(l.entries, oldest)
	}
	e := &fetchEntry{}
	l.entries[id] = e
	return e
}

// fetches returns the log, created on first use so a zero Service works.
func (s *Service) fetches() *fetchLog {
	s.fetchLogOnce.Do(func() { s.fetchLogV = &fetchLog{max: fetchLogSize} })
	return s.fetchLogV
}

// noteOpened records that filex handed out an editor configuration for the
// document.
func (s *Service) noteOpened(nodeID int64) {
	if s == nil || nodeID <= 0 {
		return
	}
	l := s.fetches()
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e := l.entry(nodeID, true)
	e.opened, e.touched = now, now
}

// NoteFetch records what the fetch endpoint answered for the document. signed
// says the request's signature checked out: only such a request may add a
// document to the log; any other only updates a document already in it.
func (s *Service) NoteFetch(nodeID int64, status int, code, reason string, signed bool) {
	if s == nil || nodeID <= 0 {
		return
	}
	l := s.fetches()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(nodeID, signed)
	if e == nil {
		return
	}
	now := time.Now()
	e.last = &FetchOutcome{At: now, Status: status, Code: code, Reason: reason}
	e.touched = now
}

// Diagnose says what this process saw of the document server's requests for
// the document.
func (s *Service) Diagnose(nodeID int64) Diagnosis {
	d := Diagnosis{Verdict: VerdictNotRequested, Scope: ScopeThisProcess}
	if s == nil {
		return d
	}
	l := s.fetches()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entry(nodeID, false)
	if e == nil {
		return d
	}
	if !e.opened.IsZero() {
		at := e.opened
		d.OpenedAt = &at
	}
	if e.last == nil {
		return d
	}
	last := *e.last
	d.Fetch = &last
	switch {
	case !e.opened.IsZero() && last.At.Before(e.opened):
		// It asked before, not since this opening: the document server is
		// working from what it already has, or did not get to filex at all.
		d.Verdict = VerdictNotRequested
	case last.Status >= 200 && last.Status < 300:
		d.Verdict = VerdictServed
	default:
		d.Verdict = VerdictRefused
	}
	return d
}

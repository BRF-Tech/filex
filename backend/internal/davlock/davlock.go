// Package davlock is the WebDAV lock system, and it exists because the one
// filex shipped with was a lie.
//
// # What was wrong
//
// /dav used webdav.NewMemLS(). It implements RFC 4918 class-2 locking
// correctly — the semantics below are its algorithm — but it holds every lock
// in a Go map, so the locks last exactly as long as the process. Restart filex,
// deploy a new version, let the container be rescheduled, and EVERY lock
// silently ceases to exist.
//
// That matters because of what a client does with a lock. Windows Explorer and
// macOS Finder take one before they write, hold it across the whole edit, and
// present the token on the PUT. After a restart the token names nothing, the
// PUT gets 412 Precondition Failed, and the person editing the file sees their
// save fail with no explanation — while the server, having forgotten, would
// happily let a SECOND client lock and write the same file. The lock said
// "exclusive" and stopped being true without telling anybody.
//
// # What this does instead
//
// The same algorithm, with the lock table written to a file in the data
// directory on every change and read back at boot — TOKENS INCLUDED, which is
// the whole point: a client's token has to keep meaning the same lock across a
// restart or the durability buys nothing.
//
// ⚠ What it does NOT do, stated plainly rather than implied: it is not a
// distributed lock. Two filex processes serving the same storage each keep
// their own file and would each grant a lock the other does not know about. One
// process per deployment is filex's shape today; if that ever changes, this is
// the thing that has to move into the database, and the honest place to say so
// is here rather than in an incident.
//
// ⚠ WebDAV locks are advisory in the first place (RFC 4918 §6.3): they
// coordinate polite clients, they do not stop the S3 endpoint or the web
// explorer from writing the same file. That is the protocol's design, not a gap
// here.
package davlock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

// Lock is one held lock. It is also the on-disk record, so the JSON tags are
// part of the file format.
type Lock struct {
	Token     string    `json:"token"`
	Root      string    `json:"root"`
	Duration  int64     `json:"duration_ns"`
	OwnerXML  string    `json:"owner_xml"`
	ZeroDepth bool      `json:"zero_depth"`
	Expiry    time.Time `json:"expiry"`

	// held is in-flight request state: webdav.LockSystem.Confirm claims a lock
	// for the duration of one request and releases it afterwards.
	//
	// ⚠ Deliberately NOT persisted. After a restart no request is in flight, so
	// a held flag read back from disk would mark a lock permanently claimed and
	// every later Confirm on it would fail — a durable lock that can never be
	// used again is worse than no durability.
	held bool `json:"-"`
}

func (l *Lock) details() webdav.LockDetails {
	return webdav.LockDetails{
		Root:      l.Root,
		Duration:  time.Duration(l.Duration),
		OwnerXML:  l.OwnerXML,
		ZeroDepth: l.ZeroDepth,
	}
}

// System is a durable webdav.LockSystem.
type System struct {
	path string

	mu    sync.Mutex
	locks map[string]*Lock // by token
}

var _ webdav.LockSystem = (*System)(nil)

// New opens (or creates) a lock system backed by `<dir>/dav-locks.json`.
//
// A missing or unreadable file is not an error: the locks are then simply
// empty, which is exactly what memLS gave and no worse. Refusing to start /dav
// because a cache file is corrupt would trade a small problem for an outage.
func New(dir string) (*System, error) {
	if dir == "" {
		return nil, fmt.Errorf("davlock: no directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("davlock: %w", err)
	}
	s := &System{path: filepath.Join(dir, "dav-locks.json"), locks: map[string]*Lock{}}
	s.load()
	return s, nil
}

// NewMemory returns a lock system that never touches the disk. For tests, and
// for a deployment that would rather have memLS's behaviour back.
func NewMemory() *System { return &System{locks: map[string]*Lock{}} }

// Len reports how many locks are held, for a status page or a test.
func (s *System) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.locks)
}

// ───────────────────────────── webdav.LockSystem ─────────────────────────────

// Confirm claims the locks that cover name0/name1 for one request.
func (s *System) Confirm(now time.Time, name0, name1 string, conditions ...webdav.Condition) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)

	var l0, l1 *Lock
	if name0 != "" {
		if l0 = s.lookup(slashClean(name0), conditions...); l0 == nil {
			return nil, webdav.ErrConfirmationFailed
		}
	}
	if name1 != "" {
		if l1 = s.lookup(slashClean(name1), conditions...); l1 == nil {
			return nil, webdav.ErrConfirmationFailed
		}
	}
	// One lock can cover both names; holding it twice would deadlock the
	// release.
	if l1 == l0 {
		l1 = nil
	}
	if l0 != nil {
		l0.held = true
	}
	if l1 != nil {
		l1.held = true
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if l1 != nil {
			l1.held = false
		}
		if l0 != nil {
			l0.held = false
		}
	}, nil
}

// Create takes a new lock, or refuses with ErrLocked (→ 423).
func (s *System) Create(now time.Time, details webdav.LockDetails) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)

	root := slashClean(details.Root)
	if !s.canCreate(root, details.ZeroDepth) {
		return "", webdav.ErrLocked
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	l := &Lock{
		Token:     token,
		Root:      root,
		Duration:  int64(details.Duration),
		OwnerXML:  details.OwnerXML,
		ZeroDepth: details.ZeroDepth,
	}
	if details.Duration >= 0 {
		l.Expiry = now.Add(details.Duration)
	}
	s.locks[token] = l
	s.save()
	return token, nil
}

// Refresh extends a lock's life.
func (s *System) Refresh(now time.Time, token string, duration time.Duration) (webdav.LockDetails, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)

	l := s.locks[token]
	if l == nil {
		return webdav.LockDetails{}, webdav.ErrNoSuchLock
	}
	if l.held {
		return webdav.LockDetails{}, webdav.ErrLocked
	}
	l.Duration = int64(duration)
	if duration >= 0 {
		l.Expiry = now.Add(duration)
	} else {
		l.Expiry = time.Time{}
	}
	s.save()
	return l.details(), nil
}

// Unlock releases a lock.
func (s *System) Unlock(now time.Time, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(now)

	l := s.locks[token]
	if l == nil {
		return webdav.ErrNoSuchLock
	}
	if l.held {
		return webdav.ErrLocked
	}
	delete(s.locks, token)
	s.save()
	return nil
}

// ───────────────────────────────── the rules ─────────────────────────────────

// lookup finds the lock that covers name and matches one of the conditions.
//
// The lock may be on an ANCESTOR of name: an infinite-depth lock on a
// collection locks everything under it, which is how a client locks a folder
// once and then writes twenty files inside it.
func (s *System) lookup(name string, conditions ...webdav.Condition) *Lock {
	// ⚠ Condition.Not and Condition.ETag are not evaluated, the same gap
	// x/net/webdav has. Every real client presents a plain token here; a
	// condition this does not understand simply fails to match, which refuses
	// the request rather than granting something unverified.
	for _, c := range conditions {
		l := s.locks[c.Token]
		if l == nil || l.held {
			continue
		}
		if name == l.Root {
			return l
		}
		if l.ZeroDepth {
			continue
		}
		if l.Root == "/" || strings.HasPrefix(name, l.Root+"/") {
			return l
		}
	}
	return nil
}

// canCreate reports whether a lock on name may be taken.
//
// Three ways it may not, and all three are about the tree rather than the one
// path: the exact path is locked; we want an infinite-depth lock and something
// BELOW is locked; or something ABOVE holds an infinite-depth lock.
func (s *System) canCreate(name string, zeroDepth bool) bool {
	for _, l := range s.locks {
		if l.Root == name {
			return false
		}
		if !zeroDepth && isDescendant(l.Root, name) {
			return false
		}
		if !l.ZeroDepth && isDescendant(name, l.Root) {
			return false
		}
	}
	return true
}

// isDescendant reports whether child is strictly below parent.
func isDescendant(child, parent string) bool {
	if parent == "/" {
		return child != "/"
	}
	return strings.HasPrefix(child, parent+"/")
}

// expire drops the locks whose time is up.
//
// ⚠ A HELD lock is never expired. It is claimed by a request that is running
// right now, and pulling it out from under that request is how a PUT ends up
// writing through a lock it was told it holds.
func (s *System) expire(now time.Time) {
	changed := false
	for token, l := range s.locks {
		if l.held || l.Duration < 0 {
			continue
		}
		if !l.Expiry.IsZero() && !now.Before(l.Expiry) {
			delete(s.locks, token)
			changed = true
		}
	}
	if changed {
		s.save()
	}
}

// ─────────────────────────────── persistence ────────────────────────────────

// save rewrites the lock file. Called with the mutex held.
//
// Best-effort by design: a lock that could not be written down is still a lock
// that works until the process ends, which is memLS's behaviour — so a failure
// here is logged and moved past rather than turned into a failed LOCK request.
func (s *System) save() {
	if s.path == "" {
		return
	}
	out := make([]*Lock, 0, len(s.locks))
	for _, l := range s.locks {
		out = append(out, l)
	}
	buf, err := json.Marshal(out)
	if err != nil {
		slog.Warn("davlock: cannot encode locks", slog.String("err", err.Error()))
		return
	}
	// Written to a temp file and renamed: a crash mid-write must not leave a
	// truncated file that loses every lock at the next boot.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		slog.Warn("davlock: cannot write locks", slog.String("err", err.Error()))
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		slog.Warn("davlock: cannot replace lock file", slog.String("err", err.Error()))
		_ = os.Remove(tmp)
	}
}

// load reads the lock file, dropping anything that expired while the process
// was down.
func (s *System) load() {
	buf, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var in []*Lock
	if err := json.Unmarshal(buf, &in); err != nil {
		slog.Warn("davlock: lock file unreadable, starting empty",
			slog.String("path", s.path), slog.String("err", err.Error()))
		return
	}
	now := time.Now()
	kept := 0
	for _, l := range in {
		if l == nil || l.Token == "" {
			continue
		}
		if l.Duration >= 0 && !l.Expiry.IsZero() && !now.Before(l.Expiry) {
			continue
		}
		l.held = false
		s.locks[l.Token] = l
		kept++
	}
	if kept > 0 {
		slog.Info("davlock: restored WebDAV locks", slog.Int("count", kept))
	}
}

// newToken returns an opaquelocktoken URI.
//
// ⚠ A URI, not a counter. RFC 4918 §6.5 requires the token to be an absolute
// URI, and memLS returns a plain incrementing integer — which also means two
// runs hand out the same tokens for different locks. Persisting the counter's
// output would make that collision permanent instead of transient.
func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("davlock: token: %w", err)
	}
	return "opaquelocktoken:" + hex.EncodeToString(b[:]), nil
}

// slashClean is x/net/webdav's own path normaliser, reproduced because it is
// unexported there: the lock tree and the request path have to agree on what a
// name looks like.
func slashClean(name string) string {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}
	return filepath.ToSlash(filepath.Clean(name))
}

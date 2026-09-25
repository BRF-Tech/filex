package filesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── one engine per pair on this computer: the pair lock ─────────────────
//
// Everything a pair remembers — its baseline, its hold, its local trash — is
// a file under the store, and the engine reads it at the start of a pass and
// writes it at the end. Two engines on the same pair at once each plan from
// the baseline the other is about to overwrite: one's uploads look like
// "changed on the server" to the other, one's local trash looks like "deleted
// here", and the checkpoint of the slower one puts back rows the faster one
// had settled. Nothing fails; files are uploaded twice, conflict copies
// appear, and a delete can travel to the side that did not ask for it.
//
// Two engines on one pair used to be a matter of how the app was run: the
// desktop app keeps one watcher per account, and `filex sync run` in a
// terminal reads the same ~/.filex/sync. Since the Microsoft Store build it is
// ordinary: the Store (MSIX) copy reads the real desktop state and so signs in
// with the same accounts as an installed (NSIS) copy, and the desktop app's
// single-instance lock lives in its userData — which MSIX virtualises — so
// the two copies do not see each other and both sync every pair.
//
// So every pass holds an OPERATING-SYSTEM lock on its pair's file under
// locks/ (LockFileEx on Windows, flock elsewhere):
//
//   - The lock belongs to the process, not to a file on disk: when the process
//     ends — however it ends, a kill included — the OS lets go of it. There is
//     no stale lock to clean up and no PID to second-guess (a PID file would
//     be "stale" exactly when a crash leaves it, and PIDs are reused).
//   - The lock FILE stays. Deleting it on release would let a third process
//     create a fresh file and lock that while a second one holds the old,
//     unlinked one — two holders of "the" lock.
//   - A pass that finds the pair locked does nothing at all and says so with a
//     *BusyError (errors.Is ErrPairBusy). `filex sync run --watch` holds its
//     pairs' locks for as long as it syncs them and retries a busy pair (see
//     cmd/filex synclive.go); a one-shot run skips it and exits with its own
//     status.
//
// The file also carries who holds it (process id, executable), written after
// the lock is taken, so "busy" can say which program to look for. That is
// information only; the lock is the only thing that decides.

// ErrPairBusy is what a pass returns when another process on this computer
// holds the pair: nothing was read or written.
var ErrPairBusy = errors.New("another filex on this computer is syncing this pair")

// BusyError is ErrPairBusy for one pair, with what the lock file says about
// its holder.
type BusyError struct {
	Pair   string
	Holder LockHolder
}

func (e *BusyError) Error() string {
	if h := e.Holder.String(); h != "" {
		return ErrPairBusy.Error() + " (" + h + ")"
	}
	return ErrPairBusy.Error()
}

func (e *BusyError) Is(target error) bool { return target == ErrPairBusy }

// LockHolder is who took a pair's lock, as that process wrote it down.
type LockHolder struct {
	PID   int       `json:"pid"`
	Exe   string    `json:"exe,omitempty"`
	Since time.Time `json:"since"`
}

// String is "process 1234, C:\…\filex.exe", or "" when nothing is known.
func (h LockHolder) String() string {
	var parts []string
	if h.PID > 0 {
		parts = append(parts, fmt.Sprintf("process %d", h.PID))
	}
	if h.Exe != "" {
		parts = append(parts, h.Exe)
	}
	return strings.Join(parts, ", ")
}

// PairLock is a held pair lock. Release gives it back; the operating system
// does the same when the process ends.
type PairLock struct {
	PairID string
	dir    string // the store directory it was taken in

	mu       sync.Mutex
	f        *os.File // nil once released, or when the file system cannot lock
	released bool
}

// errLockUnsupported marks a file system that cannot lock at all (some network
// mounts answer ENOLCK / ERROR_NOT_SUPPORTED). See LockPair.
var errLockUnsupported = errors.New("file locking is not supported here")

// lockPath is locks/<pair-id>.lock.
func (s *Store) lockPath(id string) string {
	return s.path("locks", id+".lock")
}

// LockPair takes the pair's lock without waiting. A pair another process holds
// is a *BusyError (errors.Is ErrPairBusy).
//
// ⚠ The same process asking twice is refused too — the lock is per open file,
// not per process — which is what makes a second lock taken by mistake (an
// engine that did not get its caller's) fail loudly instead of passing.
//
// ⚠ A file system that cannot lock at all is not "busy": refusing there would
// stop sync for good on a home directory mounted from a server without a lock
// daemon. The lock is then held in name only, exactly the engine as it was
// before locks, and Unenforced says so.
func (s *Store) LockPair(id string) (*PairLock, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return nil, fmt.Errorf("pair id %q cannot name a lock file", id)
	}
	path := s.lockPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("sync lock for %s: %w", id, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sync lock for %s: %w", id, err)
	}
	switch err := lockFile(f); {
	case err == nil:
	case errors.Is(err, ErrPairBusy):
		f.Close()
		return nil, &BusyError{Pair: id, Holder: readHolder(path)}
	case errors.Is(err, errLockUnsupported):
		f.Close()
		return &PairLock{PairID: id, dir: s.Dir}, nil
	default:
		f.Close()
		return nil, fmt.Errorf("sync lock for %s: %w", id, err)
	}
	l := &PairLock{PairID: id, dir: s.Dir, f: f}
	l.writeHolder()
	return l, nil
}

// Unenforced reports a lock taken on a file system that cannot lock: nothing
// stops a second engine on this pair.
func (l *PairLock) Unenforced() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f == nil && !l.released
}

// Release gives the lock back. Safe to call more than once.
func (l *PairLock) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	if l.f == nil {
		return
	}
	// The holder note goes first: a reader that finds the file unlocked must
	// not name a process that has let go.
	_ = l.f.Truncate(0)
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}

// covers reports whether this lock is the one a pass of pair id in store s
// needs — taken for that pair, in that store, and still held.
func (l *PairLock) covers(s *Store, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.released:
		return fmt.Errorf("sync lock for %s was already released", l.PairID)
	case l.PairID != id || filepath.Clean(l.dir) != filepath.Clean(s.Dir):
		return fmt.Errorf("sync lock for %s does not cover pair %s", l.PairID, id)
	}
	return nil
}

// writeHolder records this process in the lock file. Best effort: the lock is
// already held, and a note that could not be written only makes "busy" less
// specific.
func (l *PairLock) writeHolder() {
	h := LockHolder{PID: os.Getpid(), Since: time.Now().UTC().Truncate(time.Second)}
	if exe, err := os.Executable(); err == nil {
		h.Exe = exe
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	if err := l.f.Truncate(0); err != nil {
		return
	}
	_, _ = l.f.WriteAt(raw, 0)
}

// readHolder reads what the holder wrote; a note being written or never
// written reads as "unknown".
func readHolder(path string) LockHolder {
	raw, err := os.ReadFile(path)
	if err != nil {
		return LockHolder{}
	}
	var h LockHolder
	if json.Unmarshal(raw, &h) != nil {
		return LockHolder{}
	}
	return h
}

// holdLock makes sure a pass runs under its pair's lock: the caller's
// (Engine.Lock), or one taken here and given back by the returned func.
//
// Nested passes (RunDirs falling back to Run) find the outer pass's lock in
// e.Lock and take nothing.
func (e *Engine) holdLock() (func(), error) {
	if e.Lock != nil {
		if err := e.Lock.covers(e.Store, e.Pair.ID); err != nil {
			return nil, err
		}
		return func() {}, nil
	}
	lk, err := e.Store.LockPair(e.Pair.ID)
	if err != nil {
		return nil, err
	}
	e.Lock = lk
	return func() {
		e.Lock = nil
		lk.Release()
	}, nil
}

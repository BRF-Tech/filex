// Package vaultlock is the vault's write lock (encryption level 3,
// docs/E2E-VAULT-FORMAT.md → The write lock): at most one session writes a
// vault, and the server is what says which.
//
// The rules, all decided here and nowhere else:
//
//   - a lock belongs to the key file's vault.id within its tenant (Key), not to
//     a path: renaming the vault folder keeps it, and a copy of a vault made on
//     the server shares it;
//   - it is HELD while the lease has not run out (60 seconds, renewed by the
//     heartbeat and by every vault write) AND the holder is not idle (a vault
//     write or an active renewal within the person's idle time, 1 to 10
//     minutes, 3 by default, read when the lock is taken);
//   - it is free again the moment it ends - released, run out, idle or broken -
//     UNLESS an index write of its holder is still running: then it is free
//     when that write ends, or 60 seconds after it began;
//   - the token is 32 random bytes, handed out once, kept only as its SHA-256.
//
// The state lives in the database (vault_locks, migration 00096), not in this
// process: filex can run more than one process on one database, and every
// decision below reads the row and writes it back by compare-and-set on its
// revision. Nothing is cached. The clock and the random source can be
// replaced, so a test can walk a lock through a lease without waiting.
//
// A lock that ends because time ran out has nobody to say so: its row is
// closed - and OnEnded told, for the audit row and the realtime event - by
// the next call that finds it ended (a renewal, a take, a state read).
package vaultlock

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The format's numbers (docs/E2E-VAULT-FORMAT.md → Lock semantics).
const (
	Lease              = 60 * time.Second
	Heartbeat          = 15 * time.Second
	IndexWriteMax      = 60 * time.Second
	DefaultIdleMinutes = 3
	MinIdleMinutes     = 1
	MaxIdleMinutes     = 10
	// TokenBytes is the size of a lock token before encoding.
	TokenBytes = 32
)

// Why a lock ended - the `reason` of VAULT_LOCK_LOST and of the vault.unlock
// audit row. Taken is said only to the holder of a lost lock that somebody
// else holds now.
const (
	ReasonReleased = "released"
	ReasonExpired  = "expired"
	ReasonIdle     = "idle"
	ReasonBroken   = "broken"
	ReasonTaken    = "taken"
	// ReasonLockedIdle is what a client adds when it locked the vault itself
	// after 15 minutes of nothing (docs/E2E-VAULT-FORMAT.md → The idle lock)
	// while it held the write lock: a release, recorded with this reason.
	ReasonLockedIdle = "locked_idle"
)

// Clients a lock may name.
var Clients = map[string]bool{"web": true, "desktop": true, "cli": true, "mount": true}

// Store is what the service needs of the database (db.Store has it,
// VaultLockSQL).
type Store interface {
	GetVaultLock(ctx context.Context, tenantID int64, vaultID string) (*model.VaultLock, error)
	InsertVaultLock(ctx context.Context, l *model.VaultLock) (bool, error)
	UpdateVaultLock(ctx context.Context, l *model.VaultLock, rev int64) (bool, error)
	GetVaultIdleMinutes(ctx context.Context, userID int64) (int, error)
	SetVaultIdleMinutes(ctx context.Context, userID int64, minutes int) error
}

// Key names one vault's lock: its tenant and its id (32 lower-case hex).
type Key struct {
	Tenant int64
	Vault  string
}

// Holder is who holds a lock: the person, the kind of client and its label.
type Holder struct {
	UserID int64
	Name   string
	Client string
	Label  string
}

// Place is where a lock was taken: the vault folder its holder named.
type Place struct {
	StorageID int64
	Path      string
}

// Ended is a lock that ended, told once to OnEnded by the call that closed
// its row.
type Ended struct {
	Key
	Place
	Holder Holder
	// Reason: released, expired, idle, broken or locked_idle.
	Reason string
	// By is who broke the lock (Reason broken); zero otherwise.
	By       Holder
	FirstGen int64
	LastGen  int64
	Since    time.Time
	At       time.Time
}

// Lock is a lock as State shows it.
type Lock struct {
	Holder Holder
	Since  time.Time
	// ExpiresAt is when it ends if nothing more happens: the lease or the
	// idle time, whichever comes first (or the end of a running index write).
	ExpiresAt time.Time
	tokenHash string
}

// Mine reports whether token is this lock's.
func (l *Lock) Mine(token string) bool {
	h, ok := HashToken(token)
	return l != nil && ok && h == l.tokenHash
}

// Taken is a lock just taken: Token is shown this once.
type Taken struct {
	Token        string
	LeaseSeconds int
	IdleSeconds  int
	ExpiresAt    time.Time
	IdleUntil    time.Time
}

// Renewed is a renewal's answer.
type Renewed struct {
	ExpiresAt time.Time
	IdleUntil time.Time
}

// ErrLocked: somebody else holds the lock (*LockedError says who).
var ErrLocked = errors.New("the vault is locked by another session")

// LockedError names who holds the lock, since when, and how long until it
// could be free.
type LockedError struct {
	Holder     Holder
	Since      time.Time
	RetryAfter time.Duration
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("the vault is being written by %s (%s) since %s", e.Holder.Name, e.Holder.Client, e.Since.UTC().Format(time.RFC3339))
}
func (e *LockedError) Unwrap() error { return ErrLocked }

// ErrLost: the token does not hold the lock (*LostError says why).
var ErrLost = errors.New("the vault's write lock was lost")

// LostError says why a token no longer holds its lock: expired, idle,
// broken, released or taken - and, for taken and broken, who: the session
// that holds it now, or the person who broke it (only a name: a break names
// no client).
type LostError struct {
	Reason string
	Holder *Holder
}

func (e *LostError) Error() string { return "the vault's write lock was lost: " + e.Reason }
func (e *LostError) Unwrap() error { return ErrLost }

// ErrIndexRunning refuses a second index write while one of the holder's is
// still running.
var ErrIndexRunning = errors.New("an index write of this lock is still running")

// ErrIdleRange refuses an idle time outside 1 to 10 minutes.
var ErrIdleRange = fmt.Errorf("the idle time is %d to %d minutes", MinIdleMinutes, MaxIdleMinutes)

// ErrContention is a row that kept changing under every attempt to write it.
var ErrContention = errors.New("the vault's lock row kept changing; try again")

// casTries bounds the compare-and-set loop: a lost race is retried on a fresh
// read, a row that changes under every one of them is ErrContention.
const casTries = 8

// Service takes, renews, releases and breaks vault locks.
type Service struct {
	store Store
	now   func() time.Time
	rand  io.Reader
	// OnEnded is told of every lock this service sees end, once, after the
	// row that records the ending was written. Nil = nobody.
	OnEnded func(ctx context.Context, e Ended)
}

// New builds a Service on store with the real clock and crypto/rand.
func New(store Store) *Service {
	return &Service{store: store, now: time.Now, rand: rand.Reader}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SetRand replaces the token source (tests).
func (s *Service) SetRand(r io.Reader) { s.rand = r }

// Now is the service's clock.
func (s *Service) Now() time.Time { return s.now() }

// HashToken is the stored form of a token: the hex SHA-256 of its 32 bytes.
// ok is false for a string that is not a token.
func HashToken(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != TokenBytes {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), true
}

func (s *Service) newToken() (token, hash string, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := io.ReadFull(s.rand, raw); err != nil {
		return "", "", fmt.Errorf("vault lock token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256(raw)
	return token, hex.EncodeToString(sum[:]), nil
}

func fromMs(v int64) time.Time { return time.UnixMilli(v) }

// endOf is when a held row's lock ends if nothing more happens, and why: the
// idle time or the lease, whichever comes first.
func endOf(l *model.VaultLock) (int64, string) {
	idleEnd := l.ActiveMs + l.IdleSeconds*1000
	if idleEnd <= l.LeaseMs {
		return idleEnd, ReasonIdle
	}
	return l.LeaseMs, ReasonExpired
}

// live reports whether a row's lock is held at now.
func live(l *model.VaultLock, now int64) bool {
	if l == nil || l.TokenHash == "" {
		return false
	}
	end, _ := endOf(l)
	return now < end
}

// indexEnd is when the running index write of a row is abandoned, 0 when
// none runs at now.
func indexEnd(l *model.VaultLock, now int64) int64 {
	if l == nil || l.IndexStartedMs == 0 {
		return 0
	}
	end := l.IndexStartedMs + IndexWriteMax.Milliseconds()
	if now >= end {
		return 0
	}
	return end
}

func holderOf(l *model.VaultLock) Holder {
	return Holder{UserID: l.HolderUserID, Name: l.HolderName, Client: l.HolderClient, Label: l.HolderLabel}
}

// closeRow records in l that its lock ended, at `at`, for reason, and returns
// the record OnEnded is told.
func closeRow(l *model.VaultLock, reason string, at int64) Ended {
	e := Ended{
		Key:      Key{Tenant: l.TenantID, Vault: l.VaultID},
		Place:    Place{StorageID: l.StorageID, Path: l.Path},
		Holder:   holderOf(l),
		Reason:   reason,
		FirstGen: l.FirstGen,
		LastGen:  l.LastGen,
		Since:    fromMs(l.TakenMs),
		At:       fromMs(at),
	}
	l.EndedTokenHash = l.TokenHash
	l.EndedReason = reason
	l.EndedBy = ""
	l.EndedMs = at
	l.TokenHash = ""
	return e
}

func lockedBy(l *model.VaultLock, until int64, now int64) *LockedError {
	wait := time.Duration(until-now) * time.Millisecond
	if wait < time.Second {
		wait = time.Second
	}
	return &LockedError{Holder: holderOf(l), Since: fromMs(l.TakenMs), RetryAfter: wait}
}

// lostFor is what the holder of a token that does not hold the lock is
// told: taken - and by whom - when somebody else holds it now, else how the
// token's own lock ended (who broke it, for a break), else expired.
func lostFor(l *model.VaultLock, hash string, now int64) *LostError {
	if live(l, now) && l.TokenHash != hash {
		h := holderOf(l)
		return &LostError{Reason: ReasonTaken, Holder: &h}
	}
	if hash != "" && l.EndedTokenHash == hash && l.EndedReason != "" {
		switch l.EndedReason {
		case ReasonLockedIdle:
			return &LostError{Reason: ReasonReleased}
		case ReasonBroken:
			return &LostError{Reason: ReasonBroken, Holder: &Holder{Name: l.EndedBy}}
		}
		return &LostError{Reason: l.EndedReason}
	}
	if l.TokenHash != "" && l.TokenHash != hash {
		h := holderOf(l)
		return &LostError{Reason: ReasonTaken, Holder: &h}
	}
	return &LostError{Reason: ReasonExpired}
}

// step is one attempt's decision about a vault's row: what to write (nothing
// when write is false), the locks the write closes, and the error to answer.
type step func(l *model.VaultLock, exists bool, now int64) (write bool, ended []Ended, err error)

// mutate reads k's row, hands a copy of it to fn, and writes back what fn
// changed by compare-and-set - reading again and asking fn again when another
// writer came first. It returns the row as fn left it (nil when there is none
// and fn wrote none) and fn's error. OnEnded hears of every lock the winning
// write closed.
func (s *Service) mutate(ctx context.Context, k Key, fn step) (*model.VaultLock, error) {
	for i := 0; i < casTries; i++ {
		now := s.now().UnixMilli()
		row, err := s.store.GetVaultLock(ctx, k.Tenant, k.Vault)
		if err != nil {
			return nil, err
		}
		exists := row != nil
		work := model.VaultLock{TenantID: k.Tenant, VaultID: k.Vault}
		if exists {
			work = *row
		}
		write, ended, ferr := fn(&work, exists, now)
		if !write {
			if !exists {
				return nil, ferr
			}
			return &work, ferr
		}
		var ok bool
		if exists {
			ok, err = s.store.UpdateVaultLock(ctx, &work, row.Rev)
		} else {
			ok, err = s.store.InsertVaultLock(ctx, &work)
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if s.OnEnded != nil {
			for _, e := range ended {
				s.OnEnded(ctx, e)
			}
		}
		return &work, ferr
	}
	return nil, ErrContention
}

// IdleMinutes is the idle time of a person: what they set, else 3.
func (s *Service) IdleMinutes(ctx context.Context, userID int64) (int, error) {
	n, err := s.store.GetVaultIdleMinutes(ctx, userID)
	if err != nil {
		return 0, err
	}
	if n < MinIdleMinutes || n > MaxIdleMinutes {
		return DefaultIdleMinutes, nil
	}
	return n, nil
}

// SetIdleMinutes stores a person's idle time: ErrIdleRange outside 1 to 10.
// A lock already held keeps the idle time it was taken with.
func (s *Service) SetIdleMinutes(ctx context.Context, userID int64, minutes int) error {
	if minutes < MinIdleMinutes || minutes > MaxIdleMinutes {
		return ErrIdleRange
	}
	return s.store.SetVaultIdleMinutes(ctx, userID, minutes)
}

// Take gives the lock of k to a new session of h, at p: a *LockedError when
// it is held, or not free yet because its holder's index write still runs.
// A lock that ended without anybody closing its row is closed here first.
func (s *Service) Take(ctx context.Context, k Key, p Place, h Holder) (*Taken, error) {
	idle, err := s.IdleMinutes(ctx, h.UserID)
	if err != nil {
		return nil, err
	}
	idleSeconds := int64(idle) * 60
	token, hash, err := s.newToken()
	if err != nil {
		return nil, err
	}
	var taken *Taken
	_, err = s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		var ended []Ended
		if exists && l.TokenHash != "" {
			end, reason := endOf(l)
			if now < end {
				return false, nil, lockedBy(l, end, now)
			}
			if ie := indexEnd(l, now); ie != 0 {
				return false, nil, lockedBy(l, ie, now)
			}
			ended = append(ended, closeRow(l, reason, end))
		} else if ie := indexEnd(l, now); exists && ie != 0 {
			// Released or broken while its index write still runs.
			return false, nil, lockedBy(l, ie, now)
		}
		l.TokenHash = hash
		l.HolderUserID, l.HolderName, l.HolderClient, l.HolderLabel = h.UserID, h.Name, h.Client, h.Label
		l.StorageID, l.Path = p.StorageID, p.Path
		l.TakenMs, l.ActiveMs = now, now
		l.LeaseMs = now + Lease.Milliseconds()
		l.IdleSeconds = idleSeconds
		l.IndexStartedMs = 0
		l.FirstGen, l.LastGen = 0, 0
		taken = &Taken{
			Token:        token,
			LeaseSeconds: int(Lease / time.Second),
			IdleSeconds:  int(idleSeconds),
			ExpiresAt:    fromMs(l.LeaseMs),
			IdleUntil:    fromMs(now + idleSeconds*1000),
		}
		return true, ended, nil
	})
	if err != nil {
		return nil, err
	}
	return taken, nil
}

// held is the common first half of every call a holder makes: the row must
// hold token's lock now. A lock that ended is closed (write true) and its
// holder told why; any other token is told why it does not hold it.
func held(l *model.VaultLock, exists bool, hash string, now int64) (write bool, ended []Ended, err error) {
	if !exists || hash == "" {
		return false, nil, &LostError{Reason: ReasonExpired}
	}
	if l.TokenHash != hash {
		return false, nil, lostFor(l, hash, now)
	}
	end, reason := endOf(l)
	if now >= end {
		return true, []Ended{closeRow(l, reason, end)}, &LostError{Reason: reason}
	}
	return false, nil, nil
}

// Renew renews token's lock: the lease starts again, and active also counts
// as activity (the holder is about to write). A *LostError when the token does
// not hold the lock any more, with the reason.
func (s *Service) Renew(ctx context.Context, k Key, token string, active bool) (*Renewed, error) {
	hash, _ := HashToken(token)
	var out *Renewed
	_, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		if write, ended, err := held(l, exists, hash, now); err != nil || write {
			return write, ended, err
		}
		l.LeaseMs = now + Lease.Milliseconds()
		if active {
			l.ActiveMs = now
		}
		out = &Renewed{ExpiresAt: fromMs(l.LeaseMs), IdleUntil: fromMs(l.ActiveMs + l.IdleSeconds*1000)}
		return true, nil, nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Touch is a vault write by token's holder (a pack stored, files deleted): it
// must hold the lock, and the write renews the lease and counts as activity.
func (s *Service) Touch(ctx context.Context, k Key, token string) error {
	_, err := s.Renew(ctx, k, token, true)
	return err
}

// Release ends token's lock, if its row still records it: reason released,
// or locked_idle when the client says it locked the vault itself after 15
// minutes of nothing (docs/E2E-VAULT-FORMAT.md → The idle lock) - recorded as
// said, since that client's write lock has usually ended for idle minutes
// before and nobody closed the row yet. Any other reason reads as released;
// a plain release of a lock that had already run out is recorded with the
// reason it ended for. Releasing a lock the token does not hold does nothing.
func (s *Service) Release(ctx context.Context, k Key, token, reason string) error {
	hash, ok := HashToken(token)
	if !ok {
		return nil
	}
	_, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		if !exists || l.TokenHash != hash {
			return false, nil, nil
		}
		end, why := endOf(l)
		at := now
		switch {
		case reason == ReasonLockedIdle:
			why = ReasonLockedIdle
		case now >= end:
			at = end
		default:
			why = ReasonReleased
		}
		return true, []Ended{closeRow(l, why, at)}, nil
	})
	return err
}

// Break ends whatever lock k has now, for by (the handler decides who may).
// broken is the lock that was broken - nil when none was held (a lock that
// had already run out is closed with its own reason, not broken). Its
// holder's next call is told `broken`, and by whom.
func (s *Service) Break(ctx context.Context, k Key, by Holder) (*Ended, error) {
	var broken *Ended
	_, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		broken = nil
		if !exists || l.TokenHash == "" {
			return false, nil, nil
		}
		end, why := endOf(l)
		if now >= end {
			return true, []Ended{closeRow(l, why, end)}, nil
		}
		e := closeRow(l, ReasonBroken, now)
		e.By = by
		l.EndedBy = by.Name
		broken = &e
		return true, []Ended{e}, nil
	})
	if err != nil {
		return nil, err
	}
	return broken, nil
}

// BeginIndex records that token's holder starts an index write: it must hold
// the lock, no other index write of it may be running (ErrIndexRunning), and
// the write renews the lease and counts as activity. From here until
// EndIndex - or 60 seconds - the lock is not free, even if it ends.
func (s *Service) BeginIndex(ctx context.Context, k Key, token string) error {
	hash, _ := HashToken(token)
	_, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		if write, ended, err := held(l, exists, hash, now); err != nil || write {
			return write, ended, err
		}
		if indexEnd(l, now) != 0 {
			return false, nil, ErrIndexRunning
		}
		l.IndexStartedMs = now
		l.LeaseMs = now + Lease.Milliseconds()
		l.ActiveMs = now
		return true, nil, nil
	})
	return err
}

// EndIndex records the end of token's index write: committed says whether
// generation gen is now in place (the first and last generation of the lock
// follow it). The lock it was written under may have ended meanwhile; the
// record is kept all the same, and a lock still held is renewed by it.
func (s *Service) EndIndex(ctx context.Context, k Key, token string, gen int64, committed bool) error {
	hash, ok := HashToken(token)
	if !ok {
		return nil
	}
	_, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		if !exists || l.IndexStartedMs == 0 || (l.TokenHash != hash && l.EndedTokenHash != hash) {
			return false, nil, nil
		}
		l.IndexStartedMs = 0
		if committed {
			if l.FirstGen == 0 {
				l.FirstGen = gen
			}
			l.LastGen = gen
		}
		if l.TokenHash == hash && live(l, now) {
			l.LeaseMs = now + Lease.Milliseconds()
			l.ActiveMs = now
		}
		return true, nil, nil
	})
	return err
}

// State is k's lock as everybody may see it: nil when the vault is free. A
// lock found ended is closed here (OnEnded is told). A lock that ended while
// its holder's index write still runs is shown until that write ends.
func (s *Service) State(ctx context.Context, k Key) (*Lock, error) {
	row, err := s.mutate(ctx, k, func(l *model.VaultLock, exists bool, now int64) (bool, []Ended, error) {
		if !exists || l.TokenHash == "" || live(l, now) || indexEnd(l, now) != 0 {
			return false, nil, nil
		}
		end, why := endOf(l)
		return true, []Ended{closeRow(l, why, end)}, nil
	})
	if err != nil || row == nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	switch {
	case live(row, now):
		end, _ := endOf(row)
		if ie := indexEnd(row, now); ie > end {
			end = ie
		}
		return &Lock{Holder: holderOf(row), Since: fromMs(row.TakenMs), ExpiresAt: fromMs(end), tokenHash: row.TokenHash}, nil
	case indexEnd(row, now) != 0:
		hash := row.TokenHash
		if hash == "" {
			hash = row.EndedTokenHash
		}
		return &Lock{Holder: holderOf(row), Since: fromMs(row.TakenMs), ExpiresAt: fromMs(indexEnd(row, now)), tokenHash: hash}, nil
	}
	return nil, nil
}

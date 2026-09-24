package share

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// ── THE PIN GATE FOR EVERY PUBLIC LINK ─────────────────────────────────
//
// One implementation, here, for /s/, /d/ and an app plugin's public page —
// because until v3 there were two. The app-plugin table (00043) had the
// five-strikes-then-ten-minutes lock and the signed unlock cookie; the
// download and file-request links, which are the ones strangers actually
// receive, had neither: a four-digit PIN on a /s/ link could be walked
// through at the speed of HTTP with nothing counting.
//
// Both halves now live on the share row (migration 00046) and are reached
// only through this file.

const (
	// pinMaxFails strikes inside one lock window.
	pinMaxFails = 5
	// pinLockFor is how long the gate stays shut after the last strike.
	pinLockFor = 10 * time.Minute
	// unlockTTL is how long a browser that answered the PIN stays unlocked.
	unlockTTL = 12 * time.Hour
)

// PIN gate errors.
var (
	// ErrPinRequired means the link has a PIN and none was supplied.
	ErrPinRequired = errors.New("share: pin required")
	// ErrLocked means too many wrong PINs; the gate is shut for a while.
	ErrLocked = errors.New("share: too many wrong pins")
)

// AttachSecret gives the unlock cookie its signing key — the instance secret
// (FILEX_SECRET_KEY). Without it a per-process random key is used, which is
// correct but logs every visitor out when the server restarts and cannot work
// across two instances behind one address.
//
// ⚠ Since 00049 the SAME key also seals the PIN itself (Create → pin_enc,
// RevealPIN below). Deliberately the same key and the same box as the rest of
// filex: internal/secretbox is the instance's one recoverable-secret scheme,
// and a second key here would be a second thing to lose.
func (s *Service) AttachSecret(key string) {
	s.secret = strings.TrimSpace(key)
	if box, err := secretbox.New(s.secret); err == nil {
		s.box = box
	}
}

// ── SHOWING A PIN BACK ─────────────────────────────────────────────────
//
// Owner's decision, 2026-09-20: *"paylaşımın sahibi ve admin alabilir
// şifreyi."* WHO is asking is the handler's question
// (api/handlers/shares_mine.go, which also writes the audit row); what CAN be
// answered is this file's.

// Reasons a PIN cannot be shown. They are separate errors because the sentence
// a person has to read differs for each, and one "not available" for all three
// would send an operator looking for the wrong fault.
var (
	// ErrNoPIN means the link has no PIN at all — there is nothing to show.
	ErrNoPIN = errors.New("share: link has no pin")
	// ErrPinNotRecoverable means THIS link's PIN was never sealed: it was
	// minted before 00049, or while the instance had no key, or it was sealed
	// under a key that has since been rotated away. The link still works; its
	// PIN is simply gone, exactly as it was for every link before this.
	ErrPinNotRecoverable = errors.New("share: pin was not stored recoverably")
	// ErrNoSecretKey means this INSTANCE has no FILEX_SECRET_KEY, so no PIN it
	// mints can ever be shown again. An operator can fix this one; the two
	// above they cannot, and telling them apart is the whole point.
	ErrNoSecretKey = errors.New("share: no secret key configured")
)

// AuditActionPinReveal is the audit row every PIN read writes — success or
// refusal-after-authorization alike, from the "My shares" endpoint and from
// an app plugin's share_pin alike. One spelling, so an operator grepping the
// audit table finds every reveal on the instance.
const AuditActionPinReveal = "share.pin_revealed"

// RevealPIN returns a link's PIN in plain, or says why it cannot.
//
// ⚠ This function performs NO authorization. It is called from the one
// endpoint that has already decided the caller is the creator or an admin and
// that writes the audit row whatever the answer is. Keeping the two apart is
// deliberate: a permission check buried inside a decrypt helper is one the
// next caller of that helper silently inherits — or silently skips.
func (s *Service) RevealPIN(sh *model.Share) (string, error) {
	if sh == nil || sh.PinHash == "" {
		return "", ErrNoPIN
	}
	if sh.PinEnc == "" {
		// Nothing sealed. WHICH of the two reasons that is depends on whether
		// this instance could have sealed anything at all.
		if !s.box.Enabled() {
			return "", ErrNoSecretKey
		}
		return "", ErrPinNotRecoverable
	}
	pin, err := s.box.Open(sh.PinEnc)
	switch {
	case errors.Is(err, secretbox.ErrNoKey):
		// The row IS sealed and this process has no key: it was removed, or
		// this is a restore onto a host that never had it.
		return "", ErrNoSecretKey
	case err != nil:
		// ErrCorrupt — truncated, or sealed under a rotated key. Either way
		// this particular PIN is unreadable forever, which is exactly what
		// "cannot be shown" already says to the person reading it.
		return "", ErrPinNotRecoverable
	}
	return pin, nil
}

// CheckPIN verifies a PIN against a link, counting the misses.
//
// ErrLocked while the gate is shut, ErrBadPIN on a miss (counted), nil on a
// hit (counter reset). A link with no PIN always passes.
//
// ⚠ The lock is checked BEFORE the hash comparison and the RIGHT PIN during a
// lock is refused too: a lock the correct answer lifts is no lock at all
// against somebody working through the space — they would simply find it.
//
// ⚠ The counters are written through UpdateSharePinLock, which touches only
// those two columns. A whole-row save here would let a wrong guess carry a
// stale expiry, cap or exposed-file list back into the database.
func (s *Service) CheckPIN(ctx context.Context, sh *model.Share, pin string) error {
	if sh == nil {
		return errors.New("share: no link")
	}
	if sh.PinHash == "" {
		return nil
	}
	now := time.Now()
	if sh.PinLocked(now) {
		return ErrLocked
	}
	if err := bcrypt.CompareHashAndPassword([]byte(sh.PinHash), []byte(strings.TrimSpace(pin))); err != nil {
		sh.PinFails++
		if sh.PinFails >= pinMaxFails {
			until := now.Add(pinLockFor)
			sh.LockedUntil, sh.PinFails = &until, 0
			_ = s.store.UpdateSharePinLock(ctx, sh.ID, 0, &until)
			return ErrLocked
		}
		_ = s.store.UpdateSharePinLock(ctx, sh.ID, sh.PinFails, nil)
		return ErrBadPIN
	}
	if sh.PinFails != 0 || sh.LockedUntil != nil {
		sh.PinFails, sh.LockedUntil = 0, nil
		_ = s.store.UpdateSharePinLock(ctx, sh.ID, 0, nil)
	}
	return nil
}

// ── the unlock cookie ──────────────────────────────────────────────────
//
// HMAC(secret, sha256(token) | expiry). It proves the PIN was entered on THIS
// browser, carries no PIN, is bound to one link and expires on its own. The
// value the cookie is bound to is the token's hash rather than the token, so a
// cookie recovered from a log cannot be turned back into a link.

// TokenHash is the value an unlock cookie is bound to.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(h[:])
}

// CookieName is the per-link cookie name, so unlocking one link never unlocks
// another and a browser can hold several at once.
func CookieName(token string) string { return "fxp_" + TokenHash(token)[:16] }

// UnlockTTL is how long a minted unlock stays valid (for the cookie's MaxAge).
func UnlockTTL() time.Duration { return unlockTTL }

var (
	fallbackOnce sync.Once
	fallbackKey  []byte
)

func (s *Service) cookieKey() []byte {
	if s != nil && s.secret != "" {
		return []byte(s.secret)
	}
	fallbackOnce.Do(func() {
		fallbackKey = make([]byte, 32)
		_, _ = rand.Read(fallbackKey)
	})
	return fallbackKey
}

// MintUnlock returns the cookie value proving this browser answered the PIN.
func (s *Service) MintUnlock(token string) string {
	th := TokenHash(token)
	exp := strconv.FormatInt(time.Now().Add(unlockTTL).Unix(), 10)
	mac := hmac.New(sha256.New, s.cookieKey())
	mac.Write([]byte(th + "|" + exp))
	return exp + "." + hex.EncodeToString(mac.Sum(nil))
}

// VerifyUnlock checks a cookie value against a link.
func (s *Service) VerifyUnlock(token, value string) bool {
	exp, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	mac := hmac.New(sha256.New, s.cookieKey())
	mac.Write([]byte(TokenHash(token) + "|" + exp))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

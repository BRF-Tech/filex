package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Sentinel errors. Callers match on these; the wrapped text is what a person
// reads, and the API boundary is where it gets translated.
var (
	ErrInvalidUsername  = errors.New("invalid username")
	ErrReservedUsername = errors.New("reserved username")
	ErrNotFound         = errors.New("no such account")
)

// Lookup is the slice of db.Store this package needs to resolve an identifier.
// Declared here rather than imported so identity stays a leaf package that the
// db drivers, the auth drivers and every protocol server can all depend on.
type Lookup interface {
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

// Claimer additionally assigns a username. Used by account creation and by the
// one-time backfill.
type Claimer interface {
	Lookup
	SetUserUsername(ctx context.Context, id int64, username string) error
}

// Resolve turns what a person typed into an account: e-mail or username, one
// interpretation each (see the package comment for why it never tries both).
//
// This is the ONLY function a login surface should call. The browser session,
// the desktop app, WebDAV Basic, FTPS, SFTP and the protocol credential pages
// all go through here, so "which identifiers work" has exactly one answer
// instead of one answer per surface.
//
// Returns ErrNotFound when there is no such account — including when the
// identifier is not even a well-formed username, because "no account" is the
// only thing a login surface may reveal about a failed lookup.
func Resolve(ctx context.Context, l Lookup, identifier string) (*model.User, error) {
	id := Normalize(identifier)
	if id == "" {
		return nil, ErrNotFound
	}

	var (
		u   *model.User
		err error
	)
	if LooksLikeEmail(id) {
		u, err = l.GetUserByEmail(ctx, id)
	} else {
		// A malformed username cannot match a row, so skip the query — but
		// report it as "not found" all the same. Telling a caller that their
		// input was structurally invalid would distinguish "you typed
		// nonsense" from "that account does not exist", and on a login form
		// those must look identical.
		if Validate(id) != nil {
			return nil, ErrNotFound
		}
		u, err = l.GetUserByUsername(ctx, id)
	}

	// Drivers disagree about how absence is reported: the row-scanning paths
	// return sql.ErrNoRows, the tenant-scoped ones return (nil, nil). Fold both
	// into ErrNotFound so no caller has to know which one it just used.
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	return u, nil
}

// Names reports whether the identifier addresses this account — as its e-mail
// or as its username, case-insensitively.
//
// It exists for the surfaces that have already resolved a user by some other
// route and now need to confirm the typed identifier belongs to them: the
// WebDAV credential cache (is the cached account still the one being named?)
// and the WebDAV API-token path (does the Basic username name the token's
// owner?). Comparing only the e-mail there would let a credential work in the
// browser and fail over the protocol, which is the per-surface drift this
// package exists to prevent.
func Names(u *model.User, identifier string) bool {
	if u == nil {
		return false
	}
	id := Normalize(identifier)
	if id == "" {
		return false
	}
	// An empty username must never match an empty-ish identifier, hence the
	// check above and the explicit non-empty guard here.
	return id == Normalize(u.Email) || (u.Username != "" && id == Normalize(u.Username))
}

// maxClaimAttempts bounds the collision loop. Reaching it means either a
// genuinely crowded name or a bug; either way, failing loudly beats spinning.
const maxClaimAttempts = 50

// EnsureUsername assigns a username to an account that has none, derived from
// its e-mail, and returns the name it claimed. It is idempotent: an account
// that already has one is left alone, and re-running the backfill after an
// interruption renames nobody.
//
// Uniqueness is settled here rather than in Suggest because only this function
// can see the database. The suffix is a plain counter appended to the derived
// name — deterministic given the same order of arrival, and recognisable to its
// owner, which a random string would not be.
func EnsureUsername(ctx context.Context, c Claimer, u *model.User) (string, error) {
	if u == nil {
		return "", ErrNotFound
	}
	if u.Username != "" {
		return u.Username, nil
	}

	base := Suggest(u.Email)
	for attempt := 0; attempt < maxClaimAttempts; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = withSuffix(base, attempt+1)
		}
		if Validate(candidate) != nil {
			// A reserved or otherwise unusable base still gets an account: the
			// suffix makes "admin" into "admin2", which is not reserved.
			continue
		}
		existing, err := c.GetUserByUsername(ctx, candidate)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if existing != nil && existing.ID != u.ID {
			continue
		}
		if err := c.SetUserUsername(ctx, u.ID, candidate); err != nil {
			// Losing the race to a concurrent creation lands here, because the
			// unique index — not this check — is the real guard. Try the next
			// candidate rather than failing the account creation.
			continue
		}
		u.Username = candidate
		return candidate, nil
	}
	return "", fmt.Errorf("%w: no free name derived from %q after %d attempts", ErrInvalidUsername, u.Email, maxClaimAttempts)
}

// withSuffix appends n to base, trimming base first so the result still fits
// MaxLen. Trimming the NAME rather than the number keeps the suffix visible —
// a truncated counter would silently collide with the candidate before it.
func withSuffix(base string, n int) string {
	suffix := strconv.Itoa(n)
	if len(base)+len(suffix) > MaxLen {
		base = base[:MaxLen-len(suffix)]
	}
	base = trimTrailingSeparators(base)
	if base == "" {
		base = "u"
	}
	return base + suffix
}

func trimTrailingSeparators(s string) string {
	for len(s) > 0 {
		switch s[len(s)-1] {
		case '.', '-', '_':
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

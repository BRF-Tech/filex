// Package identitystore guarantees that every account has a login name.
//
// # Why a store wrapper, and not a line in each caller
//
// Accounts are created from eight places: the admin UI, the grants "invite by
// e-mail" path, the CLI, first-run seeding, and the OIDC, LDAP and
// proxy-header drivers doing just-in-time provisioning. A username assigned in
// seven of them is worse than one assigned in none: the eighth account is the
// one that cannot log in over SFTP, and nothing about it looks different until
// someone tries.
//
// What all eight share is db.Store.CreateUser — an account cannot come into
// existence without it. Wrapping that one method is therefore both complete and
// future-proof: a creation path added next month gets a username on the day it
// is written, with no line of its own. This is the pattern internal/quotastore
// and internal/tenantstore already use, and for the same reason.
//
// ⚠ Apply the wrapper immediately above dbDrv.NewStore(), and in the test
// fixtures too. A single consumer holding the raw store creates accounts nobody
// named — which is exactly the class of bug quotastore was written to fix.
package identitystore

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Store decorates db.Store, naming accounts as they are created.
type Store struct {
	db.Store
}

// Ensure the decorator still satisfies the full interface.
var _ db.Store = (*Store)(nil)

// New wraps s.
func New(s db.Store) *Store { return &Store{Store: s} }

// CreateUser creates the account and claims a username derived from its
// e-mail.
//
// A failure to claim does NOT fail the creation. The account already exists by
// then, so returning an error would leave the caller looking at a failure it
// cannot retry (the e-mail is taken now) for a field that Backfill repairs on
// the next start. It is logged at error level because an unnamed account is a
// real defect — it just is not one worth refusing a login over.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error) {
	u, err := s.Store.CreateUser(ctx, email, passwordHash, role, locale, tz)
	if err != nil || u == nil {
		return u, err
	}
	if _, err := identity.EnsureUsername(ctx, s.Store, u); err != nil {
		slog.Error("identity: could not claim a username for a new account",
			slog.String("email", email), slog.Int64("user_id", u.ID), slog.Any("err", err))
	}
	return u, nil
}

// Backfill names every account that has none. It runs at start-up, after
// migrations, and is idempotent: an account that already has a username is
// skipped, and Suggest is deterministic, so an interrupted run resumed later
// renames nobody.
//
// It returns the number of accounts named. Errors on individual accounts are
// logged and skipped rather than aborting the run — one unnameable account must
// not stop the other nine hundred from being named.
func Backfill(ctx context.Context, s db.Store) (int, error) {
	users, err := s.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	named := 0
	for _, u := range users {
		if u.Username != "" {
			continue
		}
		name, err := identity.EnsureUsername(ctx, s, u)
		if err != nil {
			slog.Error("identity: backfill could not name an account",
				slog.Int64("user_id", u.ID), slog.String("email", u.Email), slog.Any("err", err))
			continue
		}
		slog.Info("identity: named an existing account",
			slog.Int64("user_id", u.ID), slog.String("username", name))
		named++
	}
	return named, nil
}

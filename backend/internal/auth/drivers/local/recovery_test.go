package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func mkUser(t *testing.T, store interface {
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
}, email, password, role string) *model.User {
	t.Helper()
	hash := ""
	if password != "" {
		var err error
		hash, err = HashPassword(password)
		require.NoError(t, err)
	}
	u, err := store.CreateUser(context.Background(), email, hash, role, "en", "")
	require.NoError(t, err)
	return u
}

// With password sign-in switched off, recovery lets the bootstrap
// administrator in — and only that account. Another administrator holding a
// perfectly good local password is refused exactly as if the driver were not
// there; that is what keeps "password sign-in is off" true for everyone else.
func TestRecoveryLogin_OnlyTheBootstrapAdministrator(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	boot := mkUser(t, store, "admin@local", "boot-pass-1234", model.RoleAdmin)
	other := mkUser(t, store, "second@local", "other-pass-1234", model.RoleAdmin)
	require.NoError(t, RecordBootstrapAdmin(ctx, store, boot.ID))

	r := NewRecoveryLogin(store)

	u, tok, err := r.Login(ctx, "admin@local", "boot-pass-1234")
	require.NoError(t, err)
	require.Equal(t, boot.ID, u.ID)
	require.NotEmpty(t, tok, "a recovery sign-in must hand out a session like any other")

	_, _, err = r.Login(ctx, "admin@local", "wrong")
	require.ErrorIs(t, err, auth.ErrUnauthorized, "the password is still checked")

	_, _, err = r.Login(ctx, other.Email, "other-pass-1234")
	require.ErrorIs(t, err, auth.ErrUnauthorized,
		"another administrator with a valid local password must not get in through recovery")

	_, _, err = r.Login(ctx, "nobody@local", "x")
	require.ErrorIs(t, err, auth.ErrUnauthorized)
}

// No recorded bootstrap administrator: recovery lets nobody in.
func TestRecoveryLogin_NobodyRecordedMeansNobody(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	mkUser(t, store, "admin@local", "boot-pass-1234", model.RoleAdmin)

	_, _, err := NewRecoveryLogin(store).Login(ctx, "admin@local", "boot-pass-1234")
	require.ErrorIs(t, err, auth.ErrUnauthorized)
}

// An installation created before the setting existed gets its bootstrap
// administrator recovered the way it was made: the oldest administrator that
// holds a local password. An account an identity provider created has no
// password and is passed over; a plain user is never picked.
func TestEnsureBootstrapAdmin_RecoversTheOldestAdministratorWithAPassword(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	sso := mkUser(t, store, "sso-admin@example.com", "", model.RoleAdmin)
	mkUser(t, store, "user@local", "user-pass-1234", model.RoleUser)
	first := mkUser(t, store, "admin@local", "boot-pass-1234", model.RoleAdmin)
	mkUser(t, store, "later@local", "later-pass-1234", model.RoleAdmin)
	require.Less(t, sso.ID, first.ID, "the SSO admin is older, which is the case being tested")

	got, err := EnsureBootstrapAdmin(ctx, store)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, first.ID, got.ID)

	id, err := BootstrapAdminID(ctx, store)
	require.NoError(t, err)
	require.Equal(t, first.ID, id, "and it is recorded, so the answer never changes afterwards")

	// A recorded value is left alone even when an older candidate appears to
	// qualify later on.
	require.NoError(t, RecordBootstrapAdmin(ctx, store, sso.ID))
	got, err = EnsureBootstrapAdmin(ctx, store)
	require.NoError(t, err)
	require.Equal(t, sso.ID, got.ID)
}

// A recorded bootstrap administrator that has since been deleted is not
// replaced by some other administrator: recovery then lets nobody in, which is
// what an operator who removed admin@local asked for.
func TestEnsureBootstrapAdmin_ADeletedAccountIsNotReplaced(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	gone := mkUser(t, store, "admin@local", "boot-pass-1234", model.RoleAdmin)
	other := mkUser(t, store, "second@local", "other-pass-1234", model.RoleAdmin)
	require.NoError(t, RecordBootstrapAdmin(ctx, store, gone.ID))
	require.NoError(t, store.DeleteUser(ctx, gone.ID))

	got, err := EnsureBootstrapAdmin(ctx, store)
	require.NoError(t, err)
	require.Nil(t, got)
	_, _, err = NewRecoveryLogin(store).Login(ctx, other.Email, "other-pass-1234")
	require.ErrorIs(t, err, auth.ErrUnauthorized)
}

package protocolauth_test

// The directory (LDAP/AD) password path for the non-HTTP protocols.
//
// ⚠ The gap these cover: a directory account is upserted into `users` with an
// EMPTY password_hash, because filex never learns the password. The local
// branch refuses an empty hash — correctly, for a local account — so before the
// Directory hook existed, WebDAV, SFTP, FTPS, S3 and NFS answered an AD user
// 401 with the same message a wrong password gets, on an account that could
// sign in to the web UI seconds earlier.

import (
	"context"
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// fakeDirectory stands in for the LDAP driver.
type fakeDirectory struct {
	// accepts maps identifier to the password the directory will bind.
	accepts map[string]string
	// resolves maps identifier to the local account returned on success.
	resolves map[string]*model.User
	// failWith, when set, is returned instead of a verdict — the directory
	// could not judge (unreachable, TLS, bad service bind).
	failWith error

	calls []string
}

func (f *fakeDirectory) VerifyPassword(_ context.Context, identifier, password string) (*model.User, error) {
	f.calls = append(f.calls, identifier)
	if f.failWith != nil {
		return nil, f.failWith
	}
	want, ok := f.accepts[identifier]
	if !ok || want != password {
		return nil, auth.ErrUnauthorized
	}
	return f.resolves[identifier], nil
}

// mkDirectoryUser creates the row the LDAP driver upserts: no password hash.
func mkDirectoryUser(t *testing.T, store db.Store, email string) *model.User {
	t.Helper()
	u, err := store.CreateUser(context.Background(), email, "", model.RoleUser, "en", "UTC")
	if err != nil {
		t.Fatalf("create directory user: %v", err)
	}
	return u
}

func TestPasswordAcceptsADirectoryAccount(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	r := mkResolver(store, false)
	r.Directory = &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}

	p, err := r.Password(context.Background(), "ayse@example.com", "directory-pw")
	if err != nil {
		t.Fatalf("directory password refused: %v", err)
	}
	if p.User.ID != u.ID {
		t.Fatalf("got user %d, want %d", p.User.ID, u.ID)
	}
}

func TestPasswordRefusesAWrongDirectoryPassword(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	r := mkResolver(store, false)
	r.Directory = &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "right"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}

	if _, err := r.Password(context.Background(), "ayse@example.com", "wrong"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// TestPasswordPrefersTheLocalHash — the local compare has no network in it, and
// keeping it first is what makes admin@local answerable while the directory is
// unreachable.
func TestPasswordPrefersTheLocalHash(t *testing.T) {
	store := newStore(t)
	mkUser(t, store, "admin@local", model.RoleAdmin)
	dir := &fakeDirectory{}
	r := mkResolver(store, false)
	r.Directory = dir

	if _, err := r.Password(context.Background(), "admin@local", testPassword); err != nil {
		t.Fatalf("local password refused: %v", err)
	}
	if len(dir.calls) != 0 {
		t.Fatalf("the directory was consulted for a local password: %v", dir.calls)
	}
}

// TestPasswordSurvivesAnUnreachableDirectory — a directory outage must not take
// the local break-glass account down with it.
func TestPasswordSurvivesAnUnreachableDirectory(t *testing.T) {
	store := newStore(t)
	mkUser(t, store, "admin@local", model.RoleAdmin)
	r := mkResolver(store, false)
	r.Directory = &fakeDirectory{failWith: errors.New("ldap: dial: connection refused")}

	if _, err := r.Password(context.Background(), "admin@local", testPassword); err != nil {
		t.Fatalf("local password refused while the directory was down: %v", err)
	}
	if _, err := r.Password(context.Background(), "ghost@example.com", "whatever"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// TestPasswordAsksTheDirectoryWithTheAccountEmail — SFTP and FTPS carry only a
// username field, while the directory filter matches an e-mail or a UPN. Asking
// with the raw username would fail every protocol login for an account that
// signs in by name.
func TestPasswordAsksTheDirectoryWithTheAccountEmail(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	if u.Username == "" {
		t.Fatal("fixture: the account should have been named by identitystore")
	}
	dir := &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}
	r := mkResolver(store, false)
	r.Directory = dir

	if _, err := r.Password(context.Background(), u.Username, "directory-pw"); err != nil {
		t.Fatalf("username login refused for a directory account: %v", err)
	}
	if len(dir.calls) != 1 || dir.calls[0] != "ayse@example.com" {
		t.Fatalf("directory was asked with %v, want [ayse@example.com]", dir.calls)
	}
}

// TestDirectoryVerificationIsCached is why the credential cache had to learn
// about directory entries at all.
//
// ⚠ The cache re-checked `u.PasswordHash == ""` on every hit, which is ALWAYS
// true for a directory account — so no entry would ever have survived, and each
// request of a WebDAV PROPFIND storm would have been a fresh LDAPS bind.
func TestDirectoryVerificationIsCached(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	dir := &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}
	r := mkResolver(store, false)
	r.Directory = dir

	for i := 0; i < 5; i++ {
		if _, err := r.Password(context.Background(), "ayse@example.com", "directory-pw"); err != nil {
			t.Fatalf("call %d refused: %v", i, err)
		}
	}
	if len(dir.calls) != 1 {
		t.Fatalf("the directory was bound %d times, want 1 (the rest must come from the cache)", len(dir.calls))
	}

	// And the entry must be droppable, or CacheTTL would be the only bound on a
	// password revoked at the directory.
	r.Forget()
	if _, err := r.Password(context.Background(), "ayse@example.com", "directory-pw"); err != nil {
		t.Fatalf("after Forget: %v", err)
	}
	if len(dir.calls) != 2 {
		t.Fatalf("Forget did not drop the directory entry (calls=%d)", len(dir.calls))
	}
}

// TestDirectoryAccountWithTOTPIsRefused — none of these protocols has a
// second-factor channel, so accepting the password would make each of them a
// documented 2FA bypass. The rule must not depend on where the password lives.
func TestDirectoryAccountWithTOTPIsRefused(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	ctx := context.Background()
	if err := store.SetTotpPendingSecret(ctx, u.ID, "SECRET", []string{"a", "b"}); err != nil {
		t.Fatalf("totp pending: %v", err)
	}
	if err := store.ActivateTotp(ctx, u.ID); err != nil {
		t.Fatalf("totp activate: %v", err)
	}
	dir := &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}
	r := mkResolver(store, false)
	r.Directory = dir

	if _, err := r.Password(context.Background(), "ayse@example.com", "directory-pw"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	if len(dir.calls) != 0 {
		t.Fatalf("a TOTP account must be refused before the directory is asked: %v", dir.calls)
	}
}

// TestTOTPSwitchedOnInvalidatesACachedDirectoryEntry — the cache skips the
// local re-checks for a directory entry, so the ONE re-check it keeps has to
// still bite. Without it, switching 2FA on would leave the protocols open for
// the whole TTL on an account that just declared it wants a second factor.
func TestTOTPSwitchedOnInvalidatesACachedDirectoryEntry(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	r := mkResolver(store, false)
	r.Directory = &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": u},
	}

	if _, err := r.Password(ctx, "ayse@example.com", "directory-pw"); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := store.SetTotpPendingSecret(ctx, u.ID, "SECRET", []string{"a", "b"}); err != nil {
		t.Fatalf("totp pending: %v", err)
	}
	if err := store.ActivateTotp(ctx, u.ID); err != nil {
		t.Fatalf("totp activate: %v", err)
	}

	if _, err := r.Password(ctx, "ayse@example.com", "directory-pw"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("a cached directory entry survived 2FA being switched on: %v", err)
	}
}

// TestDisabledDirectoryAccountIsRefused — the account gate applies to directory
// users too; a directory that still binds a filex account somebody disabled
// must not get past principal().
func TestDisabledDirectoryAccountIsRefused(t *testing.T) {
	store := newStore(t)
	u := mkDirectoryUser(t, store, "ayse@example.com")
	if err := store.SetUserEnabled(context.Background(), u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	fresh, err := store.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	r := mkResolver(store, false)
	r.Directory = &fakeDirectory{
		accepts:  map[string]string{"ayse@example.com": "directory-pw"},
		resolves: map[string]*model.User{"ayse@example.com": fresh},
	}

	if _, err := r.Password(context.Background(), "ayse@example.com", "directory-pw"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

// TestNoDirectoryLeavesBehaviourUnchanged — the regression guard for every
// install that has no LDAP at all.
func TestNoDirectoryLeavesBehaviourUnchanged(t *testing.T) {
	store := newStore(t)
	mkUser(t, store, "admin@local", model.RoleAdmin)
	mkDirectoryUser(t, store, "ayse@example.com")
	r := mkResolver(store, false)

	if _, err := r.Password(context.Background(), "admin@local", testPassword); err != nil {
		t.Fatalf("local password refused: %v", err)
	}
	if _, err := r.Password(context.Background(), "admin@local", "wrong"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	if _, err := r.Password(context.Background(), "ayse@example.com", "anything"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("an account with no password and no directory must be refused, got %v", err)
	}
}

package protocolauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// The sign-in limit covers the protocols too: WebDAV, FTPS and SFTP all reach
// the account password through Resolver.Any, and the limit sits above the
// password check, so none of them can be a way round the web form's limit.

func guarded(t *testing.T) (*protocolauth.Resolver, db.Store, *loginguard.Guard) {
	t.Helper()
	store := newStore(t)
	r := mkResolver(store, false)
	g := loginguard.New(store)
	r.Guard = g
	return r, store, g
}

func from(ip, door string) context.Context {
	return protocolauth.WithSource(context.Background(), door, ip)
}

func TestWrongPasswordsOverEveryProtocolLockTheAccount(t *testing.T) {
	r, store, _ := guarded(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)

	// Wrong attempts arrive through different doors and addresses: the account
	// counter is one, whichever protocol carried them.
	doors := []string{"dav", "ftp", "sftp", "dav", "ftp"}
	for i, d := range doors {
		if _, err := r.Any(from("203.0.113."+string(rune('1'+i)), d), "ada@example.com", "wrong"); !errors.Is(err, protocolauth.ErrUnauthorized) {
			t.Fatalf("attempt %d over %s: %v, want ErrUnauthorized", i+1, d, err)
		}
	}

	// Locked: even the RIGHT password is refused, and the refusal says why.
	_, err := r.Any(from("203.0.113.9", "dav"), "ada@example.com", testPassword)
	var th *protocolauth.ThrottledError
	if !errors.As(err, &th) {
		t.Fatalf("right password on a locked account: %v, want a ThrottledError", err)
	}
	if !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("a ThrottledError must still read as ErrUnauthorized to code that only checks that")
	}
	if !th.Verdict.Blocked || th.Verdict.Scope != model.LoginThrottleAccount || th.Verdict.RetryAfter <= 0 {
		t.Errorf("verdict = %+v", th.Verdict)
	}
}

func TestLockedAccountRefusesItsCachedPasswordToo(t *testing.T) {
	// A credential the resolver has already verified sits in its cache for
	// minutes. If the lock let a cache hit through, "429 for a wrong guess,
	// 200 for the right one" would be an unthrottled guessing oracle for as long
	// as a legitimate client keeps the entry warm.
	r, store, _ := guarded(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)
	if _, err := r.Any(from("203.0.113.1", "dav"), "ada@example.com", testPassword); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	for i := 0; i < 5; i++ {
		_, _ = r.Any(from("203.0.113.2", "dav"), "ada@example.com", "wrong")
	}
	_, err := r.Any(from("203.0.113.1", "dav"), "ada@example.com", testPassword)
	var th *protocolauth.ThrottledError
	if !errors.As(err, &th) {
		t.Fatalf("cached password on a locked account: %v, want a ThrottledError", err)
	}
}

func TestATokenPresentedAsThePasswordIsNotAWrongAttempt(t *testing.T) {
	// Any tries the secret as a password, then as a token. The password step
	// failing on the way to a valid token must not be counted.
	r, store, g := guarded(t)
	u := mkUser(t, store, "ada@example.com", model.RoleUser)
	mkToken(t, store, u.ID, "cli", "tok-secret-1", "read,write")
	for i := 0; i < 30; i++ {
		if _, err := r.Any(from("203.0.113.1", "dav"), "ada@example.com", "tok-secret-1"); err != nil {
			t.Fatalf("token sign-in %d: %v", i, err)
		}
	}
	rows, _ := store.ListLoginThrottles(context.Background(), "", nil, 0)
	if len(rows) != 0 {
		t.Fatalf("a valid token was counted as %d wrong attempt row(s)", len(rows))
	}
	_ = g
}

func TestTokenSuccessDoesNotResetThePasswordCounter(t *testing.T) {
	// An account with a busy token client must not get an unlimited number of
	// password guesses: the client's requests would wipe the counter each time.
	r, store, _ := guarded(t)
	u := mkUser(t, store, "ada@example.com", model.RoleUser)
	mkToken(t, store, u.ID, "cli", "tok-secret-1", "read,write")
	for i := 0; i < 4; i++ {
		_, _ = r.Any(from("203.0.113.2", "dav"), "ada@example.com", "wrong")
		if _, err := r.Any(from("203.0.113.1", "dav"), "ada@example.com", "tok-secret-1"); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = r.Any(from("203.0.113.2", "dav"), "ada@example.com", "wrong")
	if _, err := r.Any(from("203.0.113.1", "dav"), "ada@example.com", "tok-secret-1"); err == nil {
		t.Fatal("five wrong passwords must lock the account even with token requests in between")
	}
}

func TestFreshPasswordSuccessResetsButACacheHitDoesNot(t *testing.T) {
	r, store, _ := guarded(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)
	ctx := from("203.0.113.1", "dav")
	for i := 0; i < 3; i++ {
		_, _ = r.Any(ctx, "ada@example.com", "wrong")
	}
	if _, err := r.Any(ctx, "ada@example.com", testPassword); err != nil {
		t.Fatalf("fresh sign-in: %v", err)
	}
	row, _ := store.GetLoginThrottle(context.Background(), model.LoginThrottleAccount, "ada@example.com")
	if row != nil {
		t.Fatalf("a fresh correct password resets the account counter, got %+v", row)
	}

	// Now warm: three more wrong ones, then cache hits. A cache hit is a
	// legitimate client re-presenting an already-verified credential every few
	// seconds; if it reset the counter, that client would let a guesser through.
	for i := 0; i < 3; i++ {
		_, _ = r.Any(from("203.0.113.2", "dav"), "ada@example.com", "wrong")
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Any(ctx, "ada@example.com", testPassword); err != nil {
			t.Fatalf("cache hit %d: %v", i, err)
		}
	}
	row, _ = store.GetLoginThrottle(context.Background(), model.LoginThrottleAccount, "ada@example.com")
	if row == nil || row.Fails != 3 {
		t.Fatalf("a cache hit must not reset the counter: %+v", row)
	}
}

func TestAddressLimitOverProtocols(t *testing.T) {
	r, _, _ := guarded(t)
	// One address, ten different names: the address locks even though no
	// single account did.
	for i := 0; i < 10; i++ {
		_, _ = r.Any(from("198.51.100.7", "sftp"), "user"+string(rune('a'+i))+"@example.com", "wrong")
	}
	_, err := r.Any(from("198.51.100.7", "sftp"), "someone-else@example.com", "x")
	var th *protocolauth.ThrottledError
	if !errors.As(err, &th) || th.Verdict.Scope != model.LoginThrottleIP {
		t.Fatalf("got %v, want an address lock", err)
	}
	// Another address is fine.
	if _, err := r.Any(from("198.51.100.8", "sftp"), "someone-else@example.com", "x"); errors.As(err, &th) {
		t.Fatalf("another address must not be blocked: %v", err)
	}
}

func TestAllowlistedAddressReachesALockedAccountOverProtocols(t *testing.T) {
	r, store, g := guarded(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)
	if err := store.UpsertSetting(context.Background(), loginguard.KeyIPAllowlist, "192.0.2.0/28"); err != nil {
		t.Fatal(err)
	}
	g.Invalidate()
	for i := 0; i < 5; i++ {
		_, _ = r.Any(from("203.0.113.2", "dav"), "ada@example.com", "wrong")
	}
	if _, err := r.Any(from("203.0.113.2", "dav"), "ada@example.com", testPassword); err == nil {
		t.Fatal("the account is locked for everybody else")
	}
	if _, err := r.Any(from("192.0.2.5", "dav"), "ada@example.com", testPassword); err != nil {
		t.Fatalf("an allow-listed address signs in to a locked account: %v", err)
	}
	// A wrong password from there is refused as usual but counts against nobody.
	if _, err := r.Any(from("192.0.2.5", "dav"), "ada@example.com", "wrong"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("wrong password from the allow-listed address: %v", err)
	}
	if row, _ := store.GetLoginThrottle(context.Background(), model.LoginThrottleIP, "192.0.2.5"); row != nil {
		t.Fatalf("allow-listed address was counted: %+v", row)
	}
}

func TestPasswordIsGuardedToo(t *testing.T) {
	r, store, _ := guarded(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)
	for i := 0; i < 5; i++ {
		_, _ = r.Password(from("203.0.113.2", "ftp"), "ada@example.com", "wrong")
	}
	if _, err := r.Password(from("203.0.113.2", "ftp"), "ada@example.com", testPassword); err == nil {
		t.Fatal("Password must honour the lock as Any does")
	}
}

func TestNoGuardNoLimit(t *testing.T) {
	store := newStore(t)
	mkUser(t, store, "ada@example.com", model.RoleUser)
	r := mkResolver(store, false)
	for i := 0; i < 50; i++ {
		_, _ = r.Any(context.Background(), "ada@example.com", "wrong")
	}
	if _, err := r.Any(context.Background(), "ada@example.com", testPassword); err != nil {
		t.Fatalf("a resolver built without a guard is unlimited, as before: %v", err)
	}
	_ = time.Second
}

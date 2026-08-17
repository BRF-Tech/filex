package protocolauth_test

import (
	"context"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// What these tests are actually asserting is one sentence: revoking a
// credential must reach the connection it already opened, not only the next
// login. That distinction is invisible to every other test in this package,
// because every other test authenticates and then stops.

func TestDeletedTokenCutsTheOpenSession(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "sftp@example.com", "user")
	tok := mkToken(t, store, u.ID, "sftp", "tok-live-1", "read,write")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Token(ctx, "", "tok-live-1")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	closed := make(chan struct{})
	ls := res.Enter(p, "sftp", "10.0.0.9:5555", u.Email, func() { close(closed) })
	defer ls.Leave()

	// Nothing has changed yet: a sweep must leave a good session alone.
	if cut := res.Sweep(ctx); cut != 0 {
		t.Fatalf("sweep cut %d live sessions before anything was revoked", cut)
	}
	if ls.Revoked() {
		t.Fatal("session reported revoked while its token was still valid")
	}

	if err := store.DeleteAPIToken(ctx, tok.ID); err != nil {
		t.Fatalf("delete token: %v", err)
	}
	if cut := res.Sweep(ctx); cut != 1 {
		t.Fatalf("sweep cut %d sessions after the token was deleted, want 1", cut)
	}
	if !ls.Revoked() {
		t.Fatal("session survived the deletion of the token it logged in with")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		// The closer is what actually hangs up on the client; a session marked
		// revoked whose connection stays open is still serving files.
		t.Fatal("the session was marked revoked but its connection was never closed")
	}
}

func TestKickCutsWithoutWaitingForTheSweep(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "kick@example.com", "user")
	tok := mkToken(t, store, u.ID, "sftp", "tok-live-2", "read")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Token(ctx, "", "tok-live-2")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	ls := res.Enter(p, "ftps", "10.0.0.9:5556", u.Email, nil)
	defer ls.Leave()

	if n := res.Kick(protocolauth.KickToken, tok.ID+1000); n != 0 {
		t.Fatalf("kicking an unrelated credential cut %d sessions", n)
	}
	if ls.Revoked() {
		t.Fatal("a session was cut by a kick aimed at somebody else's credential")
	}
	if n := res.Kick(protocolauth.KickToken, tok.ID); n != 1 {
		t.Fatalf("kick cut %d sessions, want 1", n)
	}
	if !ls.Revoked() {
		t.Fatal("kick did not mark the session revoked")
	}
}

func TestNarrowedTokenScopesCutTheSession(t *testing.T) {
	raw, base := testutil.NewTestDB(t)
	store := identitystore.New(base)
	u := mkUser(t, store, "scopes@example.com", "user")
	tok := mkToken(t, store, u.ID, "sftp", "tok-live-3", "read,write")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Token(ctx, "", "tok-live-3")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if !p.HasScope("write") {
		t.Fatal("the token was minted with write and the principal disagrees")
	}
	ls := res.Enter(p, "sftp", "10.0.0.9:5557", u.Email, nil)
	defer ls.Leave()

	// The scopes are narrowed under the live session — straight in the table,
	// which is what an admin edit amounts to. The session answers HasScope from
	// its cached copy, so without a re-check it would keep writing.
	if _, err := raw.ExecContext(ctx, "UPDATE api_tokens SET scopes = ? WHERE id = ?", "read", tok.ID); err != nil {
		t.Fatalf("narrow scopes: %v", err)
	}
	if cut := res.Sweep(ctx); cut != 1 {
		t.Fatalf("sweep cut %d sessions after the scopes were narrowed, want 1", cut)
	}
	if !ls.Revoked() {
		t.Fatal("a session kept its write scope after the token lost it")
	}
}

func TestDisabledAccountCutsEverySessionItOpened(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "gone@example.com", "user")
	mkToken(t, store, u.ID, "a", "tok-live-4a", "read")
	mkToken(t, store, u.ID, "b", "tok-live-4b", "read")
	res := mkResolver(store, false)
	ctx := context.Background()

	var sessions []*protocolauth.LiveSession
	for _, secret := range []string{"tok-live-4a", "tok-live-4b"} {
		p, err := res.Token(ctx, "", secret)
		if err != nil {
			t.Fatalf("token %s: %v", secret, err)
		}
		ls := res.Enter(p, "sftp", "10.0.0.9:0", u.Email, nil)
		defer ls.Leave()
		sessions = append(sessions, ls)
	}

	if err := store.SetUserEnabled(ctx, u.ID, false); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	if cut := res.Sweep(ctx); cut != 2 {
		t.Fatalf("sweep cut %d sessions after the account was disabled, want 2", cut)
	}
	for i, ls := range sessions {
		if !ls.Revoked() {
			t.Fatalf("session %d survived its account being disabled", i)
		}
	}
}

// TOTP switched on mid-session is the 2FA gate's live half. Resolver.Password
// refuses such an account at login on every protocol; a session that got in
// with a password BEFORE the switch is a live bypass of exactly that gate.
func TestTOTPEnabledMidSessionCutsAPasswordSession(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "totp@example.com", "user")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Password(ctx, u.Email, testPassword)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	ls := res.Enter(p, "ftps", "10.0.0.9:5558", u.Email, nil)
	defer ls.Leave()

	enableTOTP(t, store, u.ID)
	if cut := res.Sweep(ctx); cut != 1 {
		t.Fatalf("sweep cut %d sessions after TOTP was switched on, want 1", cut)
	}
	if !ls.Revoked() {
		t.Fatal("a password session outlived the account switching on 2FA")
	}
}

// The mirror of the test above: a TOKEN session is NOT affected, because an
// individually revocable credential is precisely what a TOTP account is
// supposed to use. Cutting it would make 2FA punish the safe choice.
func TestTOTPEnabledMidSessionKeepsATokenSession(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "totp-token@example.com", "user")
	mkToken(t, store, u.ID, "app", "tok-live-5", "read")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Token(ctx, "", "tok-live-5")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	ls := res.Enter(p, "sftp", "10.0.0.9:5559", u.Email, nil)
	defer ls.Leave()

	enableTOTP(t, store, u.ID)
	if cut := res.Sweep(ctx); cut != 0 {
		t.Fatalf("sweep cut %d token sessions when TOTP was switched on, want 0", cut)
	}
	if ls.Revoked() {
		t.Fatal("an API-token session was cut for the account switching on 2FA")
	}
}

func TestLeaveRemovesTheSessionFromTheRegistry(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "leave@example.com", "user")
	res := mkResolver(store, false)
	ctx := context.Background()

	p, err := res.Password(ctx, u.Email, testPassword)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	ls := res.Enter(p, "sftp", "10.0.0.9:5560", u.Email, nil)
	if got := len(res.Live()); got != 1 {
		t.Fatalf("registry holds %d sessions after one Enter, want 1", got)
	}
	ls.Leave()
	if got := len(res.Live()); got != 0 {
		t.Fatalf("registry holds %d sessions after Leave, want 0", got)
	}
	// Twice is what a defer plus an explicit call looks like, and it must not
	// panic or resurrect anything.
	ls.Leave()
	if got := len(res.Live()); got != 0 {
		t.Fatalf("registry holds %d sessions after a second Leave, want 0", got)
	}
}

// The grant cache is the other half of "when does it stop working". A stream
// session asks the same question thousands of times, so the answer is cached —
// and a cache with no expiry means a revoked grant keeps serving files for the
// life of a mount.
func TestACLSetIsNotCachedForever(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "acl@example.com", "user")
	res := mkResolver(store, false)
	res.CacheTTL = 20 * time.Millisecond
	ctx := context.Background()

	p, err := res.Password(ctx, u.Email, testPassword)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	st := mkStorage(t, store, "main")

	first, err := p.ACL(ctx, st)
	if err != nil {
		t.Fatalf("acl: %v", err)
	}
	again, err := p.ACL(ctx, st)
	if err != nil {
		t.Fatalf("acl again: %v", err)
	}
	if first != again {
		t.Fatal("the grant set was reloaded inside its own TTL; the cache is doing nothing")
	}

	time.Sleep(40 * time.Millisecond)
	later, err := p.ACL(ctx, st)
	if err != nil {
		t.Fatalf("acl after ttl: %v", err)
	}
	if later == first {
		t.Fatal("the grant set was still the cached one after its TTL passed — a revoked grant would never expire")
	}
}

func mkStorage(t *testing.T, store interface {
	CreateStorage(context.Context, *model.Storage) (*model.Storage, error)
}, name string) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
		ConfigJSON: []byte(`{"root":"/tmp"}`),
	})
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	return st
}

// enableTOTP switches 2FA on the way the account settings screen does: stage a
// secret, then activate it.
func enableTOTP(t *testing.T, store db.Store, userID int64) {
	t.Helper()
	ctx := context.Background()
	if err := store.SetTotpPendingSecret(ctx, userID, "JBSWY3DPEHPK3PXP", []string{"aaaa-bbbb"}); err != nil {
		t.Fatalf("stage totp secret: %v", err)
	}
	if err := store.ActivateTotp(ctx, userID); err != nil {
		t.Fatalf("activate totp: %v", err)
	}
}

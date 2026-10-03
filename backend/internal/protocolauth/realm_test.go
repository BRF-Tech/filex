package protocolauth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Protocol sign-in by realm on a multi-tenant install (#128, the owner's
// decisions): the tenant comes from the ADDRESS when the protocol carries one
// (WebDAV Host, FTPS SNI — auth.WithLoginHost), else from `realm/name`; a bare
// name is the platform's own tenant; an address and a realm that disagree are
// refused. A token or a registered key needs no realm, but one that is named
// must be the account's.

type realmWorld struct {
	store              db.Store
	r                  *protocolauth.Resolver
	acme, beta         *model.Provider
	main, alexA, alexB *model.User
}

// newRealmWorld: three people called alex — the platform's, acme's (acme has
// an address of its own) and beta's — with THE SAME password, so only the
// realm can tell them apart.
func newRealmWorld(t *testing.T) *realmWorld {
	t.Helper()
	store := newStore(t)
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "files.acme.test", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", AuthType: "local", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	in := func(email string, p *model.Provider) *model.User {
		u := mkUser(t, store, email, model.RoleUser)
		if p != nil {
			if err := store.SetUserProvider(ctx, u.ID, p.ID, ""); err != nil {
				t.Fatal(err)
			}
		}
		u, err := store.GetUser(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	w := &realmWorld{store: store, acme: acme, beta: beta}
	w.main = in("alex@local", nil)
	w.alexA = in("alex@acme.local", acme)
	w.alexB = in("alex@beta.local", beta)
	w.r = mkResolver(store, true)
	return w
}

func (w *realmWorld) who(t *testing.T, ctx context.Context, login string) int64 {
	t.Helper()
	p, err := w.r.Password(ctx, login, testPassword)
	if err != nil {
		return 0
	}
	return p.User.ID
}

func atHost(host string) context.Context {
	return auth.WithLoginHost(context.Background(), host)
}

func TestProtocolRealm_Password(t *testing.T) {
	w := newRealmWorld(t)
	bg := context.Background()

	// No address: the realm in the user name, else the platform's own.
	if got := w.who(t, bg, "alex"); got != w.main.ID {
		t.Errorf("alex = %d, want the platform's alex %d", got, w.main.ID)
	}
	if got := w.who(t, bg, "acme/alex"); got != w.alexA.ID {
		t.Errorf("acme/alex = %d, want %d", got, w.alexA.ID)
	}
	if got := w.who(t, bg, "BETA/Alex"); got != w.alexB.ID {
		t.Errorf("BETA/Alex = %d, want %d", got, w.alexB.ID)
	}
	if got := w.who(t, bg, "/alex"); got != w.main.ID {
		t.Errorf("/alex (the platform's realm said outright) = %d, want %d", got, w.main.ID)
	}

	// ⚠ Another tenant's account is never reached, by any name.
	for _, login := range []string{"alex@acme.local", w.alexA.Username, "beta/alex@acme.local", "beta/" + w.alexA.Username, "acme/alex@local", "nobody/alex"} {
		if got := w.who(t, bg, login); got != 0 {
			t.Errorf("%q signed in as %d — across the tenant boundary", login, got)
		}
	}

	// The address names the tenant (WebDAV Host, FTPS SNI).
	acmeHost := atHost("files.acme.test")
	if got := w.who(t, acmeHost, "alex"); got != w.alexA.ID {
		t.Errorf("alex at acme's address = %d, want %d", got, w.alexA.ID)
	}
	if got := w.who(t, acmeHost, "acme/alex"); got != w.alexA.ID {
		t.Errorf("acme/alex at acme's address = %d, want %d", got, w.alexA.ID)
	}
	for _, login := range []string{"beta/alex", "/alex", "alex@local"} {
		if got := w.who(t, acmeHost, login); got != 0 {
			t.Errorf("%q at acme's address signed in as %d — the address and the realm disagree", login, got)
		}
	}
	// An address that is nobody's is the platform's page.
	if got := w.who(t, atHost("unknown.test"), "alex"); got != w.main.ID {
		t.Errorf("alex at an unknown address = %d, want %d", got, w.main.ID)
	}
}

// ⚠ The credential cache is per realm: the same name and password in two
// realms are two people. (A cache keyed without the realm would hand the
// second sign-in the first one's account.)
func TestProtocolRealm_CacheIsPerRealm(t *testing.T) {
	w := newRealmWorld(t)
	bg := context.Background()
	for i := 0; i < 2; i++ {
		if got := w.who(t, bg, "alex"); got != w.main.ID {
			t.Fatalf("round %d: alex = %d", i, got)
		}
		if got := w.who(t, bg, "acme/alex"); got != w.alexA.ID {
			t.Fatalf("round %d: acme/alex = %d", i, got)
		}
		if got := w.who(t, atHost("files.acme.test"), "alex"); got != w.alexA.ID {
			t.Fatalf("round %d: alex at acme = %d", i, got)
		}
		if got := w.who(t, bg, "beta/alex"); got != w.alexB.ID {
			t.Fatalf("round %d: beta/alex = %d", i, got)
		}
	}
	// And a cached sign-in is re-judged: move acme's alex to beta and the
	// cached acme/alex stops working.
	if err := w.store.SetUserProvider(bg, w.alexA.ID, w.beta.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := w.who(t, bg, "acme/alex"); got != 0 {
		t.Errorf("acme/alex still signed in as %d after the account left acme", got)
	}
}

func TestProtocolRealm_TokenAndKeyNeedNoRealm(t *testing.T) {
	w := newRealmWorld(t)
	bg := context.Background()
	mkToken(t, w.store, w.alexA.ID, "acme", "tok-acme-0123456789", "read,write")

	ok := func(ctx context.Context, login string) bool {
		p, err := w.r.Any(ctx, login, "tok-acme-0123456789")
		return err == nil && p.User.ID == w.alexA.ID
	}
	for _, login := range []string{"alex", "acme/alex", "alex@acme.local", w.alexA.Username, ""} {
		if !ok(bg, login) {
			t.Errorf("acme's token as %q was refused", login)
		}
	}
	if !ok(atHost("files.acme.test"), "alex") {
		t.Error("acme's token at acme's address was refused")
	}
	// ⚠ A realm that is named must be the token's account's.
	for _, login := range []string{"beta/alex", "/alex", "beta/alex@acme.local"} {
		if ok(bg, login) {
			t.Errorf("acme's token was accepted as %q", login)
		}
	}
	if _, err := w.r.Token(bg, "beta/alex", "tok-acme-0123456789"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("Token(beta/alex) = %v", err)
	}

	// A registered SSH key: the same rule.
	if _, err := w.store.CreateSSHPublicKey(bg, &model.SSHPublicKey{
		UserID: w.alexA.ID, Name: "laptop", Fingerprint: "fp-acme-alex", PublicKey: "ssh-ed25519 AAAA",
	}); err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"alex", "acme/alex", ""} {
		if p, err := w.r.PublicKey(bg, login, "fp-acme-alex"); err != nil || p.User.ID != w.alexA.ID {
			t.Errorf("acme's key as %q: %v", login, err)
		}
	}
	for _, login := range []string{"beta/alex", "/alex", "nobody/alex"} {
		if _, err := w.r.PublicKey(bg, login, "fp-acme-alex"); err == nil {
			t.Errorf("acme's key was accepted as %q", login)
		}
	}
}

// The attempt limit counts `<realm>/<name>`: acme's alex being guessed at does
// not lock the platform's alex or beta's.
func TestProtocolRealm_AttemptLimitIsPerRealm(t *testing.T) {
	w := newRealmWorld(t)
	w.r.Guard = loginguard.New(w.store)
	ctx := protocolauth.WithSource(context.Background(), loginguard.ProtoSFTP, "203.0.113.5")
	for i := 0; i < 5; i++ {
		_, _ = w.r.Any(ctx, "acme/alex", "wrong")
	}
	if _, err := w.r.Any(ctx, "acme/alex", testPassword); err == nil {
		t.Fatal("acme/alex should be locked")
	}
	other := protocolauth.WithSource(context.Background(), loginguard.ProtoSFTP, "198.51.100.9")
	if p, err := w.r.Any(other, "alex", testPassword); err != nil || p.User.ID != w.main.ID {
		t.Errorf("the platform's alex was locked by acme's: %v", err)
	}
	if p, err := w.r.Any(other, "beta/alex", testPassword); err != nil || p.User.ID != w.alexB.ID {
		t.Errorf("beta's alex was locked by acme's: %v", err)
	}
	// The same person at acme's address shares acme's counter.
	host := auth.WithLoginHost(other, "files.acme.test")
	if _, err := w.r.Any(host, "alex", testPassword); err == nil {
		t.Error("alex at acme's address is acme/alex, and that is locked")
	}
	// A realm nobody has is a wrong attempt, counted like one.
	for i := 0; i < 5; i++ {
		_, _ = w.r.Any(other, "nobody/zed", "x")
	}
	rows, err := w.store.ListLoginThrottles(context.Background(), model.LoginThrottleAccount, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Subject] = true
	}
	for _, want := range []string{"acme/alex", "nobody/zed"} {
		if !seen[want] {
			t.Errorf("no counter %q among %v", want, seen)
		}
	}
}

// A single-tenant install reads no realm: `acme/alex` is just a name nobody
// has, exactly as before.
func TestProtocolRealm_SingleTenantUnchanged(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "alex@example.com", model.RoleUser)
	r := mkResolver(store, false)
	if p, err := r.Password(context.Background(), u.Username, testPassword); err != nil || p.User.ID != u.ID {
		t.Fatalf("plain name: %v", err)
	}
	if _, err := r.Password(context.Background(), "acme/"+u.Username, testPassword); err == nil {
		t.Error("a single-tenant install read a realm")
	}
	// An address with a `/` in it is only an address there: nothing is split.
	slash := mkUser(t, store, "a/b@example.com", model.RoleUser)
	if p, err := r.Password(context.Background(), "a/b@example.com", testPassword); err != nil || p.User.ID != slash.ID {
		t.Errorf("a/b@example.com on a single-tenant install: %v", err)
	}
}

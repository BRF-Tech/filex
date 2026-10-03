package protocolauth_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// The file protocols reach the same limiter as the web form, so a public
// demo's shared account is exempt from the per-account lock there too - and
// its address limit still holds.
func TestDemoAccountIsExemptOverTheProtocols(t *testing.T) {
	r, store, g := guarded(t)
	mkUser(t, store, "demo@demo.com", model.RoleUser)
	g.ExemptAccounts(loginguard.DemoAccount(store, "demo@demo.com", ""))

	doors := []string{"dav", "ftp", "sftp"}
	for i := 0; i < 9; i++ {
		_, err := r.Any(from("203.0.113."+strconv.Itoa(i+1), doors[i%3]), "demo@demo.com", "wrong")
		if _, throttled := protocolauth.AsThrottled(err); throttled {
			t.Fatalf("attempt %d locked the shared account: %v", i+1, err)
		}
	}
	if _, err := r.Any(from("198.51.100.7", "dav"), "demo@demo.com", testPassword); err != nil {
		t.Fatalf("the next visitor over WebDAV: %v", err)
	}

	// One address guessing is still stopped, over any door.
	for i := 0; i < 10; i++ {
		_, _ = r.Any(from("192.0.2.9", "sftp"), "demo@demo.com", "wrong")
	}
	_, err := r.Any(from("192.0.2.9", "sftp"), "demo@demo.com", testPassword)
	th, ok := protocolauth.AsThrottled(err)
	if !ok || th.Verdict.Scope != model.LoginThrottleIP {
		t.Fatalf("an address past its limit: %v, want an address lock", err)
	}
}

// Multi-tenant: the shared account lives in a tenant, and `realm/name` over
// SFTP is its counter - exempt; the same name in another realm is another
// person, limited as ever.
func TestDemoAccountExemptionIsItsRealms(t *testing.T) {
	w := newRealmWorld(t)
	g := loginguard.New(w.store)
	w.r.Guard = g
	g.ExemptAccounts(loginguard.DemoAccount(w.store, w.alexA.Email, ""))
	ctx := context.Background()
	acme, err := w.store.GetProvider(ctx, w.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := w.store.GetProvider(ctx, w.beta.ID)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 7; i++ {
		_, err := w.r.Any(from("203.0.113."+strconv.Itoa(i+1), "sftp"), acme.Realm+"/"+w.alexA.Username, "wrong")
		if _, throttled := protocolauth.AsThrottled(err); throttled {
			t.Fatalf("attempt %d locked acme's shared account: %v", i+1, err)
		}
	}
	if p, err := w.r.Any(from("198.51.100.7", "sftp"), acme.Realm+"/"+w.alexA.Username, testPassword); err != nil || p.User.ID != w.alexA.ID {
		t.Fatalf("acme's shared account after seven wrong passwords: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, _ = w.r.Any(from("192.0.2."+strconv.Itoa(i+1), "sftp"), beta.Realm+"/"+w.alexB.Username, "wrong")
	}
	_, err = w.r.Any(from("198.51.100.8", "sftp"), beta.Realm+"/"+w.alexB.Username, testPassword)
	if th, ok := protocolauth.AsThrottled(err); !ok || th.Verdict.Scope != model.LoginThrottleAccount {
		t.Fatalf("beta's alex is not the shared account and must lock: %v", err)
	}
	if !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("a lock reads as ErrUnauthorized: %v", err)
	}
}

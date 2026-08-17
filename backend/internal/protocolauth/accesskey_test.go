package protocolauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

const secretKey = "test-environment-secret-key"

func withSecrets(t *testing.T, r *protocolauth.Resolver) *protocolauth.Resolver {
	t.Helper()
	box, err := secretbox.New(secretKey)
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	r.Secrets = box
	return r
}

func TestIssueAndResolveAccessKey(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "keys@example.com", model.RoleUser)
	r := withSecrets(t, mkResolver(store, false))

	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u, Label: "rclone"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Secret == "" || len(issued.Secret) != 40 {
		t.Errorf("secret = %q (len %d), want 40 chars", issued.Secret, len(issued.Secret))
	}
	if len(issued.Key.AccessKeyID) != 20 {
		t.Errorf("access key id = %q (len %d), want 20", issued.Key.AccessKeyID, len(issued.Key.AccessKeyID))
	}

	// ⚠ The stored copy must not be the plaintext: a database dump is exactly
	// the thing encrypting this was meant to survive.
	if issued.Key.SecretEnc == issued.Secret {
		t.Fatal("the secret was stored in the clear")
	}
	if !secretbox.IsSealed(issued.Key.SecretEnc) {
		t.Fatal("the stored secret is not sealed")
	}

	p, secret, err := r.AccessKey(ctx, issued.Key.AccessKeyID)
	if err != nil {
		t.Fatalf("AccessKey: %v", err)
	}
	if secret != issued.Secret {
		t.Fatal("the secret handed to the signature check is not the one issued")
	}
	if p.User.ID != u.ID {
		t.Errorf("resolved user %d, want %d", p.User.ID, u.ID)
	}
	if p.AccessKey == nil || p.AccessKey.ID != issued.Key.ID {
		t.Error("the principal does not carry the key it was resolved from")
	}
}

// Without a key, issuing must FAIL. Storing the secret in the clear "for now"
// is how an install ends up with credentials nobody ever goes back to encrypt.
func TestIssueRefusesWithoutASecretKey(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "nokey@example.com", model.RoleUser)
	r := mkResolver(store, false) // no Secrets

	if _, err := r.Issue(context.Background(), protocolauth.IssueRequest{User: u}); !errors.Is(err, protocolauth.ErrNoSecretBox) {
		t.Fatalf("Issue without a secret key = %v, want ErrNoSecretBox", err)
	}
}

// A key minted from a token is that token projected into a protocol that
// cannot carry it — so it inherits, and it may never widen.
func TestKeyInheritsItsTokenAndCannotWidenIt(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "inherit@example.com", model.RoleUser)
	plain := "tok_" + "aaaabbbbccccddddeeeeffff00001111"
	tok := mkToken(t, store, u.ID, "confined", plain, apitoken.ScopeRootPrefix+"main://projects/acme")
	r := withSecrets(t, mkResolver(store, false))

	// No confinement asked for: it must INHERIT the token's, not stay open.
	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u, Token: tok})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Key.Bucket != "main" || issued.Key.Prefix != "projects/acme" {
		t.Fatalf("key confinement = %q/%q, want main/projects/acme — an unconfined key would widen the token",
			issued.Key.Bucket, issued.Key.Prefix)
	}

	// Narrowing is allowed.
	narrower, err := r.Issue(ctx, protocolauth.IssueRequest{
		User: u, Token: tok, Bucket: "main", Prefix: "projects/acme/reports",
	})
	if err != nil {
		t.Fatalf("narrowing must be allowed: %v", err)
	}
	if narrower.Key.Prefix != "projects/acme/reports" {
		t.Errorf("prefix = %q, want the narrower one", narrower.Key.Prefix)
	}

	// Widening is not — in either direction.
	for _, bad := range []struct{ bucket, prefix string }{
		{"main", "projects"},       // a parent of the token's root
		{"main", ""},               // the whole storage
		{"other", "projects/acme"}, // a different storage entirely
	} {
		if _, err := r.Issue(ctx, protocolauth.IssueRequest{
			User: u, Token: tok, Bucket: bad.bucket, Prefix: bad.prefix,
		}); !errors.Is(err, protocolauth.ErrWidensParent) {
			t.Errorf("Issue(%s://%s) = %v, want ErrWidensParent", bad.bucket, bad.prefix, err)
		}
	}
}

// A projection must not outlive its original: otherwise a short-lived token
// becomes a way to mint a long-lived credential.
func TestKeyExpiryIsClampedToTheTokenExpiry(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "expiry@example.com", model.RoleUser)

	soon := time.Now().Add(1 * time.Hour).UTC().Truncate(time.Second)
	plain := "tok_" + "1111222233334444555566667777aaaa"
	tok, err := store.CreateAPIToken(ctx, &model.APIToken{
		UserID: u.ID, Label: "short", TokenHash: apitoken.HashToken(plain), ExpiresAt: &soon,
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	r := withSecrets(t, mkResolver(store, false))
	far := time.Now().Add(10 * 365 * 24 * time.Hour)
	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u, Token: tok, ExpiresAt: &far})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Key.ExpiresAt == nil || issued.Key.ExpiresAt.After(soon) {
		t.Fatalf("key expiry = %v, want clamped to the token's %v", issued.Key.ExpiresAt, soon)
	}
}

func TestExpiredDisabledAndUnknownKeysAuthenticateNobody(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "revoke@example.com", model.RoleUser)
	r := withSecrets(t, mkResolver(store, false))

	if _, _, err := r.AccessKey(ctx, "FLXDOESNOTEXIST00000"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("unknown key = %v, want ErrUnauthorized", err)
	}

	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := store.SetS3AccessKeyDisabled(ctx, issued.Key.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, err := r.AccessKey(ctx, issued.Key.AccessKeyID); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("disabled key = %v, want ErrUnauthorized", err)
	}
	if err := store.SetS3AccessKeyDisabled(ctx, issued.Key.ID, false); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, _, err := r.AccessKey(ctx, issued.Key.AccessKeyID); err != nil {
		t.Errorf("re-enabled key = %v, want a principal", err)
	}

	past := time.Now().Add(-time.Hour)
	expired, err := r.Issue(ctx, protocolauth.IssueRequest{User: u, ExpiresAt: &past})
	if err != nil {
		t.Fatalf("Issue expired: %v", err)
	}
	if _, _, err := r.AccessKey(ctx, expired.Key.AccessKeyID); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("expired key = %v, want ErrUnauthorized", err)
	}
}

// Deleting the parent token must take the key with it. The FK cascade is what
// guarantees it, so this test is really asserting that the schema says
// ON DELETE CASCADE and that nothing above it papers over a stale row.
func TestDeletingTheTokenRevokesTheKey(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "cascade@example.com", model.RoleUser)
	plain := "tok_" + "99998888777766665555444433332222"
	tok := mkToken(t, store, u.ID, "parent", plain, "")
	r := withSecrets(t, mkResolver(store, false))

	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u, Token: tok})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, _, err := r.AccessKey(ctx, issued.Key.AccessKeyID); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := store.DeleteAPIToken(ctx, tok.ID); err != nil {
		t.Fatalf("delete token: %v", err)
	}

	// ⚠ Assert the ROW is gone, not merely that authentication fails.
	// AccessKey also re-checks the parent token and would refuse a stale key
	// anyway — so a test that only checked the refusal would pass with the
	// cascade broken (or with SQLite foreign keys switched off) and quietly
	// leave revoked credentials sitting in the table forever.
	if row, err := store.GetS3AccessKeyByID(ctx, issued.Key.ID); err == nil && row != nil {
		t.Fatalf("the key row survived its parent token: %+v — the ON DELETE CASCADE is not in effect", row)
	}
	if _, _, err := r.AccessKey(ctx, issued.Key.AccessKeyID); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("a key outlived the token it was minted from")
	}
}

// A key sealed under one key must not open under another. Losing
// FILEX_SECRET_KEY makes credentials unusable — which is the honest outcome,
// and much better than authenticating on garbage.
func TestKeySealedUnderADifferentKeyAuthenticatesNobody(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "rotated@example.com", model.RoleUser)

	r := withSecrets(t, mkResolver(store, false))
	issued, err := r.Issue(ctx, protocolauth.IssueRequest{User: u})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	other := mkResolver(store, false)
	box, _ := secretbox.New("a-completely-different-environment-key")
	other.Secrets = box
	if _, _, err := other.AccessKey(ctx, issued.Key.AccessKeyID); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Fatalf("key opened under the wrong secret key: %v", err)
	}
}

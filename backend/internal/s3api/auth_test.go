package s3api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// End to end for the credential: a key minted through the real issuing path,
// a request signed by a real SDK signer, and the caller that comes back out.
//
// Signature verification and key lookup are only useful together — an access
// key id travels in the clear in every request, so a handler that looked a key
// up and skipped the signature would authenticate anyone who has ever seen one.

func setup(t *testing.T) (*s3api.Authenticator, *protocolauth.IssuedKey, db.Store) {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)

	hash, err := authlocal.HashPassword("S3Pass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), "s3@example.com", hash, model.RoleUser, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	res := protocolauth.New(store, acl.New(store), false)
	box, err := secretbox.New("test-environment-secret-key")
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	res.Secrets = box

	issued, err := res.Issue(context.Background(), protocolauth.IssueRequest{User: u, Label: "e2e"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return s3api.NewAuthenticator(res), issued, store
}

func signed(t *testing.T, key *protocolauth.IssuedKey, method, url string, at time.Time) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	req.Header.Set("X-Amz-Content-Sha256", emptyHash)
	s := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: key.Key.AccessKeyID, SecretAccessKey: key.Secret}
	if err := s.SignHTTP(context.Background(), creds, req, emptyHash, "s3", "us-east-1", at); err != nil {
		t.Fatalf("sign: %v", err)
	}

	srv := httptest.NewRequest(method, req.URL.String(), nil)
	srv.Host = req.URL.Host
	for k, vs := range req.Header {
		for _, v := range vs {
			srv.Header.Add(k, v)
		}
	}
	return srv
}

func TestAuthenticateEndToEnd(t *testing.T) {
	auth, key, _ := setup(t)
	at := time.Now().UTC()
	auth.Now = func() time.Time { return at }

	r := signed(t, key, http.MethodGet, "https://s3.filex.sh/main/report.pdf", at)
	p, err := auth.Authenticate(r)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if p == nil || p.User == nil || p.User.Email != "s3@example.com" {
		t.Fatalf("resolved principal = %+v, want the key owner", p)
	}
	if p.AccessKey == nil || p.AccessKey.AccessKeyID != key.Key.AccessKeyID {
		t.Error("the principal does not carry the key it was authenticated by")
	}
}

// The access key id is public. Knowing it must get you nothing.
func TestKnowingTheKeyIDIsNotEnough(t *testing.T) {
	auth, key, _ := setup(t)
	at := time.Now().UTC()
	auth.Now = func() time.Time { return at }

	forged := &protocolauth.IssuedKey{Key: key.Key, Secret: "not-the-real-secret-at-all-000000000000"}
	r := signed(t, forged, http.MethodGet, "https://s3.filex.sh/main/report.pdf", at)
	if _, err := auth.Authenticate(r); !errors.Is(err, s3api.ErrSignatureMismatch) {
		t.Fatalf("a request signed with the wrong secret = %v, want ErrSignatureMismatch", err)
	}
}

// A revoked key stops working immediately, without waiting out any cache.
func TestRevokedKeyStopsAuthenticating(t *testing.T) {
	auth, key, store := setup(t)
	at := time.Now().UTC()
	auth.Now = func() time.Time { return at }

	if _, err := auth.Authenticate(signed(t, key, http.MethodGet, "https://s3.filex.sh/main/a", at)); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := store.SetS3AccessKeyDisabled(context.Background(), key.Key.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := auth.Authenticate(signed(t, key, http.MethodGet, "https://s3.filex.sh/main/a", at)); err == nil {
		t.Fatal("a disabled key still authenticated")
	}
}

// Clients parse the CODE, not the status, so a correct refusal with the wrong
// code looks like a broken server.
func TestErrorCodesAreTheOnesClientsExpect(t *testing.T) {
	cases := map[error]string{
		s3api.ErrNoCredentials:       "AccessDenied",
		s3api.ErrMalformed:           "InvalidRequest",
		s3api.ErrExpired:             "RequestTimeTooSkewed",
		s3api.ErrSignatureMismatch:   "SignatureDoesNotMatch",
		protocolauth.ErrUnauthorized: "SignatureDoesNotMatch",
	}
	for err, want := range cases {
		got, status := s3api.ErrorCode(err)
		if got != want {
			t.Errorf("ErrorCode(%v) = %q, want %q", err, got, want)
		}
		if status < 400 || status > 599 {
			t.Errorf("ErrorCode(%v) status = %d", err, status)
		}
	}
}

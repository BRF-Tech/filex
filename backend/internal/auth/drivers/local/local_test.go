package local

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestHashPassword_RoundTrip checks that HashPassword + bcrypt.Compare
// works end-to-end and that the hash is not the same as the plaintext.
func TestHashPassword_RoundTrip(t *testing.T) {
	pw := "MyStrongPass!123"
	hash, err := HashPassword(pw)
	require.NoError(t, err)
	require.NotEqual(t, pw, hash)
	require.True(t, strings.HasPrefix(hash, "$2"), "expected bcrypt prefix, got %q", hash[:4])

	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)))
}

// TestHashPassword_WrongPassword ensures CompareHashAndPassword fails for
// the wrong plaintext.
func TestHashPassword_WrongPassword(t *testing.T) {
	hash, err := HashPassword("correct")
	require.NoError(t, err)
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte("wrong"))
	require.Error(t, err)
}

// TestLogin_Success seeds a user and verifies Login returns a session token
// and the matching User.
func TestLogin_Success(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, pw := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	user, tok, err := d.Login(context.Background(), email, pw)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, email, user.Email)
	assert.Equal(t, model.RoleAdmin, user.Role)
	assert.NotEmpty(t, tok)
	assert.Len(t, tok, 64, "token should be 64 hex chars (32 bytes)")
}

// TestLogin_WrongPassword returns ErrUnauthorized.
func TestLogin_WrongPassword(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, _ := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	_, _, err := d.Login(context.Background(), email, "definitely-not-the-password")
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized), "expected ErrUnauthorized, got %v", err)
}

// TestLogin_NonexistentUser returns ErrUnauthorized (same shape as
// wrong-pw — mustn't leak whether the user exists).
func TestLogin_NonexistentUser(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	_, _, err := d.Login(context.Background(), "ghost@nowhere", "anything")
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
}

// TestLogin_EmailLowercased ensures Login lowercases the input — common
// usability footgun where users type "Admin@..." but DB has "admin@...".
func TestLogin_EmailLowercased(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, pw := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	user, tok, err := d.Login(context.Background(), strings.ToUpper(email), pw)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, email, user.Email)
}

// TestSessionTokenUniqueness verifies generated session tokens are unique
// across a moderate batch — catches accidental seed mishaps.
func TestSessionTokenUniqueness(t *testing.T) {
	const N = 200
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		tok, err := generateToken()
		require.NoError(t, err)
		require.NotEmpty(t, tok)
		_, dup := seen[tok]
		require.False(t, dup, "duplicate token generated: %s", tok)
		seen[tok] = struct{}{}
	}
}

// TestAuthenticate_FromCookie posts a Login then crafts a request with the
// cookie and expects Authenticate to resolve the same user.
func TestAuthenticate_FromCookie(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, pw := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	user, tok, err := d.Login(context.Background(), email, pw)
	require.NoError(t, err)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	got, err := d.Authenticate(r)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
}

// TestAuthenticate_FromBearer mirrors the cookie test but with the
// Authorization header.
func TestAuthenticate_FromBearer(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, pw := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	_, tok, err := d.Login(context.Background(), email, pw)
	require.NoError(t, err)

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	got, err := d.Authenticate(r)
	require.NoError(t, err)
	assert.Equal(t, email, got.Email)
}

// TestAuthenticate_NoCredentials returns ErrUnauthorized.
func TestAuthenticate_NoCredentials(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	_, err := d.Authenticate(r)
	require.Error(t, err)
	assert.True(t, errors.Is(err, auth.ErrUnauthorized))
}

// TestLogout deletes the session.
func TestLogout(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	email, pw := dbtest.SeedAdmin(t, store)
	d := New(store)
	require.NoError(t, d.Init(context.Background(), nil))

	_, tok, err := d.Login(context.Background(), email, pw)
	require.NoError(t, err)

	require.NoError(t, d.Logout(context.Background(), tok))

	// Subsequent authenticate must fail.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	_, err = d.Authenticate(r)
	require.Error(t, err)
}

// TestCapabilities advertises sign-in + change-password.
func TestCapabilities(t *testing.T) {
	d := &Driver{}
	caps := d.Capabilities()
	assert.True(t, caps.SignIn)
	assert.True(t, caps.Logout)
	assert.True(t, caps.ChangePassword)
	assert.False(t, caps.Register, "local driver should not advertise Register in v0.1")
}

// TestInit_NilStore returns an error.
func TestInit_NilStore(t *testing.T) {
	d := &Driver{}
	err := d.Init(context.Background(), nil)
	require.Error(t, err)
}

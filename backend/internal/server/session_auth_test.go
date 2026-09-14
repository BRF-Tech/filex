package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brf-tech/filex/backend/internal/auth"
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Issue #24: with `FILEX_AUTH_DRIVERS=oidc` — no `local` — nobody could sign in
// at all, and sessions that were already open stopped working too.
//
// Every sign-in, whichever driver judged it, ends in a row in the shared
// sessions table and a `filex_session` cookie. The ONLY enabled driver that
// could turn that cookie back into a user was `local`: the OIDC driver's
// Authenticate answers "unauthorized" by design (its authoritative moment is
// the callback), and so does LDAP's. Leave `local` out of the list and the
// IdP callback minted a perfectly good session that the very next request —
// /api/auth/me — refused, so the browser went back to the sign-in page, round
// and round.
//
// So a session is validated whether or not password sign-in is enabled, the
// way an API token already is. Password sign-in itself stays exactly as
// configured: the validator is not a LoginDriver.
func TestSessionsAuthenticateWhateverDriversAreEnabled(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	userID, _ := testutil.SeedAdminUser(t, store)
	token, err := authlocal.IssueSession(ctx, store, userID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}

	prev := auth.Enabled()
	t.Cleanup(func() { auth.SetEnabled(prev) })

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			t.Error("the handler ran without a user on the context")
		}
		w.WriteHeader(http.StatusOK)
	})

	cases := map[string][]auth.Driver{
		"oidc only (#24)": {authoidc.New(store)},
		"ldap only":       {authldap.New(store)},
		"oidc + ldap":     {authoidc.New(store), authldap.New(store)},
		"no login driver": {},
		"local":           {authlocal.New(store)},
	}
	for name, drivers := range cases {
		t.Run(name, func(t *testing.T) {
			auth.SetEnabled(withSessionAuthenticator(drivers, store))
			h := auth.Middleware(true)(ok)

			req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			req.AddCookie(&http.Cookie{Name: authlocal.SessionCookieName, Value: token})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("a valid session cookie answered %d — signed-in people are sent back to the sign-in page", rec.Code)
			}

			anon := httptest.NewRecorder()
			h.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
			if anon.Code != http.StatusUnauthorized {
				t.Fatalf("a request with no session answered %d, want 401", anon.Code)
			}
		})
	}
}

// The validator is added once, and not at all when `local` — which already
// validates sessions — is in the list: two drivers doing the same lookup would
// cost every request a second database round trip.
func TestSessionAuthenticatorIsNotDuplicated(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	count := func(ds []auth.Driver) int {
		n := 0
		for _, d := range ds {
			if d.Name() == "local" || d.Name() == "session" {
				n++
			}
		}
		return n
	}
	if got := count(withSessionAuthenticator([]auth.Driver{authlocal.New(store)}, store)); got != 1 {
		t.Fatalf("with local enabled: %d session validators, want 1", got)
	}
	once := withSessionAuthenticator([]auth.Driver{authoidc.New(store)}, store)
	if got := count(withSessionAuthenticator(once, store)); got != 1 {
		t.Fatalf("applied twice: %d session validators, want 1", got)
	}
}

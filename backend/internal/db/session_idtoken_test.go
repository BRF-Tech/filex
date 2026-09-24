package db_test

// Migration 00057 + the two session id_token methods, on every engine.
//
// An OIDC sign-in keeps the IdP's id_token beside the filex session so that
// signing out can end the IdP's session too (id_token_hint, OpenID Connect
// RP-Initiated Logout 1.0). Before this, sign-out could only drop filex's own
// cookie: with FILEX_OIDC_AUTO_REDIRECT the login page sent the browser
// straight back to the IdP, whose session was still open, and the same
// account was signed in again without a form — nobody could switch accounts,
// and on a shared computer the next person got the previous one's files.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionIDTokenOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			user, err := store.CreateUser(ctx, "sso@example.com", "", "user", "en", "")
			require.NoError(t, err)
			_, err = store.CreateSession(ctx, user.ID, "tok-sso", time.Now().Add(time.Hour), "", "")
			require.NoError(t, err)

			// A session nobody attached a token to — a password sign-in, or one
			// minted before this version — has none.
			got, err := store.GetSessionIDToken(ctx, "tok-sso")
			require.NoError(t, err)
			require.Empty(t, got)

			// Real ones run to 1–2 KB (Keycloak with roles); well past any
			// VARCHAR(255) a careless column type would silently cut it to.
			idt := "eyJhbGciOiJSUzI1NiJ9." + strings.Repeat("x", 3000) + ".sig"
			require.NoError(t, store.SetSessionIDToken(ctx, "tok-sso", idt))
			got, err = store.GetSessionIDToken(ctx, "tok-sso")
			require.NoError(t, err)
			require.Equal(t, idt, got)

			// Sign-out must never fail on a session it cannot find.
			got, err = store.GetSessionIDToken(ctx, "no-such-session")
			require.NoError(t, err)
			require.Empty(t, got)

			// The token leaves with its session.
			require.NoError(t, store.DeleteSession(ctx, "tok-sso"))
			got, err = store.GetSessionIDToken(ctx, "tok-sso")
			require.NoError(t, err)
			require.Empty(t, got)
		})
	}
}

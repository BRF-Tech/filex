package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// On a public demo the shared account is exempt from the per-ACCOUNT lock -
// otherwise five wrong passwords from anybody lock it for every visitor - and
// the per-ADDRESS limit still stops one address guessing. The account is the
// one FILEX_DEMO_USER names, by its e-mail and by its username.
//
// ⚠ Measured before the fix (2026-10-01): five wrong passwords for the
// published demo account answered 429 "this account is locked" to the next
// visitor typing the right one.

type loginReply struct {
	status    int
	scope     string
	message   string
	remaining float64
}

func (e *secEnv) loginFrom(t *testing.T, ip, identifier, password string) loginReply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/auth/login",
		strings.NewReader(`{"email":"`+identifier+`","password":"`+password+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	r := loginReply{status: resp.StatusCode}
	r.scope, _ = body["scope"].(string)
	r.message, _ = body["message"].(string)
	r.remaining, _ = body["remaining"].(float64)
	return r
}

func demoEnv(t *testing.T, demo bool) (*secEnv, string) {
	t.Helper()
	e := newSecEnv(t, func(c *config.Config) {
		c.Demo.Mode = demo
		c.Demo.User = "demo@demo.com"
	})
	u := seedUser(t, e.store, "demo@demo.com", "DemoPass!1")
	u, err := e.store.GetUser(context.Background(), u.ID)
	require.NoError(t, err)
	require.NotEmpty(t, u.Username)
	// The administrator signed in (newSecEnv) before the demo account existed:
	// the account is looked up again at every tick of the limiter (a minute),
	// which is what makes one seeded after the start exempt by every name.
	e.guard.Tick(context.Background())
	return e, u.Username
}

func TestDemoAccountIsExemptFromTheAccountLock(t *testing.T) {
	e, username := demoEnv(t, true)

	// Seven wrong passwords from one address: never an account lock, and the
	// form counts down the ADDRESS's tries (default 10), not the account's.
	for i := 1; i <= 7; i++ {
		r := e.loginFrom(t, "203.0.113.1", "demo@demo.com", "wrong")
		require.Equal(t, http.StatusUnauthorized, r.status, "attempt %d", i)
		require.Equal(t, "ip", r.scope, "attempt %d", i)
		require.EqualValues(t, 10-i, r.remaining)
		require.Contains(t, r.message, "from this address")
		require.NotContains(t, r.message, "account is locked")
	}
	// …and through the username, from another address.
	for i := 0; i < 6; i++ {
		require.Equal(t, http.StatusUnauthorized, e.loginFrom(t, "203.0.113.2", username, "wrong").status)
	}
	require.Equal(t, http.StatusOK, e.loginFrom(t, "198.51.100.7", "demo@demo.com", "DemoPass!1").status,
		"the next visitor signs in")
	require.Equal(t, http.StatusOK, e.loginFrom(t, "198.51.100.8", username, "DemoPass!1").status)

	// The address that keeps guessing is stopped.
	for i := 0; i < 3; i++ {
		e.loginFrom(t, "203.0.113.1", "demo@demo.com", "wrong")
	}
	r := e.loginFrom(t, "203.0.113.1", "demo@demo.com", "DemoPass!1")
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "ip", r.scope)

	// Every other account is limited as before.
	seedUser(t, e.store, "ada@example.com", "AdaPass!1")
	for i := 0; i < 5; i++ {
		e.loginFrom(t, "192.0.2.1", "ada@example.com", "wrong")
	}
	r = e.loginFrom(t, "192.0.2.2", "ada@example.com", "AdaPass!1")
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "account", r.scope)
}

// Not a demo: the same account is an account like any other.
func TestOrdinaryInstallLocksTheDemoNamedAccount(t *testing.T) {
	e, _ := demoEnv(t, false)
	for i := 0; i < 5; i++ {
		e.loginFrom(t, "203.0.113.1", "demo@demo.com", "wrong")
	}
	r := e.loginFrom(t, "198.51.100.7", "demo@demo.com", "DemoPass!1")
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "account", r.scope)
}

package dav

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/loginguard"
)

// WebDAV re-authenticates every request with HTTP Basic, so it is the protocol
// a password guesser reaches at the highest rate. Its refusals: 401 for a wrong
// password (unchanged), 429 with Retry-After once the sign-in limit's lock is in
// force — and the RIGHT password is refused too while it lasts.

func propfind(t *testing.T, ha *harness, user, pass, xff string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("PROPFIND", ha.srv.URL+Prefix+"/", nil)
	require.NoError(t, err)
	req.SetBasicAuth(user, pass)
	req.Header.Set("Depth", "0")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	req.Header.Set("Accept-Language", "tr")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDAVWrongPasswordsLockWith429(t *testing.T) {
	ha := newHarness(t)
	ha.h.auth.Guard = loginguard.New(ha.store)
	t.Cleanup(clientip.SetSource(nil))

	for i := 0; i < 4; i++ {
		resp := propfind(t, ha, ha.adminEmail, "wrong", "203.0.113.1")
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "attempt %d", i+1)
		require.NotEmpty(t, resp.Header.Get("WWW-Authenticate"), "the challenge is unchanged")
	}
	resp := propfind(t, ha, ha.adminEmail, "wrong", "203.0.113.1")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "the fifth wrong one locks")
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	require.NoError(t, err)
	require.Equal(t, 60, secs)
	require.Empty(t, resp.Header.Get("WWW-Authenticate"), "a lock is not a request to try another password")

	// Right password, same lock: refused. A different address: refused too, the
	// lock is on the account.
	for _, ip := range []string{"203.0.113.1", "203.0.113.77"} {
		resp = propfind(t, ha, ha.adminEmail, ha.adminPass, ip)
		require.Equal(t, http.StatusTooManyRequests, resp.StatusCode, "from %s", ip)
	}
}

func TestDAVAllowlistedAddressGetsThroughALock(t *testing.T) {
	ha := newHarness(t)
	g := loginguard.New(ha.store)
	ha.h.auth.Guard = g
	t.Cleanup(clientip.SetSource(nil))
	require.NoError(t, ha.store.UpsertSetting(t.Context(), loginguard.KeyIPAllowlist, "192.0.2.0/24"))
	g.Invalidate()

	for i := 0; i < 5; i++ {
		propfind(t, ha, ha.adminEmail, "wrong", "203.0.113.1")
	}
	require.Equal(t, http.StatusTooManyRequests, propfind(t, ha, ha.adminEmail, ha.adminPass, "203.0.113.1").StatusCode)
	resp := propfind(t, ha, ha.adminEmail, ha.adminPass, "192.0.2.9")
	require.Less(t, resp.StatusCode, 300, "the allow-listed address signs in to the locked account")
}

func TestDAVWithoutAGuardIsUnchanged(t *testing.T) {
	ha := newHarness(t)
	for i := 0; i < 30; i++ {
		require.Equal(t, http.StatusUnauthorized, propfind(t, ha, ha.adminEmail, "wrong", "203.0.113.1").StatusCode)
	}
	require.Less(t, propfind(t, ha, ha.adminEmail, ha.adminPass, "203.0.113.1").StatusCode, 300)
}

package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The web sign-in limit, measured through the real router: what a person is
// told after a wrong password, the lock, the allow-list, the trusted-proxy
// rule, the second factor, and that a name nobody owns is answered exactly like
// one somebody does.

type throttleEnv struct {
	srv   *httptest.Server
	store db.Store
	guard *loginguard.Guard
	clock time.Time
}

func newThrottleEnv(t *testing.T) *throttleEnv {
	t.Helper()
	e := &throttleEnv{clock: time.Now().UTC()}
	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		e.guard = loginguard.New(d.Store)
		e.guard.Now = func() time.Time { return e.clock }
		d.LoginGuard = e.guard
	})
	e.srv, e.store = srv, store
	// The test client is on 127.0.0.1, a trusted proxy by default, so a test
	// picks the "client" with X-Forwarded-For. Put the default back afterwards.
	t.Cleanup(clientip.SetSource(nil))
	return e
}

func (e *throttleEnv) set(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		require.NoError(t, e.store.UpsertSetting(context.Background(), k, v))
	}
	e.guard.Invalidate()
}

type loginResult struct {
	status int
	header http.Header
	body   map[string]any
}

func (e *throttleEnv) login(t *testing.T, ip, lang, identifier, password string, extra map[string]string) loginResult {
	t.Helper()
	payload := map[string]string{"email": identifier, "password": password}
	for k, v := range extra {
		payload[k] = v
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/auth/login", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return loginResult{resp.StatusCode, resp.Header, out}
}

func TestLoginCountsDownThenLocksWith429(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")

	for i := 1; i <= 4; i++ {
		r := e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
		require.Equal(t, http.StatusUnauthorized, r.status)
		require.Equal(t, "invalid credentials", r.body["error"], "the machine-readable error is unchanged")
		require.EqualValues(t, 5-i, r.body["remaining"])
		require.EqualValues(t, 5, r.body["limit"])
		require.Equal(t, "account", r.body["scope"])
		want := "Wrong credentials. " + strconv.Itoa(5-i) + " attempts left; the account is locked at failed attempt 5."
		if i == 4 {
			want = "Wrong credentials. 1 attempt left; the account is locked at failed attempt 5."
		}
		require.Equal(t, want, r.body["message"])
	}

	r := e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
	require.Equal(t, http.StatusTooManyRequests, r.status, "the fifth wrong attempt locks the account")
	require.Equal(t, true, r.body["locked"])
	require.Equal(t, "60", r.header.Get("Retry-After"))
	require.EqualValues(t, 60, r.body["retry_after"])
	require.Equal(t, "Too many failed attempts: this account is locked. Try again in 1 minute.", r.body["message"])
	// The same sentence with its time left open, for the sign-in form's own
	// countdown (0.54 audit A8: the form kept a copy of these words).
	require.Equal(t, "Too many failed attempts: this account is locked. Try again in {wait}.", r.body["countdown"])

	// The RIGHT password is refused while the lock is in force, from anywhere.
	for _, ip := range []string{"203.0.113.1", "203.0.113.99"} {
		r = e.login(t, ip, "en", "ada@example.com", "RightPass!1", nil)
		require.Equal(t, http.StatusTooManyRequests, r.status, "from %s", ip)
	}

	// The lock ends by itself, and the right password then works.
	e.clock = e.clock.Add(61 * time.Second)
	r = e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", nil)
	require.Equal(t, http.StatusOK, r.status)
}

func TestLoginMessagesFollowTheReadersLanguage(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	r := e.login(t, "203.0.113.1", "tr-TR,tr;q=0.9", "ada@example.com", "wrong", nil)
	require.Equal(t, "Kullanıcı adı ya da parola hatalı. 4 hakkınız kaldı; 5. hatalı denemede hesap kilitlenir.", r.body["message"])
	for i := 0; i < 4; i++ {
		r = e.login(t, "203.0.113.1", "tr", "ada@example.com", "wrong", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "Çok fazla hatalı deneme: hesap kilitlendi. 1 dakika sonra yeniden deneyin.", r.body["message"])
}

func TestLoginAnUnknownNameIsAnsweredLikeARealOne(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	for i := 0; i < 6; i++ {
		real := e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
		ghost := e.login(t, "203.0.113.2", "en", "nobody-at-all@example.com", "wrong", nil)
		require.Equal(t, real.status, ghost.status, "attempt %d", i+1)
		require.Equal(t, real.body, ghost.body, "attempt %d: same body, same numbers, same words", i+1)
		require.Equal(t, real.header.Get("Retry-After"), ghost.header.Get("Retry-After"))
	}
}

func TestLoginAddressLimitAcrossNames(t *testing.T) {
	e := newThrottleEnv(t)
	var r loginResult
	for i := 0; i < 10; i++ {
		r = e.login(t, "198.51.100.7", "en", "user"+strconv.Itoa(i)+"@example.com", "wrong", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "ip", r.body["scope"])
	require.Contains(t, r.body["message"], "from this address")
	// Any name from that address is refused; another address is not.
	require.Equal(t, http.StatusTooManyRequests, e.login(t, "198.51.100.7", "en", "fresh@example.com", "x", nil).status)
	require.Equal(t, http.StatusUnauthorized, e.login(t, "198.51.100.8", "en", "fresh@example.com", "x", nil).status)
}

func TestLoginSuccessResetsTheAccountCounter(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	for i := 0; i < 4; i++ {
		e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
	}
	require.Equal(t, http.StatusOK, e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", nil).status)
	r := e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
	require.Equal(t, http.StatusUnauthorized, r.status, "the four earlier misses are forgotten")
	require.EqualValues(t, 4, r.body["remaining"])
}

func TestLoginAllowlistedAddressIsExemptAndGetsIntoALockedAccount(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "admin@local", "RightPass!1")
	e.set(t, map[string]string{loginguard.KeyIPAllowlist: "192.0.2.0/29"})

	for i := 0; i < 5; i++ {
		e.login(t, "198.51.100.66", "en", "admin@local", "wrong", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, e.login(t, "198.51.100.66", "en", "admin@local", "RightPass!1", nil).status)

	// The allow-listed address gets in, with the right password only.
	require.Equal(t, http.StatusOK, e.login(t, "192.0.2.3", "en", "admin@local", "RightPass!1", nil).status)
	r := e.login(t, "192.0.2.3", "en", "admin@local", "wrong", nil)
	require.Equal(t, http.StatusUnauthorized, r.status, "a wrong password is still a wrong password")
	require.Nil(t, r.body["remaining"], "and there is no count to report: nothing is counted for that address")

	// It does not lift the lock for anybody else.
	require.Equal(t, http.StatusTooManyRequests, e.login(t, "198.51.100.66", "en", "admin@local", "RightPass!1", nil).status)

	// It is exempt from the address limit.
	for i := 0; i < 30; i++ {
		require.Equal(t, http.StatusUnauthorized, e.login(t, "192.0.2.4", "en", "guess"+strconv.Itoa(i)+"@example.com", "x", nil).status)
	}
	// Audited: the pass and every miss.
	rows, err := e.store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	var pass, failed int
	for _, a := range rows {
		switch a.Action {
		case loginguard.ActionAllowlistPass:
			pass++
			require.Equal(t, "192.0.2.3", a.IP)
		case loginguard.ActionFailed:
			failed++
		}
	}
	require.Equal(t, 1, pass)
	require.GreaterOrEqual(t, failed, 5+1+30)
}

func TestLoginForgedForwardedForFromAnUntrustedPeerChangesNothing(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	e.set(t, map[string]string{loginguard.KeyIPAllowlist: "192.0.2.5"})
	// Nobody is a trusted proxy: the socket's peer (127.0.0.1) is the client,
	// whatever the headers say.
	none, err := clientip.ParseList("none")
	require.NoError(t, err)
	restore := clientip.SetSource(func() *clientip.Set { return none })
	t.Cleanup(restore)

	// (1) Rotating forged addresses does not dodge the address limit.
	var r loginResult
	for i := 0; i < 10; i++ {
		r = e.login(t, "203.0.113."+strconv.Itoa(i+1), "en", "user"+strconv.Itoa(i)+"@example.com", "wrong", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "ip", r.body["scope"], "ten misses from one socket lock it, ten different forged headers notwithstanding")

	// (2) Claiming to be the allow-listed address does not get in.
	r = e.login(t, "192.0.2.5", "en", "ada@example.com", "RightPass!1", nil)
	require.Equal(t, http.StatusTooManyRequests, r.status, "a header the client wrote is not an allow-list pass")
}

func TestLoginTrustedProxyHandsOverTheClientAddress(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	// Default trust (loopback): the forwarded address is the client. Ten misses
	// from 198.51.100.7 lock THAT address and leave another one alone.
	for i := 0; i < 10; i++ {
		e.login(t, "198.51.100.7", "en", "user"+strconv.Itoa(i)+"@example.com", "wrong", nil)
	}
	require.Equal(t, http.StatusTooManyRequests, e.login(t, "198.51.100.7", "en", "ada@example.com", "x", nil).status)
	require.Equal(t, http.StatusUnauthorized, e.login(t, "198.51.100.8", "en", "ada@example.com", "x", nil).status)
}

func TestLoginWrongSecondFactorCountsButMissingOneDoesNot(t *testing.T) {
	e := newThrottleEnv(t)
	u := seedUser(t, e.store, "ada@example.com", "RightPass!1")
	const secret = "JBSWY3DPEHPK3PXP"
	require.NoError(t, e.store.SetTotpPendingSecret(context.Background(), u.ID, secret, []string{"AAAAA-BBBBB"}))
	require.NoError(t, e.store.ActivateTotp(context.Background(), u.ID))

	// The form asking for its second step is not a mistake.
	for i := 0; i < 10; i++ {
		r := e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", nil)
		require.Equal(t, http.StatusUnauthorized, r.status)
		require.Equal(t, true, r.body["totp_required"])
		require.Nil(t, r.body["remaining"], "asking for the code counts for nothing")
	}
	// A wrong code is.
	var r loginResult
	for i := 1; i <= 4; i++ {
		r = e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", map[string]string{"totp": "000000"})
		require.Equal(t, http.StatusUnauthorized, r.status)
		require.Equal(t, "invalid two-factor code", r.body["error"])
		require.Equal(t, true, r.body["totp_required"])
		require.EqualValues(t, 5-i, r.body["remaining"])
	}
	r = e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", map[string]string{"totp": "000000"})
	require.Equal(t, http.StatusTooManyRequests, r.status)

	// Locked: even password + a valid live code is refused.
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	r = e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", map[string]string{"totp": code})
	require.Equal(t, http.StatusTooManyRequests, r.status)

	e.clock = e.clock.Add(2 * time.Minute)
	r = e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", map[string]string{"totp": code})
	require.Equal(t, http.StatusOK, r.status)
}

func TestLoginDisabledAccountOrMaintenanceIsNotAWrongPassword(t *testing.T) {
	e := newThrottleEnv(t)
	u := seedUser(t, e.store, "ada@example.com", "RightPass!1")
	require.NoError(t, e.store.SetUserEnabled(context.Background(), u.ID, false))
	for i := 0; i < 8; i++ {
		r := e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", nil)
		require.Equal(t, http.StatusForbidden, r.status, "the password was right; the account is off")
	}
}

func TestLoginTheSwitchTurnsTheLimitOff(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	e.set(t, map[string]string{loginguard.KeyEnabled: "false"})
	for i := 0; i < 40; i++ {
		r := e.login(t, "203.0.113.1", "en", "ada@example.com", "wrong", nil)
		require.Equal(t, http.StatusUnauthorized, r.status)
		require.Nil(t, r.body["remaining"])
	}
	require.Equal(t, http.StatusOK, e.login(t, "203.0.113.1", "en", "ada@example.com", "RightPass!1", nil).status)
}

func TestLoginBodyNeverEchoesThePassword(t *testing.T) {
	e := newThrottleEnv(t)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	const attempt = "Sup3r-Secret-Guess"
	for i := 0; i < 6; i++ {
		r := e.login(t, "203.0.113.1", "en", "ada@example.com", attempt, nil)
		raw, _ := json.Marshal(r.body)
		require.NotContains(t, string(raw), attempt)
	}
	rows, err := e.store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	for _, a := range rows {
		raw, _ := json.Marshal(a)
		require.False(t, strings.Contains(string(raw), attempt), "the audit trail never holds a password: %s", raw)
	}
}

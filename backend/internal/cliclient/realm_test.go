package cliclient

// Signing in to a tenant by its realm (docs/MULTI-TENANCY.md, Realms): the CLI
// sends the realm, follows a handoff to the tenant's own address and redeems it
// there, and answers a wrong realm exactly like a wrong password. The real
// server's side of every step is measured in realm_server_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realmFake is a platform address that knows two realms: "beta" (no address
// of its own - signed in right here) and "acme" (handed to handoffTo).
type realmFake struct {
	mu        sync.Mutex
	bodies    []map[string]any // every login body, in order
	handoffTo string           // origin the acme realm is handed to
	handoffs  int              // requests to /api/auth/handoff on THIS address
}

func (f *realmFake) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/auth/login":
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		if body["password"] != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid credentials","remaining":4,"limit":5,"scope":"account"}`))
			return
		}
		switch realm, _ := body["realm"].(string); realm {
		case "", "beta":
			_, _ = w.Write([]byte(`{"token":"sess-here","user":{"email":"alex@beta.local"}}`))
		case "acme":
			_ = json.NewEncoder(w).Encode(map[string]any{"handoff": map[string]string{"origin": f.handoffTo, "code": "one-use-code"}})
		default:
			// A realm nobody has: the one answer a wrong password gets.
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid credentials","remaining":4,"limit":5,"scope":"account"}`))
		}
	case "/api/auth/handoff":
		f.mu.Lock()
		f.handoffs++
		f.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or expired handoff"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *realmFake) last(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.bodies, "no login reached the server")
	return f.bodies[len(f.bodies)-1]
}

// tenantFake is a tenant's own address: it redeems the code once.
type tenantFake struct {
	mu    sync.Mutex
	paths []string
	codes []string
}

func (f *tenantFake) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	if !strings.HasSuffix(r.URL.Path, "/api/auth/handoff") || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.codes = append(f.codes, req.Code)
	if req.Code != "one-use-code" || len(f.codes) > 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or expired handoff"}`))
		return
	}
	_, _ = w.Write([]byte(`{"token":"sess-tenant","user":{"email":"alex@acme.local"}}`))
}

func newRealmFakes(t *testing.T, tenantBase string) (*realmFake, *httptest.Server, *tenantFake, *httptest.Server) {
	t.Helper()
	tf := &tenantFake{}
	tsrv := httptest.NewServer(http.HandlerFunc(tf.handle))
	t.Cleanup(tsrv.Close)
	pf := &realmFake{handoffTo: tsrv.URL + tenantBase}
	psrv := httptest.NewServer(http.HandlerFunc(pf.handle))
	t.Cleanup(psrv.Close)
	return pf, psrv, tf, tsrv
}

func TestLogin_SendsTheRealm(t *testing.T) {
	pf, psrv, _, _ := newRealmFakes(t, "")
	api := New(Conn{URL: psrv.URL})

	lr, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "beta"})
	require.NoError(t, err)
	assert.Equal(t, "beta", pf.last(t)["realm"], "the realm travels in the body's own field")
	assert.Equal(t, "alex", pf.last(t)["email"])
	assert.Equal(t, "sess-here", lr.Token)
	assert.Equal(t, psrv.URL, lr.URL, "signed in right here")
	assert.False(t, lr.HandedOff)
}

// No realm: the body is what it always was, so an older server - or a
// single-tenant one - sees nothing new.
func TestLogin_NoRealmSendsNoField(t *testing.T) {
	pf, psrv, _, _ := newRealmFakes(t, "")
	api := New(Conn{URL: psrv.URL})

	_, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret"})
	require.NoError(t, err)
	_, has := pf.last(t)["realm"]
	assert.False(t, has)
}

func TestLogin_FollowsTheHandoff(t *testing.T) {
	for _, base := range []string{"", "/filex"} {
		t.Run("base "+base, func(t *testing.T) {
			pf, psrv, tf, tsrv := newRealmFakes(t, base)
			api := New(Conn{URL: psrv.URL})

			lr, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "acme"})
			require.NoError(t, err)
			assert.Equal(t, "sess-tenant", lr.Token, "the session is the tenant address's")
			assert.Equal(t, tsrv.URL+base, lr.URL, "and belongs to the tenant's address, base path and all")
			assert.True(t, lr.HandedOff)
			assert.Equal(t, []string{"one-use-code"}, tf.codes, "redeemed once, at the tenant's address")
			assert.Equal(t, []string{base + "/api/auth/handoff"}, tf.paths)
			assert.Zero(t, pf.handoffs, "never redeemed at the platform's address")
		})
	}
}

// The tenant's address refusing the code (spent, expired - it lives 60 s):
// the error says what happened and what to do, and never prints the code.
func TestLogin_HandoffRefusedExplains(t *testing.T) {
	pf, psrv, _, _ := newRealmFakes(t, "")
	pf.handoffTo = psrv.URL // an address that refuses every code
	api := New(Conn{URL: psrv.URL})

	_, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "acme"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "invalid or expired handoff")
	assert.Contains(t, msg, "--url "+psrv.URL, "says where to sign in instead")
	assert.Contains(t, msg, "60")
	assert.NotContains(t, msg, "one-use-code", "the code is never printed")
}

// The code is a session for a minute: it never goes to an address that is not
// http(s), and never from an encrypted address to an unencrypted one.
func TestLogin_HandoffOriginIsChecked(t *testing.T) {
	tf := &tenantFake{}
	plain := httptest.NewServer(http.HandlerFunc(tf.handle))
	t.Cleanup(plain.Close)

	pf := &realmFake{handoffTo: plain.URL}
	tls := httptest.NewTLSServer(http.HandlerFunc(pf.handle))
	t.Cleanup(tls.Close)
	api := New(Conn{URL: tls.URL})
	api.HTTP = tls.Client()

	_, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "acme"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unencrypted")
	assert.Empty(t, tf.paths, "https -> http: the code was not sent")

	for _, origin := range []string{"", "javascript:alert(1)", "file:///etc/passwd", "ftp://files.acme.test", "//files.acme.test", "https://"} {
		pf.handoffTo = origin
		_, err := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "acme"})
		require.Error(t, err, "origin %q", origin)
		assert.Contains(t, err.Error(), "an address the CLI does not use", "origin %q", origin)
		assert.NotContains(t, err.Error(), "one-use-code")
	}
	assert.Empty(t, tf.paths)
}

// A realm nobody has is answered - and reported - exactly as a wrong password.
func TestLogin_WrongRealmIsAWrongPassword(t *testing.T) {
	_, psrv, _, _ := newRealmFakes(t, "")
	api := New(Conn{URL: psrv.URL})

	_, errRealm := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "s3cret", Realm: "nobody"})
	_, errPass := api.Login(context.Background(), LoginRequest{Email: "alex", Password: "wrong", Realm: "beta"})
	require.Error(t, errRealm)
	require.Error(t, errPass)
	assert.True(t, IsUnauthorized(errRealm))
	assert.Equal(t, errPass.Error(), errRealm.Error())
}

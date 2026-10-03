package main

// `filex client login` and tenant realms: --realm is sent, saved with the
// address in cli.yaml, used again by the next login there (and dropped by
// --realm ""), printed where the CLI says who is signed in, and a handoff to
// the tenant's own address is followed and saved. A wrong realm saves nothing.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// loginFake is a platform address: realm "" and "beta" sign in here, "acme"
// is handed to handoffTo, anything else is a wrong password.
type loginFake struct {
	mu        sync.Mutex
	realms    []*string // the realm of each login; nil = none sent
	handoffTo string
}

func (f *loginFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/auth/login":
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var realm *string
		if v, ok := body["realm"].(string); ok {
			realm = &v
		}
		f.mu.Lock()
		f.realms = append(f.realms, realm)
		f.mu.Unlock()
		name := ""
		if realm != nil {
			name = *realm
		}
		switch {
		case body["password"] != "s3cret":
		case name == "" || name == "beta":
			_, _ = w.Write([]byte(`{"token":"sess-here"}`))
			return
		case name == "acme":
			_ = json.NewEncoder(w).Encode(map[string]any{"handoff": map[string]string{"origin": f.handoffTo, "code": "one-use-code"}})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid credentials"}`))
	case "/api/auth/handoff":
		var req struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Code == "one-use-code" {
			_, _ = w.Write([]byte(`{"token":"sess-tenant"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or expired handoff"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *loginFake) lastRealm(t *testing.T) *string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.realms)
	return f.realms[len(f.realms)-1]
}

// runLogin runs `filex client login <args>` with the password on stdin and
// cli.yaml at cfgPath.
func runLogin(t *testing.T, cfgPath, password string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FILEX_CLI_CONFIG", cfgPath)
	t.Setenv("FILEX_URL", "")
	c := clientCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetIn(strings.NewReader(password + "\n"))
	c.SetArgs(append([]string{"login"}, args...))
	err := c.Execute()
	return out.String(), err
}

func TestClientLogin_RealmIsSentSavedAndReused(t *testing.T) {
	f := &loginFake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")

	out, err := runLogin(t, cfgPath, "s3cret", "--url", srv.URL, "--email", "alex", "--realm", "beta")
	require.NoError(t, err)
	require.NotNil(t, f.lastRealm(t))
	assert.Equal(t, "beta", *f.lastRealm(t))
	assert.Contains(t, out, "Logged in as alex (realm beta) on "+srv.URL)
	cfg, err := cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cliclient.FileConfig{URL: srv.URL, Token: "sess-here", Realm: "beta"}, cfg)

	// The next login at the saved address uses the saved realm.
	out, err = runLogin(t, cfgPath, "s3cret", "--email", "alex")
	require.NoError(t, err)
	require.NotNil(t, f.lastRealm(t), "the saved realm is sent again")
	assert.Equal(t, "beta", *f.lastRealm(t))
	assert.Contains(t, out, "(realm beta)")

	// Another address: the realm saved with the first one is not sent there.
	other := &loginFake{}
	osrv := httptest.NewServer(other)
	t.Cleanup(osrv.Close)
	_, err = runLogin(t, cfgPath, "s3cret", "--url", osrv.URL, "--email", "alex")
	require.NoError(t, err)
	assert.Nil(t, other.lastRealm(t), "a realm belongs to the address it was saved with")
	cfg, err = cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cliclient.FileConfig{URL: osrv.URL, Token: "sess-here"}, cfg)

	// --realm "" is the platform's own accounts, and forgets the saved realm.
	_, err = runLogin(t, cfgPath, "s3cret", "--url", srv.URL, "--email", "alex", "--realm", "beta")
	require.NoError(t, err)
	out, err = runLogin(t, cfgPath, "s3cret", "--email", "alex", "--realm", "")
	require.NoError(t, err)
	assert.Nil(t, f.lastRealm(t), "no realm sent")
	assert.Contains(t, out, "Logged in as alex on "+srv.URL)
	assert.NotContains(t, out, "(realm")
	cfg, err = cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.Realm)
}

func TestClientLogin_HandoffSavesTheTenantsAddress(t *testing.T) {
	tenant := &loginFake{}
	tsrv := httptest.NewServer(tenant)
	t.Cleanup(tsrv.Close)
	f := &loginFake{handoffTo: tsrv.URL}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")

	out, err := runLogin(t, cfgPath, "s3cret", "--url", srv.URL, "--email", "alex", "--realm", "acme")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged in as alex (realm acme) on "+tsrv.URL)
	assert.Contains(t, out, "handed")
	cfg, err := cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cliclient.FileConfig{URL: tsrv.URL, Token: "sess-tenant", Realm: "acme"}, cfg,
		"later commands go to the tenant's own address")

	// --json says the same.
	out, err = runLogin(t, cfgPath, "s3cret", "--url", srv.URL, "--email", "alex", "--realm", "acme", "--json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, true, got["ok"])
	assert.Equal(t, tsrv.URL, got["url"])
	assert.Equal(t, "acme", got["realm"])
	assert.Equal(t, true, got["handed_off"])
	assert.Equal(t, cfgPath, got["config"])
}

func TestClientLogin_WrongRealmSavesNothing(t *testing.T) {
	f := &loginFake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	before := cliclient.FileConfig{URL: srv.URL, Token: "sess-old", Realm: "beta"}
	require.NoError(t, cliclient.SaveFileConfig(cfgPath, before))
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	_, err = runLogin(t, cfgPath, "s3cret", "--email", "alex", "--realm", "nobody")
	require.Error(t, err)
	assert.True(t, cliclient.IsUnauthorized(err))
	assert.Contains(t, err.Error(), "invalid credentials")
	assert.Contains(t, err.Error(), `realm "nobody"`, "the one answer, and the realm named among what may be wrong")
	after, rerr := os.ReadFile(cfgPath)
	require.NoError(t, rerr)
	assert.Equal(t, string(raw), string(after), "a refused login leaves cli.yaml as it was")

	// The saved realm, refused: the hint says it was the saved one and how to
	// sign in without it.
	_, err = runLogin(t, cfgPath, "wrong", "--email", "alex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `realm "beta"`)
	assert.Contains(t, err.Error(), `--realm ""`)
}

func TestClientLogin_RealmFlagAndHelp(t *testing.T) {
	c := clientCmd()
	login, _, err := c.Find([]string{"login"})
	require.NoError(t, err)
	require.NotNil(t, login.Flags().Lookup("realm"), "login takes --realm")
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"login", "--help"})
	require.NoError(t, c.Execute())
	help := out.String()
	assert.Contains(t, help, "--realm")
	assert.Contains(t, help, "multi-tenant")
	assert.Contains(t, help, "handoff")
}

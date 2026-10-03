package main

// The session saved in cli.yaml belongs to the address it was saved with.
// `filex client`, `filex sync` and `filex mount` resolve their connection the
// same way (clientOpts.api), and none of them may send that token to another
// address named by --url or FILEX_URL: that would hand one server's session to
// another. A token given on purpose (--token, FILEX_TOKEN) goes where it is
// sent.
//
// RED PROOF (before 0.50's fix): `filex client ls --url <other>` sent the
// saved token to <other> in its Authorization header.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// tokenSpy answers every request with an empty listing and remembers the
// Authorization header of each.
type tokenSpy struct {
	mu    sync.Mutex
	auths []string
}

func (s *tokenSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.auths = append(s.auths, r.Header.Get("Authorization"))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"adapter":"docs","storages":["docs"],"files":[]}`))
}

func (s *tokenSpy) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// connEnv points the CLI at a cli.yaml saved for `saved` with token
// "saved-token", and clears the environment's own connection.
func connEnv(t *testing.T, saved string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, cliclient.SaveFileConfig(cfgPath, cliclient.FileConfig{URL: saved, Token: "saved-token"}))
	t.Setenv("FILEX_CLI_CONFIG", cfgPath)
	t.Setenv("FILEX_URL", "")
	t.Setenv("FILEX_TOKEN", "")
	t.Setenv("FILEX_SYNC_DIR", filepath.Join(t.TempDir(), "sync"))
}

// runConn runs `filex <root> <args...>`.
func runConn(t *testing.T, root string, args ...string) error {
	t.Helper()
	roots := map[string]func() *cobra.Command{"client": clientCmd, "sync": syncCmd, "mount": mountCmd}
	build, ok := roots[root]
	require.True(t, ok, "unknown root %q", root)
	c := build()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	return c.Execute()
}

func TestSavedToken_StaysWithItsAddress(t *testing.T) {
	home := &tokenSpy{}
	hsrv := httptest.NewServer(home)
	t.Cleanup(hsrv.Close)
	other := &tokenSpy{}
	osrv := httptest.NewServer(other)
	t.Cleanup(osrv.Close)
	connEnv(t, hsrv.URL)

	// The saved address: the saved session.
	require.NoError(t, runConn(t, "client", "ls"))
	require.NoError(t, runConn(t, "client", "ls", "--url", hsrv.URL+"/"))
	assert.Equal(t, []string{"Bearer saved-token", "Bearer saved-token"}, home.seen())

	// Another address: refused before anything is sent, and the error says
	// what to do.
	for _, cmd := range [][]string{
		{"client", "ls", "--url", osrv.URL},
		{"sync", "run", "--url", osrv.URL},
		{"mount", "--url", osrv.URL, t.TempDir()},
	} {
		t.Run(cmd[0], func(t *testing.T) {
			err := runConn(t, cmd[0], cmd[1:]...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no saved session for "+osrv.URL)
			assert.Contains(t, err.Error(), "filex client login --url "+osrv.URL)
			assert.Contains(t, err.Error(), "FILEX_TOKEN")
			assert.NotContains(t, err.Error(), "saved-token", "the token is never printed")
			assert.Empty(t, other.seen(), "the saved token never reached another server")
		})
	}

	// FILEX_URL naming another server: the same.
	t.Setenv("FILEX_URL", osrv.URL)
	err := runConn(t, "client", "ls")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no saved session for "+osrv.URL)
	assert.Empty(t, other.seen())

	// A token given on purpose goes where it is sent.
	require.NoError(t, runConn(t, "client", "ls", "--token", "flag-token"))
	t.Setenv("FILEX_TOKEN", "env-token")
	require.NoError(t, runConn(t, "client", "ls"))
	assert.Equal(t, []string{"Bearer flag-token", "Bearer env-token"}, other.seen())
}

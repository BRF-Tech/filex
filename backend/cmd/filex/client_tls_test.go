package main

// `filex client` against a server with a certificate nothing trusts (#298):
// --insecure-skip-tls-verify at login is saved with the address, a later command
// uses it, another address does not, and =false takes it back.

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

func selfSignedLogin(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(&loginFake{})
	t.Cleanup(srv.Close)
	return srv
}

// runTLSClient is runLogin with stderr kept, and any subcommand.
func runTLSClient(t *testing.T, cfgPath, stdin string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("FILEX_CLI_CONFIG", cfgPath)
	t.Setenv("FILEX_URL", "")
	t.Setenv("FILEX_TOKEN", "")
	t.Setenv(cliclient.EnvInsecureSkipTLSVerify, "")
	c := clientCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetIn(strings.NewReader(stdin))
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), errOut.String(), err
}

func TestClientLogin_ASelfSignedServerNeedsTheSwitch(t *testing.T) {
	srv := selfSignedLogin(t)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")

	_, _, err := runTLSClient(t, cfgPath, "s3cret\n", "login", "--url", srv.URL, "--email", "alex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")
	cfg, err := cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Empty(t, cfg.Token, "a refused server saved nothing")
}

func TestClientLogin_TheSwitchIsSavedWithTheAddressAndSaysWhatItCosts(t *testing.T) {
	srv := selfSignedLogin(t)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")

	_, stderr, err := runTLSClient(t, cfgPath, "s3cret\n", "login", "--url", srv.URL, "--email", "alex", "--insecure-skip-tls-verify")
	require.NoError(t, err)
	assert.Contains(t, stderr, cliclient.InsecureNotice)
	cfg, err := cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cliclient.FileConfig{URL: srv.URL, Token: "sess-here", InsecureSkipTLSVerify: true}, cfg)

	// The next login there needs no flag.
	_, _, err = runTLSClient(t, cfgPath, "s3cret\n", "login", "--email", "alex")
	require.NoError(t, err)
	cfg, err = cliclient.LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.True(t, cfg.InsecureSkipTLSVerify, "it stays with the address, like the realm")
}

func TestClientLogin_FalseTakesTheSwitchBack(t *testing.T) {
	srv := selfSignedLogin(t)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	_, _, err := runTLSClient(t, cfgPath, "s3cret\n", "login", "--url", srv.URL, "--email", "alex", "--insecure-skip-tls-verify")
	require.NoError(t, err)

	_, _, err = runTLSClient(t, cfgPath, "s3cret\n", "login", "--email", "alex", "--insecure-skip-tls-verify=false")
	require.Error(t, err, "said false, the check is on again and this server fails it")
	assert.Contains(t, err.Error(), "certificate")
}

func TestClientLogin_AnotherAddressDoesNotInheritIt(t *testing.T) {
	first := selfSignedLogin(t)
	second := selfSignedLogin(t)
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	_, _, err := runTLSClient(t, cfgPath, "s3cret\n", "login", "--url", first.URL, "--email", "alex", "--insecure-skip-tls-verify")
	require.NoError(t, err)

	_, _, err = runTLSClient(t, cfgPath, "s3cret\n", "login", "--url", second.URL, "--email", "alex")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")
}

func TestClientOpts_TheFlagBeatsTheFileBothWays(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, cliclient.SaveFileConfig(cfgPath, cliclient.FileConfig{URL: "https://nas.example.com", Token: "t", InsecureSkipTLSVerify: true}))

	conn, err := cliclient.Resolve("", "", cfgPath, func(string) string { return "" })
	require.NoError(t, err)
	said := tlsFlag{}
	require.NoError(t, said.Set("false"))
	said.apply(&conn)
	assert.False(t, conn.InsecureSkipTLSVerify)

	require.NoError(t, said.Set("true"))
	said.apply(&conn)
	assert.True(t, conn.InsecureSkipTLSVerify)

	quiet := tlsFlag{}
	quiet.apply(&conn)
	assert.True(t, conn.InsecureSkipTLSVerify, "not said: what the connection answered stands")
}

// Everything that connects takes the flag: a command without it could not reach
// the server the rest reach.
func TestEveryCommandThatConnectsTakesTheFlag(t *testing.T) {
	for name, c := range map[string]*cobra.Command{
		"client": clientCmd(), "sync": syncCmd(), "mount": mountCmd(),
		"encrypt": encryptCmd(), "decrypt": decryptCmd(), "vault prune": vaultPruneCmd(),
	} {
		require.NotNil(t, c.Flag(tlsFlagName), "%s has no --%s", name, tlsFlagName)
	}
}

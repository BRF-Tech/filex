package cliclient

// A filex server with a certificate nothing trusts (#298). httptest's TLS
// server is exactly that: every test here goes over real TLS.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tlsEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func TestResolve_TheSwitchBelongsToTheSavedAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "https://nas.example.com", Token: "t", InsecureSkipTLSVerify: true}))

	saved, err := Resolve("", "", path, tlsEnv(nil))
	require.NoError(t, err)
	assert.True(t, saved.InsecureSkipTLSVerify)

	same, err := Resolve("https://NAS.example.com:443/", "", path, tlsEnv(nil))
	require.NoError(t, err)
	assert.True(t, same.InsecureSkipTLSVerify, "the same address, however it is written")

	other, err := Resolve("https://other.example.com", "", path, tlsEnv(nil))
	require.NoError(t, err)
	assert.False(t, other.InsecureSkipTLSVerify, "what one address was allowed says nothing about another")

	env, err := Resolve("", "", path, tlsEnv(map[string]string{"FILEX_URL": "https://other.example.com"}))
	require.NoError(t, err)
	assert.False(t, env.InsecureSkipTLSVerify)
}

func TestResolve_TheEnvironmentSaysItForOneRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "https://nas.example.com", Token: "t", InsecureSkipTLSVerify: true}))

	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", true},       // not said: the saved answer
		{"false", false}, // said: it beats the file, both ways
		{"0", false},
		{"true", true},
		{"1", true},
		{"banana", false}, // reads as nothing: the check stays on
	} {
		conn, err := Resolve("", "", path, tlsEnv(map[string]string{EnvInsecureSkipTLSVerify: tc.value}))
		require.NoError(t, err)
		assert.Equal(t, tc.want, conn.InsecureSkipTLSVerify, "%q", tc.value)
	}

	// With nothing saved, for the desktop's engine: FILEX_URL + the switch.
	conn, err := Resolve("", "", filepath.Join(t.TempDir(), "none.yaml"), tlsEnv(map[string]string{
		"FILEX_URL": "https://nas.example.com", EnvInsecureSkipTLSVerify: "1",
	}))
	require.NoError(t, err)
	assert.True(t, conn.InsecureSkipTLSVerify)
}

// A file without the key reads as off, and off is not written: an older CLI
// reads the same file.
func TestFileConfig_TheSwitchIsWrittenOnlyWhenOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")

	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "u", Token: "t"}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "insecure")

	require.NoError(t, SaveFileConfig(path, FileConfig{URL: "u", Token: "t", InsecureSkipTLSVerify: true}))
	back, err := LoadFileConfig(path)
	require.NoError(t, err)
	assert.True(t, back.InsecureSkipTLSVerify)
}

func selfSignedAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[],"dirname":"docs://"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClient_ASelfSignedServerIsRefusedUnlessTheConnectionSaysOtherwise(t *testing.T) {
	srv := selfSignedAPI(t)

	_, err := New(Conn{URL: srv.URL, Token: "t"}).List(context.Background(), "docs://")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")

	_, err = New(Conn{URL: srv.URL, Token: "t", InsecureSkipTLSVerify: true}).List(context.Background(), "docs://")
	require.NoError(t, err)
}

// The switch reaches the transport HTTP/2 was configured on, not a copy of it.
func TestClient_TheSwitchKeepsHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proto", r.Proto)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := New(Conn{URL: srv.URL, InsecureSkipTLSVerify: true})
	resp, err := c.HTTP.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "HTTP/2.0", resp.Header.Get("X-Proto"))
}

// The change stream dials with a client of its own: the switch has to reach it,
// or sync falls back to polling on the one server that needed the switch.
func TestChangeStream_TheHandshakeHonoursTheSwitch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	t.Cleanup(srv.Close)
	url := "wss" + srv.URL[len("https"):]

	_, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPClient: wsHTTPClient(false)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")

	conn, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{HTTPClient: wsHTTPClient(true)})
	require.NoError(t, err)
	_ = conn.CloseNow()
}

func TestNew_TheStreamIsGivenTheConnectionsAnswer(t *testing.T) {
	assert.True(t, New(Conn{URL: "https://x", InsecureSkipTLSVerify: true}).skipTLSVerify)
	assert.False(t, New(Conn{URL: "https://x"}).skipTLSVerify)
}

package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// One IPv6 machine is handed a whole /64: its addresses share one bucket, so
// a caller cannot register past the limit by rotating through them.
func TestOAuthLimiterCountsAnIPv6NetworkOnce(t *testing.T) {
	var l oauthLimiter
	r := httptest.NewRequest("POST", "/api/oauth/register", nil)
	for i := 0; i < oauthBurst; i++ {
		r.RemoteAddr = "[2001:db8:1:2::" + string(rune('a'+i%6)) + "]:443"
		require.True(t, l.allow(r), i)
	}
	r.RemoteAddr = "[2001:db8:1:2:ffff::1]:443"
	require.False(t, l.allow(r), "the same /64")
	r.RemoteAddr = "[2001:db8:1:3::1]:443"
	require.True(t, l.allow(r), "another /64")
	r.RemoteAddr = "192.0.2.7:443"
	require.True(t, l.allow(r))
}

func TestOAuthRedirectMatching(t *testing.T) {
	for _, c := range []struct {
		registered, asked string
		ok                bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/auth_callback", true},
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/auth_callback/", false},
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai:444/api/mcp/auth_callback", false},
		{"https://claude.ai/cb", "https://CLAUDE.ai/cb", false},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:65000/cb", true},
		{"http://[::1]:1/cb", "http://[::1]:2/cb", true},
		{"http://localhost/cb", "http://localhost:8080/cb", true},
		{"http://localhost/cb", "http://127.0.0.1:8080/cb", false},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:2/cb#x", false},
		{"http://127.0.0.1:1/cb", "http://u:p@127.0.0.1:2/cb", false},
	} {
		require.Equal(t, c.ok, redirectMatches(c.registered, c.asked), "%s vs %s", c.registered, c.asked)
	}
	for _, raw := range []string{"https://a.example/cb", "http://127.0.0.1:9/cb", "http://localhost:9/cb", "http://app.localhost/cb"} {
		require.True(t, validOAuthRedirect(raw), raw)
	}
	for _, raw := range []string{"http://a.example/cb", "https://a b.example/cb", "https://a.example/cb#f", "javascript:alert(1)", "custom-app://cb", "https://exa_mple.com/cb"} {
		require.False(t, validOAuthRedirect(raw), raw)
	}
}

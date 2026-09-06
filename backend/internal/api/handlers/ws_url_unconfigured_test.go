package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An instance that was never told its public URL used to hand every browser
// `ws://localhost:5212/api/ws`, because that string is filex's default guess
// and the ticket derived the socket address from it. On any other port the
// connection was refused and RealtimeClient dropped to 12-second polling —
// silently, with the server perfectly healthy and nothing logged. The symptom
// reads as "live updates do not work"; the cause is an address nobody is
// listening on.
//
// Measured 2026-09-06 on a real instance at 127.0.0.1:5299: the ticket
// returned ws://localhost:5212/api/ws and the socket client failed to connect.
//
// ⚠ Taking the origin from the request is safe HERE and nowhere else in this
// codebase: the ticket goes back to the client that asked for it, so a forged
// Host misleads only its sender about its own socket. A share link is mailed
// to somebody else, which is why those keep using the configured origin — the
// last case below pins that difference so a future change cannot blur it.
func wsTicketURL(t *testing.T, h *http.Handler, host string, tls bool) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/files/ws-ticket", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &model.User{ID: 1, Email: "a@short.test"}))
	req.Host = host
	if tls {
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	rec := httptest.NewRecorder()
	(*h).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		WsURL string `json:"ws_url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.WsURL
}

func TestWSTicketURL_UnconfiguredPublicURLFollowsTheRequest(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	wsh := handlers.NewWS(store, nil, realtime.NewHub(), realtime.NewTicketStore(),
		"http://localhost:5212") // the default guess, as an unconfigured install has it
	wsh.AttachPublicURLConfigured(false)

	var h http.Handler = http.HandlerFunc(wsh.Ticket)

	assert.Equal(t, "ws://127.0.0.1:5299/api/ws", wsTicketURL(t, &h, "127.0.0.1:5299", false),
		"an instance on another port must not send the browser to :5212")
	assert.Equal(t, "wss://files.example.com/api/ws", wsTicketURL(t, &h, "files.example.com", true),
		"behind a TLS-terminating proxy the socket has to be wss, or the browser refuses it as mixed content")
}

func TestWSTicketURL_ConfiguredPublicURLWins(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	wsh := handlers.NewWS(store, nil, realtime.NewHub(), realtime.NewTicketStore(),
		"https://fm.example.com")
	wsh.AttachPublicURLConfigured(true)

	var h http.Handler = http.HandlerFunc(wsh.Ticket)

	// ⚠ The operator has stated the origin, so a Host header — forged or
	// merely internal (a health checker, a sidecar, a container name) — must
	// not redirect the socket somewhere else.
	assert.Equal(t, "wss://fm.example.com/api/ws", wsTicketURL(t, &h, "evil.example", false),
		"a configured public URL is a decision, not a hint")
	assert.Equal(t, "wss://fm.example.com/api/ws", wsTicketURL(t, &h, "127.0.0.1:5299", false))
}

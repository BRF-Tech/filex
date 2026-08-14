package handlers_test

/* The profile picture, along the path it actually travels: profile → account →
   ticket → presence roster.

   The requirement is "set it once and every API key I mint shows it", so the
   picture is resolved from the ACCOUNT the connection authenticates as, not
   from the client. The interesting half is the exception: a shared proxy token
   speaks for many people, and drawing the token owner's face on somebody
   else's row would be a worse lie than initials. */

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A 1x1 gif as a data URI — small, and a shape the client will actually draw.
const testAvatarURI = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

func TestWSTicketAvatar(t *testing.T) {
	burak := &model.User{ID: 3, DisplayName: "Burak", Email: "burak@brf.sh", AvatarURL: testAvatarURI}

	// The account's own session.
	require.Equal(t, testAvatarURI, mintVia(t, burak, nil, "", nil).Avatar)

	// ⭐ The point of the feature: a key minted under the account carries the
	// account's face, so the desktop app / CLI / any personal token shows it.
	desktop := &model.APIToken{ID: 12, UserID: 3, Label: "filex desktop — Win32"}
	got := mintVia(t, burak, desktop, "", nil)
	require.Equal(t, "Burak (filex desktop)", got.Name)
	require.Equal(t, testAvatarURI, got.Avatar, "an API key minted under the account is that person")

	// ⚠ A shared proxy token is not a person. The entry reads "work", and the
	// owner's photo on it would claim every work user is Burak.
	shared := &model.APIToken{ID: 6, UserID: 3, Label: "shared", Usernames: "work,fishapp"}
	viaShared := mintVia(t, burak, shared, "work", nil)
	require.Equal(t, "work", viaShared.Name)
	require.Empty(t, viaShared.Avatar, "a shared proxy token must not wear its owner's face")

	// A host that re-identifies the connection as a real end user may supply
	// THAT person's picture; without one the row falls back to initials rather
	// than borrowing the token owner's.
	stamped := mintVia(t, burak, shared, "work", map[string]string{
		"X-Filex-Presence-Name": "Gökçil",
	})
	require.Equal(t, "Gökçil (work)", stamped.Name)
	require.Empty(t, stamped.Avatar, "a different human with no picture of their own gets initials")

	withPhoto := mintVia(t, burak, shared, "work", map[string]string{
		"X-Filex-Presence-Name":   "Gökçil",
		"X-Filex-Presence-Avatar": testAvatarURI,
	})
	require.Equal(t, testAvatarURI, withPhoto.Avatar)

	// Junk in the header is dropped, not forwarded into everyone's DOM.
	junk := mintVia(t, burak, shared, "work", map[string]string{
		"X-Filex-Presence-Name":   "Gökçil",
		"X-Filex-Presence-Avatar": "javascript:alert(1)",
	})
	require.Empty(t, junk.Avatar, "only image sources are accepted")

	// An account without a picture keeps the initials behaviour.
	plain := &model.User{ID: 9, DisplayName: "Kimse"}
	require.Empty(t, mintVia(t, plain, nil, "", nil).Avatar)
}

// TestWSPresenceCarriesAvatar walks the last hop: what a second viewer in the
// same folder actually receives.
func TestWSPresenceCarriesAvatar(t *testing.T) {
	burak := &model.User{ID: 3, DisplayName: "Burak", Email: "burak@brf.sh", AvatarURL: testAvatarURI}
	gokcil := &model.User{ID: 4, DisplayName: "Gökçil", Email: "gokcil@brf.sh"}

	_, store := testutil.NewTestDB(t)
	_, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"/tmp/ws-avatar-test"}`),
	})
	require.NoError(t, err)

	hub := realtime.NewHub()
	wsh := handlers.NewWS(store, nil, hub, nil, "")
	// One server per user: each connection authenticates as a different person.
	dial := func(u *model.User) *websocket.Conn {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsh.Handle(w, r.WithContext(auth.WithUser(r.Context(), u)))
		}))
		t.Cleanup(srv.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 4e9)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", nil)
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
		return conn
	}

	burakConn := dial(burak)
	wsSend(t, burakConn, map[string]any{"type": "subscribe", "path": "main://projeler"})
	wsReadType(t, burakConn, "presence")

	gokcilConn := dial(gokcil)
	wsSend(t, gokcilConn, map[string]any{"type": "subscribe", "path": "main://projeler"})

	frame := wsReadType(t, gokcilConn, "presence")
	users, _ := frame["users"].([]any)
	require.Len(t, users, 1, "Gökçil should see exactly Burak")
	entry, _ := users[0].(map[string]any)
	require.Equal(t, "Burak", entry["name"])
	require.Equal(t, testAvatarURI, entry["avatar"], "the roster must carry the picture, not just the name")

	// And the other way round: no picture → no key at all, so the client keeps
	// its initials fallback instead of rendering an empty <img>.
	back := wsReadType(t, burakConn, "presence")
	others, _ := back["users"].([]any)
	require.Len(t, others, 1)
	gEntry, _ := others[0].(map[string]any)
	require.Equal(t, "Gökçil", gEntry["name"])
	_, hasAvatar := gEntry["avatar"]
	require.False(t, hasAvatar, "an account with no picture must not send an empty avatar")
}

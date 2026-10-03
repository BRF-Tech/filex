package handlers_test

// The header proxy's first-login refusal (0.50, docs/LDAP.md "The first
// sign-in rule"): the proxy has said who the person is, so the 401 of every
// request it names them in carries the reason code for the sign-in page; a
// request it names nobody in is the plain 401 it always was. The code is
// written out as a string: it is the wire contract with
// web/src/lib/ssoRefusal.ts (red proof against the code before it).

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The header proxy names the person; the first-login rule refuses them. The
// 401 of every request says why; a request the proxy names nobody in is the
// plain 401 it always was.
func TestHeaderProxyRefusal_TheReasonRidesOnTheMeAnswer(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	status, body := patchProvider(t, client, srv.URL, "proxy-header", map[string]any{"enabled": true,
		"confirm_failed_test": true, "config": map[string]any{"trusted_proxies": "127.0.0.1/32, ::1/128", "auto_create": false}})
	require.Equal(t, http.StatusOK, status, "%v", body)

	me := func(user string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/me", nil)
		require.NoError(t, err)
		if user != "" {
			req.Header.Set("X-Auth-User", user)
		}
		resp, err := freshClient(t).Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		raw, _ := json.Marshal(out)
		return resp.StatusCode, string(raw)
	}
	code, raw := me("newbie@corp.test")
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.JSONEq(t, `{"error":"unauthorized","reason":"auto_create_off"}`, raw)
	_, gerr := store.GetUserByEmail(context.Background(), "newbie@corp.test")
	assert.Error(t, gerr)

	code, raw = me("")
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, raw, "nobody named: unchanged")

	// In: an account that exists signs in through the header as before.
	_, err := store.CreateUser(context.Background(), "known@corp.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	code, _ = me("known@corp.test")
	assert.Equal(t, http.StatusOK, code)
}

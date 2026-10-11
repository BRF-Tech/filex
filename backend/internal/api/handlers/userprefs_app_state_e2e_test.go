package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filex 0.56 (task #189): what an app keeps while it holds a file of an
// encrypted folder in the clear is sealed in the browser (packages/core
// lib/appStateE2e) and stored in its own store, `appStateE2e`, apart from
// the ordinary `appState`. The server cannot read it and holds it to the
// same share per app, measured on the ciphertext it stores.
//
// Red before: `appStateE2e` was not judged at all - an app could park the
// whole 64 KiB document there.
func TestUserPrefs_AnAppsEncryptedStateIsHeldToItsShareToo(t *testing.T) {
	h, _, ada, _ := newPrefsFixture(t)
	sealed := func(n int) string {
		b, _ := json.Marshal(map[string]any{"office-editor": map[string]any{"bGVkZ2VyLWlk": "AQ" + strings.Repeat("x", n)}})
		return string(b)
	}

	doc, _ := json.Marshal(map[string]any{"prefs": map[string]any{"appStateE2e": sealed(1 << 10)}})
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", string(doc)).Code)
	assert.Contains(t, prefsGet(t, h, ada, "web").Body.String(), "bGVkZ2VyLWlk", "kept as the page sent it")

	doc, _ = json.Marshal(map[string]any{"prefs": map[string]any{"appStateE2e": sealed(17 << 10)}})
	rec := prefsPut(t, h, ada, "web", string(doc))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "APP_STATE_TOO_LARGE")
	assert.Contains(t, rec.Body.String(), `"app":"office-editor"`)

	// The two stores are judged apart: each app has its share in each.
	plain, _ := json.Marshal(map[string]any{"office-editor": map[string]any{"k": strings.Repeat("p", 15<<10)}})
	doc, _ = json.Marshal(map[string]any{"prefs": map[string]any{"appState": string(plain), "appStateE2e": sealed(15 << 10)}})
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", string(doc)).Code)
}

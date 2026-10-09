package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// An app's store link refused, through the production router: `message` is
// the server's sentence in the reader's language (handlers/app_store_words.go),
// Go's English is detail.reason, and the panel prints `message` as it came.
//
// RED PROOF (int/055-wave b5c58508): the rollback answered "lang-eo 1.1.0 is
// installed; the link is for 1.0.0, which is not newer. ..." in English to a
// Turkish reader, and a review that was no longer open "this review is no
// longer open; open the store's install link again"; the panel threw both
// away and said its own copy (web lib/storeRefusal.ts, appStore.err.*).
func TestStoreInstall_AnAppsRefusalIsTheServersSentence(t *testing.T) {
	f := newAsFix(t)
	f.trust(t)
	f.publish("tokentoken-old", "1.0.0", "Nuligi", nil)
	f.publish("tokentoken-1", "1.1.0", "Nuligi", nil)
	_, rb, _ := f.review(t, "tokentoken-1")
	code, body := f.install(t, rb, nil)
	require.Equal(t, http.StatusCreated, code, "%s", body)

	var refusal struct {
		Error   string         `json:"error"`
		Message string         `json:"message"`
		Detail  map[string]any `json:"detail"`
	}
	// The reader is the administrator's account, which speaks Turkish here:
	// requestLang reads the account's language before Accept-Language, so a
	// header cannot turn testutil's "en" administrator Turkish.
	require.NoError(t, f.store.UpdateUserLocale(t.Context(), f.adminID, "tr", "UTC"))
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent",
		map[string]any{"store": f.st.Origin(), "token": "tokentoken-old"})
	require.Equal(t, http.StatusConflict, code, "%s", body)
	require.NoError(t, json.Unmarshal(body, &refusal))
	assert.Equal(t, appstore.CodeVersionRollback, refusal.Error)
	assert.Equal(t, srvtext.Text("tr", "server.store.intent_version_rollback", srvtext.Vars{"installed": "1.1.0", "link": "1.0.0"}), refusal.Message)
	assert.Contains(t, refusal.Detail["reason"], "is not newer", "Go's English stays beside it, for a log")
	assert.NotEqual(t, appstore.KindStorage, refusal.Detail["kind"])
	assert.NotEmpty(t, refusal.Detail["kind"], "an app's refusal says what it is about, like a storage plugin's")

	// A refusal internal/appstore makes with no detail at all: said too.
	refusal.Detail = nil
	code, body = f.call(t, "", http.MethodPost, "/api/admin/app-plugins/store-intent/install",
		map[string]any{"handle": "no-such-review", "permissions": []string{}})
	require.Equal(t, http.StatusNotFound, code, "%s", body)
	require.NoError(t, json.Unmarshal(body, &refusal))
	assert.Equal(t, appstore.CodeIntentNotFound, refusal.Error)
	assert.Equal(t, srvtext.Text("tr", "server.store.intent_session_unknown", nil), refusal.Message)
	assert.NotEmpty(t, refusal.Detail["reason"])
}

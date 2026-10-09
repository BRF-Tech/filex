package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The one shape of a refusal (internal/apierr, docs/API-ERRORS.md, 0.54 audit
// A1/A2): `error` is a code, `message` is the server's sentence in the
// reader's language, and every client prints `message`.
//
// RED PROOF (int/054-wave d4107407): a rename on a read-only storage answered
// 403 {"error":"storage is read-only"} - an English sentence in the code's
// place and no `message` - and the explorer matched the English with a
// regular expression to say it; the queue's READ_ONLY said "destination
// storage is read-only: <name>" in English to every language.

// langJSON is doJSON with an Accept-Language: the reader's language when the
// account has none of its own.
func langJSON(t *testing.T, client *http.Client, method, url, lang string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", lang)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestErrorEnvelope_AReadOnlyStorageIsACodeAndTheReadersSentence(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	f.seedFile(t, f.StA2, f.RootA2, "arsiv.txt", "kept as it is")
	ro, err := f.Store.GetStorage(ctx, f.StA2.ID)
	require.NoError(t, err)
	ro.ReadOnly = true
	require.NoError(t, f.Store.UpdateStorage(ctx, ro))
	// The reader's language is the account's (requestLang), then the browser's.
	require.NoError(t, f.Store.UpdateUserLocale(ctx, f.UserA, "tr", "UTC"))

	status, body := langJSON(t, f.A, http.MethodPost, f.URL+"/api/files/manager?action=rename", "tr",
		map[string]any{"path": "alpha-arsiv://", "item": "alpha-arsiv://arsiv.txt", "name": "yeni.txt"})
	require.Equal(t, http.StatusForbidden, status, "%v", body)
	assert.Equal(t, "read_only", body["error"], "the code, not an English sentence: %v", body)
	assert.Equal(t, srvtext.Text("tr", "server.error.read_only", nil), body["message"], "%v", body)

	// The queue's door keeps the upper-case code its clients read, and says
	// the same sentence.
	status, body = langJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops", "tr",
		map[string]any{"kind": "delete", "storage_id": f.StA2.ID, "sources": []string{"arsiv.txt"}})
	require.Equal(t, http.StatusForbidden, status, "%v", body)
	assert.Equal(t, "read_only", body["error"], "%v", body)
	assert.Equal(t, "READ_ONLY", body["code"], "the older clients' code stays: %v", body)
	assert.Equal(t, "Bu depo salt okunur.", body["message"], "%v", body)
}

// The catalogue has a sentence for every code a handler sends through
// writeError, in both shipped languages, and no code is answered with an
// empty message.
func TestErrorEnvelope_EveryCodeIsSaidInBothLanguages(t *testing.T) {
	codes := apierr.Codes()
	require.NotEmpty(t, codes)
	for _, code := range []string{"read_only", "quota_exceeded", "name_taken", "locked", "reserved_name",
		"not_cancellable", "finished", "bad_kind", "too_many", "draft_limit", "draft_folder_gone",
		"drafts_unavailable", "entry_unavailable", "no_secret_key", "e2e_policy_undecided", "kind_mismatch",
		"not_requestable", "path_missing", "too_many_pending", "not_in_trash", "restarted", "not_applicable"} {
		assert.Contains(t, codes, code)
		for _, lang := range []string{"en", "tr"} {
			assert.NotEmpty(t, apierr.Text(lang, code, apierr.Params{"name": "x", "max": "2", "limit": "3"}), "%s %s", lang, code)
		}
		assert.NotEqual(t, apierr.Text("en", code, nil), apierr.Text("tr", code, nil), "%s is not translated", code)
	}
}

// POST /api/auth/account/check answers with the save's own rules and words
// (0.54 audit B15): the browser keeps no copy of either.
//
// RED PROOF: the route did not exist, and the browser's own e-mail rule let
// "a,b@x" and "ada.@x" through while the save refused them.
func TestAccountCheck_TheSavesRulesInTheReadersLanguage(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	ayse, err := store.CreateUser(ctx, "ayse@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "bob@local", "x", model.RoleUser, "tr", "")
	require.NoError(t, err)
	h := handlers.NewAuthSelf(store)

	check := func(body, lang string) map[string]map[string]string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/account/check", strings.NewReader(body))
		req.Header.Set("Accept-Language", lang)
		u, err := store.GetUser(ctx, ayse.ID)
		require.NoError(t, err)
		u.Locale = "" // the reader's language comes from the request here
		rec := httptest.NewRecorder()
		h.CheckAccount(rec, req.WithContext(auth.WithUser(req.Context(), u)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		out := map[string]map[string]string{}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
		return out
	}

	for _, bad := range []string{"a,b@x", "ada.@x", "no-at-sign"} {
		out := check(`{"email":`+strconv.Quote(bad)+`}`, "tr")
		require.Contains(t, out, "email", "%q was not refused", bad)
		assert.Equal(t, "email_invalid", out["email"]["error"], "%q", bad)
		assert.Equal(t, srvtext.Text("tr", "server.account.email_invalid", srvtext.Vars{"email": bad}), out["email"]["message"], "%q", bad)
	}

	// Another account's address and its own: taken, and no change.
	out := check(`{"email":"BOB@local"}`, "en")
	assert.Equal(t, "email_taken", out["email"]["error"], "%v", out)
	assert.Empty(t, check(`{"email":"ayse@local"}`, "en"), "its own address is no change")

	// A username the rule refuses, in the rule's sentence.
	out = check(`{"username":"9lives"}`, "en")
	assert.Equal(t, "username_invalid", out["username"]["error"], "%v", out)
	assert.Equal(t, srvtext.Text("en", "server.account.username_digit", nil), out["username"]["message"])

	// Acceptable values, or nothing asked: nothing to say.
	assert.Empty(t, check(`{"email":"someone.new@example.com","username":"someone.new"}`, "en"))
	assert.Empty(t, check(`{}`, "en"))

	// For a NEW account (an administrator's form) the caller's own values are
	// not "no change"; a caller who is not an administrator is told only
	// about the format, never whether an address is taken.
	out = check(`{"email":"bob@local","for":"new"}`, "en")
	assert.Empty(t, out, "a plain account learnt that an address is taken: %v", out)
}

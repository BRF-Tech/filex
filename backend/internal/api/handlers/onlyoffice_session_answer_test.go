package handlers_test

// POST /api/files/onlyoffice/session: an answer for an editing session -
// "write mine", "keep the outside version" - is taken only from one of that
// session's own editors, and who answered is recorded (filex 0.54,
// onlyoffice/session_answer.go).
//
// ⚠ Red before 0.54: anybody who could modify the file answered for any
// session whose key they sent, and every viewer's editor configuration
// carries the key, so Bob's "keep the outside version" dropped Ada's save.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestOnlyOfficeSession_OnlyTheSessionsEditorsAnswer(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixture(t, root)
	ctx := context.Background()
	adminID, _ := testutil.SeedAdminUser(t, store)
	admin := issueToken(t, store, adminID, fullScopes, nil)
	seedServerFile(t, store, "oo", "Documents/Rapor.docx", "PK already there")
	const path = "oo://Documents/Rapor.docx"

	grant := func(email string) (int64, string) {
		testutil.SeedRegularUser(t, store, email, "SomePass1!")
		u, err := store.GetUserByEmail(ctx, email)
		require.NoError(t, err)
		status, body := fxPost(t, srv.URL+"/api/files/permissions", admin,
			map[string]any{"path": "oo://Documents", "user_id": u.ID, "level": "editor"})
		require.Equal(t, http.StatusOK, status, "grant: %s", body)
		return u.ID, personToken(t, store, u.ID)
	}
	adaID, ada := grant("ada@oo.test")
	_, bob := grant("bob@oo.test")

	status, body := fxPost(t, srv.URL+"/api/files/onlyoffice/config", ada, map[string]any{"path": path, "mode": "edit"})
	require.Equal(t, http.StatusOK, status, body)
	var cfg struct {
		Config struct {
			Token    string `json:"token"`
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &cfg), body)
	key, adaEditor := cfg.Config.Document.Key, cfg.Config.Token
	require.NotEmpty(t, key)
	require.NotEmpty(t, adaEditor)

	session := func(tok string, payload map[string]any) (int, string) {
		t.Helper()
		return fxPost(t, srv.URL+"/api/files/onlyoffice/session", tok, payload)
	}

	// Bob may modify the file, but the session is Ada's.
	status, body = session(bob, map[string]any{"path": path, "key": key, "action": "theirs"})
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "not_your_session")
	status, body = session(bob, map[string]any{"path": path, "key": key, "action": "mine", "token": adaEditor})
	assert.Equal(t, http.StatusForbidden, status, "Ada's editor token answered for Bob: %s", body)
	row, err := store.GetOfficeSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, row, "the rig: the editing config recorded the session")
	assert.False(t, row.Dropped, "Bob dropped Ada's save")

	// Asking stays a viewer's question.
	status, body = session(bob, map[string]any{"path": path, "key": key, "action": "state"})
	assert.Equal(t, http.StatusOK, status, body)

	// A key filex never made for this document: nothing to say about it, and
	// nothing is recorded under it.
	status, body = session(bob, map[string]any{"path": path, "key": "not-a-key-of-it", "action": "state"})
	require.Equal(t, http.StatusOK, status, body)
	assert.JSONEq(t, `{"stale":false,"known":false}`, body)
	stray, err := store.GetOfficeSession(ctx, "not-a-key-of-it")
	require.NoError(t, err)
	assert.Nil(t, stray)

	// Ada answers, with the configuration she was handed.
	status, body = session(ada, map[string]any{"path": path, "key": key, "action": "theirs", "token": adaEditor})
	require.Equal(t, http.StatusOK, status, body)
	row, err = store.GetOfficeSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.True(t, row.Dropped)

	// Who answered is on record.
	rows, err := store.ListAuditRecent(ctx, 50)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == "file.office_session_answered" && r.UserID != nil && *r.UserID == adaID && r.Metadata["answer"] == "theirs" {
			found = true
		}
	}
	assert.True(t, found, "no audit row says Ada answered")
}

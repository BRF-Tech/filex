package handlers_test

// The server's own record of a rewritten encrypted-folder key file
// (internal/e2e/keyfile.go), through the upload path the browser uses:
// every rewrite is audited whether or not the client announces it, and a
// rewrite that changes a key slot deletes the key file's older versions —
// each of them still opens the folder with the old password — when its
// writer is the folder's owner or an administrator (e2e/slotchange).

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	kfOne   = `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="}}`
	kfNames = `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"names":{"alg":"AES-SIV-512"}}`
	kfNewPw = `{"v":3,"req":["names"],"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"cms=","blob":"YmxvYg=="},"names":{"alg":"AES-SIV-512"}}`
)

func keyFileAudits(t *testing.T, f *stagedFixture) []*db.AuditEntryWithUser {
	t.Helper()
	rows, _, err := f.store.ListAuditFiltered(context.Background(), nil, "e2e.key_file_rewritten", nil, nil, 50, 0)
	require.NoError(t, err)
	return rows
}

func TestE2EKeyFile_RewritesAreAuditedAndAPasswordChangeDropsOldVersions(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	const marker = ".filex-e2e.json"

	f.uploadStaged(t, marker, []byte(kfOne))
	require.Empty(t, keyFileAudits(t, f), "a new key file is a new folder, not a rewrite")

	// Level 1 → 2: audited, and the old version stays (it opens nothing new).
	f.uploadStaged(t, marker, []byte(kfNames))
	rows := keyFileAudits(t, f)
	require.Len(t, rows, 1)
	require.Equal(t, []any{"level"}, rows[0].Entry.Metadata["changes"])
	require.Len(t, versionsOf(t, f, marker), 1)

	// A new password: audited, and every older version is gone — each wraps
	// the folder key under a password that no longer opens it.
	f.uploadStaged(t, marker, []byte(kfNewPw))
	rows = keyFileAudits(t, f)
	require.Len(t, rows, 2)
	var pw map[string]any
	for _, r := range rows {
		if ch, _ := r.Entry.Metadata["changes"].([]any); len(ch) == 1 && ch[0] == "password" {
			pw = r.Entry.Metadata
		}
	}
	require.NotNil(t, pw, "the password change is in the audit log: %v", rows)
	require.EqualValues(t, 2, pw["versions_deleted"])
	require.Empty(t, versionsOf(t, f, marker), "no version of the key file holds the old password")

	// Something that is not a key file, under its name: audited, deletes nothing.
	f.uploadStaged(t, marker, []byte("not a key file"))
	require.Len(t, keyFileAudits(t, f), 3)
	require.Len(t, versionsOf(t, f, marker), 1)
}

// asEditor is f signed in as a second account: one that may write the
// storage, owns nothing in it and is no administrator.
func (f *stagedFixture) asEditor(t *testing.T) *stagedFixture {
	t.Helper()
	const email, pw = "editor@test.local", "EditorPass!1"
	testutil.SeedRegularUser(t, f.store, email, pw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, c, email, pw)
	ed := *f
	ed.client = c
	return &ed
}

// An editor who does not own the folder uploads a key file of their own. Up
// to 0.53 its changed salt was enough to delete every earlier key file, and
// the folder was left with none that opens it. Now they all stay, and the
// rewrite says so; an administrator's password change still retires them.
func TestE2EKeyFile_AnEditorsKeyFileErasesNoKeyHistory(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	const marker = ".filex-e2e.json"
	f.uploadStaged(t, marker, []byte(kfOne))
	f.uploadStaged(t, marker, []byte(kfNames))
	require.Len(t, versionsOf(t, f, marker), 1)

	ed := f.asEditor(t)
	ed.uploadStaged(t, marker, []byte(`{"v":3,"salt":"x"}`))
	require.Len(t, versionsOf(t, f, marker), 2, "every key file that opens the folder is still there")
	var kept map[string]any
	for _, r := range keyFileAudits(t, f) {
		if _, ok := r.Entry.Metadata["versions_kept"]; ok {
			kept = r.Entry.Metadata
		}
	}
	require.NotNil(t, kept, "the rewrite records what it kept")
	assert.EqualValues(t, 2, kept["versions_kept"])
	assert.Nil(t, kept["versions_deleted"])

	// The administrator's own password change retires them all.
	f.uploadStaged(t, marker, []byte(kfNewPw))
	require.Empty(t, versionsOf(t, f, marker))
}

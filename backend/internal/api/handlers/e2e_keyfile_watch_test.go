package handlers_test

// The server's own record of a rewritten encrypted-folder key file
// (internal/e2e/keyfile.go), through the upload path the browser uses:
// every rewrite is audited whether or not the client announces it, and a
// rewrite that changes a key slot deletes the key file's older versions —
// each of them still opens the folder with the old password.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
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

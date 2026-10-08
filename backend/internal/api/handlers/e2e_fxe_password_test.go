package handlers_test

// A `.fxe` password change is the server's to record, like a folder's: every
// rewrite of a `.fxe` is audited as e2e.fxe_header_rewritten, whatever wrote
// it; a rewrite that changes the password of the same file key is recorded as
// e2e.password_change and told to the file's OWNER; and the older versions
// that would open the file's current contents with the old secret are deleted
// - when the writer is the file's owner or an administrator (e2e/slotchange).
// An older client's announcement (POST /api/files/e2e/password-changed) is
// answered and records nothing.

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/notify"
)

func TestE2ePasswordChanged_ASingleEncryptedFile(t *testing.T) {
	ctx := context.Background()
	fx, _ := seedFxeFile(t)
	owner, err := fx.store.CreateUser(ctx, "owner@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	other, err := fx.store.CreateUser(ctx, "other@example.com", "x", "user", "en", "UTC")
	require.NoError(t, err)
	setFxeOwner(t, fx, owner)

	sink := &capturingSink{got: make(chan notify.Event, 4)}
	handlers.SetNotifySink(sink)
	t.Cleanup(func() { handlers.SetNotifySink(nil) })
	h := handlers.NewE2E(fx.store, nil)

	// An older client's announcement: answered, and nothing more.
	rec := postPasswordChanged(t, h, other, map[string]any{"path": "alpha://Rapor 2027.pdf.fxe", "via": "password"})
	require.Equal(t, 200, rec.Code, "an older client's announcement is answered: %s", rec.Body.String())
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())

	// Not a .fxe, not a file: refused as before.
	for _, p := range []string{"alpha://klasor.fxe", "alpha://yok.fxe", "alpha://duz.txt"} {
		rec = postPasswordChanged(t, h, other, map[string]any{"path": p, "via": "password"})
		assert.Equal(t, 400, rec.Code, "%s: %s", p, rec.Body.String())
	}
	select {
	case ev := <-sink.got:
		t.Fatalf("an announcement must tell nobody, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
	rows, err := fx.store.ListAuditRecent(ctx, 10)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, "e2e.password_change", r.Action, "an announcement writes no audit row")
	}
}

// ── the server's own record, through the upload path the browser uses ──────

// fxeBytes frames a header the way a writer does, followed by a body.
func fxeBytes(header, body string) []byte {
	b := append([]byte("filexfxe"), 1, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[9:13], uint32(len(header)))
	return append(append(b, header...), body...)
}

const (
	fxeHdrOne   = `{"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":4}`
	fxeHdrEsc   = `{"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"esc":{"kid":"abcd","blob":"ZQ=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":4}`
	fxeHdrNewPw = `{"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"cms=","blob":"YmxvYg=="},"esc":{"kid":"abcd","blob":"ZQ=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":4}`
	fxeHdrOther = `{"salt":"b3Ro","iter":600000,"verify":"b3Rodg==","fmk":"wrapped","fmk_pw":"b3RocA==","rk":{"salt":"cms=","blob":"YmxvYg=="},"dek":"YW5vdGhlcg==","name":"bmFtZQ==","chunk":20,"nonce":"b3RoZXJu","size":9}`
)

func fxeAudits(t *testing.T, f *stagedFixture) []*db.AuditEntryWithUser {
	t.Helper()
	rows, _, err := f.store.ListAuditFiltered(context.Background(), nil, "e2e.fxe_header_rewritten", nil, nil, 50, 0)
	require.NoError(t, err)
	return rows
}

func changesOf(r *db.AuditEntryWithUser) []any {
	ch, _ := r.Entry.Metadata["changes"].([]any)
	return ch
}

func TestE2EFxe_HeaderRewritesAreAuditedAndARetiredPasswordDropsItsVersions(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	const name = "Rapor 2027.pdf.fxe"

	f.uploadStaged(t, name, fxeBytes(fxeHdrOne, "body"))
	require.Empty(t, fxeAudits(t, f), "a new .fxe is a new encrypted file, not a rewrite")

	// An escrow slot added: audited, and the old version stays — it opens
	// nothing the new one does not.
	f.uploadStaged(t, name, fxeBytes(fxeHdrEsc, "body"))
	rows := fxeAudits(t, f)
	require.Len(t, rows, 1)
	assert.Equal(t, []any{"escrow"}, changesOf(rows[0]))
	require.Len(t, versionsOf(t, f, name), 1)

	// A different file under the same name (new content, new key): its
	// version is history, and stays.
	f.uploadStaged(t, name, fxeBytes(fxeHdrOther, "other body"))
	require.Len(t, fxeAudits(t, f), 2)
	require.Len(t, versionsOf(t, f, name), 2)

	// Back to the first file's key, then a new password over it: every
	// version holding THAT key under an old password goes — and only those.
	f.uploadStaged(t, name, fxeBytes(fxeHdrEsc, "body"))
	require.Len(t, versionsOf(t, f, name), 3)
	f.uploadStaged(t, name, fxeBytes(fxeHdrNewPw, "body"))
	rows = fxeAudits(t, f)
	require.Len(t, rows, 4)
	var pw map[string]any
	for _, r := range rows {
		if ch := changesOf(r); len(ch) == 1 && ch[0] == "password" {
			pw = r.Entry.Metadata
		}
	}
	require.NotNil(t, pw, "the password change is in the audit log: %v", rows)
	assert.Equal(t, name, pw["file"])
	assert.EqualValues(t, 3, pw["versions_deleted"], "the three versions under the first file's key")
	left := versionsOf(t, f, name)
	require.Len(t, left, 1, "the other file's version is kept")
	assert.Contains(t, readSnapshot(t, f, left[0]), "other body")

	// Something that is not a .fxe under its name: audited, deletes nothing.
	f.uploadStaged(t, name, []byte("not encrypted at all"))
	rows = fxeAudits(t, f)
	require.Len(t, rows, 5)
	require.Len(t, versionsOf(t, f, name), 2)
}

// An editor who does not own the file writes a header of their own over the
// same file key: up to 0.53 every version under the old password went, and
// with a password slot nobody knows the file opened no more. Now they stay.
func TestE2EFxe_AnEditorsHeaderErasesNoVersion(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	const name = "Rapor 2027.pdf.fxe"
	f.uploadStaged(t, name, fxeBytes(fxeHdrOne, "body"))
	f.uploadStaged(t, name, fxeBytes(fxeHdrEsc, "body"))
	require.Len(t, versionsOf(t, f, name), 1)

	ed := f.asEditor(t)
	ed.uploadStaged(t, name, fxeBytes(fxeHdrNewPw, "body"))
	require.Len(t, versionsOf(t, f, name), 2, "the versions under the old password are kept")
	var kept map[string]any
	for _, r := range fxeAudits(t, f) {
		if _, ok := r.Entry.Metadata["versions_kept"]; ok {
			kept = r.Entry.Metadata
		}
	}
	require.NotNil(t, kept, "the rewrite records what it kept")
	assert.EqualValues(t, 2, kept["versions_kept"])
}

func TestE2EFxe_OtherFilesAreNotWatched(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	f.uploadStaged(t, "notes.txt", fxeBytes(fxeHdrOne, "body"))
	f.uploadStaged(t, "notes.txt", fxeBytes(fxeHdrNewPw, "body"))
	assert.Empty(t, fxeAudits(t, f), "only a file named .fxe is one")
	require.Len(t, versionsOf(t, f, "notes.txt"), 1)
}

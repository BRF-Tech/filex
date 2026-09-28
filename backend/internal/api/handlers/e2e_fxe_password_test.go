package handlers_test

// A `.fxe` password change is announced like a folder's (the same audit
// action, the file's OWNER told) — and the server also sees it for itself:
// every rewrite of a `.fxe` is audited as e2e.fxe_header_rewritten, whatever
// wrote it, and a rewrite that retires a secret deletes the older versions
// that would open the file's current contents with it.

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
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

	rec := postPasswordChanged(t, h, other, map[string]any{"path": "alpha://Rapor 2027.pdf.fxe", "via": "password"})
	require.Equal(t, 200, rec.Code, "a .fxe's password change is announced like a folder's: %s", rec.Body.String())
	assert.JSONEq(t, `{"ok":true,"notified":true}`, rec.Body.String())

	select {
	case ev := <-sink.got:
		assert.Equal(t, notify.EventE2EPasswordChanged, ev.Event)
		assert.Equal(t, "Encrypted file password changed", ev.Title)
		require.NotNil(t, ev.UserID)
		assert.Equal(t, owner.ID, *ev.UserID, "the file's OWNER is told")
		assert.Equal(t, "Rapor 2027.pdf.fxe", ev.Meta["file"])
		assert.Equal(t, "file", ev.Meta["kind"])
		assert.Equal(t, notify.FileTarget("Rapor 2027.pdf.fxe"), ev.Target)
	case <-time.After(5 * time.Second):
		t.Fatal("no notification was sent")
	}

	rows, err := fx.store.ListAuditRecent(ctx, 10)
	require.NoError(t, err)
	var found *model.AuditEntry
	for _, r := range rows {
		if r.Action == "e2e.password_change" {
			found = r
		}
	}
	require.NotNil(t, found, "the change is in the audit log")
	assert.Equal(t, "Rapor 2027.pdf.fxe", found.Metadata["file"])
	assert.Equal(t, "node", found.TargetType)
	assert.NotEmpty(t, found.TargetID)

	// Not a .fxe, not a file: refused, nobody told.
	for _, p := range []string{"alpha://klasor.fxe", "alpha://yok.fxe", "alpha://duz.txt"} {
		rec = postPasswordChanged(t, h, other, map[string]any{"path": p, "via": "password"})
		assert.Equal(t, 400, rec.Code, "%s: %s", p, rec.Body.String())
	}
	select {
	case ev := <-sink.got:
		t.Fatalf("a refused announcement must notify nobody, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
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

func TestE2EFxe_OtherFilesAreNotWatched(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	f.uploadStaged(t, "notes.txt", fxeBytes(fxeHdrOne, "body"))
	f.uploadStaged(t, "notes.txt", fxeBytes(fxeHdrNewPw, "body"))
	assert.Empty(t, fxeAudits(t, f), "only a file named .fxe is one")
	require.Len(t, versionsOf(t, f, "notes.txt"), 1)
}

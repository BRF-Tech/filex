package onlyoffice

// The editor page's questions about its session (#184, session_base.go):
// is it still on the current version, and the person's answer when it is not.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestSessionState_FollowsTheFile(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)

	stale, known := h.svc.SessionState(ctx, h.node, h.sessionKey)
	assert.False(t, stale, "nothing changed yet")
	assert.True(t, known)

	outside(t, h, "V2 from outside")
	stale, known = h.svc.SessionState(ctx, h.node, h.sessionKey)
	assert.True(t, stale, "the file moved on from the session's version")
	assert.True(t, known)
}

func TestSession_WriteMine_TheSaveGoesOverTheVersionThePersonSaw(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "V2 from outside")

	require.NoError(t, h.svc.RebaseSession(ctx, h.node, h.sessionKey))
	stale, _ := h.svc.SessionState(ctx, h.node, h.sessionKey)
	assert.False(t, stale, "the answer moved the session on")

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, docxBytes, h.disk(t), "the person chose their version")
	assert.Empty(t, h.besideFiles(t))
}

func TestSession_WriteMine_OnlyOverTheVersionAnswered(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "V2 from outside")
	require.NoError(t, h.svc.RebaseSession(ctx, h.node, h.sessionKey))

	// Another change after the answer: a version nobody answered about (its
	// size alone tells it apart).
	outside(t, h, "V3 from outside, after the answer")

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "V3 from outside, after the answer", h.disk(t))
	assert.Len(t, h.besideFiles(t), 1)
}

func TestSession_KeepTheOutsideVersion_TheSaveIsNotWritten(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "V2 from outside")

	require.NoError(t, h.svc.DropSession(context.Background(), h.node, h.sessionKey))
	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"], "answered: the document server is not asked to retry")
	assert.Equal(t, "V2 from outside", h.disk(t))
	assert.Empty(t, h.besideFiles(t), "the person threw those edits away")
	select {
	case e := <-h.sink.ch:
		t.Fatalf("nothing was written, nobody is told: %v", e.Event)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSessionState_WithoutARecordTheKeyDecides(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)

	// A session this process never handed out (a restart, another replica).
	stale, known := h.svc.SessionState(ctx, h.node, h.svc.keyFor(ctx, h.node))
	assert.False(t, stale, "its key is the one the document would get now")
	assert.False(t, known)

	// An older key of this document (sealed for it, callback_trust.go).
	older := h.svc.sealKey(ctx, h.node.ID, md5Hex("an-older-session"))
	stale, known = h.svc.SessionState(ctx, h.node, older)
	assert.True(t, stale, "a key the document has moved on from")
	assert.False(t, known)

	// …and its save, when it comes, is kept beside the file.
	h.sessionKey = older
	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "V1", h.disk(t))
	assert.Len(t, h.besideFiles(t), 1)
}

// 0.54: a key filex never made for this document is no session of it. It is
// answered "current, unknown", nothing is recorded under it, and an answer
// for it is refused (it used to be recorded as an older session of whatever
// document the request named).
func TestSessionState_AKeyThatIsNotThisDocumentsRecordsNothing(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")

	stale, known := h.svc.SessionState(ctx, h.node, "an-older-session")
	assert.False(t, stale)
	assert.False(t, known)
	row, err := h.store.GetOfficeSession(ctx, "an-older-session")
	require.NoError(t, err)
	assert.Nil(t, row, "a record under a key filex never made for this document")

	assert.ErrorIs(t, h.svc.DropSession(ctx, h.node, "an-older-session"), ErrNotThisDocument)
	assert.ErrorIs(t, h.svc.RebaseSession(ctx, h.node, "an-older-session"), ErrNotThisDocument)
	row, err = h.store.GetOfficeSession(ctx, "an-older-session")
	require.NoError(t, err)
	assert.Nil(t, row)
}

func TestSessionBase_ASecondOpenerJoinsTheRunningSessionsVersion(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada, bob := h.user(t, "ada@example.com", model.RoleUser), h.user(t, "bob@example.com", model.RoleUser)
	h.open(t, ada)
	first := h.sessionKey
	// The file changes on the storage before the catalogue knows: the next
	// opening still gets the running session's key (the document server
	// shows it that session's version), so it must not take the new
	// version as the session's base.
	outside(t, h, "V2 from outside")
	h.open(t, bob)
	require.Equal(t, first, h.sessionKey, "the rig: the same key")

	stale, _ := h.svc.SessionState(ctx, h.node, first)
	assert.True(t, stale, "the second opener moved the base to a version the session never had")
}

func TestConflictName(t *testing.T) {
	at := time.Date(2026, 10, 6, 13, 15, 0, 0, time.FixedZone("TRT", 3*3600))
	assert.Equal(t, "Bütçe Özeti.filex-conflict-20261006T101500.xlsx", conflictName("Bütçe Özeti.xlsx", at), "UTC, like the desktop's")
	assert.Equal(t, "README.filex-conflict-20261006T101500", conflictName("README", at))
}

package onlyoffice

// Who may answer for an editing session (session_answer.go, filex 0.54).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// editorConfigOf is the editor configuration uid is handed, in mode.
func (h *csvHarness) editorConfigOf(t *testing.T, uid int64, mode string) (key, token string) {
	t.Helper()
	u, err := h.store.GetUser(context.Background(), uid)
	require.NoError(t, err)
	cfg, err := h.svc.BuildConfigForNode(context.Background(), h.node, u, "en", mode)
	require.NoError(t, err)
	return cfg.Config["document"].(map[string]any)["key"].(string), cfg.Config["token"].(string)
}

func TestMayAnswer_OnlyTheSessionsOwnEditors(t *testing.T) {
	ctx := context.Background()
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	bob := h.user(t, "bob@example.com", model.RoleUser)
	vera := h.user(t, "vera@example.com", model.RoleViewer)
	key, adaToken := h.editorConfigOf(t, ada, "edit")
	viewKey, veraToken := h.editorConfigOf(t, vera, "view")
	require.Equal(t, key, viewKey, "the rig: a viewer is handed the same key")

	assert.True(t, h.svc.MayAnswer(ctx, h.node, key, ada, ""), "handed an editing session here")
	assert.False(t, h.svc.MayAnswer(ctx, h.node, key, bob, ""), "never opened it")
	assert.False(t, h.svc.MayAnswer(ctx, h.node, key, bob, adaToken), "somebody else's editor token")
	assert.False(t, h.svc.MayAnswer(ctx, h.node, key, vera, ""), "only looked at it")
	assert.False(t, h.svc.MayAnswer(ctx, h.node, key, vera, veraToken), "a view configuration is no editing session")

	// Another instance (or this one after a restart) has no record of who
	// opened it: the editor's own signed configuration says so.
	b := h.another()
	assert.False(t, b.MayAnswer(ctx, h.node, key, ada, ""))
	assert.True(t, b.MayAnswer(ctx, h.node, key, ada, adaToken))
	assert.False(t, b.MayAnswer(ctx, h.node, key, bob, adaToken))

	// Not for another document's session, whoever asks.
	other := h.secondDoc(t, "other.docx", "OTHER")
	assert.False(t, h.svc.MayAnswer(ctx, other, key, ada, adaToken))
}

func TestNoteAnswer_RecordsWhoAnswered(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.svc.NoteAnswer(context.Background(), h.node, ada, "theirs")
	a := auditRow(t, h, AuditActionSessionAnswered)
	require.NotNil(t, a)
	assert.EqualValues(t, ada, a["user_id"])
	assert.Equal(t, "theirs", a["answer"])
	assert.Equal(t, "/rapor.docx", a["target_name"])
}

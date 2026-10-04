package onlyoffice

// A save written beside a document is written only for somebody who may
// create that file there (callback_identity.go; the maintainer's decision,
// 2026-10-04).
//
// ⚠ Fails on the callback before it: the .docx was written beside rapor.doc
// for whoever the callback named, a viewer and a stranger included.

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// idStrings are account ids as the callback's `users` carries them.
func idStrings(ids ...int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}

// user is an account of the test database, switched on.
func (h *csvHarness) user(t *testing.T, email, role string) int64 {
	t.Helper()
	u, err := h.store.CreateUser(context.Background(), email, "x", role, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, h.store.SetUserEnabled(context.Background(), u.ID, true))
	return u.ID
}

// open hands uid an editing session of the document, as the editor config
// endpoint does, and makes its key the one the callbacks name.
func (h *csvHarness) open(t *testing.T, uid int64) {
	t.Helper()
	u, err := h.store.GetUser(context.Background(), uid)
	require.NoError(t, err)
	cfg, err := h.svc.BuildConfigForNode(context.Background(), h.node, u, "en", "edit")
	require.NoError(t, err)
	h.sessionKey = cfg.Config["document"].(map[string]any)["key"].(string)
}

// besideFiles are the files beside the document (everything but itself).
func (h *csvHarness) besideFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(h.root)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if e.Name() != h.node.Name {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestBeside_AnEditorWhoMayNotCreateFilesThereIsRefused(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	vera := h.user(t, "vera@example.com", model.RoleViewer)
	h.open(t, vera)
	before := h.key(t)

	resp := h.save(t, docxBytes, "docx", idStrings(vera)...)
	assert.Equal(t, 1, resp["error"], "the document server is told the save failed")
	assert.Equal(t, "THE OLD .DOC", h.disk(t))
	assert.Empty(t, h.besideFiles(t), "no .docx for somebody who may not create files here")

	e := h.sink.wait(t)
	assert.Equal(t, notify.EventFileUploadFailed, e.Event)
	require.NotNil(t, e.UserID)
	assert.Equal(t, vera, *e.UserID, "the editor is told")
	assert.Equal(t, "Your edit to rapor.doc was not saved", e.Meta["title_en"])
	assert.Contains(t, e.Meta["body_en"], "You cannot create new files in this folder")
	assert.Contains(t, e.Meta["body_tr"], "Bu klasörde yeni dosya oluşturamazsınız")
	assert.Contains(t, e.Meta["body_tr"], "rapor.doc değişmedi.")

	a := auditRow(t, h, "file.office_save_refused")
	require.NotNil(t, a)
	assert.EqualValues(t, vera, a["user_id"])
	assert.Contains(t, a["reason"], "You cannot create new files in this folder")
	assert.NotEqual(t, before, h.key(t), "the next opening is a new editing session")
}

func TestBeside_OneEditorWhoMayIsEnough(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	vera, ada := h.user(t, "vera@example.com", model.RoleViewer), h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, vera)
	h.open(t, ada)

	resp := h.save(t, docxBytes, "docx", idStrings(vera, ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "THE OLD .DOC", h.disk(t))
	assert.Equal(t, []string{"rapor.docx"}, h.besideFiles(t))
	h.sink.wait(t)
	a := auditRow(t, h, "file.office_saved_beside")
	require.NotNil(t, a)
	assert.EqualValues(t, ada, a["user_id"], "written for the one who may")
}

func TestBeside_AnIDTheSessionNeverHadIsNoEditor(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	vera, ada := h.user(t, "vera@example.com", model.RoleViewer), h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, vera)

	// Ada may create files there, but this session was never hers.
	resp := h.save(t, docxBytes, "docx", idStrings(vera, ada)...)
	assert.Equal(t, 1, resp["error"])
	assert.Empty(t, h.besideFiles(t))
	e := h.sink.wait(t)
	require.NotNil(t, e.UserID)
	assert.Equal(t, vera, *e.UserID, "told: the editor the session had")
}

func TestBeside_TheBodyCannotNameAnotherEditor(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	vera, ada := h.user(t, "vera@example.com", model.RoleViewer), h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, vera)
	h.open(t, ada)
	// The signed token says Vera; a body changed beside it says Ada.
	h.bodyUsers = idStrings(ada)

	resp := h.save(t, docxBytes, "docx", idStrings(vera)...)
	assert.Equal(t, 1, resp["error"], "the signed payload is who edited it")
	assert.Empty(t, h.besideFiles(t))
	h.sink.wait(t)
}

func TestBeside_ATenantReachesOnlyItsOwnStorages(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	ctx := context.Background()
	acme, err := h.store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	ada := h.user(t, "ada@example.com", model.RoleUser)
	require.NoError(t, h.store.SetUserProvider(ctx, ada, acme.ID, ""))
	h.open(t, ada)

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 1, resp["error"], "a storage Acme is not linked to")
	assert.Empty(t, h.besideFiles(t))
	h.sink.wait(t)

	require.NoError(t, h.store.LinkProviderStorage(ctx, acme.ID, h.node.StorageID))
	h.open(t, ada)
	resp = h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"], "linked: Ada's tenant reaches it")
	assert.Equal(t, []string{"rapor.docx"}, h.besideFiles(t))
	h.sink.wait(t)
}

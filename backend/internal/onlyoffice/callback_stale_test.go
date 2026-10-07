package onlyoffice

// A save never lands over a version its editing session did not see (#184,
// session_base.go).
//
// ⚠ These tests drive only what the callback had before #184 (the harness,
// open, save), so run against that code they fail on their assertions - the
// outside change was written over - rather than on a missing name. The tests
// of the new calls (SessionState, RebaseSession, DropSession) are in
// session_base_test.go.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

const docxMime = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// outside writes the document the way something that is not filex does: on
// the storage, past the catalogue, a moment later than it was.
func outside(t *testing.T, h *csvHarness, content string) {
	t.Helper()
	p := filepath.Join(h.root, h.node.Name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	later := time.Now().Add(5 * time.Second)
	require.NoError(t, os.Chtimes(p, later, later))
}

func TestStaleSave_AnEditOfAnOlderVersionIsKeptBesideTheFileNotWrittenOverIt(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1 - opened in the editor")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "V2 - written outside while it was open")

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"], "the save is kept: the document server is told so")
	assert.Equal(t, "V2 - written outside while it was open", h.disk(t),
		"the session's save went over a version it never saw - the outside change is gone")

	files := h.besideFiles(t)
	require.Len(t, files, 1, "the edit is kept beside the document")
	assert.Regexp(t, `^rapor\.filex-conflict-\d{8}T\d{6}\.docx$`, files[0])
	kept, err := os.ReadFile(filepath.Join(h.root, files[0]))
	require.NoError(t, err)
	assert.Equal(t, docxBytes, string(kept))

	e := h.sink.wait(t)
	assert.Equal(t, notify.EventFileUploaded, e.Event, "a new file, and the editor is told")
	require.NotNil(t, e.UserID)
	assert.Equal(t, ada, *e.UserID)
	assert.Equal(t, "/rapor.docx", e.Meta["saved_beside"])
	assert.Equal(t, "rapor.docx changed while you were editing it", e.Meta["title_en"])
	assert.Equal(t, "rapor.docx siz düzenlerken değişti", e.Meta["title_tr"])
	assert.Contains(t, e.Meta["body_en"], files[0])

	a := auditRow(t, h, "file.office_saved_conflict")
	require.NotNil(t, a, "an audit row says what happened")
	assert.Equal(t, "/rapor.docx", a["original"])
}

func TestStaleSave_TheSessionsOwnEarlierSaveIsNotAChangeFromOutside(t *testing.T) {
	// A session that saves twice (a force save, then the last one): the
	// second save is of the version the first one wrote.
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)

	resp := h.callback(t, StatusForceSave, "PK\x03\x04 the first save", "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "PK\x03\x04 the first save", h.disk(t))
	h.sink.wait(t)

	resp = h.save(t, "PK\x03\x04 the last save", "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "PK\x03\x04 the last save", h.disk(t), "its own first save made the session look stale")
	assert.Empty(t, h.besideFiles(t))
}

func TestStaleSave_ACSVChangedOutsideIsNotRewrittenEither(t *testing.T) {
	h := newCSVHarness(t, "ad;adet\r\nelma;3\r\n")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	outside(t, h, "ad;adet\r\nelma;99\r\nkiraz;7\r\n")

	resp := h.save(t, "\xEF\xBB\xBFad,adet\nelma,4\n", "csv", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "ad;adet\r\nelma;99\r\nkiraz;7\r\n", h.disk(t), "the outside change is not written over")
	files := h.besideFiles(t)
	require.Len(t, files, 1)
	assert.Regexp(t, `^list\.filex-conflict-\d{8}T\d{6}\.csv$`, files[0])
}

func TestStaleSave_ASessionOnTheCurrentVersionIsWrittenAsAlways(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, docxBytes, h.disk(t))
	assert.Empty(t, h.besideFiles(t), "no conflict copy when nothing changed")
}

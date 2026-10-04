package onlyoffice

// A save that comes back in another format than the file's name (filex 0.51,
// callback_format.go). Measured on ONLYOFFICE Docs 9.4 with its defaults: an
// edited .xls comes back as XLSX, a .doc as DOCX (`filetype`, zip bytes).
//
// ⚠ Every test that expects the old file untouched fails on the 0.50
// callback: it wrote the DOCX bytes over rapor.doc.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

const docxBytes = "PK\x03\x04 the DOCX the editor saved"

// auditRow is the newest audit row of action, or nil.
func auditRow(t *testing.T, h *csvHarness, action string) map[string]any {
	t.Helper()
	rows, err := h.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Action == action {
			m := map[string]any{"target_type": r.TargetType, "target_id": r.TargetID}
			for k, v := range r.Metadata {
				m[k] = v
			}
			if r.UserID != nil {
				m["user_id"] = *r.UserID
			}
			return m
		}
	}
	return nil
}

func TestCallback_AnOldFormatSavedAsAnotherIsWrittenBesideIt(t *testing.T) {
	h := newDocHarness(t, "rapor.doc", "application/msword", "THE OLD .DOC")
	before := h.key(t)
	ada, bob := h.user(t, "ada@example.com", "user"), h.user(t, "bob@example.com", "user")
	h.open(t, ada)
	h.open(t, bob)

	resp := h.save(t, docxBytes, "docx", idStrings(ada, bob)...)
	assert.Equal(t, 0, resp["error"], "the save is kept: the document server is told so")
	assert.Equal(t, "THE OLD .DOC", h.disk(t), "rapor.doc is not touched")
	beside, err := os.ReadFile(filepath.Join(h.root, "rapor.docx"))
	require.NoError(t, err, "the edit is beside it, under the format's extension")
	assert.Equal(t, docxBytes, string(beside))

	row, err := h.store.GetNodeByPath(context.Background(), h.node.StorageID, pathkey.Hash(h.node.StorageID, "/rapor.docx"))
	require.NoError(t, err)
	require.NotNil(t, row, "the new file is catalogued")
	assert.Equal(t, int64(len(docxBytes)), row.Size)

	// Each editor is told, in their language; the webhook hears it once.
	got := map[int64]notify.Event{}
	for i := 0; i < 2; i++ {
		e := h.sink.wait(t)
		assert.Equal(t, notify.EventFileUploaded, e.Event, "a new file")
		require.NotNil(t, e.UserID)
		got[*e.UserID] = e
	}
	require.Contains(t, got, ada)
	require.Contains(t, got, bob)
	assert.False(t, got[ada].NoWebhook)
	assert.True(t, got[bob].NoWebhook, "the webhook is told once")
	e := got[ada]
	assert.Equal(t, "/rapor.docx", e.Node.Path)
	assert.Equal(t, writehook.OriginOnlyOffice, e.Meta["origin"])
	assert.Equal(t, "/rapor.doc", e.Meta["saved_beside"])
	assert.Equal(t, "Your edit was saved as rapor.docx", e.Meta["title_en"])
	assert.Equal(t, "Düzenlemeniz rapor.docx olarak kaydedildi", e.Meta["title_tr"])
	assert.Contains(t, e.Meta["body_en"], "rapor.doc did not change.")
	assert.Contains(t, e.Meta["body_tr"], "rapor.doc değişmedi.")
	assert.Contains(t, e.Meta["body_tr"], "DOCX")

	a := auditRow(t, h, "file.office_saved_beside")
	require.NotNil(t, a, "an audit row says what happened")
	assert.Equal(t, "/rapor.docx", a["target_name"])
	assert.Equal(t, "/rapor.doc", a["original"])
	assert.Equal(t, "docx", a["filetype"])
	assert.Equal(t, fmt.Sprintf("[%d %d]", ada, bob), fmt.Sprint(a["editors"]), "the editors the callback named")
	assert.EqualValues(t, ada, a["user_id"], "written for the first who may")

	assert.NotEqual(t, before, h.key(t), "the next opening of rapor.doc is a new editing session")
}

func TestCallback_TheNameBesideIsTakenGetsTheNextNumber(t *testing.T) {
	h := newDocHarness(t, "rapor.xls", "application/vnd.ms-excel", "THE OLD .XLS")
	require.NoError(t, os.WriteFile(filepath.Join(h.root, "rapor.xlsx"), []byte("somebody else's"), 0o644))
	ada := h.user(t, "ada@example.com", "user")
	h.open(t, ada)

	resp := h.save(t, "PK\x03\x04 the XLSX", "xlsx", idStrings(ada)...)
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, "THE OLD .XLS", h.disk(t))
	other, err := os.ReadFile(filepath.Join(h.root, "rapor.xlsx"))
	require.NoError(t, err)
	assert.Equal(t, "somebody else's", string(other), "nothing is replaced")
	beside, err := os.ReadFile(filepath.Join(h.root, "rapor (2).xlsx"))
	require.NoError(t, err, "the next free name, numbered as the New document dialog numbers")
	assert.Equal(t, "PK\x03\x04 the XLSX", string(beside))
	e := h.sink.wait(t)
	assert.Equal(t, "Your edit was saved as rapor (2).xlsx", e.Meta["title_en"])
}

func TestCallback_AnotherFormatThatIsNotWhatItSaysIsNotWritten(t *testing.T) {
	for name, c := range map[string]struct {
		file, filetype, saved, why string
	}{
		"DOCX that is not a zip": {"rapor.doc", "docx", "%PDF-1.7 not a document package", "ONLYOFFICE said DOCX, but what it sent is not a DOCX file."},
		"a PDF for a .docx":      {"mektup.docx", "pdf", "%PDF-1.7", "ONLYOFFICE saved it as PDF, which filex does not keep for a .docx file."},
	} {
		t.Run(name, func(t *testing.T) {
			h := newDocHarness(t, c.file, "application/octet-stream", "THE ORIGINAL")
			resp := h.save(t, c.saved, c.filetype, "7")
			assert.Equal(t, 1, resp["error"])
			assert.Equal(t, "THE ORIGINAL", h.disk(t))
			entries, err := os.ReadDir(h.root)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "nothing written beside it either")

			e := h.sink.wait(t)
			assert.Equal(t, notify.EventFileUploadFailed, e.Event)
			assert.Equal(t, c.why+" "+c.file+" did not change.", e.Meta["body_en"])
			assert.NotEmpty(t, e.Meta["body_tr"])
			a := auditRow(t, h, "file.office_save_refused")
			require.NotNil(t, a)
			assert.Equal(t, c.filetype, a["filetype"])
		})
	}
}

func TestCallback_TheFilesOwnFormatIsWrittenInPlace(t *testing.T) {
	h := newDocHarness(t, "mektup.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "PK\x03\x04 OLD")
	resp := h.save(t, docxBytes, "docx", "7")
	assert.Equal(t, 0, resp["error"])
	assert.Equal(t, docxBytes, h.disk(t))
	assert.Equal(t, notify.EventFileUpdated, h.sink.wait(t).Event)
	entries, err := os.ReadDir(h.root)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

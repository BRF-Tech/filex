package handlers_test

// Tests for ?action=newfile — the "New document" create path.
//
// They exercise the handler against the same real local-FS driver + in-memory
// store fixture the other mutation verbs use (newMutateFixture in
// manager_mutate_test.go), so every assertion below is about bytes that
// actually landed on a disk and a row that actually entered the catalogue.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/newdoc"
)

func newFileBody(dir, name, typ string) map[string]any {
	return map[string]any{"path": dir, "name": name, "type": typ}
}

func decodeNewFile(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

// ---------- the text half: empty really is the right content ----------

func TestNewFile_TextTypeIsEmptyOnDisk(t *testing.T) {
	mh, store, _, st, dir := newMutateFixture(t)

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "notes", "md"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := decodeNewFile(t, rec.Body.Bytes())
	assert.Equal(t, "notes.md", got["name"])
	assert.Equal(t, "main://notes.md", got["path"])
	assert.Equal(t, float64(0), got["size"])

	fi, err := os.Stat(filepath.Join(dir, "notes.md"))
	require.NoError(t, err)
	assert.Equal(t, int64(0), fi.Size(), "a new .md must be a genuinely empty file")

	// And it entered the catalogue, or the listing would not show it until the
	// next sync sweep.
	node, err := store.GetNodeByPath(context.Background(), st.ID, mutTestPathHash(st.ID, "notes.md"))
	require.NoError(t, err)
	require.NotNil(t, node)
	assert.Equal(t, model.NodeTypeFile, node.Type)
	assert.Equal(t, "text/markdown; charset=utf-8", node.Mime)
}

// ---------- the office half: real bytes, and a real archive ----------

func TestNewFile_OfficeTypeWritesAValidContainer(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "Q3 report", "docx"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	raw, err := os.ReadFile(filepath.Join(dir, "Q3 report.docx"))
	require.NoError(t, err)
	require.NotEmpty(t, raw, "a zero-byte .docx is the bug this endpoint exists to prevent")

	// Byte-identical to what the template package produces: the handler must
	// not be re-deriving or re-compressing anything on the way through.
	want, err := newdoc.Bytes("docx")
	require.NoError(t, err)
	assert.Equal(t, want, raw)

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err, "the written file must open as a zip")
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	assert.Contains(t, names, "[Content_Types].xml")
	assert.Contains(t, names, "word/document.xml")
}

func TestNewFile_MimeComesFromTheRegistry(t *testing.T) {
	// Sniffing the bytes we just wrote would answer "application/zip" for a
	// .docx and "text/plain" for an empty .json. We know what we wrote.
	mh, store, _, st, _ := newMutateFixture(t)
	rec := callMutate(t, mh, "newfile", newFileBody("main://", "book", "xlsx"))
	require.Equal(t, http.StatusOK, rec.Code)

	node, err := store.GetNodeByPath(context.Background(), st.ID, mutTestPathHash(st.ID, "book.xlsx"))
	require.NoError(t, err)
	require.NotNil(t, node)
	assert.Equal(t,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		node.Mime)
}

// ---------- the name ----------

func TestNewFile_ExtensionIsAppendedOnlyWhenMissing(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	for _, tc := range []struct{ typed, want string }{
		{"plain", "plain.txt"},
		{"already.txt", "already.txt"},
		{"SHOUTING.TXT", "SHOUTING.TXT"},
		{"two.dots.here", "two.dots.here.txt"},
	} {
		rec := callMutate(t, mh, "newfile", newFileBody("main://", tc.typed, "txt"))
		require.Equal(t, http.StatusOK, rec.Code, tc.typed+": "+rec.Body.String())
		assert.Equal(t, tc.want, decodeNewFile(t, rec.Body.Bytes())["name"])
		_, err := os.Stat(filepath.Join(dir, tc.want))
		assert.NoError(t, err, tc.typed)
	}
}

func TestNewFile_RejectsNamesThatAreNotLeaves(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	for _, bad := range []string{"", "   ", "../escape", "a/b", `a\b`, ".", "..", ".md"} {
		rec := callMutate(t, mh, "newfile", newFileBody("main://", bad, "md"))
		assert.Equal(t, http.StatusBadRequest, rec.Code, "name %q must be refused", bad)
	}
	// Nothing escaped the storage root.
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, "escape.md", e.Name())
	}
}

// ---------- the refusals ----------

func TestNewFile_SecondCreateWithTheSameNameIs409(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "minutes", "docx"))
	require.Equal(t, http.StatusOK, rec.Code)
	first, err := os.ReadFile(filepath.Join(dir, "minutes.docx"))
	require.NoError(t, err)

	rec2 := callMutate(t, mh, "newfile", newFileBody("main://", "minutes.docx", "docx"))
	assert.Equal(t, http.StatusConflict, rec2.Code)
	assert.Equal(t, "NAME_TAKEN", decodeNewFile(t, rec2.Body.Bytes())["code"])

	// The refusal must not have touched the existing file. This is the whole
	// reason creation refuses rather than snapshotting-and-overwriting.
	after, err := os.ReadFile(filepath.Join(dir, "minutes.docx"))
	require.NoError(t, err)
	assert.Equal(t, first, after)
}

func TestNewFile_UnknownTypeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "payload", "exe"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "UNSUPPORTED_TYPE", decodeNewFile(t, rec.Body.Bytes())["code"])

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused type must leave no file behind")
}

func TestNewFile_ReadOnlyStorageIs403(t *testing.T) {
	mh, store, _, st, dir := newMutateFixture(t)
	st.ReadOnly = true
	require.NoError(t, store.UpdateStorage(context.Background(), st))

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "nope", "md"))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestNewFile_LandsInTheRequestedSubfolder(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)
	require.Equal(t, http.StatusOK,
		callMutate(t, mh, "newfolder", map[string]any{"path": "main://", "name": "Reports"}).Code)

	rec := callMutate(t, mh, "newfile", newFileBody("main://Reports", "budget", "xlsx"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "main://Reports/budget.xlsx", decodeNewFile(t, rec.Body.Bytes())["path"])

	_, err := os.Stat(filepath.Join(dir, "Reports", "budget.xlsx"))
	assert.NoError(t, err)
}

func TestNewFile_EveryOfferedTypeCanActuallyBeCreated(t *testing.T) {
	// The registry is published to clients as the list of things they may
	// offer. A type that is advertised and then 500s on click is worse than a
	// type that was never offered, so the two are pinned together here.
	mh, _, _, _, dir := newMutateFixture(t)
	for _, ty := range newdoc.Types() {
		rec := callMutate(t, mh, "newfile", newFileBody("main://", "sample-"+ty.Ext, ty.Ext))
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", ty.Ext, rec.Body.String())
		fi, err := os.Stat(filepath.Join(dir, "sample-"+ty.Ext+"."+ty.Ext))
		require.NoError(t, err, ty.Ext)
		if ty.Group == newdoc.GroupDocument {
			assert.Greater(t, fi.Size(), int64(512), "%s must carry real bytes", ty.Ext)
		}
	}
}

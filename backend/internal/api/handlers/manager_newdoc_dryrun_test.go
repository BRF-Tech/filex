package handlers_test

// ?action=newfile with `dry_run` (filex #211, audit B18): the New document
// dialog asks the server whether a name is free and which free name to offer
// instead, rather than lower-casing the listing itself.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dryRunBody(dir, name, typ string) map[string]any {
	return map[string]any{"path": dir, "name": name, "type": typ, "exact_name": true, "dry_run": true}
}

func TestNewFileDryRun_AFreeNameWritesNothing(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)

	rec := callMutate(t, mh, "newfile", dryRunBody("main://", "notes.md", "md"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeNewFile(t, rec.Body.Bytes())
	assert.Equal(t, true, got["dry_run"])
	assert.Equal(t, false, got["taken"])
	assert.Equal(t, "notes.md", got["name"])
	assert.Equal(t, "main://notes.md", got["path"])

	_, err := os.Stat(filepath.Join(dir, "notes.md"))
	assert.True(t, os.IsNotExist(err), "a dry run must not create the file")
}

func TestNewFileDryRun_ATakenNameIsSaidWithTheServersFreeName(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Untitled.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Untitled (2).txt"), []byte("x"), 0o644))

	rec := callMutate(t, mh, "newfile", dryRunBody("main://", "Untitled.txt", "txt"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeNewFile(t, rec.Body.Bytes())
	assert.Equal(t, true, got["taken"])
	assert.Equal(t, "NAME_TAKEN", got["code"])
	assert.Equal(t, "Untitled (3).txt", got["suggested"], "the numbering is ops.UniqueDestNumbered's")

	b, err := os.ReadFile(filepath.Join(dir, "Untitled.txt"))
	require.NoError(t, err)
	assert.Equal(t, "x", string(b), "a dry run must not touch what is there")
}

// The browser used to compare lower-cased names, so "Report.docx" was refused
// beside "report.docx" on a store where the two are different files. The
// answer is the Stat the create makes; on a case-sensitive disk the other
// spelling is free.
func TestNewFileDryRun_IsTheCreatesOwnExistenceCheck(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.md"), []byte("x"), 0o644))
	if _, err := os.Stat(filepath.Join(dir, "REPORT.md")); err == nil {
		t.Skip("case-insensitive file system: the other spelling IS the same file here")
	}

	rec := callMutate(t, mh, "newfile", dryRunBody("main://", "REPORT.md", "md"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, false, decodeNewFile(t, rec.Body.Bytes())["taken"])

	create := callMutate(t, mh, "newfile", exactFileBody("main://", "REPORT.md", "md"))
	assert.Equal(t, http.StatusOK, create.Code, create.Body.String())
}

// The create's own refusal names the free name too, for a client that did
// not ask first.
func TestNewFile_TheConflictOffersTheFreeName(t *testing.T) {
	mh, _, _, _, dir := newMutateFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "minutes.docx"), []byte("x"), 0o644))

	rec := callMutate(t, mh, "newfile", newFileBody("main://", "minutes", "docx"))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	got := decodeNewFile(t, rec.Body.Bytes())
	assert.Equal(t, "NAME_TAKEN", got["code"])
	assert.Equal(t, "minutes (2).docx", got["suggested"])
}

// A dry run is held to every refusal the create makes - a name that is not
// one is refused, not answered "free".
func TestNewFileDryRun_RefusesWhatTheCreateRefuses(t *testing.T) {
	mh, _, _, _, _ := newMutateFixture(t)
	rec := callMutate(t, mh, "newfile", dryRunBody("main://", "a/b.md", "md"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

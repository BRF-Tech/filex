package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// Saving from the editor into a folder the catalogue has never seen wrote the
// bytes where they belong and the row at the storage ROOT: the handler looked
// the parent up, found nothing, and created the file with no parent. The file
// then listed under neither folder — not the root (its path is not a root
// path) and not its own (no row says that folder exists) — until the next
// full scan re-homed it.
func TestSaveText_IntoAFolderTheCatalogueHasNotSeen(t *testing.T) {
	f := newStagedFixtureWith(t, nil)
	ctx := context.Background()

	require.Equal(t, http.StatusOK, f.saveText(t, "main://notes/2026/september.md", "# September\n"))

	file, err := f.store.GetNodeByPath(ctx, f.storage.ID, pathkey.Hash(f.storage.ID, "/notes/2026/september.md"))
	require.NoError(t, err)
	require.NotNil(t, file, "the saved file has a catalogue row")
	require.NotNil(t, file.ParentID, "the row must hang under its folder, not the storage root")

	parent, err := f.store.GetNode(ctx, *file.ParentID)
	require.NoError(t, err)
	assert.Equal(t, "/notes/2026", parent.Path)
	require.NotNil(t, parent.ParentID, "and that folder under its own parent")

	grand, err := f.store.GetNode(ctx, *parent.ParentID)
	require.NoError(t, err)
	assert.Equal(t, "/notes", grand.Path)
	assert.Nil(t, grand.ParentID, "the chain ends at the storage root")
}

package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// WriteStreamOn's own pin: the redeem reads the destination's current name
// from the ticket's storage id, and the write resolves that name again. Were
// the name to belong to another storage by then (renamed and replaced in
// between), the write must not land there. No door can arrange that race, so
// it is asked of the funnel directly.
func TestWriteStreamOn_ANameOfAnotherStorageWritesNothing(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	a := newAIOps(store, func(id int64) (storage.Driver, error) { return drv, nil }, nil, "")

	_, err = a.WriteStreamOn(ctx, st.ID+1000, "main://pinned.txt", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, errTicketStorageMoved)
	_, statErr := os.Stat(filepath.Join(root, "pinned.txt"))
	assert.True(t, os.IsNotExist(statErr), "nothing written")

	e, err := a.WriteStreamOn(ctx, st.ID, "main://pinned.txt", strings.NewReader("x"), 1)
	require.NoError(t, err, "the storage the ticket names")
	assert.Equal(t, "main://pinned.txt", e.Path)
}

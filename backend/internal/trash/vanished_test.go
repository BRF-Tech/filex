package trash_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Issue #74: a row soft-deleted WHERE IT STOOD — what the storage sync did to
// every file it found gone, up to 0.47 — holds nothing in the trash. It is not
// a trash entry: the listing marks it so the handler leaves it out, and a
// restore refuses it instead of bringing back a row for a file that is not
// there. A row whose bytes are parked in `.filex-trash/` is unaffected.
func TestVanishedRow_IsNoTrashEntryAndDoesNotRestore(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	ghost := r.tombstone(t, "/dışarıda-silindi.txt", model.NodeTypeFile)

	key := "/.filex-trash/1790000000-abc123__gercek.txt"
	r.write(t, key[1:], "trashed bytes")
	real := r.row(t, "/gercek.txt", model.NodeTypeFile)
	require.NoError(t, r.store.SoftDeleteAndRetag(ctx, real.ID, key, pathkey.Hash(r.sid, key), "/gercek.txt"))

	g, err := r.store.GetNode(ctx, ghost.ID)
	require.NoError(t, err)
	assert.True(t, trash.Vanished(g))
	rn, err := r.store.GetNode(ctx, real.ID)
	require.NoError(t, err)
	assert.False(t, trash.Vanished(rn), "a row whose bytes are in the trash is a trash entry")
	live := r.row(t, "/canli.txt", model.NodeTypeFile)
	assert.False(t, trash.Vanished(live), "a live row is never vanished")
	assert.False(t, trash.Vanished(nil))

	entries, _, err := r.svc.List(ctx, &r.sid, 50, 0)
	require.NoError(t, err)
	flags := map[int64]bool{}
	for _, e := range entries {
		flags[e.ID] = e.Vanished
	}
	assert.True(t, flags[ghost.ID], "the listing does not mark the row deleted where it stood")
	assert.False(t, flags[real.ID])

	err = r.svc.Restore(ctx, ghost.ID)
	assert.ErrorIs(t, err, trash.ErrNotInTrash, "a restore of a row with nothing in the trash must be refused")
	still, err := r.store.GetNode(ctx, ghost.ID)
	require.NoError(t, err)
	assert.NotNil(t, still.DeletedAt, "the refused restore brought the row back to life")

	require.NoError(t, r.svc.Restore(ctx, real.ID), "a real trash entry still restores")
	assert.True(t, r.exists("gercek.txt"))
}

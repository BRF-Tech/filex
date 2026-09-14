package protocolsync

// A move whose bytes have already landed on an OCCUPIED destination.
//
// Every caller here runs after the storage has done the move — WebDAV MOVE
// with `Overwrite: T`, an S3 copy+delete, an SFTP/FTPS/NFS rename, the AI/MCP
// move. Whatever the destination held is gone by then. The catalogue still has
// a live row at that path, and the live-only unique index over
// (storage_id, path_hash) (migration 00032) refuses to let the moved file's
// row take the address.
//
// The old answer was to soft-delete the moved file's row where it stood. That
// is not a deletion: the bytes are at the DESTINATION, never under
// `.filex-trash/`, and `storage_key` still names the path they left. The row
// lands in the user-facing trash listing, Restore takes nothing back and
// reports success, and nothing reaps it — sync.reconcileTrash only looks at
// LIVE rows under the trash prefix.
//
// Found by @alfatm in his fork (a4241577); the tests are his, ported.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestMoveRowsTakesTheDestinationFromAStaleRow(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "Belgeler/rapor.txt", 4, "text/plain"))
	require.True(t, s.Write(ctx, st, "Arsiv/rapor.txt", 9, "text/plain"))

	src, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Belgeler/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, src)
	occupant, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Arsiv/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, occupant)

	assert.True(t, s.MoveRows(ctx, st, "Belgeler/rapor.txt", "Arsiv/rapor.txt"),
		"the bytes are at the destination; the row has to say so")

	got, err := s.Store.GetNode(ctx, src.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.DeletedAt,
		"the moved file's row was parked in the trash while its bytes sit at the destination")
	assert.Equal(t, "/Arsiv/rapor.txt", got.Path)
	assert.Equal(t, "/Arsiv/rapor.txt", got.StorageKey,
		"storage_key is what versioning, quarantine and the id-addressed download hand the driver")

	at, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Arsiv/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, at)
	assert.Equal(t, src.ID, at.ID,
		"the destination answers with somebody else's identity: the overwritten file's versions, shares and comments now hang off these bytes")

	_, err = s.Store.GetNode(ctx, occupant.ID)
	assert.Error(t, err,
		"the row for the bytes this move overwrote is still live, so two rows claim one file")

	_, total, err := s.Store.ListTrashed(ctx, &st.ID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, total,
		"a phantom trash entry: its bytes were never retagged into .filex-trash, so Restore reports success and delivers nothing")
}

// A DIRECTORY row holding the destination is left where it is. nodes.parent_id
// cascades, so dropping a folder row would take its whole cached subtree with
// it, silently and past the search index.
func TestMoveRowsLeavesADirectoryHoldingTheDestinationAlone(t *testing.T) {
	installEmitter(t)
	s, st := newSyncer(t)
	ctx := context.Background()

	require.True(t, s.Write(ctx, st, "Belgeler/rapor.txt", 4, "text/plain"))
	_, err := s.EnsureDirChain(ctx, st, "Arsiv/rapor.txt")
	require.NoError(t, err)
	require.True(t, s.Write(ctx, st, "Arsiv/rapor.txt/icerik.txt", 4, "text/plain"))

	dir, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Arsiv/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, dir)
	require.Equal(t, model.NodeTypeDirectory, dir.Type)
	inside, err := s.Store.GetNodeByPath(ctx, st.ID, hashOf(st.ID, "/Arsiv/rapor.txt/icerik.txt"))
	require.NoError(t, err)
	require.NotNil(t, inside)

	assert.False(t, s.MoveRows(ctx, st, "Belgeler/rapor.txt", "Arsiv/rapor.txt"))

	kept, err := s.Store.GetNode(ctx, inside.ID)
	require.NoError(t, err)
	require.NotNil(t, kept, "the cached subtree of the folder at the destination was cascaded away")
}

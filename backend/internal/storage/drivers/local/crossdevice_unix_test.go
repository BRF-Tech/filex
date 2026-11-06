//go:build unix

package local

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// A pipe cannot be carried: opening one nobody writes to never returns (issue
// #38), and skipping it, the way a copy does, would delete it with the folder.
// Nothing is removed, and the refusal names what is in the way.
func TestMove_APipeRefusesTheMoveAndLeavesEverything(t *testing.T) {
	d, root := mounted(t)
	src := filepath.Join(root, "gdrive", "folder")
	put(t, filepath.Join(src, "a.txt"), "A")
	pipe := filepath.Join(src, "z.fifo")
	require.NoError(t, syscall.Mkfifo(pipe, 0o600))

	_, err := promptly(t, "Move", func() (struct{}, error) {
		return struct{}{}, d.Move(context.Background(), "gdrive/folder", ".filex-trash/key__folder")
	})

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	require.ErrorIs(t, err, storage.ErrKindConflict)
	assert.Contains(t, err.Error(), "z.fifo")
	assert.Equal(t, "A", body(t, filepath.Join(src, "a.txt")))
	info, serr := os.Lstat(pipe)
	require.NoError(t, serr)
	assert.NotZero(t, info.Mode()&os.ModeNamedPipe)
	assert.NoDirExists(t, filepath.Join(root, ".filex-trash", "key__folder"))
	noStage(t, filepath.Join(root, ".filex-trash"))
}

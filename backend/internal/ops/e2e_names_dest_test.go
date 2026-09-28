package ops

// wiring:e2 names — inside an end-to-end encrypted folder whose names are
// encrypted, a colliding copy or move must NOT be given a name the server
// made up (`<ciphertext>-copy`): nobody could ever decrypt it. It is refused
// with ErrEncryptedNameTaken instead — and only there, so every other paste
// keeps its `-copy` suffix exactly as before.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// A name the browser would store: base64url, no dot, >= 23 characters.
const encName = "RK_chqfshg00TF_YAhrf6nAQFJIkmjUbmdrmCcSjV9Y"

func encDestDriver(t *testing.T) *local.Driver {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"enc/.filex-e2e.json",
		"enc/" + encName,
		"enc/notes.txt",
		"enc/sub/" + encName,
		"plain/" + encName,
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	}
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	return drv
}

func TestUniqueCopyDest_EncryptedNameCollisionIsRefused(t *testing.T) {
	drv := encDestDriver(t)
	ctx := context.Background()

	_, err := uniqueCopyDest(ctx, drv, "", "/enc/"+encName)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrEncryptedNameTaken), "want ErrEncryptedNameTaken, got %v", err)
	require.True(t, errors.Is(err, ErrNoFreeName), "callers that answer 'name taken' must answer it here too")

	// A subfolder of the encrypted folder is inside it too.
	_, err = uniqueCopyDest(ctx, drv, "", "/enc/sub/"+encName)
	require.True(t, errors.Is(err, ErrEncryptedNameTaken), "got %v", err)

	// Duplicate (source == destination) is a collision like any other.
	_, err = uniqueCopyDest(ctx, drv, "/enc/"+encName, "/enc/"+encName)
	require.True(t, errors.Is(err, ErrEncryptedNameTaken), "got %v", err)
}

func TestUniqueCopyDest_EverythingElseKeepsItsSuffix(t *testing.T) {
	drv := encDestDriver(t)
	ctx := context.Background()

	// A plaintext name inside an encrypted folder (a content-only folder, or a
	// file written over WebDAV) is still de-collided the old way.
	got, err := uniqueCopyDest(ctx, drv, "", "/enc/notes.txt")
	require.NoError(t, err)
	require.Equal(t, "/enc/notes-copy.txt", got)

	// A name that merely LOOKS encrypted outside any encrypted folder too.
	got, err = uniqueCopyDest(ctx, drv, "", "/plain/"+encName)
	require.NoError(t, err)
	require.Equal(t, "/plain/"+encName+"-copy", got)

	// No collision, no question asked.
	got, err = uniqueCopyDest(ctx, drv, "", "/enc/free-name")
	require.NoError(t, err)
	require.Equal(t, "/enc/free-name", got)
}

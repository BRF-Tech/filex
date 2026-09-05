package writehook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Unconfigured is a no-op: every existing deployment and test keeps its
// byte-for-byte behaviour until someone wires a guard. This is also what an
// installation with FILEX_VERSIONS_ON_OVERWRITE=0 runs.
func TestBeforeOverwrite_NilGuardIsNoOp(t *testing.T) {
	writehook.ConfigureOverwriteGuard(nil)
	require.NoError(t, writehook.BeforeOverwrite(context.Background(), 1, "/a.txt"))
}

// The guard's error is the caller's error — that is the whole point of a
// fail-closed gate: no snapshot, no write.
func TestBeforeOverwrite_PropagatesGuardError(t *testing.T) {
	boom := errors.New("snapshot failed")
	writehook.ConfigureOverwriteGuard(func(_ context.Context, _ int64, _ string) error {
		return boom
	})
	t.Cleanup(func() { writehook.ConfigureOverwriteGuard(nil) })

	err := writehook.BeforeOverwrite(context.Background(), 7, "/x/y.pdf")
	assert.ErrorIs(t, err, boom)
}

func TestBeforeOverwrite_PassesStorageAndPath(t *testing.T) {
	var gotID int64
	var gotRel string
	writehook.ConfigureOverwriteGuard(func(_ context.Context, id int64, rel string) error {
		gotID, gotRel = id, rel
		return nil
	})
	t.Cleanup(func() { writehook.ConfigureOverwriteGuard(nil) })

	require.NoError(t, writehook.BeforeOverwrite(context.Background(), 42, "folder/file.pdf"))
	assert.Equal(t, int64(42), gotID)
	assert.Equal(t, "folder/file.pdf", gotRel)
}

// A guard that returns nil must not be mistaken for an absent one: the write
// proceeds either way, but only the wired case may have run a snapshot.
func TestBeforeOverwrite_ConfiguredGuardRuns(t *testing.T) {
	calls := 0
	writehook.ConfigureOverwriteGuard(func(context.Context, int64, string) error {
		calls++
		return nil
	})
	t.Cleanup(func() { writehook.ConfigureOverwriteGuard(nil) })

	require.NoError(t, writehook.BeforeOverwrite(context.Background(), 1, "a"))
	require.NoError(t, writehook.BeforeOverwrite(context.Background(), 1, "b"))
	assert.Equal(t, 2, calls)
}

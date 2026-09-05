package versioning_test

// Unit tests for the pre-write overwrite guard (guard.go): what it snapshots,
// what it deliberately leaves alone, and -- the part that matters most -- that
// a snapshot it cannot take becomes an error the caller must not write past.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// newGuardFixture gives a real local driver over a temp root, so a snapshot
// actually copies bytes and can actually be made to fail.
func newGuardFixture(t *testing.T) (db.Store, *versioning.Service, int64, string) {
	t.Helper()
	root := t.TempDir()
	_, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: root, Enabled: true,
	})
	require.NoError(t, err)

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	svc := versioning.New(store, func(int64) (storage.Driver, error) { return drv, nil })
	return store, svc, st.ID, root
}

// seedLiveFile puts real bytes on the driver AND catalogues them, which is the
// only state in which the guard has anything to protect.
func seedLiveFile(t *testing.T, store db.Store, storageID int64, root, rel, body string) *model.Node {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))

	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID:  storageID,
		Name:       filepath.Base(rel),
		Path:       "/" + rel,
		PathHash:   pathkey.Hash(storageID, rel),
		StorageKey: rel,
		Type:       model.NodeTypeFile,
		Size:       int64(len(body)),
	})
	require.NoError(t, err)
	return n
}

// The headline: an existing catalogued file is snapshotted before the caller
// replaces it, and the snapshot holds the OLD bytes.
func TestGuardOverwrite_SnapshotsExistingFile(t *testing.T) {
	store, svc, stID, root := newGuardFixture(t)
	n := seedLiveFile(t, store, stID, root, "notes.txt", "ORIGINAL")

	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "notes.txt"))

	versions, err := svc.List(context.Background(), n.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	snap, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(versions[0].StorageKey)))
	require.NoError(t, err)
	assert.Equal(t, "ORIGINAL", string(snap))
}

// A path spelled without a leading slash and one that has it must address the
// same node -- pathkey.Hash cleans what it is given, and the guard relies on
// that rather than normalising a second time.
func TestGuardOverwrite_AcceptsEitherPathSpelling(t *testing.T) {
	store, svc, stID, root := newGuardFixture(t)
	n := seedLiveFile(t, store, stID, root, "dir/file.bin", "OLD")

	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "/dir/file.bin"))

	versions, err := svc.List(context.Background(), n.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

// Nothing catalogued at this path: a first write costs one lookup and takes no
// snapshot. Getting this wrong would put a version row on every new file.
func TestGuardOverwrite_FirstWriteTakesNoSnapshot(t *testing.T) {
	_, svc, stID, _ := newGuardFixture(t)
	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "brand-new.txt"))
}

// Directories are not versioned, so a node that is one must not be snapshotted.
func TestGuardOverwrite_IgnoresDirectories(t *testing.T) {
	store, svc, stID, _ := newGuardFixture(t)
	d, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: stID, Name: "sub", Path: "/sub",
		PathHash: pathkey.Hash(stID, "sub"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "sub"))
	versions, err := svc.List(context.Background(), d.ID)
	require.NoError(t, err)
	assert.Empty(t, versions)
}

// The bookkeeping trees are exempt. Without this, snapshotting a write into
// .versions/ would recurse -- Restore writes the live path back from there.
func TestGuardOverwrite_SkipsInternalTrees(t *testing.T) {
	store, svc, stID, root := newGuardFixture(t)
	for _, rel := range []string{
		".versions/9/1",
		".thumbs/abc.jpg",
		".filex-trash/old.txt",
		"project/.keepdir",
	} {
		n := seedLiveFile(t, store, stID, root, rel, "internal")
		require.NoError(t, svc.GuardOverwrite(context.Background(), stID, rel), rel)
		versions, err := svc.List(context.Background(), n.ID)
		require.NoError(t, err)
		assert.Empty(t, versions, "expected no snapshot for internal path %q", rel)
	}
}

// The contract the whole feature rests on: a snapshot that cannot be taken is
// an ERROR, not a warning. A caller that writes past this destroys bytes with
// no copy anywhere.
func TestGuardOverwrite_SnapshotFailureIsAnError(t *testing.T) {
	root := t.TempDir()
	_, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: root, Enabled: true,
	})
	require.NoError(t, err)

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	seedLiveFile(t, store, st.ID, root, "doomed.txt", "PRECIOUS")

	// The resolver fails, so Snapshot cannot reach the bytes at all.
	svc := versioning.New(store, func(int64) (storage.Driver, error) {
		return nil, errors.New("storage unreachable")
	})

	err = svc.GuardOverwrite(context.Background(), st.ID, "doomed.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage unreachable")
}

// A client-side encrypted file is snapshotted as the ciphertext it already is:
// the guard never reads content, so the filexe2e magic prefix survives and the
// version stays decryptable by whoever holds the password.
func TestGuardOverwrite_EncryptedFileSnapshotsAsCiphertext(t *testing.T) {
	store, svc, stID, root := newGuardFixture(t)
	cipher := "filexe2e\x00\x01ciphertext-bytes"
	n := seedLiveFile(t, store, stID, root, "vault/secret.txt", cipher)

	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "vault/secret.txt"))

	versions, err := svc.List(context.Background(), n.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	snap, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(versions[0].StorageKey)))
	require.NoError(t, err)
	assert.Equal(t, cipher, string(snap), "snapshot must be byte-identical ciphertext")
}

// The encrypted-folder marker is an ordinary file to the guard, and that is the
// wanted behaviour: losing it makes the folder permanently unopenable, so
// keeping history of it is a gain, not a leak (it holds only public salt).
func TestGuardOverwrite_SnapshotsE2EMarker(t *testing.T) {
	store, svc, stID, root := newGuardFixture(t)
	n := seedLiveFile(t, store, stID, root, "vault/.filex-e2e.json", `{"salt":"abc"}`)

	require.NoError(t, svc.GuardOverwrite(context.Background(), stID, "vault/.filex-e2e.json"))
	versions, err := svc.List(context.Background(), n.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

// A nil service is inert rather than a panic: routes.go only wires the guard
// when d.Versions is non-nil, and BeforeOverwrite is called unconditionally.
func TestGuardOverwrite_NilServiceIsSafe(t *testing.T) {
	var svc *versioning.Service
	require.NoError(t, svc.GuardOverwrite(context.Background(), 1, "x.txt"))
}

package server

// Every storage writes into a folder of its own on a target (#186, the maintainers'
// decision, 2026-10-06), and filex's own folders never reach the target.
//
// ⚠ Before: a target shared by two storages was written at its root, so two
// storages each holding "rapor.docx" overwrote each other's backup, and the
// trash, version history, thumbnails, open-with copies and drafts were copied
// like a person's files.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// another adds a storage linked to the fixture's target.
func (f *cacheFixture) another(t *testing.T, name, mount string) *model.Storage {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	tid := f.target.ID
	st, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: mount, ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: &tid,
	})
	require.NoError(t, err)
	return st
}

func writeThrough(t *testing.T, c *storageCache, id int64, name, body string) *storage.ReplicatedDriver {
	t.Helper()
	drv, err := c.resolve(id)
	require.NoError(t, err)
	require.NoError(t, drv.(storage.Writer).Write(context.Background(), name, strings.NewReader(body), int64(len(body))))
	rd, ok := storage.AsReplicated(drv)
	require.True(t, ok)
	return rd
}

func readTarget(t *testing.T, dir string, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{dir}, parts...)...))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestReplicaFolder_TwoStoragesOnOneTargetNeverMeet(t *testing.T) {
	f := newCacheFixture(t, true)
	twin := f.another(t, "Arsiv", "/arsiv-2") // the same name but for case: one folder on a case-insensitive share

	writeThrough(t, f.cache, f.st.ID, "rapor.docx", "from the first").Stop()
	writeThrough(t, f.cache, twin.ID, "rapor.docx", "from the second").Stop()

	first, err := f.store.GetReplicaLink(context.Background(), f.st.ID)
	require.NoError(t, err)
	second, err := f.store.GetReplicaLink(context.Background(), twin.ID)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.NotEqual(t, strings.ToLower(first.Folder), strings.ToLower(second.Folder), "two storages share a folder on the target")
	assert.Equal(t, "from the first", readTarget(t, f.targetDir, first.Folder, "rapor.docx"))
	assert.Equal(t, "from the second", readTarget(t, f.targetDir, second.Folder, "rapor.docx"),
		"one storage's backup overwrote the other's")
}

func TestReplicaFolder_ARenameKeepsTheFolder(t *testing.T) {
	f := newCacheFixture(t, true)
	writeThrough(t, f.cache, f.st.ID, "a.txt", "x").Stop()
	before := f.folder()
	require.Equal(t, "arsiv", before)

	f.st.Name = "Yeni Arşiv"
	require.NoError(t, f.store.UpdateStorage(context.Background(), f.st))
	f.cache.forget(f.st.ID)
	writeThrough(t, f.cache, f.st.ID, "b.txt", "y").Stop()

	assert.Equal(t, before, f.folder(), "a rename moved the backup to a new folder and left the old one behind")
	assert.Equal(t, "y", readTarget(t, f.targetDir, before, "b.txt"))
}

func TestReplicaFolder_RepairAndTheCopysComparisonUseIt(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, true)
	rd := writeThrough(t, f.cache, f.st.ID, "kept.txt", "on both")
	rd.Stop()

	// The file changes on the primary behind filex's back; Fix all's repair
	// and the initial copy's "already there" look in the storage's folder.
	require.NoError(t, os.WriteFile(filepath.Join(f.primaryDir, "kept.txt"), []byte("changed"), 0o644))
	require.NoError(t, rd.Repair(ctx, "kept.txt", "write"))
	assert.Equal(t, "changed", readTarget(t, f.targetDir, "arsiv", "kept.txt"), "a repair wrote outside the storage's folder")
	held, err := f.store.GetReplicaLink(ctx, f.st.ID)
	require.NoError(t, err)
	st, err := os.Stat(filepath.Join(f.primaryDir, "kept.txt"))
	require.NoError(t, err)
	assert.True(t, rd.ReplicaHolds(ctx, storage.Object{Path: "/kept.txt", Size: st.Size(), Mtime: st.ModTime()}),
		"the target's copy in %q was not found", held.Folder)
}

func TestReplicaInternalFolders_NeverReachTheTarget(t *testing.T) {
	f := newCacheFixture(t, true)
	drv, err := f.cache.resolve(f.st.ID)
	require.NoError(t, err)
	ctx := context.Background()
	w := drv.(storage.Writer)
	for _, p := range []string{".thumbs/1.jpg", ".filex-open/s1-rapor.docx", ".filex-drafts/7/k/yeni.docx", ".versions/12/1", ".filex-trash/x/old.txt"} {
		require.NoError(t, w.Write(ctx, p, strings.NewReader("internal"), 8))
	}
	require.NoError(t, w.Write(ctx, "belge.txt", strings.NewReader("mine"), 4))
	rd, _ := storage.AsReplicated(drv)

	// Into the trash: on the backup that is a delete.
	require.Eventually(t, func() bool { return f.onTarget("belge.txt") }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, drv.(storage.Mover).Move(ctx, "belge.txt", ".filex-trash/9/belge.txt"))
	require.Eventually(t, func() bool { return !f.onTarget("belge.txt") }, 5*time.Second, 10*time.Millisecond,
		"a file moved to the trash stayed in the backup's live tree")

	// Out of the trash: the backup gets it back.
	require.NoError(t, drv.(storage.Mover).Move(ctx, ".filex-trash/9/belge.txt", "belge.txt"))
	require.Eventually(t, func() bool { return f.onTarget("belge.txt") }, 5*time.Second, 10*time.Millisecond,
		"a file restored from the trash never came back to the backup")
	rd.Stop()

	for _, dir := range []string{".thumbs", ".filex-open", ".filex-drafts", ".versions", ".filex-trash"} {
		assert.False(t, f.onTarget(dir), "%s reached the target", dir)
	}
}

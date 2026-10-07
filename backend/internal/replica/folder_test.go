package replica

// Every storage in a folder of its own on its target (#186, the maintainers'
// decision, 2026-10-06): chosen once from its name, safe on every backend, unique per
// target, kept through a rename and a target switched off and on, changed
// only on purpose.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func TestFolderName_SafeOnEveryBackend(t *testing.T) {
	cases := map[string]string{
		"Arşiv":                 "Arşiv",
		"Müşteri Belgeleri":     "Müşteri Belgeleri",
		`a/b\c:d*e?f"g<h>i|j`:   "a-b-c-d-e-f-g-h-i-j",
		"  .gizli.  ":           "gizli",
		".versions":             "versions",
		"--x--":                 "x",
		"CON":                   "CON-7",
		"lpt1.txt":              "lpt1-7.txt",
		"":                      "storage-7",
		"///":                   "storage-7",
		strings.Repeat("ç", 80): strings.Repeat("ç", 64),
	}
	for in, want := range cases {
		assert.Equal(t, want, FolderName(in, 7), "FolderName(%q)", in)
	}
}

func TestEnsureFolder_ChosenOnceUniquePerTargetKeptThroughARename(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	a := newLinked(t, store)
	a.st.Name = "Arşiv"
	require.NoError(t, store.UpdateStorage(ctx, a.st))

	first, err := EnsureFolder(ctx, store, a.st, a.target.ID)
	require.NoError(t, err)
	assert.Equal(t, "Arşiv", first)

	// Another storage of the same name (but for case) on the same target.
	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	tid := a.target.ID
	b, err := store.CreateStorage(ctx, &model.Storage{
		Name: "arşiv", Driver: "local", MountPath: "/arsiv-b", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: &tid,
	})
	require.NoError(t, err)
	second, err := EnsureFolder(ctx, store, b, a.target.ID)
	require.NoError(t, err)
	assert.NotEqual(t, strings.ToLower(first), strings.ToLower(second), "two storages got one folder")
	assert.True(t, strings.HasSuffix(second, "-"+itoa(b.ID)), "the second takes its id: %q", second)

	// A rename moves nothing.
	a.st.Name = "Yeni ad"
	require.NoError(t, store.UpdateStorage(ctx, a.st))
	again, err := EnsureFolder(ctx, store, a.st, a.target.ID)
	require.NoError(t, err)
	assert.Equal(t, first, again, "a rename moved the backup to another folder")
}

func TestSyncInitialCopy_ASwitchedOffTargetKeepsTheFolder(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	l := newLinked(t, store)
	svc := New(store, staticWrappers{}, newMemQueue(), nil)
	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	link, err := store.GetReplicaLink(ctx, l.st.ID)
	require.NoError(t, err)
	require.NotNil(t, link, "linking chose no folder")

	l.target.Enabled = false
	require.NoError(t, store.UpdateReplicationTarget(ctx, l.target))
	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	kept, err := store.GetReplicaLink(ctx, l.st.ID)
	require.NoError(t, err)
	require.NotNil(t, kept, "switching the target off gave the folder up: switched on, another storage could take it")
	assert.Equal(t, link.Folder, kept.Folder)

	// Unlinked: the folder goes with the link.
	l.st.ReplicaTargetID = nil
	require.NoError(t, store.UpdateStorage(ctx, l.st))
	require.NoError(t, svc.LinkChanged(ctx, l.st.ID))
	gone, err := store.GetReplicaLink(ctx, l.st.ID)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

func TestSetFolder_OnPurposeAndNotOntoAnotherStorage(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	a := newLinked(t, store)
	svc := New(store, staticWrappers{}, newMemQueue(), nil)
	_, err := EnsureFolder(ctx, store, a.st, a.target.ID)
	require.NoError(t, err)

	got, err := svc.SetFolder(ctx, a.st.ID, "yedek/2026")
	require.NoError(t, err)
	assert.Equal(t, "yedek-2026", got.Folder, "the folder is made safe like a chosen one")

	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	tid := a.target.ID
	b, err := store.CreateStorage(ctx, &model.Storage{
		Name: "ikinci", Driver: "local", MountPath: "/ikinci", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: &tid,
	})
	require.NoError(t, err)
	_, err = EnsureFolder(ctx, store, b, a.target.ID)
	require.NoError(t, err)
	_, err = svc.SetFolder(ctx, b.ID, "YEDEK-2026")
	assert.ErrorIs(t, err, ErrFolderTaken, "a storage was moved into another storage's folder")
}

func TestWalkFiles_LeavesFilexsOwnFoldersOut(t *testing.T) {
	d := &treeDriver{dirs: map[string][]storage.Object{
		"/":                 {fileObj("a.txt"), dirObj(".versions"), dirObj(".filex-trash"), dirObj("docs"), dirObj(".thumbs")},
		"/.versions":        {fileObj("1")},
		"/.filex-trash":     {fileObj("old.txt")},
		"/.thumbs":          {fileObj("t.jpg")},
		"/docs":             {fileObj("b.txt"), dirObj(".filex-open")},
		"/docs/.filex-open": {fileObj("s-x.docx")},
	}}
	assert.Equal(t, []string{"/a.txt", "/docs/b.txt"}, walkAll(t, d, ""),
		"the copy walked into filex's own folders")
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

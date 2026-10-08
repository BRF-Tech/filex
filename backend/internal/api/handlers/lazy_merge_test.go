package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// mergeListing lays a folder's catalogue rows over its listing on disk.
// Every rule of it, one entry each.
func TestMergeListing(t *testing.T) {
	old := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	rows := []*model.Node{
		{ID: 1, Name: "ayni.txt", Type: model.NodeTypeFile, Size: 3, BackendMtime: &old},
		{ID: 2, Name: "degisti.txt", Type: model.NodeTypeFile, Size: 3, BackendMtime: &old},
		{ID: 3, Name: "gitti.txt", Type: model.NodeTypeFile, Size: 3, TransferState: model.TransferStateStored},
		{ID: 4, Name: "yukleniyor.bin", Type: model.NodeTypeFile, Size: 9, TransferState: model.TransferStateStaged},
		{ID: 5, Name: "klasor", Type: model.NodeTypeDirectory},
		{ID: 6, Name: "turu-degisti", Type: model.NodeTypeFile, Size: 1},
	}
	objs := []storage.Object{
		{Name: "ayni.txt", Kind: storage.KindFile, Size: 3, Mtime: old},
		{Name: "degisti.txt", Kind: storage.KindFile, Size: 7, Mtime: now},
		{Name: "klasor", Kind: storage.KindDirectory, Size: 4096, Mtime: now},
		{Name: "yeni.txt", Kind: storage.KindFile, Size: 1, Mtime: now},
		{Name: "turu-degisti", Kind: storage.KindDirectory, Mtime: now},
	}
	got, disk := mergeListing(rows, objs)

	byName := map[string]*model.Node{}
	for _, n := range got {
		byName[n.Name] = n
	}
	assert.Same(t, rows[0], byName["ayni.txt"], "an unchanged entry is its row")
	if assert.NotNil(t, byName["degisti.txt"]) {
		assert.Equal(t, int64(2), byName["degisti.txt"].ID, "a drifted entry keeps its row's identity")
		assert.Equal(t, int64(7), byName["degisti.txt"].Size, "and shows the disk's size")
		assert.True(t, byName["degisti.txt"].BackendMtime.Equal(now), "and the disk's date")
		assert.Equal(t, int64(3), rows[1].Size, "on a copy: the stored row is the reconcile's to update")
	}
	assert.Nil(t, byName["gitti.txt"], "a row that is not on disk is not listed")
	assert.NotNil(t, byName["yukleniyor.bin"], "an upload still on its way is")
	assert.Same(t, rows[4], byName["klasor"])
	assert.Nil(t, byName["turu-degisti"], "a row of another kind is not the entry on disk")

	var diskNames []string
	for _, o := range disk {
		diskNames = append(diskNames, o.Name)
	}
	assert.ElementsMatch(t, []string{"yeni.txt", "turu-degisti"}, diskNames)
}

// A link the catalogue knows, listed while the catalogue cannot vouch for its
// folder (the first sync still running, a lazy folder not caught up). The row
// carries the reason the last sync recorded (hydrateLinkStates, migration
// 00098); the storage's listing, read for the merge, is newer and wins when it
// gives one. Before 0.54 the row knew no reason at all and the same folder
// said "Outside storage" one moment (driver only) and the general "Link" the
// next (merged).
func TestMergedListing_ALinkRowSaysTheStoragesReason(t *testing.T) {
	rows := []*model.Node{
		// Recorded before the target came back outside the root.
		{ID: 1, Name: "archive", Path: "/archive", Type: model.NodeTypeSymlink, LinkState: storage.LinkBroken},
		// Recorded, and the storage now gives no reason: the record stands.
		{ID: 2, Name: "old", Path: "/old", Type: model.NodeTypeSymlink, LinkState: storage.LinkOutsideRoot},
		// Never recorded, no reason from the storage either.
		{ID: 3, Name: "gone", Path: "/gone", Type: model.NodeTypeSymlink},
		{ID: 4, Name: "README.md", Path: "/README.md", Type: model.NodeTypeFile, Size: 3},
	}
	objs := []storage.Object{
		{Name: "archive", Kind: storage.KindSymlink, Metadata: map[string]string{storage.MetaLinkState: storage.LinkOutsideRoot}},
		{Name: "old", Kind: storage.KindSymlink},
		{Name: "gone", Kind: storage.KindSymlink},
		{Name: "README.md", Kind: storage.KindFile, Size: 3},
	}
	merged, _ := mergeListing(rows, objs)
	files := projectFileNodes("projects", merged, false, nil, nil, 0)

	byName := map[string]map[string]any{}
	for _, e := range files {
		byName[e["basename"].(string)] = e
	}
	assert.Equal(t, true, byName["archive"]["symlink"])
	assert.Equal(t, storage.LinkOutsideRoot, byName["archive"]["link_state"], "the storage said why just now: outside the root")
	assert.Equal(t, storage.LinkBroken, rows[0].LinkState, "on a copy: the recorded reason is the sync's to update")
	assert.Equal(t, storage.LinkOutsideRoot, byName["old"]["link_state"], "a link the storage gave no reason for keeps the recorded one")
	assert.Equal(t, true, byName["gone"]["symlink"])
	_, said := byName["gone"]["link_state"]
	assert.False(t, said, "no reason recorded and none given: the general case")
	_, onFile := byName["README.md"]["link_state"]
	assert.False(t, onFile, "an ordinary row is not a link")
}

// The catalogue's own listing - every listing after a storage's first sync -
// sends the reason the sync recorded for a link row. It used to send
// `symlink: true` alone, so the explorer said the general "Link" for a link
// it had just called "Outside storage" in the same folder before the sync.
func TestProjectFileNodes_ALinkRowSendsItsRecordedReason(t *testing.T) {
	files := projectFileNodes("projects", []*model.Node{
		{ID: 1, Name: "archive", Path: "/archive", Type: model.NodeTypeSymlink, LinkState: storage.LinkOutsideRoot},
		{ID: 2, Name: "gone", Path: "/gone", Type: model.NodeTypeSymlink},
	}, false, nil, nil, 0)
	byName := map[string]map[string]any{}
	for _, e := range files {
		byName[e["basename"].(string)] = e
	}
	assert.Equal(t, true, byName["archive"]["symlink"])
	assert.Equal(t, storage.LinkOutsideRoot, byName["archive"]["link_state"])
	assert.Equal(t, "file", byName["archive"]["type"], "the wire type stays the closed file | dir union")
	_, said := byName["gone"]["link_state"]
	assert.False(t, said, "a row with no recorded reason sends none")
}

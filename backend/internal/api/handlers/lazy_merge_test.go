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

package dbtest

import (
	"context"
	"encoding/binary"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// VaultKeyFile is a vault's key file as the format reads it
// (docs/E2E-VAULT-FORMAT.md → The key file): v 3, req ["vault"], a wrapped
// folder key, packs of 2^22 bytes. Its slots open nothing: the doors under
// test never decrypt.
const VaultKeyFile = `{"v":3,"req":["vault"],"salt":"c2FsdA==","iter":1000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","vault":{"v":1,"id":"wpU155hSR3hD1u8bSnGv7w","pack":22}}`

// VaultSeed names what SeedVault put in place, relative to the storage.
type VaultSeed struct {
	Root    string // the vault folder
	KeyFile string
	Index   string // generation 1's index file
	Pack    string // one pack
}

// SeedVault puts a vault at rel on a local storage whose root is dir: its key
// file, generation 1's index file and one pack (its header only - the doors
// under test read no further), on disk and in the catalogue. The catalogue
// rows matter: the vault rule finds a vault by its key file's row
// (vaultlock.Finder), as every door's writegate check does.
func SeedVault(t *testing.T, store db.Store, storageID int64, dir, rel string) VaultSeed {
	t.Helper()
	ctx := context.Background()
	rel = strings.Trim(rel, "/")
	idx := make([]byte, e2e.VaultIndexMinSize)
	copy(idx, e2e.VaultMagicPrefix)
	idx[8], idx[9] = e2e.VaultFormat, e2e.VaultKindIndex
	binary.BigEndian.PutUint64(idx[16:24], 1)
	id := [16]byte{0xb0, 0x67, 0xd7, 0xbc, 0xd6, 0x2c, 0x9f, 0x81, 0x72, 0x16, 0xa5, 0xef, 0x1b, 0x1b, 0x65, 0x3b}
	pack := make([]byte, e2e.VaultPackHeaderLen)
	copy(pack, e2e.VaultMagicPrefix)
	pack[8], pack[9], pack[10] = e2e.VaultFormat, e2e.VaultKindPack, e2e.VaultDefaultPackLog2
	copy(pack[16:32], id[:])

	seed := VaultSeed{
		Root:    rel,
		KeyFile: rel + "/" + e2e.MarkerName,
		Index:   rel + "/" + e2e.VaultIndexPath(1),
		Pack:    rel + "/" + e2e.VaultPackPath(id),
	}
	files := map[string][]byte{seed.KeyFile: []byte(VaultKeyFile), seed.Index: idx, seed.Pack: pack}
	for p, b := range files {
		abs := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("dbtest: vault: %v", err)
		}
		if err := os.WriteFile(abs, b, 0o644); err != nil {
			t.Fatalf("dbtest: vault: %v", err)
		}
	}

	ids := map[string]int64{}
	var row func(p string, isDir bool, size int64) int64
	row = func(p string, isDir bool, size int64) int64 {
		if id, ok := ids[p]; ok {
			return id
		}
		var parent *int64
		if d := path.Dir(p); d != "." && d != "" {
			pid := row(d, true, 0)
			parent = &pid
		}
		n := &model.Node{
			StorageID: storageID, ParentID: parent, Name: path.Base(p), Path: "/" + p,
			PathHash: pathkey.Hash(storageID, "/"+p), StorageKey: "/" + p, Size: size,
			Type: model.NodeTypeFile, SeenAt: time.Now(), SyncState: model.SyncStateSynced,
		}
		if isDir {
			n.Type = model.NodeTypeDirectory
		} else {
			n.Etag = "seed"
		}
		if existing, _ := store.GetNodeByPath(ctx, storageID, n.PathHash); existing != nil {
			ids[p] = existing.ID
			return existing.ID
		}
		created, err := store.CreateNode(ctx, n)
		if err != nil {
			t.Fatalf("dbtest: vault row %s: %v", p, err)
		}
		ids[p] = created.ID
		return created.ID
	}
	for p, b := range files {
		row(p, false, int64(len(b)))
	}
	return seed
}

package vaultlock

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/memcache"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// keyFileReadMax caps what is read of a key file to tell whether it is a
// vault's: key files are a few hundred bytes.
const keyFileReadMax = 1 << 20

// Finder answers which vault folder holds a path - the question every write
// door asks through writegate (acl.Resolver.AttachVaults): inside a vault,
// only the vault API writes.
//
// A path is in a vault when the encrypted folder it sits in (e2e.FindRoot:
// the nearest folder whose key file is in the catalogue) has a key file that
// names the vault feature (e2e.ParseVaultKeyFile). The key file is read from
// the storage once per version of it: the answer is kept, keyed by the key
// file's row as the catalogue has it NOW (size, etag, modification time), so a
// rewritten key file is read again. What it remembers is a fact about bytes
// that cannot change - a vault's `v`, `req` and `vault` never do (writegate
// refuses a rewrite that would) - not a decision another process could have
// changed.
//
// ⚠ It sees what the catalogue sees, as e2e.FindRoot does: a vault whose key
// file has no row yet (a lazily catalogued folder nobody opened) is not
// recognised until it has one.
type Finder struct {
	nodes   e2e.NodeByPathLookup
	drivers func(int64) (storage.Driver, error)
	cache   *memcache.Cache[string, finderEntry]
}

type finderEntry struct {
	info  e2e.VaultInfo
	vault bool
}

// NewFinder builds a Finder over the catalogue and the storage drivers.
func NewFinder(nodes e2e.NodeByPathLookup, drivers func(int64) (storage.Driver, error)) *Finder {
	return &Finder{
		nodes:   nodes,
		drivers: drivers,
		cache:   memcache.New[string, finderEntry](memcache.Options{MaxEntries: 4096, TTL: 30 * time.Minute}),
	}
}

// VaultRoot is the vault folder rel is inside of, or is ("" for a vault at
// the storage's root); ok is false when rel is in no vault.
func (f *Finder) VaultRoot(ctx context.Context, storageID int64, rel string) (string, bool) {
	root, _, ok := f.Vault(ctx, storageID, rel)
	return root, ok
}

// Vault is VaultRoot with the vault's key-file block.
func (f *Finder) Vault(ctx context.Context, storageID int64, rel string) (root string, info e2e.VaultInfo, ok bool) {
	if f == nil || f.nodes == nil {
		return "", e2e.VaultInfo{}, false
	}
	root, enc := e2e.FindRoot(ctx, f.nodes, storageID, rel)
	if !enc {
		return "", e2e.VaultInfo{}, false
	}
	info, vault := f.keyFile(ctx, storageID, root)
	if !vault {
		return "", e2e.VaultInfo{}, false
	}
	return root, info, true
}

// keyFile reads the key file of the encrypted folder root and says whether
// it is a vault's. A key file that names the vault feature but breaks a rule
// still makes its folder a vault: what is inside is the vault API's to
// write, and nothing else's to repair.
func (f *Finder) keyFile(ctx context.Context, storageID int64, root string) (e2e.VaultInfo, bool) {
	marker := strings.Trim(path.Join(root, e2e.MarkerName), "/")
	n, err := f.nodes.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, marker))
	if err != nil || n == nil {
		return e2e.VaultInfo{}, false
	}
	var mtime int64
	if n.BackendMtime != nil {
		mtime = n.BackendMtime.UnixNano()
	}
	key := fmt.Sprintf("%d:%q:%d:%q:%d", storageID, marker, n.Size, n.Etag, mtime)
	if e, ok := f.cache.Get(key); ok {
		return e.info, e.vault
	}
	if f.drivers == nil {
		return e2e.VaultInfo{}, false
	}
	drv, err := f.drivers(storageID)
	if err != nil || drv == nil {
		return e2e.VaultInfo{}, false
	}
	rc, err := drv.Read(ctx, marker)
	if err != nil {
		// Not remembered: the next write asks again. A key file the storage
		// cannot hand over is a storage that will refuse the write anyway.
		slog.Warn("vault: key file unreadable", slog.Int64("storage", storageID), slog.String("err", err.Error()))
		return e2e.VaultInfo{}, false
	}
	b, rerr := io.ReadAll(io.LimitReader(rc, keyFileReadMax))
	_ = rc.Close()
	if rerr != nil {
		return e2e.VaultInfo{}, false
	}
	info, vault, _ := e2e.ParseVaultKeyFile(b)
	f.cache.Put(key, finderEntry{info: info, vault: vault})
	return info, vault
}

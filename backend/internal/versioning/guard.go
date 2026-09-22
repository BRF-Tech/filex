package versioning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// internalDirs are filex's own bookkeeping trees. Nothing inside them is a
// user file, and versioning a write into .versions/ would recurse: Restore
// writes the live path back from there, and Snapshot writes into it.
//
// VersionsPrefix (service.go), not the bare literal ".versions": it is the
// exported const versionKey() itself builds snapshot keys from, in this same
// package, so this exemption and that key construction share one source of
// truth instead of two copies that could drift.
var internalDirs = []string{VersionsPrefix, ThumbsPrefix, trash.Prefix}

// ThumbsPrefix is the thumbnail tree some storages still carry at their root.
// Thumbnails live on local disk now (Thumbs.CacheDir); the name stays reserved
// so that nothing ever treats what is left there as a user's file.
const ThumbsPrefix = ".thumbs"

// IsInternalTree reports whether rel is one of filex's own bookkeeping trees
// at the ROOT of a storage — `.versions/`, `.thumbs/`, `.filex-trash/` — or
// sits inside one.
//
// It is the one rule for every surface that WALKS a storage: the sync worker,
// a cross-storage copy, the public folder-share pages. Each of them used to
// keep its own list, and the lists drifted — the sync walk skipped only the
// trash, so a full scan catalogued every version snapshot as a file. Two
// properties are load-bearing:
//
//   - Anchored at the root. filex writes these trees nowhere else (versionKey,
//     trash.NewKey), so `docs/.versions` is a user's folder and stays
//     catalogued, copyable and shareable.
//   - Names compare exactly. `.versions-old` and `.Versions` are not the tree:
//     storages compare names byte for byte, and so does this.
//
// ⚠ Unlike isInternalPath below it answers false for the storage root and for
// `.keepdir` markers: a walk that skipped "" would skip everything, and a
// marker is a file inside a user's folder, not a tree.
func IsInternalTree(rel string) bool {
	clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if clean == "" {
		return false
	}
	for _, d := range internalDirs {
		if clean == d || strings.HasPrefix(clean, d+"/") {
			return true
		}
	}
	return false
}

// isInternalPath reports whether rel lives inside one of filex's own trees, or
// is a keepdir marker.
func isInternalPath(rel string) bool {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(rel)), "/")
	if clean == "" {
		return true
	}
	if path.Base(clean) == ".keepdir" {
		return true
	}
	return IsInternalTree(clean)
}

// GuardOverwrite is the versioning half of writehook.BeforeOverwrite: if a
// catalogued FILE already lives at rel, snapshot it before the caller replaces
// it. Returns nil when there is nothing to lose (new file, directory, internal
// path) so a first write costs one indexed lookup and nothing else.
//
// An error here MUST abort the write — see Snapshot's contract: "losing
// version history is preferable to corruption".
//
// The bytes are copied verbatim, so a client-side (E2E) encrypted file is
// snapshotted as the ciphertext it already is: restorable by whoever holds the
// password, unreadable to the server, and with its `filexe2e` magic prefix
// intact. The guard never reads content and never touches the folder marker
// (`.filex-e2e.json`) except to snapshot it like any other file — which is a
// gain, since a lost marker makes an encrypted folder permanently unopenable.
func (s *Service) GuardOverwrite(ctx context.Context, storageID int64, rel string) error {
	if s == nil || s.Store == nil {
		return nil
	}
	if isInternalPath(rel) {
		return nil
	}
	// pathkey.Hash cleans the path it is handed (leading slash, no trailing
	// slash), so callers may pass either spelling.
	node, err := s.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // nothing catalogued here — a first write
		}
		return fmt.Errorf("versioning: lookup %q: %w", rel, err)
	}
	if node == nil || node.Type != model.NodeTypeFile {
		return nil
	}
	if _, err := s.Snapshot(ctx, node.ID); err != nil {
		return err
	}
	return nil
}

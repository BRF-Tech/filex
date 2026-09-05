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
var internalDirs = []string{VersionsPrefix, ".thumbs", trash.Prefix}

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
	for _, d := range internalDirs {
		if clean == d || strings.HasPrefix(clean, d+"/") {
			return true
		}
	}
	return false
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

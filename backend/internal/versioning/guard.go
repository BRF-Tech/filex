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
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// isInternalPath reports whether rel lives inside one of filex's own trees, or
// is a keepdir marker — syspath.Hidden, the one list. Nothing there is a user
// file, and versioning a write into .versions/ would recurse: Restore writes
// the live path back from there, and Snapshot writes into it.
//
// ⚠ It used to carry its own three-name list (versions, thumbs, trash), so
// every editor save of a desktop open-with working copy (`.filex-open/`) was
// snapshotted into `.versions/<id>/<n>`: history for a transient copy whose
// original lives on somebody's computer, in a folder nobody can see, kept for
// the full retention window. VersionsPrefix is syspath.Versions, so the key
// versionKey() builds and this exemption still share one source of truth.
func isInternalPath(rel string) bool {
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(rel)), "/")
	if clean == "" {
		return true
	}
	return syspath.Hidden(clean)
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

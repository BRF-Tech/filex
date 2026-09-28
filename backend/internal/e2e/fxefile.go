package e2e

import (
	"context"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// FileAt reports whether rel names a live, catalogued single encrypted file
// (`.fxe`): a FILE row whose name carries the extension. It is what the
// escrow and password endpoints act on when their subject is one file rather
// than a folder — the file holds its own key slots, so it is its own "root".
//
// A directory named `something.fxe` is not one, and neither is a name the
// catalogue does not know: the endpoints answer 400 for both, as they do for
// a folder without a key file.
func FileAt(ctx context.Context, lk NodeByPathLookup, storageID int64, rel string) bool {
	rel = strings.Trim(path.Clean("/"+strings.Trim(rel, "/")), "/")
	if lk == nil || rel == "" || !LooksEncryptedFile(path.Base(rel)) {
		return false
	}
	n, err := lk.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel))
	return err == nil && n != nil && n.DeletedAt == nil && n.Type == model.NodeTypeFile
}

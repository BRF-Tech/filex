package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// packRefusal is packSourceRefusal's permission refusal, carrying the
// verdict so each door can say WHY in its own wire shape: the explorer's
// structured permission_denied body, the AI surface's errAIForbidden.
type packRefusal struct{ v permVerdict }

func (e *packRefusal) Error() string { return "insufficient permission" }

// packSourceRefusal is THE rule for packing a selected item into an archive,
// whichever door packs it: POST /api/files/archive/create, and the AI
// surface's server-side zip (POST /api/ai/zip, the MCP file_zip tool).
//
// Packing takes the item's bytes - the archive can be downloaded or shared a
// moment later - so it asks what a download asks: never one of filex's own
// names (writegate.Names), and files.download on the item (acl.NeedLevel:
// viewer and up).
//
// ⚠⚠ One function so there is one rule. The AI zip asked only "may the
// caller see it" (resolveStorage, viewer) until v0.50: an account whose
// files.download was taken away, with create rights in some folder, packed
// what it could not download with file_zip and fetched the archive (task
// #112). A new door that packs files calls this and writes no rule of its
// own.
//
// It returns nil when the item may be packed, a writegate error, or a
// *packRefusal.
func packSourceRefusal(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string) error {
	// An unwired resolver (a handler built by hand in a test) is "nothing
	// locked" to writegate - and must reach it as a nil interface, not as an
	// interface holding a nil pointer.
	var locks writegate.Locks
	if resolver != nil {
		locks = resolver.Locks(ctx, storageID)
	}
	if err := writegate.Check(locks, 0, writegate.Names(rel)); err != nil {
		return err
	}
	if v := aclCanID(ctx, resolver, store, storageID, rel, perm.FilesDownload); !v.ok {
		return &packRefusal{v: v}
	}
	return nil
}

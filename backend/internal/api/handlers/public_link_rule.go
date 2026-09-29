package handlers

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// errLinkNeedsEdit refuses a public link to a caller who may only view the
// file. Each door words it in its own wire shape (REST 403 "insufficient
// permission", AI/MCP errAIForbidden).
var errLinkNeedsEdit = errors.New("insufficient permission")

// linkRefusal is errLinkNeedsEdit carrying the verdict, so a door can say WHY:
// the account's role or exception withholds the link permission (REST writes
// it with the permission and its source), or the path level is below editor.
type linkRefusal struct{ v permVerdict }

func (e *linkRefusal) Error() string { return errLinkNeedsEdit.Error() }
func (e *linkRefusal) Unwrap() error { return errLinkNeedsEdit }

// publicLinkRefusal is THE rule for minting a public link to a node, whichever
// door asks — POST /api/files/share (a download or a drop link), POST
// /api/ai/share and the MCP `file_share` tool:
//
//   - filex's own names are never linked (writegate.Names): a link into
//     `.filex-trash` hands out every deleted file of the storage, one into
//     `.filex-open` other people's open documents;
//   - a public link is an outbound-access grant — the file leaves for people
//     with no account at all — so it needs EDIT rights on the node, not the
//     right to see it (RBAC: viewer < editor), AND the per-user permission
//     for that kind of link (internal/perm): share.links for a download link,
//     share.upload_links for a file-drop link. acl.NeedLevel carries the
//     editor floor for both, so one aclCanID answers both halves.
//
// It returns nil when the link may be made, a writegate error, or a
// *linkRefusal (errors.Is errLinkNeedsEdit).
//
// ⚠⚠ One function so there is one rule: a second copy on a second door is
// the copy that drifts, and a door that asks less than the explorer hands the
// file out where the explorer would not. A new door that mints a public link
// calls this and writes no rule of its own.
func publicLinkRefusal(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string, p perm.Perm) error {
	// An unwired resolver (a handler built by hand in a test) is "nothing
	// locked" to writegate — and must reach it as a nil interface, not as an
	// interface holding a nil pointer.
	var locks writegate.Locks
	if resolver != nil {
		locks = resolver.Locks(ctx, storageID)
	}
	if err := writegate.Check(locks, 0, writegate.Names(rel)); err != nil {
		return err
	}
	if resolver != nil {
		if v := aclCanID(ctx, resolver, store, storageID, rel, p); !v.ok {
			return &linkRefusal{v: v}
		}
	}
	return nil
}

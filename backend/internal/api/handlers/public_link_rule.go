package handlers

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
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
//     editor floor for both, so one aclCanID answers both halves;
//   - an end-to-end encrypted folder, and anything inside one, is never
//     linked (wiring:e2): a download link would hand visitors ciphertext they
//     cannot open, together with the folder's key file, and a file request
//     would store their uploads in the folder UNENCRYPTED. The web UI hides
//     Share there; until v0.50 the API minted the link anyway, on every door.
//     A single encrypted file (`.fxe`) IS linked, as it is: it carries its
//     own key slots and its recipient opens it with its password
//     (docs/E2E-ENCRYPTION.md, "Single encrypted files").
//
// It returns nil when the link may be made, a writegate error, a
// *linkRefusal (errors.Is errLinkNeedsEdit), or an E2E_ENCRYPTED refusal
// (errors.Is errE2EEncrypted).
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
	if lk, ok := store.(e2e.NodeByPathLookup); ok {
		if root, enc := e2e.FindRoot(ctx, lk, storageID, rel); enc {
			return denied(errE2EEncrypted,
				"public links are off for an end-to-end encrypted folder and everything in it (the folder %q): "+
					"a visitor would get ciphertext with no way to open it, and a file request would store their uploads there unencrypted",
				"/"+root)
		}
	}
	return nil
}

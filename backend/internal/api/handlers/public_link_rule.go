package handlers

import (
	"context"
	"errors"
	"net/http"

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

// errRootLink refuses a public link to a whole storage. A storage is handed
// to people and groups - a grant on its root, the Access section of the
// storage and the explorer's storage row (#117) - and never to an address
// anybody holding it can open. The root has no catalogue row, so the share
// door's path and node-id shapes could not name it anyway; the invitation
// door could, and answered "not indexed yet". One refusal, in the one rule.
var errRootLink = errors.New("a storage as a whole takes no public link")

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
//   - a storage root is never linked (errRootLink): the whole storage goes
//     to people and groups, by a grant;
//   - filex's own names are never linked (writegate.Names): a link into
//     `.filex-trash` hands out every deleted file of the storage, one into
//     `.filex-open` other people's open documents;
//   - a public link is an outbound-access grant — the file leaves for people
//     with no account at all — so it needs EDIT rights on the node, not the
//     right to see it (RBAC: viewer < editor), AND the per-user permission
//     for that kind of link (internal/perm): share.links for a download link,
//     share.upload_links for a file-drop link. acl.NeedLevel carries the
//     editor floor for both, so one aclCanID answers both halves. A download
//     link also needs files.download on the node: it hands the bytes out;
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
	if acl.CleanRel(rel) == "" {
		return errRootLink
	}
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
	// A download link hands the item's bytes to whoever holds it, so its
	// creator must be allowed to download the item there - files.download,
	// a role's part for some folders or names included. linkCreatorAllows
	// asks it again on every visit, and LinkMembers of every file inside a
	// folder link.
	if resolver != nil && p == perm.ShareLinks {
		if v := aclCanID(ctx, resolver, store, storageID, rel, perm.FilesDownload); !v.ok {
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

// writeLinkRefusal answers a REST door with publicLinkRefusal's verdict: the
// permission and where it comes from when the account's permissions withhold
// the link, 400 for a storage root, 409 for an encrypted folder, the gate's
// own answer for one of filex's names, and 403 for a level below editor. The
// share door and the invitation's link fallback both write it, so the same
// refusal reads the same on both.
func writeLinkRefusal(w http.ResponseWriter, r *http.Request, err error) {
	var lr *linkRefusal
	if errors.As(err, &lr) && lr.v.WritePerm(w, r) {
		return
	}
	switch {
	case errors.Is(err, errRootLink):
		writeError(w, r, http.StatusBadRequest, "root_link", nil)
	case errors.Is(err, errE2EEncrypted):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": codeE2EEncrypted})
	case !answerGate(w, r, err):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
	}
}

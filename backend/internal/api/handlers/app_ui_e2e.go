// Package handlers — app_ui_e2e.go
//
// An app's interface editing a file of an end-to-end encrypted folder
// (filex 0.56.0, task #189; the grant is wasmplugin/e2eplain.go):
//
//	GET /api/files/plugins/ui/{plugin}/{view}/read?path=<q>&e2e=1
//	PUT /api/files/plugins/ui/{plugin}/{view}/save?path=<q>&e2e=1[&expect=<size>:<ms>]
//
// ⚠⚠ The server never holds the plaintext, here either. `e2e=1` is the
// explorer saying "I hold this folder's key and will decrypt in the browser":
// the read door then hands over the CIPHERTEXT the server stores, the person's
// browser decrypts it and gives the plaintext to the app's sandboxed frame
// only; the save door takes a body that is ALREADY encrypted in the browser
// with the same folder key, and refuses one that does not start the way an
// encrypted file starts (a careless client cannot store plaintext in an
// encrypted folder through it). What the server decides is everything it can
// decide without the key:
//
//   - the app holds files:e2e-plaintext - the administrator's yes at install,
//     with the review's own sentence (server.perm.files_e2e_plaintext);
//   - the administrator's switch for every app (wasmplugin.E2EPlaintextSetting)
//     is on;
//   - the file sits in an encrypted folder that is not a vault, and is not a
//     single encrypted file (`.fxe`, with its own password): 0.56 offers
//     neither;
//   - everything an interface's read and save always asked (UIRead, UISave:
//     the app runs, the view, the kind, the person's rights, the gates) -
//     except the kind where the server cannot read it (below);
//   - the save is over the file the app opened as it was opened (`expect`,
//     the listing's size and date - upload_expect.go), so a version written
//     meanwhile is never overwritten unseen.
//
// Every handover and every save is an audit row: app_plugin.e2e_read,
// app_plugin.e2e_save (the app, the view, the file; never a byte of it).
//
// Without `e2e=1` nothing changed: both doors refuse an encrypted file as
// 0.55 did (`403 encrypted`), so an older explorer, or one that does not hold
// the key, never hands an app ciphertext as if it were the file.
//
// ⚠⚠ A folder whose NAMES are encrypted (level 2): the stored name of a file
// there is ciphertext, so the server cannot tell its kind - neither the
// administrator's Default apps rule for it (openAllowed) nor the kinds the
// view opens (viewSaves) can be asked of it. The BROWSER decides the kind
// there: it decrypts the name, picks the app by the name's extension under
// the same rules (packages/core lib/appViewer `plaintextOpenOf`), and neither
// the name nor its extension is ever sent. What the server still decides is
// everything above but the kind: the grant, the switch, an encrypted folder
// that is no vault, the person's read (and, to save, write) right, the gates,
// `expect`, ciphertext only (e2eKindUnreadable). A name the server CAN read
// in such a folder (one written in the clear, a names pass under way) is
// held to its kind as at level 1.
package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// wantsE2E reports whether the explorer asked for an encrypted folder's door
// (`e2e=1`): it decrypts the read and sends the save encrypted.
func wantsE2E(r *http.Request) bool { return r.URL.Query().Get("e2e") == "1" }

// e2eDoor decides whether app p may be handed the file at rel of storage sid
// (writing: may save over it) through the encrypted folder's door, kind being
// what encryptedAtDoor found there. On refusal the answer is written.
func (h *AppPlugins) e2eDoor(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, sid int64, rel, kind string, writing bool) bool {
	switch kind {
	case encryptedInFolder:
	case encryptedSingle:
		// A `.fxe` carries its own password; 0.56 opens it in no app.
		refuseEncryptedAtDoor(w, r, kind, rel, writing)
		return false
	default:
		// The file is not (or no longer) in an encrypted folder: what the
		// explorer opened is not what is there.
		writeErrorSaid(w, r, http.StatusConflict, "changed", "app_save_changed", apierr.Params{"name": rel})
		return false
	}
	if !p.Grants.Has(wasmplugin.PermFilesE2EPlaintext) {
		writeError(w, r, http.StatusForbidden, "not_granted", apierr.Params{"permission": string(wasmplugin.PermFilesE2EPlaintext)})
		return false
	}
	if !wasmplugin.E2EPlaintextSetting.Resolve(r.Context(), h.Store) {
		writeErrorSaid(w, r, http.StatusForbidden, "encrypted", "app_e2e_off", nil)
		return false
	}
	if _, inVault := h.ACL.VaultRoot(r.Context(), sid, rel); inVault {
		writeErrorSaid(w, r, http.StatusForbidden, "encrypted", "app_e2e_vault", apierr.Params{"name": rel})
		return false
	}
	return true
}

// e2eHandover is a file of an encrypted folder the read door may hand over
// (e2eRead): where it is, to record the handover, and whether the server can
// read its kind at all.
type e2eHandover struct {
	storageID int64
	rel       string
	// kindUnread: the stored name is ciphertext (e2eKindUnreadable) - the
	// browser chose the app by the decrypted name, the server asks no kind.
	kindUnread bool
}

// e2eRead is UIRead's question for `e2e=1`: ok when the read may go on to
// the preview route (which serves the bytes the storage holds - for a file of
// an encrypted folder, its ciphertext), false when the answer is written.
// The handover is a file of an encrypted folder the door let through (UIRead
// records it once the kind is settled), nil for anything else.
//
// Asked only of a path this person may read (their tenant's storage, inside
// their root, at least viewer), as refuseEncryptedRead is: anything else is
// left to the preview route's own refusals, so the answer tells nobody
// whether a folder they cannot open is encrypted. A plain file reads as it
// always did, with nothing to record.
func (h *AppPlugins) e2eRead(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, target string) (*e2eHandover, bool) {
	sid, rels, err := h.resolvePaths(r.Context(), 0, []string{target})
	if err != nil || len(rels) != 1 || !ownsStorageQuiet(r, sid) {
		return nil, true
	}
	rel := rels[0]
	if !rootAllows(r.Context(), h.Store, sid, rel) || !aclAllowID(r.Context(), h.ACL, h.Store, sid, rel, acl.LevelViewer) {
		return nil, true
	}
	kind := encryptedAtDoor(r.Context(), h.Store, sid, rel)
	if kind == "" {
		return nil, true
	}
	if !h.e2eDoor(w, r, p, sid, rel, kind, false) {
		return nil, false
	}
	out := &e2eHandover{storageID: sid, rel: rel}
	if h.StorageResolver != nil {
		if drv, err := h.StorageResolver(sid); err == nil {
			out.kindUnread = e2eKindUnreadable(r.Context(), h.Store, drv, sid, rel)
		}
	}
	return out, true
}

// e2eKindUnreadable reports whether the server cannot read the kind of the
// file at rel, a file of an encrypted folder: its stored name has the shape
// of an encrypted name (e2e.LooksEncryptedName) AND the folder's key file
// says its names are encrypted (e2e.NamesEncrypted, level 2). Then the app
// doors leave the kind to the browser (see the file's header). A readable
// name - level 1, or a name written in the clear into a level-2 folder - is
// false: the server holds it to its kind.
func e2eKindUnreadable(ctx context.Context, store any, drv storage.Driver, storageID int64, rel string) bool {
	clean := strings.Trim(rel, "/")
	if drv == nil || !e2e.LooksEncryptedName(path.Base("/"+clean)) {
		return false
	}
	lk, _ := store.(e2e.NodeByPathLookup)
	root, ok := e2e.FindRoot(ctx, lk, storageID, path.Dir("/"+clean))
	if !ok {
		return false
	}
	return e2e.NamesEncrypted(readSmall(ctx, drv, strings.Trim(path.Join(root, e2e.MarkerName), "/")))
}

// encryptedBody hands back a save's body when it starts the way a file of an
// encrypted folder starts (e2e.MagicPrefix and a version this filex writes:
// 0x01 one-shot, 0x02 streamed), false when it does not - the save door's
// refusal of plaintext. The bytes it read to decide are put back in front.
func encryptedBody(body io.Reader) (io.Reader, bool) {
	head := make([]byte, len(e2e.MagicPrefix)+1)
	n, _ := io.ReadFull(body, head)
	head = head[:n]
	if n < len(e2e.MagicPrefix)+1 || !e2e.HasMagicPrefix(head) {
		return nil, false
	}
	switch head[len(e2e.MagicPrefix)] {
	case 1, 2:
		return io.MultiReader(bytes.NewReader(head), body), true
	}
	return nil, false
}

// The audit actions of an app's handover and save of a file of an encrypted
// folder. Named constants, not a string built from op: the audit panel's
// label check (web/tests/lib/auditLabel.test.ts) reads AuditAction constants
// and Action literals, and a built "app_plugin.e2e_"+op reached it as the
// verb "e2e_" - each row needs its own verb label.
const (
	AuditActionAppE2ERead = "app_plugin.e2e_read"
	AuditActionAppE2ESave = "app_plugin.e2e_save"
)

// auditE2E writes one row for a handover (op "read") or a save (op "save",
// with the bytes written) of a file of an encrypted folder: the app, the view
// and the file - never a byte of it.
func (h *AppPlugins) auditE2E(r *http.Request, p *wasmplugin.Installed, v *wire.View, storageID int64, rel, op string, size int64) {
	var uid *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		uid = &id
	}
	md := map[string]any{"storage_id": storageID, "path": rel, "view": v.ID}
	if op == "save" {
		md["size"] = size
	}
	action := AuditActionAppE2ERead
	if op == "save" {
		action = AuditActionAppE2ESave
	}
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID: uid, Action: action, TargetType: "app_plugin", TargetID: p.Row.Name,
		Metadata: md, IP: clientIP(r),
	})
}

// listingModified is the file's last_modified as a folder listing says it
// (Unix ms, listingMtimeMillis) - with the size, the `expect` an interface
// sends with its next save over the same file. The catalogue row first, as
// uploadExpectHolds reads it; the storage when there is no row.
func (h *AppPlugins) listingModified(ctx context.Context, storageID int64, rel string) (int64, bool) {
	clean := normalizeDBPath(rel)
	if n, err := h.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, clean)); err == nil && n != nil {
		if ms, ok := listingMtimeMillis(n); ok {
			return ms, true
		}
	}
	if obj, known := statOnStorage(ctx, h.StorageResolver, storageID, clean); known && obj != nil {
		return obj.Mtime.UnixMilli(), true
	}
	return 0, false
}

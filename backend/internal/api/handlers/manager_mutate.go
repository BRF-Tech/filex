package handlers

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/throughput"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// cryptoRead is a tiny indirection so randHex6 can be tested deterministically.
var cryptoRead = crand.Read

// Mutate handles the POST verbs the FileExplorer SFC fires from its
// toolbar: newfolder, rename, move, delete, upload.
//
// All bodies use the @brftech/filex-core wire format (adapter://path).
// On success each verb re-renders the parent dir via vfIndex so the
// SFC's reactive store updates without a follow-up GET.
//
// Routes that hit this dispatcher live behind the auth middleware in
// routes.go (POST /api/files/manager?action=…). Reads stay on the GET
// dispatcher in manager.go to preserve cache semantics.
func (h *Manager) Mutate(w http.ResponseWriter, r *http.Request) {
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storage resolver"})
		return
	}

	q := r.URL.Query()
	action := q.Get("action")
	if action == "" {
		action = q.Get("q")
	}
	if action == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing action"})
		return
	}
	// A token does what its verbs name (auth/token_verbs.go). This one route
	// carries every verb of the explorer, so the verb is read from the action.
	//
	// ⚠ `allowed` changes nothing — it is the question the explorer asks before
	// it offers an action that differs from folder to folder — so it needs
	// `read`, like the listing it decorates. Under `write` a read-only token
	// (an embed's, a viewer's own) got 403 for the question, and the explorer,
	// reading a failed question as "unknown", offered every such action.
	verb := auth.VerbWrite
	switch action {
	case "delete":
		verb = auth.VerbDelete
	case "allowed":
		verb = auth.VerbRead
	}
	if !auth.AllowVerb(w, r, verb) {
		return
	}

	switch action {
	case "newfolder":
		h.vfNewFolder(w, r)
	case "newfile":
		h.vfNewFile(w, r)
	case "rename":
		h.vfRename(w, r)
	case "move":
		h.vfMove(w, r)
	case "delete":
		h.vfDelete(w, r)
	case "upload":
		h.vfUpload(w, r)
	case "allowed":
		h.vfAllowed(w, r)
	default:
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "action not implemented: " + action})
	}
}

// vfAllowedBody is POST /api/files/manager?action=allowed.
type vfAllowedBody struct {
	Items       []vfPathHolder `json:"items"`
	Permissions []string       `json:"permissions"`
}

// vfAllowedMaxItems bounds one question; a bigger selection is asked in parts.
const vfAllowedMaxItems = 1000

// vfAllowed answers, for each item path (adapter://rel), which of the asked
// permissions the caller holds THERE — the same acl.Set.Can every file
// action checks, so a role that allows Delete only in Scratch gets Delete
// offered on Scratch/draft.txt and not on Finance/report.txt. It changes
// nothing; the action itself still decides. Paths it cannot place (unknown
// storage, "..") answer an empty list.
//
// The answer is a list in the order of the items, not keyed by path: a
// confined token's paths are rewritten on the way in (confine.Middleware),
// so the caller would not recognise them.
func (h *Manager) vfAllowed(w http.ResponseWriter, r *http.Request) {
	var body vfAllowedBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(body.Items) > vfAllowedMaxItems {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items"})
		return
	}
	var asked []perm.Perm
	for _, k := range body.Permissions {
		if _, ok := perm.Lookup(perm.Perm(k)); ok {
			asked = append(asked, perm.Perm(k))
		}
	}
	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	byName := make(map[string]*model.Storage, len(storages))
	for _, s := range storages {
		byName[s.Name] = s
	}
	sets := map[int64]*acl.Set{}
	out := make([][]string, len(body.Items))
	for i, it := range body.Items {
		held := []string{}
		out[i] = held
		adapter, rel := splitAdapterPath(confinedPath(r.Context(), it.Path))
		if adapter == "" && len(storages) > 0 {
			adapter = storages[0].Name
		}
		st := byName[adapter]
		// Outside the token's root nothing is held, whatever the ACL says.
		if st == nil || pathHasDotDot(rel) || !rootAllowsIn(r.Context(), st, rel) {
			continue
		}
		if h.ACL == nil {
			for _, p := range asked {
				held = append(held, string(p))
			}
			out[i] = held
			continue
		}
		set, ok := sets[st.ID]
		if !ok {
			set, _ = h.ACL.LoadSet(r.Context(), auth.UserFrom(r.Context()), st)
			sets[st.ID] = set
		}
		for _, p := range asked {
			if set.Can(rel, p) {
				held = append(held, string(p))
			}
		}
		out[i] = held
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowed": out})
}

// vfNewFolderBody is POST /api/files/manager?action=newfolder.
type vfNewFolderBody struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// vfNewFolder creates `name` under `path`'s adapter+dir on the backing
// driver, mirrors the create into the DB cache, and re-renders the
// parent listing.
func (h *Manager) vfNewFolder(w http.ResponseWriter, r *http.Request) {
	var body vfNewFolderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || strings.ContainsAny(body.Name, "/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad folder name"})
		return
	}

	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path, perm.FilesCreate)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support mkdir"})
		return
	}

	fullRel := path.Join(parentRel, body.Name)
	// The one folder a person may create among filex's own names is the
	// desktop's open-with working area, at the storage root — every desktop
	// release since 0.29.0 makes it this way (syspath.MakeWorkArea).
	if gate(w, r, h.ACL, current.ID, writegate.Writes(fullRel).As(syspath.MakeWorkArea)) {
		return
	}
	// A folder opened on top of an existing file name is the same collision
	// from the other side (storage.ErrKindConflict).
	if err := storage.EnsureDirTarget(r.Context(), drv, fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	if err := mk.Mkdir(r.Context(), fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "mkdir: " + err.Error()})
		return
	}

	// Mirror into DB cache so the very next index call shows the dir.
	parentID, err := h.lookupDirID(r.Context(), current.ID, parentRel)
	if err != nil {
		slog.Warn("manager: newfolder parent lookup",
			slog.String("path", parentRel),
			slog.String("err", err.Error()))
	} else {
		clean := normalizeDBPath(fullRel)
		hash := pathkey.Hash(current.ID, clean)
		if existing, _ := h.Store.GetNodeByPath(r.Context(), current.ID, hash); existing == nil {
			n := &model.Node{
				StorageID:  current.ID,
				ParentID:   parentID,
				Name:       body.Name,
				Path:       clean,
				PathHash:   hash,
				StorageKey: clean,
				Type:       model.NodeTypeDirectory,
				SyncState:  model.SyncStateSynced,
			}
			if created, err := h.Store.CreateNode(r.Context(), n); err != nil {
				slog.Warn("manager: newfolder db create",
					slog.String("path", clean),
					slog.String("err", err.Error()))
			} else {
				// Push to Bleve so the search box finds the new dir
				// without waiting for the next sync run.
				h.indexNode(r.Context(), created)
			}
		}
	}

	// Live: a folder appeared in this directory — refresh everyone viewing it.
	emitFolderChange(current.ID, parentRel, realtime.ChangeEvent{Action: "create", Name: body.Name})
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// vfRenameBody is POST /api/files/manager?action=rename.
type vfRenameBody struct {
	Path string `json:"path"`
	Item string `json:"item"`
	Name string `json:"name"`
}

// vfRename renames the single item to a new sibling name in the same
// dir. (Cross-dir moves go through vfMove.)
func (h *Manager) vfRename(w http.ResponseWriter, r *http.Request) {
	var body vfRenameBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	// "." and ".." are not names: joined onto the item's folder they point at
	// the folder itself or at its parent. sanitizeUploadName is the one leaf
	// guard every upload surface uses; a name must not be legal to rename to
	// and illegal to upload.
	if _, ok := sanitizeUploadName(body.Name); !ok || strings.ContainsAny(body.Name, "/\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad new name"})
		return
	}

	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path, perm.FilesRename)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}

	srcAdapter, srcRel := splitAdapterPath(body.Item)
	if srcAdapter != "" && srcAdapter != current.Name {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rename across adapters not supported"})
		return
	}
	if srcRel == "" || pathHasDotDot(srcRel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path"})
		return
	}
	// Neither what is renamed nor what it becomes may be one of filex's own:
	// `report.txt` → `.versions` would vanish from every view the moment it
	// was renamed, and a working copy renamed out of `.filex-open` would
	// never be written back.
	// …nor may an app's freeze be renamed away (writegate: names and
	// locks, one call).
	if gate(w, r, h.ACL, current.ID, writegate.Writes(srcRel), writegate.Writes(path.Join(path.Dir(srcRel), body.Name))) {
		return
	}
	if !h.require(w, r, current, srcRel, perm.FilesRename, "insufficient permission") {
		return
	}
	// The NEW name too: it is the path the file will have, so a rule's
	// blocked file types (acl.Set.Can) apply to it — renaming report.txt to
	// report.exe is producing an .exe.
	if !h.require(w, r, current, path.Join(path.Dir(srcRel), body.Name), perm.FilesRename, "insufficient permission") {
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support move"})
		return
	}

	dstRel := path.Join(path.Dir(srcRel), body.Name)
	if dstRel == srcRel {
		// No-op rename — just re-render.
		h.vfIndex(w, r, current, parentRel, storageNames, false)
		return
	}
	// ⚠⚠ A rename never replaces what already has the name. Every driver's
	// Move would (see destinationTaken), and the catalogue would then drop that
	// file's row — versions, shares and comments included. Refused rather
	// than given a "-copy" name the way a move is: the person typed this name,
	// and the client's undo assumes the item landed exactly there.
	taken, terr := destinationTaken(r.Context(), h.Store, drv, current.ID, srcRel, dstRel)
	if terr != nil {
		slog.Warn("rename refused: existence check inconclusive",
			slog.Int64("storage", current.ID),
			slog.String("path", dstRel),
			slog.String("err", terr.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": errNameCheckFailed.Error(),
			"code":  "EXISTS_CHECK_FAILED",
		})
		return
	}
	if taken {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "name_taken",
			"code":  "NAME_TAKEN",
			"name":  body.Name,
		})
		return
	}
	// Who may encrypt (e2e_policy_gate.go): a rename onto a key file's or a
	// `.fxe`'s name is a new encryption unless it carries what is encrypted
	// already (e2epolicy.RelocationEncrypts). Asked once the name is known to
	// be free, so a NAME_TAKEN answer does not spend an approval, and before a
	// folder's rename is queued: the worker has nobody to judge.
	done, settled := refuseE2ERenameAt(w, r, h.E2EPolicy, h.Store, drv, current.ID, srcRel, dstRel, true)
	if done {
		return
	}
	// Asked with `queued=1` (the explorer asks for a folder): the checks above
	// have answered, and the rename is a job of the queue. A folder on an
	// object store is one request per object, longer than any proxy waits.
	// A folder that becomes a file before the job runs is the rule's again
	// (ops.WithEncryptionSettled).
	if h.Ops != nil && r.URL.Query().Get("queued") == "1" {
		qctx := r.Context()
		if settled {
			qctx = ops.WithEncryptionSettled(qctx, []string{srcRel})
		}
		op, err := h.Ops.Submit(qctx, ops.OpRename, current.ID, []string{srcRel}, dstRel)
		if answerGate(w, r, err) {
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rename: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"op": op})
		return
	}
	// From here the storage changes: a folder on an object store is renamed
	// one object at a time, and finished even if the client leaves.
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	// The bytes, then the rows, under the storage's row gate (rowgate, issue
	// #192): a scan never judges the catalogue while the one has moved and the
	// other has not. The queue's rename does the same (ops.runRename). The
	// gate is waited for on the request (a client gone by then renames
	// nothing), and the name is asked again once it is held: a name taken
	// while the rename waited is refused, never replaced (sec055). A folder
	// on an object store fences its old and new prefixes instead of holding
	// the storage's whole scan off while it moves (storage.FenceAt,
	// rowgate.FencedChangeCtx).
	if err := rowgate.FencedChangeCtx(r.Context(), current.ID, storage.FenceAt(ctx, drv, srcRel, dstRel),
		func() error {
			again, aerr := destinationTaken(ctx, h.Store, drv, current.ID, srcRel, dstRel)
			if aerr != nil {
				return aerr
			}
			if again {
				return storage.ErrTakenMeanwhile
			}
			return mv.Move(ctx, srcRel, dstRel)
		},
		func() { h.finishRename(ctx, current.ID, srcRel, dstRel, writehook.OriginManager) },
	); err != nil {
		if errors.Is(err, storage.ErrTakenMeanwhile) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "name_taken",
				"code":  "NAME_TAKEN",
				"name":  body.Name,
			})
			return
		}
		slog.Warn("rename failed",
			slog.Int64("storage", current.ID),
			slog.String("from", srcRel),
			slog.String("to", dstRel),
			slog.String("err", err.Error()))
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "rename: " + clientErrText(err)})
		return
	}
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// vfMoveBody is POST /api/files/manager?action=move.
type vfMoveBody struct {
	Path  string         `json:"path"`
	Item  string         `json:"item,omitempty"`
	Items []vfPathHolder `json:"items"`
}

// vfPathHolder matches the {"path":"..."} shape the SFC sends per item.
type vfPathHolder struct {
	Path string `json:"path"`
}

// vfMove moves each item into the destination dir, preserving the
// item's basename. Cross-adapter moves are rejected — the SFC never
// generates them, but a stale paste could.
func (h *Manager) vfMove(w http.ResponseWriter, r *http.Request) {
	var body vfMoveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	current, destRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path, perm.FilesMove)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no items"})
		return
	}
	// Checked over the WHOLE batch before anything moves — the loop below
	// moves one item at a time, and a refusal half-way would leave half of
	// the selection moved.
	for _, it := range body.Items {
		_, srcRel := splitAdapterPath(it.Path)
		// The destination de-collides (ops.MoveDest), so it is named, not
		// replaced; the source — and, for a folder, everything in it — leaves.
		if gate(w, r, h.ACL, current.ID, writegate.Names(destRel), writegate.Writes(srcRel), writegate.Names(path.Join(destRel, path.Base(srcRel)))) {
			return
		}
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support move"})
		return
	}

	/* wiring:e2 — the synchronous move goes through the same boundary rule
	 * as the queued one. Checked up front over the WHOLE batch: this loop
	 * moves files one at a time, so a mid-loop refusal would leave the first
	 * half moved and the second half not. */
	if lk, ok := h.Store.(e2e.NodeByPathLookup); ok {
		rels := make([]string, 0, len(body.Items))
		for _, it := range body.Items {
			_, srcRel := splitAdapterPath(it.Path)
			rels = append(rels, srcRel)
		}
		if err := e2e.GuardTransfer(r.Context(), lk, current.ID, rels, current.ID, destRel); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}

	// The batch is finished even if the client leaves half-way through it, or
	// through a folder in it (detachedMutation).
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()

	srcDirs := make(map[string]struct{})
	moved := make([]string, 0, 1)
	// The row gate is waited for on the request until the first item holds
	// it: a client gone before anything moved moves nothing; one that leaves
	// half way does not leave the batch half done (sec055).
	wait := r.Context()
	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "move across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || pathHasDotDot(srcRel) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
			return
		}
		// Items keep their names (a de-collided "a (1).txt" is filex's pick,
		// not the caller's), so this is files.move alone.
		if !h.require(w, r, current, srcRel, perm.FilesMove, "insufficient permission: "+it.Path) {
			return
		}
		// The bytes, then the rows, under the storage's row gate (rowgate):
		// see vfRename. The destination is picked once the gate is held, so
		// a name taken while the move waited is stepped around, not replaced
		// (sec055). A folder on an object store fences its prefix and its
		// destination's instead of holding the storage's gate (moveFenced).
		var dstRel string
		var noFreeName error
		err := h.moveFenced(wait, ctx, current.ID, drv, srcRel, path.Join(destRel, path.Base(srcRel)),
			func(fenceDst func(string) error) error {
				d, derr := ops.MoveDest(ctx, drv, srcRel, path.Join(destRel, path.Base(srcRel)),
					liveRowTaken(ctx, h.Store, current.ID))
				if derr != nil {
					noFreeName = derr
					return derr
				}
				dstRel = d
				if dstRel == srcRel {
					return nil
				}
				if err := fenceDst(dstRel); err != nil {
					return err
				}
				return mv.Move(ctx, srcRel, dstRel)
			},
			func() {
				if dstRel != srcRel {
					h.applyDBMove(ctx, current.ID, srcRel, dstRel)
				}
			},
		)
		wait = ctx
		if noFreeName != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "move: " + clientErrText(noFreeName), "code": "NO_FREE_NAME"})
			return
		}
		if err == nil && dstRel == srcRel {
			continue
		}
		if err != nil {
			slog.Warn("move failed",
				slog.Int64("storage", current.ID),
				slog.String("from", srcRel),
				slog.String("to", dstRel),
				slog.String("err", err.Error()))
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "move: " + clientErrText(err)})
			return
		}
		/* bag:b3 event */
		writehook.OnFileMoved(ctx, current.ID, normalizeDBPath(srcRel), normalizeDBPath(dstRel), path.Base(dstRel),
			writehook.OriginManager)
		srcDirs[path.Dir(srcRel)] = struct{}{}
		moved = append(moved, path.Base(dstRel))
	}

	// Live: items landed in the destination — and left their source folders.
	//
	// ⚠ These frames used to carry no Name at all, while every other surface's
	// do. A client keying off `name` — to highlight the row that changed, or to
	// let the hub carry a viewer's presence focus — got nothing from the oldest
	// path in the product and something from all the newer ones. A multi-item
	// move has no single name, so it stays unnamed; a single item is named.
	destEv := realtime.ChangeEvent{Action: "move"}
	if len(moved) == 1 {
		destEv.Name = moved[0]
	}
	emitFolderChange(current.ID, destRel, destEv)
	destKey := normalizeDBPath(destRel)
	for d := range srcDirs {
		if normalizeDBPath(d) == destKey {
			continue // same room as dest, already emitted
		}
		emitFolderChange(current.ID, d, destEv)
	}
	h.vfIndex(w, r, current, destRel, storageNames, false)
}

// vfDeleteBody is POST /api/files/manager?action=delete.
type vfDeleteBody struct {
	Path  string         `json:"path"`
	Items []vfPathHolder `json:"items"`
}

// vfDelete soft-deletes each item by RENAMING the underlying file to
// `.filex-trash/<unix>-<rand>__<basename>` on the storage and flipping
// the DB row's `deleted_at`. The original path is preserved in
// `storage_key` so trash.Service.Restore can move the file back.
//
// Background: an earlier implementation called Driver.Delete here,
// which made restore impossible (the file was already gone). Now
// purge is the only thing that hard-deletes — see trash.purgeOne.
func (h *Manager) vfDelete(w http.ResponseWriter, r *http.Request) {
	var body vfDeleteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	current, parentRel, storageNames, ok := h.resolveAdapterDir(w, r, body.Path, perm.FilesDelete)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no items"})
		return
	}
	// The only thing among filex's own a person deletes by path is a
	// desktop open-with working copy, after its edit was written back
	// (syspath.DropWorkCopy). Nothing in `.filex-trash` is deleted by path:
	// removing from the bin for good is the admin trash API, by id — this
	// verb used to hard-delete `.filex-trash/<key>` for any editor, which
	// bypassed exactly that.
	for _, it := range body.Items {
		_, srcRel := splitAdapterPath(it.Path)
		if gate(w, r, h.ACL, current.ID, writegate.Writes(srcRel).As(syspath.DropWorkCopy)) {
			return
		}
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	// The batch is finished even if the client leaves half-way through it, or
	// through a folder in it (detachedMutation).
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	// The row gate is waited for on the request until the first item holds
	// it (see vfMove).
	wait := r.Context()

	for _, it := range body.Items {
		srcAdapter, srcRel := splitAdapterPath(it.Path)
		if srcAdapter != "" && srcAdapter != current.Name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "delete across adapters not supported: " + it.Path})
			return
		}
		if srcRel == "" || pathHasDotDot(srcRel) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item path: " + it.Path})
			return
		}
		if !h.require(w, r, current, srcRel, perm.FilesDelete, "insufficient permission: "+it.Path) {
			return
		}

		// Soft delete: rename to `.filex-trash/<unix>-<rand>__<basename>`.
		base := path.Base(srcRel)
		if base == "" || base == "." || base == "/" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad item base: " + it.Path})
			return
		}
		// The bytes and the rows, under the storage's row gate
		// (deleteItem, internal/rowgate, issue #201).
		verb, err := h.deleteItem(wait, ctx, current, drv, srcRel)
		wait = ctx
		if err != nil {
			writeJSON(w, mapDriverErr(err), map[string]string{"error": verb + ": " + err.Error()})
			return
		}
	}

	// Live: items were removed from this directory. A single item is named so
	// the hub can clear the presence focus of anyone previewing it; a batch has
	// no one name to give.
	delEv := realtime.ChangeEvent{Action: "delete"}
	if len(body.Items) == 1 {
		delEv.Name = path.Base(normalizeDBPath(body.Items[0].Path))
	}
	emitFolderChange(current.ID, parentRel, delEv)
	h.vfIndex(w, r, current, parentRel, storageNames, false)
}

// deleteItem is one item of vfDelete, from its first byte to its last row,
// under the storage's row gate (internal/rowgate, issue #201): a storage scan
// between the two would see a live row whose bytes had left for the trash,
// confirm it gone by a Stat of its old path and drop it - and the trash entry
// the person could restore from would go with it. The gate is let go before
// the handler answers. On failure it says which step failed ("trash" or
// "delete") and the driver's error. The gate is waited for on wait (the
// request's context for a batch's first item, rowgate.MoveCtx); the item is
// changed on ctx.
func (h *Manager) deleteItem(wait, ctx context.Context, current *model.Storage, drv storage.Driver, srcRel string) (string, error) {
	// A folder on an object store goes to the trash object by object: it
	// fences its prefix instead of holding the storage's gate (FenceAt).
	release, err := rowgate.HoldCtx(wait, current.ID, storage.FenceAt(ctx, drv, srcRel)...)
	if err != nil {
		return "delete", err
	}
	defer release()
	base := path.Base(srcRel)
	// trash.Put is the shared implementation every delete surface uses
	// (WebDAV, AI/REST, the async ops worker, and this one), so the same
	// item deleted from any of them lands in the trash the same way.
	out, terr := trash.Put(ctx, drv, srcRel)
	switch {
	case terr == nil && out.Trashed:
		/* bag:b3 event */
		writehook.OnFileTrashed(ctx, current.ID, normalizeDBPath(srcRel), base,
			normalizeDBPath(out.Key), writehook.OriginManager)

	case terr == nil && out.Missing:
		// Source object already gone (stale index / out-of-band delete):
		// drop the cache rows and continue so one missing item doesn't
		// fail the whole delete batch. ⚠ Every row below a folder too, one
		// by one (dropGoneRows): the folder's row alone took its contents
		// through the parent_id cascade and released none of their bytes
		// from the owners' quota (issue #104).
		h.dropGoneRows(ctx, current.ID, srcRel)
		return "", nil

	case errors.Is(terr, trash.ErrUnsupported):
		// Driver can neither move nor copy — fall back to hard delete.
		if del, ok := drv.(storage.Deleter); ok {
			if err := del.Delete(ctx, srcRel); err != nil && !errors.Is(err, storage.ErrNotFound) {
				return "delete", err
			}
		}
		/* bag:b3 event */
		writehook.OnFileDeleted(ctx, current.ID, normalizeDBPath(srcRel), base, writehook.OriginManager)
		// Bytes are gone for good: drop the rows instead of parking a
		// trash entry whose Restore could never find anything - every row
		// below a folder with it, each file's bytes released from its
		// owner's quota (dropGoneRows; issue #104).
		h.dropGoneRows(ctx, current.ID, srcRel)
		return "", nil

	default:
		return "trash", terr
	}

	// Update DB: store the original path in storage_key so Restore
	// can find it; flip deleted_at; rewrite path/path_hash to the
	// trash location so a fresh upload at the original path works.
	// Directory rows drag their cached subtree into the trash inside
	// SoftDeleteAndRetag (issue #5) — collect the subtree ids UP
	// FRONT (children are still live) so the search index forgets
	// them too.
	origClean := normalizeDBPath(srcRel)
	origHash := pathkey.Hash(current.ID, origClean)
	if existing, err := h.Store.GetNodeByPath(ctx, current.ID, origHash); err == nil && existing != nil {
		var subtreeIDs []int64
		if existing.Type == model.NodeTypeDirectory {
			subtreeIDs = h.collectSubtreeIDs(ctx, current.ID, existing.ID)
		}
		newClean := normalizeDBPath(out.Key)
		newHash := pathkey.Hash(current.ID, newClean)
		_ = h.Store.SoftDeleteAndRetag(ctx, existing.ID, newClean, newHash, origClean)
		h.removeFromIndex(ctx, existing.ID)
		for _, cid := range subtreeIDs {
			h.removeFromIndex(ctx, cid)
		}
	}
	return "", nil
}

// collectSubtreeIDs walks the live cached descendants of a directory node
// (DFS via ListNodesByParent) and returns their ids — used to purge the
// search index when a folder is trashed (the DB rows themselves are
// retagged inside Store.SoftDeleteAndRetag).
func (h *Manager) collectSubtreeIDs(ctx context.Context, storageID, rootID int64) []int64 {
	var out []int64
	var walk func(parentID int64, depth int)
	walk = func(parentID int64, depth int) {
		if depth > 64 {
			return
		}
		children, err := h.Store.ListNodesByParent(ctx, storageID, &parentID)
		if err != nil {
			return
		}
		for _, c := range children {
			if c.DeletedAt != nil {
				continue
			}
			out = append(out, c.ID)
			if c.Type == model.NodeTypeDirectory {
				walk(c.ID, depth+1)
			}
		}
	}
	walk(rootID, 0)
	return out
}

// randHex6 returns a 6-char lowercase hex string for trash key uniqueness.
func randHex6() string {
	var b [3]byte
	_, _ = cryptoRead(b[:])
	return hex.EncodeToString(b[:])
}

// vfUpload accepts multipart/form-data with one or more file[] parts
// and writes each into the destination dir on the backing driver.
//
// Limits: 32 MiB in-memory body (per ParseMultipartForm), the rest spilled to
// a temp file. This is the SMALL-FILE fast path and stays that way — every
// client now sends anything above the chunk size over the staged protocol
// (/api/files/upload/*, docs/UPLOADS.md), which is resumable and works on
// every driver. The old presigned `/upload/init` flow was removed in 0.54.
func (h *Manager) vfUpload(w http.ResponseWriter, r *http.Request) {
	// Spilled multipart temp files outlive the response unless dropped here —
	// see the note in AI.Upload. This is the browser upload path, so it is the
	// highest-volume producer of them.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
		return
	}
	pathStr := r.FormValue("path")
	current, destRel, storageNames, ok := h.resolveAdapterDir(w, r, pathStr, "")
	if !ok {
		return
	}
	if current.ReadOnly {
		writeReadOnly(w, r, http.StatusForbidden)
		return
	}

	files := r.MultipartForm.File["file[]"]
	if len(files) == 0 {
		// Some clients send `file` instead of `file[]`.
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files in upload"})
		return
	}
	// Before a byte is written, over the whole batch: a file an app has
	// locked is not overwritten (the folder grants editor, the LOCK is on the
	// file — without this the "read-only until the signatures are in" promise
	// would end at drag-and-drop), and the desktop's working
	// copy (`.filex-open/<session>-<name>`, syspath.PutWorkCopy) is the one
	// upload into filex's own names that is let through; everything else —
	// a file dropped into `.filex-trash/`, a document named `.keepdir` — would
	// be written and never be seen again.
	for _, fh := range files {
		name, ok := sanitizeUploadName(fh.Filename)
		if !ok {
			continue
		}
		target := writegate.Writes(path.Join(destRel, name)).As(syspath.PutWorkCopy)
		// A vault's key file is rewritten here and nowhere else (a new
		// password, a recovery reset, an escrow slot): the claim lets it past
		// the vault rule, and refuseVaultKeyFile below checks the bytes.
		if name == e2e.MarkerName {
			target = target.RewritesKeyFile()
		}
		if gate(w, r, h.ACL, current.ID, target) {
			return
		}
	}
	// Optional overwrite precondition (upload_expect.go). It names ONE file's
	// state, so it is refused on a batch rather than applied to whichever part
	// happens to come first.
	expect := r.FormValue("expect")
	if expect != "" && len(files) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expect needs exactly one file"})
		return
	}

	// The ceiling applies here too. Large uploads reach the staged path, which
	// checks at `begin`; this is the small-file path, and without a check a
	// user could sail past their quota a few megabytes at a time. Checked once
	// for the whole batch — refusing halfway through would leave some files
	// written and some not.
	var batch int64
	for _, fh := range files {
		batch += fh.Size
		// The per-file limit (permission rules) is per FILE, so it is asked
		// file by file; the quota below is asked of the batch.
		if h.Quota != nil {
			if ferr := h.Quota.CheckFileSize(r.Context(), quotastore.OwnerFrom(r.Context()), fh.Size); ferr != nil {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": ferr.Error(), "code": "FILE_TOO_LARGE", "file": fh.Filename})
				return
			}
		}
	}
	if err := h.checkQuota(r.Context(), 0, batch); err != nil {
		if errors.Is(err, quota.ErrQuotaExceeded) {
			slog.Info("upload refused: quota",
				slog.Int64("user", quotastore.OwnerFrom(r.Context())),
				slog.Int64("size", batch))
			writeQuotaExceeded(w, r)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}

	// ⚠ ENSURE rather than look up: a destination whose directories have no
	// node rows yet is the normal case for a first upload into a new folder,
	// and giving up here used to mean the file never entered the catalogue.
	parentID, parentLookupErr := h.ensureDirChain(r.Context(), current.ID, destRel)

	for _, fh := range files {
		name, nameOK := sanitizeUploadName(fh.Filename)
		if !nameOK {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad upload filename"})
			return
		}
		fullRel := path.Join(destRel, name)

		src, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open part: " + err.Error()})
			return
		}

		// Sniff the first 512 bytes for mime detection, then rewind. ZIP-based
		// office formats get refined via storage.RefineOfficeMime so
		// pptx/docx/odt don't end up tagged "application/zip" — see
		// internal/storage/mime.go for the OnlyOffice mismatch story.
		//
		// Rewind rather than io.MultiReader(sniff, src): multipart.File is an
		// io.Seeker, and handing the driver a seekable body keeps the S3 SDK
		// able to measure and to replay it on retry. Wrapping it cost us both
		// and put every upload on the chunked path (a production report, 2026-08-05).
		var sniff [512]byte
		n, _ := io.ReadFull(src, sniff[:])
		mime := ""
		if n > 0 {
			mime = storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), name)
		}
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			_ = src.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rewind part: " + err.Error()})
			return
		}
		// A file named exactly like an existing subfolder would leave `X` and
		// `X/…` side by side on an object store (storage.ErrKindConflict).
		if err := storage.EnsureFileTarget(r.Context(), drv, fullRel); err != nil {
			_ = src.Close()
			writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
			return
		}
		// Adding a file is files.create; replacing one that is already there
		// is a change to it, files.modify.
		upNeed := perm.FilesCreate
		if _, statErr := drv.Stat(r.Context(), fullRel); statErr == nil {
			upNeed = perm.FilesModify
		}
		if !h.require(w, r, current, fullRel, upNeed, "insufficient permission") {
			_ = src.Close()
			return
		}
		// The vault's key file (docs/E2E-VAULT-FORMAT.md → The key file): a
		// rewrite keeps its v, req and vault, and no upload makes a folder a
		// vault - only POST /api/files/e2e/vault/create does. Asked before the
		// encryption rule, so a refused write spends no approval.
		if name == e2e.MarkerName && refuseVaultKeyFile(w, r, drv, fullRel, src) {
			_ = src.Close()
			return
		}
		// A new key file or `.fxe` is a new encryption (e2e_policy_gate.go);
		// replacing one that is there never asks. "There" is the rule's own
		// look — a FILE at the path — not upNeed's: a folder made between the
		// kind guard's look and upNeed's is not the file.
		if refuseE2EWrite(w, r, h.E2EPolicy, drv, current, fullRel) {
			_ = src.Close()
			return
		}

		// Checked as late as possible — after every other refusal, right
		// before the snapshot and the write — so the window in which a
		// concurrent save can slip between the check and the bytes is as
		// narrow as this handler can make it.
		if !uploadExpectHolds(r.Context(), h.Store, h.StorageResolver, current.ID, fullRel, expect) {
			_ = src.Close()
			writePreconditionFailed(w)
			return
		}

		// The last moment at which the bytes we are about to replace still
		// exist. A refusal here is a refusal to overwrite -- see
		// writehook/overwrite.go. wiring:e2 convert — unless this is an
		// in-place E2E conversion write, whose replaced bytes are the
		// plaintext being removed (e2e_convert.go checks that it is one).
		guardCtx := e2eConversionContext(r.Context(), h.Store, h.ACL, drv, current.ID, fullRel,
			r.FormValue("e2e_convert") == "1", sniff[:n])
		if err := writehook.BeforeOverwrite(guardCtx, current.ID, fullRel); err != nil {
			_ = src.Close()
			slog.Warn("upload refused: snapshot",
				slog.Int64("storage", current.ID),
				slog.String("path", fullRel),
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "could not preserve the existing file: " + err.Error(),
				"code":  "SNAPSHOT_FAILED",
			})
			return
		}

		started := time.Now()
		if err := wr.Write(r.Context(), fullRel, src, fh.Size); err != nil {
			_ = src.Close()
			// Before this, a failed write logged only
			// method=POST path=/api/files/manager status=500 -- no storage, no
			// path, no name, no reason, so "which file failed yesterday
			// afternoon" was unanswerable from the server side alone.
			// "upload failed" is the one grep target this and the staged
			// commit path (upload_staged.go) share.
			slog.Warn("upload failed",
				slog.Int64("storage", current.ID),
				slog.String("path", fullRel),
				slog.String("name", name),
				slog.Int64("size", fh.Size),
				slog.String("reason", err.Error()))
			writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
			return
		}
		// The driver call, timed. Without this the write side of
		// filex_transfer_bytes_total would count only staged uploads and
		// silently under-report every small one.
		throughput.Observe(current.ID, throughput.Write, fh.Size, time.Since(started))
		_ = src.Close()

		if parentLookupErr != nil {
			/* bag:b3 event — DB mirror unavailable; the bytes ARE on
			   storage, so still announce the write. Transient (unsaved)
			   node → the writehook skips the AV enqueue. */
			writehook.OnFileWritten(r.Context(), current.ID, &model.Node{
				StorageID: current.ID,
				Name:      name,
				Path:      normalizeDBPath(fullRel),
				Type:      model.NodeTypeFile,
				Size:      fh.Size,
				Mime:      mime,
			}, writehook.OriginManager, writehook.Created)
			continue
		}
		clean := normalizeDBPath(fullRel)
		hash := pathkey.Hash(current.ID, clean)
		if existing, _ := h.Store.GetNodeByPath(r.Context(), current.ID, hash); existing != nil {
			// What landed — never `existing.Etag`, the etag of the file this
			// upload just replaced (see storage.Landed).
			size, etag, mtime := storage.Landed(r.Context(), drv, fullRel, fh.Size)
			_ = h.Store.UpdateNodeMeta(r.Context(), existing.ID, size, mime, etag, mtime)
			// Refresh the row pointer so the index entry carries the
			// new size/mime — IndexNode keys off node fields.
			if fresh, _ := h.Store.GetNode(r.Context(), existing.ID); fresh != nil {
				h.indexNode(r.Context(), fresh)
				// Re-upload of an existing node — the bytes changed so
				// the stored thumb is stale. Mark it pending and let
				// the pipeline regenerate.
				h.dispatchThumb(fresh)
				/* bag:b3 event + koru:k2 av — single post-write gate */
				writehook.OnFileWritten(r.Context(), current.ID, fresh, writehook.OriginManager, writehook.Replaced)
			}
			continue
		}
		lsize, etag, mtime := storage.Landed(r.Context(), drv, fullRel, fh.Size)
		n2 := &model.Node{
			StorageID:    current.ID,
			ParentID:     parentID,
			Name:         name,
			Path:         clean,
			PathHash:     hash,
			StorageKey:   clean,
			Type:         model.NodeTypeFile,
			Size:         lsize,
			Mime:         mime,
			Etag:         etag,
			BackendMtime: &mtime,
			SyncState:    model.SyncStateSynced,
		}
		if created, err := h.Store.CreateNode(r.Context(), n2); err != nil {
			slog.Warn("manager: upload db create",
				slog.String("path", clean),
				slog.String("err", err.Error()))
		} else {
			h.indexNode(r.Context(), created)
			h.dispatchThumb(created)
			/* bag:b3 event + koru:k2 av — single post-write gate */
			writehook.OnFileWritten(r.Context(), current.ID, created, writehook.OriginManager, writehook.Created)
		}
	}

	// Live: new/updated files in this directory.
	emitFolderChange(current.ID, destRel, realtime.ChangeEvent{Action: "upload"})
	h.vfIndex(w, r, current, destRel, storageNames, false)
}

// resolveAdapterDir is the shared first half of every mutation: split
// the adapter prefix off `pathStr`, look up the storage row, and
// validate the relative path. On error it writes the response and
// returns ok=false so the caller can early-exit.
func (h *Manager) resolveAdapterDir(w http.ResponseWriter, r *http.Request, pathStr string, need perm.Perm) (*model.Storage, string, []string, bool) {
	// Read the way confine.Middleware reads a JSON body it can see: for a
	// confined caller no path is its root and a bare path is on its storage,
	// whatever shape the request came in (the root check below still holds).
	pathStr = confinedPath(r.Context(), pathStr)
	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil, "", nil, false
	}
	storageNames := make([]string, 0, len(storages))
	for _, s := range storages {
		storageNames = append(storageNames, s.Name)
	}

	adapter, rel := splitAdapterPath(pathStr)
	if adapter == "" {
		if len(storages) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no storages configured"})
			return nil, "", nil, false
		}
		adapter = storages[0].Name
	}

	var current *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			current = s
			break
		}
	}
	if current == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return nil, "", nil, false
	}
	if pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return nil, "", nil, false
	}
	// The token `root:` confinement. Every manager mutation resolves its base
	// dir here, so this is the one place it is held to the token's folder
	// WHATEVER shape the path arrived in: a JSON body (confine.Middleware
	// rewrites it, but only when it reads the body as the handler does — the
	// same bytes under a different Content-Type, or with a tail after the
	// object, it did not), a multipart form field (the upload reads `path`
	// from the form; the middleware never looks at form fields), or absent
	// (the storage root, which for a confined token is its folder, never the
	// top of a storage). rootAllows is inert for an unconfined caller, so the
	// native panel is unchanged.
	if !rootAllows(r.Context(), h.Store, current.ID, rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "outside this token's root"})
		return nil, "", nil, false
	}
	// RBAC: every mutation writes into this base dir (create/upload/move-dest
	// /rename-parent/delete-parent) → require ≥editor on it (acl.NeedLevel of
	// every mutating permission), plus the caller's per-user permission for
	// the action. Viewer accounts (ceiling=viewer) are thus read-only even on
	// RBAC-off storages.
	// need == "" is an upload: whether each file is an addition or a
	// replacement is only known per file, so the caller checks that itself
	// and only the path level is asked here.
	if need == "" {
		// require asks for the other verbs; an upload into an entry the
		// storage could not answer for is refused here (issue #104).
		if refuseUnavailable(w, r, h.Store, current, rel) {
			return nil, "", nil, false
		}
		if !h.allowed(r.Context(), current, rel, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return nil, "", nil, false
		}
	} else if !h.require(w, r, current, rel, need, "insufficient permission") {
		return nil, "", nil, false
	}
	return current, rel, storageNames, true
}

// lookupDirID resolves a relative dir to a *int64 parent ID inside the
// DB cache. Returns nil for the storage root. The error path is
// surfaced separately so callers can keep the driver mutation when DB
// cache is missing — the next sync will refill it.
func (h *Manager) lookupDirID(ctx context.Context, storageID int64, rel string) (*int64, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil, nil
	}
	hash := pathkey.Hash(storageID, normalizeDBPath(rel))
	n, err := h.Store.GetNodeByPath(ctx, storageID, hash)
	if err != nil || n == nil {
		// Walk the parent chain — the DB might lag the driver.
		return h.walkDirID(ctx, storageID, rel)
	}
	id := n.ID
	return &id, nil
}

// ensureDirChain makes sure every directory on the way to rel has a node row,
// creating the ones that are missing, and returns the id of the deepest.
//
// ⚠⚠ This exists because looking a parent up and GIVING UP when it is missing
// loses the file from the catalogue entirely. Measured 2026-08-16: uploading to
// `main://newdir/a.txt` when `newdir` had no node row wrote the bytes to the
// driver and created NO rows at all — the file was on disk, the subfolder
// listing found it through the driver fallback, and the level above was empty.
// In the explorer that is a folder somebody just uploaded into that does not
// exist until the next sync run.
//
// The directories are real on the driver by the time this runs (the write went
// through them), so creating the rows is recording what is there rather than
// inventing anything. Idempotent: an existing row is reused, never duplicated.
func (h *Manager) ensureDirChain(ctx context.Context, storageID int64, rel string) (*int64, error) {
	rel = strings.Trim(normalizeDBPath(rel), "/")
	if rel == "" {
		return nil, nil
	}
	var parent *int64
	built := ""
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" {
			continue
		}
		built = path.Join(built, segment)
		clean := normalizeDBPath(built)
		hash := pathkey.Hash(storageID, clean)
		if existing, _ := h.Store.GetNodeByPath(ctx, storageID, hash); existing != nil {
			id := existing.ID
			parent = &id
			continue
		}
		created, err := h.Store.CreateNode(ctx, &model.Node{
			StorageID:  storageID,
			ParentID:   parent,
			Name:       segment,
			Path:       clean,
			PathHash:   hash,
			StorageKey: clean,
			Type:       model.NodeTypeDirectory,
			SyncState:  model.SyncStateSynced,
		})
		if err != nil {
			// A concurrent writer may have created it between the read and the
			// write; re-reading is cheaper than a transaction and is the same
			// answer.
			if again, _ := h.Store.GetNodeByPath(ctx, storageID, hash); again != nil {
				id := again.ID
				parent = &id
				continue
			}
			return nil, err
		}
		id := created.ID
		parent = &id
		h.indexNode(ctx, created)
	}
	return parent, nil
}

// walkDirID is the slow fallback path that uses ListNodesByParent step
// by step (used when GetNodeByPath misses, e.g. directory created
// outside the cache).
func (h *Manager) walkDirID(ctx context.Context, storageID int64, rel string) (*int64, error) {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	var parentPtr *int64
	for _, segment := range parts {
		if segment == "" {
			continue
		}
		nodes, err := h.Store.ListNodesByParent(ctx, storageID, parentPtr)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, n := range nodes {
			if n.Name == segment && n.Type == model.NodeTypeDirectory {
				id := n.ID
				parentPtr = &id
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("directory not found: %s", segment)
		}
	}
	return parentPtr, nil
}

// applyDBMove re-homes the cached rows for a move or rename.
//
// ⚠⚠ It moves the SUBTREE, through the same gate every other write surface
// uses (protocolsync.Syncer.MoveRows). It used to move exactly one row, and
// both halves of that were wrong in ways nothing reported:
//
//   - Descendants kept their OLD path. The next storage sync could not find
//     those paths any more and tombstoned them — the contents of a renamed
//     folder appeared in the TRASH — and the files it did find at the new path
//     had no row, so it tried to create one and collided with the live
//     descendant still sitting under the same parent with the same name:
//     `duplicate key value violates unique constraint
//     idx_nodes_storage_parent_name`, once per file, on every sync run.
//   - A rename at the storage ROOT soft-deleted the row outright.
//     `path.Dir("Leonid")` is ".", which is not a directory any lookup can
//     find, and the failure branch here trashed the node it was asked to move.
//
// Both are issue #21, reported on v0.38.0 against S3 + PostgreSQL. The subtree
// walk and the root case were already correct in MoveRows — whose own comment
// says why — and WebDAV, SFTP, S3, NFS and the AI/MCP tools were pointed at it.
// The HTTP manager, the surface a person actually clicks, was not.
func (h *Manager) applyDBMove(ctx context.Context, storageID int64, srcRel, dstRel string) {
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		slog.Warn("manager: db move: storage lookup",
			slog.Int64("storage", storageID),
			slog.String("from", srcRel), slog.String("to", dstRel))
		return
	}
	// Thumbnails are deliberately not wired here: a move does not change the
	// bytes, so there is nothing to regenerate.
	protocolsync.New(h.Store, h.Index, nil, writehook.OriginManager).
		MoveRows(ctx, st, srcRel, dstRel)
}

// mapDriverErr normalizes driver errors into HTTP statuses for the
// FileExplorer toast.
func mapDriverErr(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, storage.ErrReadOnly) {
		return http.StatusForbidden
	}
	if errors.Is(err, storage.ErrUnsupported) {
		return http.StatusNotImplemented
	}
	if errors.Is(err, os.ErrExist) {
		return http.StatusConflict
	}
	// The item is not the one the change was checked against any more: it
	// went, or was replaced, while the change waited for its storage's row
	// gate (storage.StillAsSeen, sec055).
	if errors.Is(err, storage.ErrChangedMeanwhile) {
		return http.StatusPreconditionFailed
	}
	// The target exists as the other kind (file vs folder). A conflict, not a
	// server fault — and not a 4xx the client can fix by retrying.
	if errors.Is(err, storage.ErrKindConflict) {
		return http.StatusConflict
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "exists") || strings.Contains(msg, "already") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no such") {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// normalizeDBPath canonicalises a relative path to the form sync.poll
// stores in the nodes table: leading slash, no trailing slash, no `.`.
// sanitizeUploadName is the ONE filename guard every upload surface uses:
// vfUpload (browser multipart), IngestFile (public file-drop) and the staged
// upload path. Keeping it in one place is the point — three copies of a path
// guard is three chances to fix a hole in two of them.
//
// It reduces the client's filename to a basename and refuses the values that
// would escape the destination directory once path.Join'ed. ".." is refused
// explicitly: path.Base("..") is ".." and joining it walks OUT of the target
// folder — the earlier inline copies of this check did not cover it.
func sanitizeUploadName(raw string) (string, bool) {
	name := path.Base(raw)
	switch name {
	case "", ".", "..", "/":
		return "", false
	}
	if strings.ContainsAny(name, "\\") {
		return "", false
	}
	return name, true
}

func normalizeDBPath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// IngestFile writes one uploaded file into destRel/filename on the given
// storage and upserts + indexes + thumbnails its node. It is the shared
// ingest path behind the authenticated multipart upload (vfUpload's loop) and
// the public file-drop handler, so both surface identical mime sniffing, node
// caching and thumbnail dispatch. Parent dir nodes are looked up lazily — call
// EnsureDir first when writing into a freshly-created folder so the new file's
// node links to the right parent.
func (h *Manager) IngestFile(ctx context.Context, st *model.Storage, destRel, filename string, src io.Reader, size int64) (*model.Node, error) {
	name, ok := sanitizeUploadName(filename)
	if !ok {
		return nil, fmt.Errorf("bad filename: %q", filename)
	}
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		return nil, err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	fullRel := path.Join(destRel, name)
	// A visitor's file named `.keepdir` or `.filex-trash` would be written
	// and hidden from the very owner who asked for it.
	if err := writegate.Check(h.ACL.Locks(ctx, st.ID), 0, writegate.Writes(fullRel)); err != nil {
		return nil, err
	}
	// The ceiling, before a byte is written. For the public drop link the
	// account measured is the LINK CREATOR (quotastore.OwnerFrom), because
	// theirs is the disk being filled — the uploader has no account at all.
	if err := h.checkQuota(ctx, size, size); err != nil {
		return nil, err
	}
	// A file named exactly like an existing subfolder would leave `X` and `X/…`
	// side by side on an object store (storage.ErrKindConflict).
	if err := storage.EnsureFileTarget(ctx, drv, fullRel); err != nil {
		return nil, err
	}

	// Large body → filex's own staging, then a background transfer. The caller
	// gets a listed node as soon as the bytes are safe here instead of waiting
	// out a slow or distant driver. ErrStagingUnavailable (no staging dir, no
	// ops queue) falls through to the synchronous write below, so an instance
	// without staging behaves exactly as it did before.
	if h.Staged.ShouldStage(size) {
		node, serr := h.Staged.IngestStream(ctx, st.ID, fullRel, src, size, currentUserID(ctx), "")
		if serr == nil {
			return node, nil
		}
		if !errors.Is(serr, ErrStagingUnavailable) {
			return nil, serr
		}
		// ⚠ Staging consumed nothing on the unavailable path (it refuses
		// before touching src), so falling through here is safe. Any other
		// error has already eaten part of the body and must NOT be retried
		// synchronously — that would write a truncated file.
	}

	// Sniff the first 512 bytes for mime, then REWIND — see vfUpload for the
	// OnlyOffice office-format refinement rationale.
	//
	// Rewinding rather than io.MultiReader(sniff, src) is not a style choice:
	// wrapping the body destroys its Seeker, the S3 SDK can then neither
	// measure nor replay it, and the request goes out chunked with no
	// Content-Length. Providers that require one answer 411 — which is exactly
	// how browser uploads broke for ten days (H1). vfUpload was fixed then;
	// this path, which serves the PUBLIC file-drop link, was missed and kept
	// failing. A caller whose reader is not seekable still gets the old
	// behaviour rather than an error.
	var sniff [512]byte
	n, _ := io.ReadFull(src, sniff[:])
	mime := ""
	if n > 0 {
		mime = storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), name)
	}
	body := io.Reader(io.MultiReader(bytes.NewReader(sniff[:n]), src))
	if s, ok := src.(io.Seeker); ok && n > 0 {
		if _, err := s.Seek(0, io.SeekStart); err == nil {
			body = src
		}
	}
	// The last moment at which the bytes we are about to replace still exist --
	// see writehook/overwrite.go. IngestFile serves the PUBLIC file-drop link,
	// so an anonymous submitter picking a name that already exists in the drop
	// folder would otherwise destroy the earlier file with nothing kept.
	if err := writehook.BeforeOverwrite(ctx, st.ID, fullRel); err != nil {
		return nil, err
	}
	started := time.Now()
	if err := wr.Write(ctx, fullRel, body, size); err != nil {
		return nil, err
	}
	throughput.Observe(st.ID, throughput.Write, size, time.Since(started))

	clean := normalizeDBPath(fullRel)
	hash := pathkey.Hash(st.ID, clean)
	if existing, _ := h.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		// What landed, not the replaced file's etag (see storage.Landed).
		lsize, etag, mtime := storage.Landed(ctx, drv, fullRel, size)
		_ = h.Store.UpdateNodeMeta(ctx, existing.ID, lsize, mime, etag, mtime)
		if fresh, _ := h.Store.GetNode(ctx, existing.ID); fresh != nil {
			h.indexNode(ctx, fresh)
			h.dispatchThumb(fresh)
			return fresh, nil
		}
		return existing, nil
	}
	parentID, _ := h.ensureDirChain(ctx, st.ID, path.Dir(clean))
	lsize, etag, mtime := storage.Landed(ctx, drv, fullRel, size)
	node := &model.Node{
		StorageID:    st.ID,
		ParentID:     parentID,
		Name:         name,
		Path:         clean,
		PathHash:     hash,
		StorageKey:   clean,
		Type:         model.NodeTypeFile,
		Size:         lsize,
		Mime:         mime,
		Etag:         etag,
		BackendMtime: &mtime,
		SyncState:    model.SyncStateSynced,
	}
	created, err := h.Store.CreateNode(ctx, node)
	if err != nil {
		return nil, err
	}
	h.indexNode(ctx, created)
	h.dispatchThumb(created)
	return created, nil
}

// EnsureDir makes sure a directory exists on the driver AND has a node row,
// returning its node id. The file-drop handler calls it to materialise a
// per-submission subfolder before ingesting files into it, so the owner sees
// the folder (and its parent link) immediately without waiting for a sync.
// Idempotent: returns the existing node id when the dir is already known.
func (h *Manager) EnsureDir(ctx context.Context, st *model.Storage, rel string) (*int64, error) {
	clean := normalizeDBPath(rel)
	if clean == "" || clean == "/" {
		return nil, fmt.Errorf("EnsureDir: empty path")
	}
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		return nil, err
	}
	if mk, ok := drv.(storage.Mkdirer); ok {
		// Best-effort — object stores have no real dirs; a placeholder or a
		// no-op is fine, the files written under the prefix stand on their own.
		_ = mk.Mkdir(ctx, strings.TrimPrefix(clean, "/"))
	}
	hash := pathkey.Hash(st.ID, clean)
	if existing, _ := h.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		id := existing.ID
		return &id, nil
	}
	parentID, _ := h.ensureDirChain(ctx, st.ID, path.Dir(clean))
	node := &model.Node{
		StorageID:  st.ID,
		ParentID:   parentID,
		Name:       path.Base(clean),
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeDirectory,
		SyncState:  model.SyncStateSynced,
	}
	created, err := h.Store.CreateNode(ctx, node)
	if err != nil {
		return nil, err
	}
	h.indexNode(ctx, created)
	id := created.ID
	return &id, nil
}

// moveFenced runs one item of a move under storageID's hold: the row gate, or -
// for a folder on an object store, which moves one object at a time - a fence
// on src and on tentative, the destination asked for (storage.FenceAt,
// rowgate.FencedChangeCtx). The destination is picked inside move, once the
// hold is taken; move calls fenceDst with the one it picked, which fences it
// too when it is not tentative (a de-collided name) and does nothing
// otherwise. Every fence is let go once follow has run, however move ends.
// The hold is waited for on wait; the extra fence on ctx.
func (h *Manager) moveFenced(wait, ctx context.Context, storageID int64, drv storage.Driver, src, tentative string, move func(fenceDst func(string) error) error, follow func()) error {
	fence := storage.FenceAt(ctx, drv, src, tentative)
	releaseDst := func() {}
	defer func() { releaseDst() }()
	fenceDst := func(dst string) error {
		if len(fence) == 0 || rowgate.Clean(dst) == rowgate.Clean(tentative) {
			return nil
		}
		r, err := rowgate.FenceCtx(ctx, storageID, dst)
		if err != nil {
			return err
		}
		releaseDst = r
		return nil
	}
	return rowgate.FencedChangeCtx(wait, storageID, fence, func() error { return move(fenceDst) }, follow)
}

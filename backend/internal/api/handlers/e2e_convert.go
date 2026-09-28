package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// wiring:e2 convert — encrypting a folder that already exists, in place
// (docs/E2E-ENCRYPTION.md → "Encrypting a folder you already have").
//
// The browser writes the folder's key file first (a conversion under way:
// `req: ["conv"]`), then reads every file, encrypts it and writes it back over
// itself. Two things on the server make that honest:
//
//   - a CONVERSION WRITE keeps no version of what it replaces — that is the
//     plaintext being removed (e2eConversionContext);
//   - POST /api/files/e2e/cleanup removes what filex itself still holds from
//     before: versions and trash entries (the owner's choice), thumbnails and
//     extracted search content (always — they are caches).
//
// Neither needs a key; neither reads content beyond its first 8 bytes.

// readHead returns the first n bytes of a stored file, or nil.
func readHead(ctx context.Context, drv storage.Driver, rel string, n int) []byte {
	rc, err := drv.Read(ctx, rel)
	if err != nil {
		return nil
	}
	defer rc.Close()
	buf := make([]byte, n)
	got, _ := io.ReadFull(rc, buf)
	return buf[:got]
}

func readSmall(ctx context.Context, drv storage.Driver, rel string) []byte {
	rc, err := drv.Read(ctx, rel)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return nil
	}
	return b
}

// e2eConversionContext marks ctx so the overwrite it guards keeps no version
// — when, and only when, the write is a conversion write:
//
//   - the client asked for it (`e2e_convert=1`);
//   - the file is inside an encrypted folder whose key file says a
//     conversion is under way;
//   - the bytes it replaces are plaintext (no `filexe2e` magic), and the
//     bytes it writes are ciphertext (the magic).
//
// Anything else is an ordinary overwrite and keeps its version as always: the
// flag cannot be used to overwrite history anywhere else.
func e2eConversionContext(ctx context.Context, store db.Store, drv storage.Driver, storageID int64, rel string, requested bool, newHead []byte) context.Context {
	if !requested || drv == nil || store == nil || !e2e.HasMagicPrefix(newHead) {
		return ctx
	}
	clean := strings.Trim(rel, "/")
	root, ok := e2e.FindRoot(ctx, store, storageID, path.Dir("/"+clean))
	if !ok {
		return ctx
	}
	if !e2e.ConversionPending(readSmall(ctx, drv, strings.Trim(path.Join(root, e2e.MarkerName), "/"))) {
		return ctx
	}
	old := readHead(ctx, drv, clean, len(e2e.MagicPrefix))
	if len(old) == 0 || e2e.HasMagicPrefix(old) {
		return ctx
	}
	return versioning.WithoutSnapshot(ctx)
}

// ── cleanup ────────────────────────────────────────────────────────────────

// E2ECleanupVersions is the versioning surface the cleanup needs.
type E2ECleanupVersions interface {
	List(ctx context.Context, nodeID int64) ([]*model.NodeVersion, error)
	HardDeleteVersion(ctx context.Context, versionID int64) error
}

// E2ECleanupTrash is the trash surface the cleanup needs.
type E2ECleanupTrash interface {
	List(ctx context.Context, storageID *int64, limit, offset int) ([]trash.TrashEntry, int, error)
	PurgeOne(ctx context.Context, nodeID int64) error
}

// E2ECleanupThumbs drops a node's thumbnail (the JPEG and its row).
type E2ECleanupThumbs interface {
	Forget(ctx context.Context, nodeID int64)
}

// E2ECleanupIndex blanks a node's extracted content in the search index.
type E2ECleanupIndex interface {
	IndexNodeContent(ctx context.Context, n *model.Node, content string) error
}

// AttachCleanup wires what POST /api/files/e2e/cleanup removes from. Each may
// be nil; that part is then skipped (and reported as zero).
func (h *E2E) AttachCleanup(v E2ECleanupVersions, t E2ECleanupTrash, th E2ECleanupThumbs, ix E2ECleanupIndex) {
	h.cleanVersions, h.cleanTrash, h.cleanThumbs, h.cleanIndex = v, t, th, ix
}

type e2eCleanupReq struct {
	Path     string `json:"path"`     // wire path of the encrypted folder (or inside it)
	Versions bool   `json:"versions"` // delete every version of every file in it
	Trash    bool   `json:"trash"`    // purge trash entries that were deleted from it
}

// Cleanup removes what filex holds from before a folder was encrypted.
//
//	POST /api/files/e2e/cleanup {path, versions, trash}
//	  → {versions_deleted, trash_purged, thumbnails_dropped, index_cleared}
//
// Thumbnails and extracted search content are always dropped: they are caches
// of plaintext the folder no longer has. Versions and trash entries are
// someone's safety net and are removed only when asked, and only by the
// folder's owner or an administrator.
func (h *E2E) Cleanup(w http.ResponseWriter, r *http.Request) {
	var req e2eCleanupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	st, rel := h.resolveDir(w, r, req.Path)
	if st == nil {
		return
	}
	ctx := r.Context()
	root, ok := e2e.FindRoot(ctx, h.Store, st.ID, rel)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not an encrypted folder"})
		return
	}
	if !aclAllowID(ctx, h.ACL, h.Store, st.ID, root, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	actor := auth.UserFrom(ctx)
	if req.Versions || req.Trash {
		owner := h.folderOwner(r, st.ID, root)
		mine := actor != nil && owner != nil && *owner == actor.ID
		if !mine && (actor == nil || !actor.IsAdmin()) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "only the folder's owner or an administrator can delete its versions or trash entries",
			})
			return
		}
	}

	out := map[string]int{"versions_deleted": 0, "trash_purged": 0, "thumbnails_dropped": 0, "index_cleared": 0}
	nodes, err := h.Store.ListNodesUnder(ctx, st.ID, root, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, n := range nodes {
		if n.Type != model.NodeTypeFile {
			continue
		}
		if h.cleanThumbs != nil {
			h.cleanThumbs.Forget(ctx, n.ID)
			out["thumbnails_dropped"]++
		}
		if h.cleanIndex != nil && n.Name != e2e.MarkerName {
			if h.cleanIndex.IndexNodeContent(ctx, n, "") == nil {
				out["index_cleared"]++
			}
		}
		if req.Versions && h.cleanVersions != nil {
			vs, _ := h.cleanVersions.List(ctx, n.ID)
			for _, v := range vs {
				if h.cleanVersions.HardDeleteVersion(ctx, v.ID) == nil {
					out["versions_deleted"]++
				}
			}
		}
	}
	if req.Trash && h.cleanTrash != nil {
		prefix := strings.Trim(root, "/") + "/"
		var ids []int64
		for offset := 0; ; offset += 500 {
			rows, total, err := h.cleanTrash.List(ctx, &st.ID, 500, offset)
			if err != nil || len(rows) == 0 {
				break
			}
			for _, e := range rows {
				if strings.HasPrefix(strings.Trim(e.Path, "/")+"/", prefix) {
					ids = append(ids, e.ID)
				}
			}
			if offset+len(rows) >= total {
				break
			}
		}
		for _, id := range ids {
			if h.cleanTrash.PurgeOne(ctx, id) == nil {
				out["trash_purged"]++
			}
		}
	}

	meta := map[string]any{"storage": st.Name, "folder": root, "versions": req.Versions, "trash": req.Trash}
	for k, v := range out {
		meta[k] = v
	}
	var actorID *int64
	if actor != nil {
		actorID = &actor.ID
		meta["actor_email"] = actor.Email
	}
	target := ""
	if n, err := h.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, root)); err == nil && n != nil {
		target = strconv.FormatInt(n.ID, 10)
	}
	_ = h.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		UserID: actorID, Action: "e2e.folder_cleanup", TargetType: "node", TargetID: target,
		Metadata: meta, IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, out)
}

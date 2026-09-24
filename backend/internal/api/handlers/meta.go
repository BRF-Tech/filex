// Package handlers — meta.go
//
// Starred / Recently-opened endpoints, and the row shape the tag listings
// share with them. All require an authenticated user; starred and recent are
// PER-USER (stored on user_node_meta). Tags — personal and team since v0.43.0
// — have their own file, tags.go.
//
//	POST /api/files/manager/star        body {node_id, starred: bool}
//	GET  /api/files/manager/star/list?storage_id=…&limit=
//	POST /api/files/manager/recent      body {node_id}
//	GET  /api/files/manager/recent?limit=
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

const (
	userMetaKeyStarred = "starred"
	userMetaKeyOpened  = "last_opened"
)

// Meta hosts the tags / star / recent endpoints.
type Meta struct {
	Store db.Store
	// ACL is the RBAC resolver the folder listing consults for its per-row
	// `perm`. Optional — nil means no enforcement and no `perm` on the wire,
	// exactly as the folder listing behaves with the resolver unwired.
	ACL *acl.Resolver
	// ThumbSigner stamps the `thumb_url` these rows hand out, as the folder
	// listing's does (thumb_url.go). Nil emits it unsigned.
	ThumbSigner *thumb.Signer
}

// NewMeta constructs the handler.
func NewMeta(store db.Store) *Meta { return &Meta{Store: store} }

// AttachACL wires the RBAC/ACL resolver so every starred / recent / tag row
// carries the caller's effective level on it.
func (h *Meta) AttachACL(r *acl.Resolver) { h.ACL = r }

// AttachThumbSigner wires the thumbnail URL stamp.
func (h *Meta) AttachThumbSigner(s *thumb.Signer) { h.ThumbSigner = s }

// metaRow is a node row as the starred / recent / tag listings put it on the
// wire: the indexed node, plus the two facts the explorer's context menu
// needs to decide which verbs a row gets.
//
// ⚠⚠ THE CONTEXT MENU MUST BE THE SAME EVERYWHERE (owner, 2026-09-19: "son
// kullanılanlar, ana sayfa gibi sayfalarda context menu eksik kalıyor").
// A folder listing stamps `perm` on each entry (manager.go, projectFileNodes)
// and `read_only` on the response, and the explorer gates Rename / Delete /
// Move / Share on those two. These three listings emitted neither, so a row on
// Recent, Starred, a tag view or the Home cards had no level of its own and
// fell back to the level of the folder last opened — which on the landing
// page is no folder at all — and the menu came up with every write verb
// missing (or, after a writable folder had been visited, with all of them,
// on a read-only mount). The row has to say for itself what may be done to
// it, because in these views there is no folder to ask.
type metaRow struct {
	*model.Node
	// Perm is the caller's effective level on this node (`set.Effective`),
	// omitted when the ACL resolver is unwired — the same contract as the
	// folder listing's per-entry `perm`.
	Perm string `json:"perm,omitempty"`
	// ReadOnly is the holding storage's read-only flag. Never omitted: a
	// consumer must be able to tell "writable" from "an older server that
	// did not say".
	ReadOnly bool `json:"read_only"`
	// Locked/Lock: an app plugin holds this file read-only (see
	// app_plugins_badges.go); AppState: the `<plugin>:<key>` state keys apps
	// keep on it.
	Locked   bool           `json:"locked,omitempty"`
	Lock     map[string]any `json:"lock,omitempty"`
	AppState []string       `json:"app_state,omitempty"`
	// ThumbURL is present exactly when the thumbnail endpoint would serve an
	// image for this file (thumbServable) — the folder listing's rule. The
	// client used to build `/api/files/thumb/<id>` for EVERY file on these
	// rows and got `404 "not ready"` for each one it could not have.
	ThumbURL string `json:"thumb_url,omitempty"`
}

// rows turns the store's node rows into the wire shape: storage NAME,
// `perm` and `read_only` on every row. Always a non-nil slice.
//
// The storage name is what a qualified path needs — a node row carries only a
// numeric `storage_id`, and a client in multi-storage mode cannot build the
// `name://path` it needs to open a row from a number, so before the name was
// attached these lists rendered names the user could click and nothing
// happened. One storage query per call, one ACL set per storage per call.
func (h *Meta) rows(ctx context.Context, nodes []*model.Node) []metaRow {
	out := make([]metaRow, 0, len(nodes))
	if len(nodes) == 0 {
		return out
	}
	byID := map[int64]*model.Storage{}
	if storages, err := h.Store.ListEnabledStorages(ctx); err == nil {
		for _, st := range storages {
			byID[st.ID] = st
		}
	}
	user := auth.UserFrom(ctx)
	sets := map[int64]*acl.Set{}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		// ⚠ Recent, Starred and every tag view come through here, and each
		// is keyed on a node id recorded while the node was in front of
		// somebody. Anyone who reached `.filex-open/` before it was hidden —
		// a notification click did exactly that (2026-09-21) — and opened
		// the working copy there has it in Recent, its Location column naming
		// the internal folder; a starred or tagged file later moved into one
		// would follow it the same way. syspath.Hidden is the rule every
		// listing applies; this is the listing for these three views.
		if syspath.Hidden(n.Path) {
			continue
		}
		row := metaRow{Node: n}
		if n.Type == model.NodeTypeFile {
			if t, err := h.Store.GetThumbnail(ctx, n.ID); err == nil && thumbServable(t) {
				row.ThumbURL = thumbURL(h.ThumbSigner, n.ID)
			}
		}
		st := byID[n.StorageID]
		if st != nil {
			n.Storage = st.Name
			row.ReadOnly = st.ReadOnly
			if h.ACL != nil {
				set, ok := sets[n.StorageID]
				if !ok {
					set, _ = h.ACL.LoadSet(ctx, user, st)
					sets[n.StorageID] = set
				}
				row.Perm = permString(set, n.Path)
			}
		}
		out = append(out, row)
	}
	// App badges, one storage at a time.
	byStorage := map[int64][]int{}
	for i, row := range out {
		byStorage[row.StorageID] = append(byStorage[row.StorageID], i)
	}
	for sid, idx := range byStorage {
		rels := make([]string, len(idx))
		for j, i := range idx {
			rels[j] = strings.Trim(out[i].Path, "/")
		}
		badges := appBadgesFor(ctx, h.Store, sid, rels)
		for j, i := range idx {
			b, ok := badges[rels[j]]
			if !ok {
				continue
			}
			if b.Lock != nil {
				out[i].Locked = true
				out[i].Lock = lockView(b.Lock)
				if out[i].Perm == "editor" || out[i].Perm == "owner" {
					out[i].Perm = "viewer"
				}
			}
			out[i].AppState = b.State
		}
	}
	return out
}

// Tags live in tags.go (personal + team, v0.43.0).

// ─────────────────── Starred ───────────────────

type starReq struct {
	NodeID  int64 `json:"node_id"`
	Starred bool  `json:"starred"`
}

// SetStar toggles the starred flag for the current user on a node.
func (h *Meta) SetStar(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req starReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	// ⚠⚠ The write is what makes this a leak, which is why the check is HERE
	// and not only on the listing below.
	//
	// "user_node_meta rows are keyed by user_id, so a user only ever reads
	// their own" is true of the SQL and false of the system: the caller
	// chooses which node id enters their own keyset. Star an arbitrary id,
	// list it back, read the joined node row's path/name/size/storage, unstar.
	// That turns a bookmark into a universal node-id oracle — the
	// reconnaissance step that aims every other id-addressed endpoint. The
	// listing is filtered too (defence in depth, and it covers rows planted
	// before this landed), but only the write closes the oracle.
	if !ownsNode(w, r, h.Store, req.NodeID, "node") {
		return
	}
	if !rootNodeAllowed(w, r, h.Store, req.NodeID) {
		return
	}
	var err error
	if req.Starred {
		err = h.Store.SetUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyStarred, "1")
	} else {
		err = h.Store.DeleteUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyStarred)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"starred": req.Starred,
		"node_id": req.NodeID,
	})
}

// ListStarred returns the user's starred nodes (newest-first by star time).
func (h *Meta) ListStarred(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 500)
	nodes, err := h.Store.ListNodesByUserMeta(r.Context(), u.ID, userMetaKeyStarred, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	nodes = confineNodesToTenant(r.Context(), nodes)
	nodes = confineNodesToRoot(r.Context(), h.Store, nodes)
	if v := r.URL.Query().Get("storage_id"); v != "" {
		if storageID, err := strconv.ParseInt(v, 10, 64); err == nil && storageID > 0 {
			nodes = filterByStorage(nodes, storageID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": h.rows(r.Context(), nodes),
		"limit": limit,
	})
}

// ─────────────────── Recently opened ───────────────────

type recentReq struct {
	NodeID int64 `json:"node_id"`
}

// SetRecent records that the current user opened a node.
func (h *Meta) SetRecent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var req recentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	// Same oracle as SetStar, same reasoning — see the note there.
	if !ownsNode(w, r, h.Store, req.NodeID, "node") {
		return
	}
	if !rootNodeAllowed(w, r, h.Store, req.NodeID) {
		return
	}
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	if err := h.Store.SetUserNodeMeta(r.Context(), u.ID, req.NodeID, userMetaKeyOpened, ts); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ListRecent returns nodes the current user opened recently (newest-first).
func (h *Meta) ListRecent(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
	nodes, err := h.Store.ListNodesByUserMeta(r.Context(), u.ID, userMetaKeyOpened, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	nodes = confineNodesToTenant(r.Context(), nodes)
	nodes = confineNodesToRoot(r.Context(), h.Store, nodes)
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": h.rows(r.Context(), nodes),
		"limit": limit,
	})
}

// parseLimit defaults / clamps a string query param.
func parseLimit(s string, def, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// filterByStorage narrows the slice to nodes in a particular storage.
func filterByStorage(in []*model.Node, storageID int64) []*model.Node {
	out := in[:0]
	for _, n := range in {
		if n.StorageID == storageID {
			out = append(out, n)
		}
	}
	return out
}

// confineNodesToTenant drops nodes whose storage the request's tenant cannot
// reach. Unscoped (single-tenant mode) and supertenant callers get the slice
// back untouched, so this is inert on the installs that are not multi-tenant.
//
// Deliberately a post-filter rather than a WHERE clause: these listings come
// from three different store queries, none of which takes a storage set, and a
// filter applied once at the handler cannot be forgotten by one of them. ⚠ The
// cost is that `limit` is spent before the filter runs, so a tenant on a busy
// instance can get fewer rows than it asked for — the same known trade-off
// handlers/search.go makes.
func confineNodesToTenant(ctx context.Context, in []*model.Node) []*model.Node {
	scope, confined := confinedScope(ctx)
	if !confined {
		return in
	}
	out := in[:0]
	for _, n := range in {
		if n != nil && scope.CanAccessStorage(n.StorageID) {
			out = append(out, n)
		}
	}
	return out
}

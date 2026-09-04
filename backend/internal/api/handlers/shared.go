// Package handlers — shared.go
//
// "Shared with me": everything the caller reaches through a per-item grant
// somebody else gave them, rather than through their own role on a storage.
//
//	GET /api/files/manager/shared-with-me?limit=&offset=
//
// The data has always existed in `file_grants`, but nothing could answer this
// question: Grants.List is path-scoped ("who can see THIS folder") and
// owner-only, so a user had no way to find out what had been shared with them
// unless they were told the path. That is the one item on a Drive-style
// navigation panel with no backing endpoint (issue #14).
package handlers

import (
	"context"
	"net/http"
	"sort"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// Shared serves the shared-with-me listing. Mounted inside the authenticated
// /api/files group, so the session/token middleware and confine.Middleware are
// already in force.
type Shared struct {
	Store db.Store
}

// NewShared constructs the handler.
func NewShared(store db.Store) *Shared { return &Shared{Store: store} }

// SharedWithMe lists the items the caller holds a grant on, newest grant
// first, paginated with ?limit= (default 100, max 500) and ?offset=.
//
// Four decisions worth stating, because each of them is a place where the
// obvious implementation is wrong:
//
//  1. A grant on a FOLDER lists the folder itself, not its contents. That is
//     what the explorer needs: the row is a `dir` whose `path` is
//     adapter-qualified, so opening it navigates into the folder through the
//     ordinary listing endpoint (which applies the same grants again). Listing
//     the contents here would flatten the tree and make the row unopenable.
//
//  2. "Does not own" is expressed the way filex actually models ownership.
//     There is no per-node owner: `nodes.owner_id` exists for quota accounting
//     only and is nil for everything a sync discovered. What separates "shared
//     with me" from "mine" is where the access comes from — on an RBAC-off
//     storage every authenticated user already reaches everything by role, so
//     a grant row there is inert and the items are not "shared", they are just
//     the user's files. Hence: RBAC-enabled storages only.
//
//  3. A grant with an empty path_prefix is a whole-storage grant. It is not
//     listed as an item (its name would be empty); it is what makes the
//     storage appear in `storages` below — the "shared drive" that the panel
//     shows in its storage list rather than as a file row.
//
//  4. Tenancy is filtered explicitly, not assumed. tenantstore wraps exactly
//     three methods, and per-grant reads are not among them, so an unfiltered
//     listing here would hand a tenant the names and paths of another tenant's
//     shared folders — the same cross-tenant leak the search handler filters
//     out at handlers/search.go. Filtering only in ListEnabledStorages would
//     be filtering in a place the test harness does not even wrap.
func (h *Shared) SharedWithMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100, 500)
	offset := parseLimit(r.URL.Query().Get("offset"), 0, 1_000_000)

	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	scope := scopeOf(r.Context())
	root, confined := confine.RootFrom(r.Context())

	type row struct {
		entry map[string]any
		at    int64 // grant created_at, milliseconds — the sort key
	}
	var rows []row
	sharedStorages := []string{}

	for _, st := range storages {
		// (4) tenant gate — before any read that could reveal the storage.
		if !scope.CanAccessStorage(st.ID) {
			continue
		}
		// (2) grants are only consulted on RBAC-enabled storages.
		if !st.RBACEnabled {
			continue
		}
		grants, gerr := h.Store.ListFileGrantsByStorageUser(r.Context(), st.ID, u.ID)
		if gerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": gerr.Error()})
			return
		}
		if len(grants) == 0 {
			continue
		}
		sharedStorages = append(sharedStorages, st.Name)

		for _, g := range grants {
			rel := acl.CleanRel(g.PathPrefix)
			if rel == "" {
				continue // (3) whole-storage grant — reported via `storages`
			}
			if confined && !root.Within(st.Name, rel) {
				continue
			}
			entry := h.project(r.Context(), st, g, rel)
			rows = append(rows, row{entry: entry, at: g.CreatedAt.UnixMilli()})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at > rows[j].at })

	total := len(rows)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	// A nil slice marshals to `null`, and "nothing has been shared with me" is
	// the normal state for most accounts — exactly when this list is read.
	files := make([]map[string]any, 0, end-offset)
	for _, rw := range rows[offset:end] {
		files = append(files, rw.entry)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files":    files,
		"storages": sharedStorages,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// project turns one grant into the FileNode row the explorer renders.
//
// The indexed node is preferred — it carries size, mime, etag and a thumbnail
// — but a grant can name a path the indexer has never walked, and dropping
// those would make "shared with me" quietly incomplete for exactly the folder
// somebody just shared. So an un-indexed grant becomes a synthetic row built
// from the grant itself: enough to render and to navigate into.
func (h *Shared) project(ctx context.Context, st *model.Storage, g *model.FileGrant, rel string) map[string]any {
	var entry map[string]any
	hash := pathkey.Hash(st.ID, normalizeDBPath(rel))
	if node, err := h.Store.GetNodeByPath(ctx, st.ID, hash); err == nil && node != nil {
		if th, terr := h.Store.GetThumbnail(ctx, node.ID); terr == nil {
			node.Thumb = th
		}
		// set=nil: the caller's visibility is already decided — they hold the
		// grant. Passing an acl.Set here would re-derive the same answer and
		// stamp `perm` from it; the grant's own level is the more precise
		// value and is written below.
		if projected := projectFileNodes(st.Name, []*model.Node{node}, false, nil); len(projected) == 1 {
			entry = projected[0]
		}
	}
	if entry == nil {
		typ := "file"
		if g.IsDir {
			typ = "dir"
		}
		entry = map[string]any{
			"path":     joinAdapterPath(st.Name, rel),
			"basename": baseName(rel),
			"type":     typ,
			"size":     int64(0),
			"storage":  st.Name,
		}
	}
	entry["perm"] = acl.ParseLevel(g.Level).String()
	entry["shared"] = true
	entry["shared_at"] = g.CreatedAt.UnixMilli()
	return entry
}

// attachStorageNames fills each node's Storage with the name of the storage
// holding it.
//
// Nodes returned outside a folder listing (starred, recently opened) carry
// only `storage_id`, and a client in multi-storage mode cannot build the
// `name://path` it needs to open a row from a numeric id — so those lists
// rendered names the user could click and nothing happened. One map lookup per
// node, one storage query per call.
func attachStorageNames(ctx context.Context, store db.Store, nodes []*model.Node) []*model.Node {
	if len(nodes) == 0 {
		return nodes
	}
	storages, err := store.ListEnabledStorages(ctx)
	if err != nil {
		return nodes
	}
	byID := make(map[int64]string, len(storages))
	for _, st := range storages {
		byID[st.ID] = st.Name
	}
	for _, n := range nodes {
		if n != nil {
			n.Storage = byID[n.StorageID]
		}
	}
	return nodes
}

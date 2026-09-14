// Package handlers — confine_guard.go
//
// The token `root:` confinement, for the surfaces confine.Middleware cannot
// reach.
//
// confine.Middleware (mounted on /api/files) rewrites the `?path=` query and
// the path fields of a JSON body, so every path-addressed read and every
// mutation is confined by construction. It cannot touch two shapes:
//
//   - an id-addressed request (`?id=`, `node_id`, a raw `?storage=`), because a
//     numeric id is not a path there is nothing to rewrite; and
//   - a listing or search that walks a whole storage, because the rows come back
//     AFTER the request the middleware saw.
//
// Those are exactly the shapes handlers/thumb.go, handlers/versions.go,
// handlers/trash.go, handlers/shared.go and handlers/quota_storages.go already
// gate with confine.Root.Within. This file is the ONE place the rest of the
// package answers the same question, so a sixth copy of "resolve the storage,
// then Within" cannot drift from the other five. It is the confinement twin of
// the tenant guards in tenantown.go.
package handlers

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// rootAllows reports whether the request's token confinement (if any) permits
// reaching rel in storageID. No confinement ⇒ always true, so it is inert on
// unconfined (native/admin session, or an unconfined token) callers.
//
// ⚠ Fails CLOSED: a storage id that will not resolve yields the empty adapter
// name, which confine.Root.Within refuses. A confined caller naming an id the
// store cannot read is refused either way, so nothing is lost by it.
func rootAllows(ctx context.Context, store db.Store, storageID int64, rel string) bool {
	root, confined := confine.RootFrom(ctx)
	if !confined {
		return true
	}
	return root.Within(rootStorageName(ctx, store, storageID), rel)
}

// rootStorageName resolves a storage id to its adapter name for a confinement
// check, "" when it cannot be resolved.
func rootStorageName(ctx context.Context, store db.Store, storageID int64) string {
	if store == nil {
		return ""
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return ""
	}
	return st.Name
}

// confineNodesToRoot drops the nodes a token's confinement does not reach,
// caching each storage's adapter name for the pass. Inert for unconfined
// callers, so single-tenant native listings are unchanged.
//
// The confinement twin of confineNodesToTenant (tenantown.go): the tag /
// starred / recently-opened listings answer from store queries that carry no
// path predicate, so the filter is applied once here rather than pushed into
// three different queries where one could be forgotten.
func confineNodesToRoot(ctx context.Context, store db.Store, in []*model.Node) []*model.Node {
	root, confined := confine.RootFrom(ctx)
	if !confined {
		return in
	}
	names := map[int64]string{}
	out := in[:0]
	for _, n := range in {
		if n == nil {
			continue
		}
		name, ok := names[n.StorageID]
		if !ok {
			name = rootStorageName(ctx, store, n.StorageID)
			names[n.StorageID] = name
		}
		if root.Within(name, n.Path) {
			out = append(out, n)
		}
	}
	return out
}

// rootNodeAllowed resolves a node id and reports whether the request's token
// confinement permits it, writing the same 404 an unknown node produces when it
// does not. For the node_id-addressed metadata surfaces (tags, star, recent),
// where ownsNode has already applied the tenant half. Fails closed on an
// unreadable node, matching ownsNode.
func rootNodeAllowed(w http.ResponseWriter, r *http.Request, store db.Store, nodeID int64) bool {
	if _, confined := confine.RootFrom(r.Context()); !confined {
		return true
	}
	n, err := store.GetNode(r.Context(), nodeID)
	if err != nil || n == nil || !rootAllows(r.Context(), store, n.StorageID, n.Path) {
		return notFound(w, "node")
	}
	return true
}

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
	"io"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/apierr"
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

// rootAllowsNamed is rootAllows for a path that names its storage
// (`<adapter>://<rel>`) and has not been resolved to a row yet. A door asks it
// FIRST, before the storage, the folder or the storage's read-only flag: a
// path outside the root then gets one answer whether or not any of those
// exist, instead of an answer that tells them apart (filex #155). Inert for an
// unconfined caller.
func rootAllowsNamed(ctx context.Context, adapter, rel string) bool {
	root, confined := callerRoot(ctx)
	if !confined {
		return true
	}
	return root.Within(adapter, rel)
}

// rootAllowsIn is rootAllows for a storage row the caller already holds (no
// lookup). Inert for an unconfined caller; a nil storage is refused.
func rootAllowsIn(ctx context.Context, s *model.Storage, rel string) bool {
	root, confined := confine.RootFrom(ctx)
	if !confined {
		return true
	}
	if s == nil {
		return false
	}
	return root.Within(s.Name, rel)
}

// confinedBody reads a request body a handler decodes as JSON (at most limit
// bytes) and, for a caller confined to a folder, holds it to the root exactly
// as confine.Middleware holds it, whatever Content-Type the request has. A
// path outside the root is refused with the middleware's own answer
// (confine.RefuseFor): the same 403, byte for byte, as the middleware gives,
// before anything is asked about the storage or the path - so neither the
// shape of the body nor what lies outside the root changes the answer. An
// object that does not parse gets the middleware's 400. Where it refused, ok
// is false and the answer is written.
func confinedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return nil, false
	}
	root, confined := callerRoot(r.Context())
	if !confined {
		return body, true
	}
	held, err := confine.HoldBody(root, body)
	if err != nil {
		confine.RefuseFor(w, err)
		return nil, false
	}
	return held, true
}

// refuseOutsideRoot writes the 403 a path outside the token's root gets (the
// answer app_ui.go and resolveAdapterDir give): code `permission_denied`, the
// sentence `server.error.outside_root` in the reader's language. Pass the
// request - every caller here does; without one (a caller written before
// 0.55) the sentence is the instance default's.
func refuseOutsideRoot(w http.ResponseWriter, r ...*http.Request) {
	if len(r) > 0 && r[0] != nil {
		writeErrorSaid(w, r[0], http.StatusForbidden, "permission_denied", "outside_root", nil)
		return
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied",
		"message": apierr.Text("", "outside_root", nil)})
}

// confinedPath reads a client path the way confine.Middleware reads one it
// can see: for a confined caller an empty path is the confinement root (not
// the top of a storage) and a path that names no storage is on the confined
// one. Unconfined callers, and a path outside the root, get raw back
// unchanged - the latter is then refused by the root check that follows
// (rootAllows / rootAllowsIn), not quietly moved inside.
//
// ⚠⚠ The middleware rewrites only `?path=` and the keys of a JSON object
// body (since 0.53 whatever its Content-Type; up to 0.52 only one labelled
// JSON, GHSA-8gvc-6w52-6c7j). The same path in a multipart field, in the body
// of a route it passes on unread (confine.rawBodyRoutes), or not given at all
// reaches the handler as the client wrote it, so a handler that resolves a
// client path reads it through here before it picks a storage.
func confinedPath(ctx context.Context, raw string) string {
	root, confined := confine.RootFrom(ctx)
	if !confined {
		return raw
	}
	if np, err := root.EnforcePath(raw); err == nil {
		return np
	}
	return raw
}

// callerRoot is the request's confinement root on every mount a handler is
// reached through: the one confine.Middleware stashed (token `root:` narrowed
// by `X-Filex-Root`), or — on the /api/ai surfaces, which do not pass through
// that middleware — the token's own `root:` scope (confine.RootFromToken, the
// helper aiOps uses). ok=false for an unconfined caller.
func callerRoot(ctx context.Context) (confine.Root, bool) {
	return confine.CallerRoot(ctx)
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

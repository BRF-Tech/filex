// Package handlers — tenantown.go
//
// ONE ownership predicate for the admin routes that take a raw {id}.
//
// # A different question from supertenant.go
//
// `supertenant.go` asks "may this tenant touch an instance-wide switch" and
// answers 403 when it may not — the SURFACE is being refused and the operator
// needs to read why. This file asks a narrower and more dangerous question:
// "does the caller's tenant own the ROW it just named". The routes here are
// legitimate tenant features — a tenant admin absolutely may reset their own
// user's password, revoke their own share, purge their own trash. They may not
// do it to somebody else's, and until this file existed nothing asked.
//
// That distinction is why the failure is worse than an over-broad control.
// Measured on the pre-fix build (`TestOwnership_ForeignIdsAreRefused`), an
// admin of one tenant reached FIFTEEN of sixteen id-taking admin routes
// belonging to another tenant — including `POST /users/{id}/reset-password`,
// which answered 200 and returned the other customer's new cleartext password
// in the response body, and `GET /storages/{id}`, which returns the storage's
// whole config blob (for an S3 or SFTP storage: the access key and secret).
//
// # Why 404 and not 403
//
// A 403 confirms the row exists. Repeat it over an id range and the endpoint
// becomes a census of the platform's other customers — how many storages,
// how many users, which ids are live. A foreign id must be indistinguishable
// from one that never existed, so these refusals borrow the shape of the
// "not found" the handler would otherwise have produced.
//
// # Single-tenant installs are untouched, by construction
//
// Same mechanism as supertenant.go: `auth.TenantResolver` attaches no scope
// when multi-tenant mode is off, absence means "unscoped", and unscoped passes
// every predicate here. There is no flag to set. The supertenant passes too —
// the platform operator must keep being able to administer a tenant's rows,
// including recovering a locked-out tenant admin.
//
// # Why the check lives in the handler
//
// Because the chi route is not the only door — `/api/ai/admin` mounts the same
// handler instances behind an admin-scoped API token and the MCP admin tools
// drive them in-process. Same reasoning as supertenant.go; see its header.
package handlers

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// confinedScope returns the scope only when it actually confines: a real
// tenant, in multi-tenant mode. Unscoped (single-tenant mode, workers) and the
// supertenant both report false, which is how every predicate below stays
// invisible to the installs that are not multi-tenant.
func confinedScope(ctx context.Context) (*tenant.Scope, bool) {
	s, ok := tenant.FromContext(ctx)
	if !ok || s == nil || s.IsSupertenant {
		return nil, false
	}
	return s, true
}

// notFound writes the refusal. Deliberately shaped like a genuine miss — see
// the file header on why this is not a 403.
//
// ⚠⚠ `what` must match the wording the SAME handler uses for a row that really
// does not exist, and an empty `what` gives the bare "not found" several of
// them use. Getting the status right and the body wrong leaves the oracle
// open: if a foreign id answers `{"error":"storage not found"}` while an
// unused id answers `{"error":"not found"}`, the two are still distinguishable
// and the 404 achieved nothing.
func notFound(w http.ResponseWriter, what string) bool {
	msg := "not found"
	if what != "" {
		msg = what + " not found"
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": msg})
	return false
}

// ownsStorageQuiet is ownsStorage without the 404, for handlers whose answer
// to "no such id" is not an error at all — a 200 carrying an empty list. Those
// callers write their own empty answer, because a 404 there would be the
// giveaway rather than the protection.
func ownsStorageQuiet(r *http.Request, storageID int64) bool {
	scope, confined := confinedScope(r.Context())
	if !confined {
		return true
	}
	return scope.CanAccessStorage(storageID)
}

// ownsStorage reports whether the request's tenant may reach storageID,
// answering 404 itself when it may not.
func ownsStorage(w http.ResponseWriter, r *http.Request, storageID int64, what string) bool {
	scope, confined := confinedScope(r.Context())
	if !confined {
		return true
	}
	if scope.CanAccessStorage(storageID) {
		return true
	}
	return notFound(w, what)
}

// ownsNode resolves a node to its storage and applies ownsStorage.
//
// ⚠ Fails CLOSED when the node cannot be read. A confined caller naming an id
// the store will not resolve gets the same 404 either way, so nothing is lost
// by refusing; treating an unreadable row as "no storage, therefore allowed"
// would hand the whole class straight back.
func ownsNode(w http.ResponseWriter, r *http.Request, store db.Store, nodeID int64, what string) bool {
	if _, confined := confinedScope(r.Context()); !confined {
		return true
	}
	n, err := store.GetNode(r.Context(), nodeID)
	if err != nil || n == nil {
		return notFound(w, what)
	}
	return ownsStorage(w, r, n.StorageID, what)
}

// userInTenant is the non-writing form of ownsUser, for callers whose refusal
// is not a 404 — a picker that must answer "no such user" in its own shape
// rather than erroring, so that a foreign address is indistinguishable from an
// unregistered one.
func userInTenant(ctx context.Context, u *model.User) bool {
	scope, confined := confinedScope(ctx)
	if !confined {
		return true
	}
	return u != nil && u.ProviderID != nil && *u.ProviderID == scope.ProviderID
}

// ownsUser reports whether userID is homed in the caller's tenant.
//
// This is the directory half of the boundary rather than the storage half: a
// user is not reachable through StorageIDs, so the comparison is on
// provider_id. A user with no provider at all is refused to a confined caller
// — an unhomed account belongs to the platform, not to a customer.
func ownsUser(w http.ResponseWriter, r *http.Request, store db.Store, userID int64, what string) bool {
	scope, confined := confinedScope(r.Context())
	if !confined {
		return true
	}
	u, err := store.GetUser(r.Context(), userID)
	if err != nil || u == nil || u.ProviderID == nil || *u.ProviderID != scope.ProviderID {
		return notFound(w, what)
	}
	return true
}

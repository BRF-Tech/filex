// Package tenant carries the resolved tenant (provider) scope through a request.
//
// A tenant is a provider (see model.Provider). In multi-tenant mode a resolver
// middleware puts a *Scope in the request context from the Host (browser) or the
// api_token's user (API/agents); every tenant-scoped read then filters by it.
// In single-tenant mode no Scope is set and the absence means "unscoped — see
// everything", so existing behaviour is unchanged.
//
// This package is deliberately tiny and dependency-free (only stdlib) so both
// the HTTP layer and the scoped store can share it without import cycles.
package tenant

import "context"

// Scope is the resolved tenant for a request: which provider it belongs to,
// whether that provider is the platform supertenant (confine-exempt and
// cross-tenant), and the storage ids it may reach.
type Scope struct {
	ProviderID    int64
	Slug          string
	IsSupertenant bool
	// StorageIDs are the provider's linked storages. Empty for a supertenant,
	// which is confine-exempt and reaches every storage (see CanAccessStorage).
	StorageIDs []int64
}

// DenyAll is a non-nil scope that grants nothing. The resolver attaches it to an
// authenticated request whose tenant cannot be resolved, so the request fails
// closed — it sees no storages and no directory (ProviderID 0 never matches a
// real provider) — instead of falling through to an unscoped "see everything".
var DenyAll = &Scope{ProviderID: 0}

type ctxKey struct{}

// WithScope returns a context carrying s.
func WithScope(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the request's tenant scope, or (nil, false) when none is
// set — single-tenant mode, or a pre-auth/public path. Callers must treat the
// absence as "unscoped": in multi-tenant mode a tenant-scoped operation that
// finds no scope must fail closed rather than fall back to unscoped.
func FromContext(ctx context.Context) (*Scope, bool) {
	s, ok := ctx.Value(ctxKey{}).(*Scope)
	return s, ok
}

// CanAccessStorage reports whether this scope may reach storage id. A nil scope
// (single-tenant mode / unscoped) and a supertenant can reach any storage; a
// regular tenant only its linked storages. This is the file-layer gate — the
// isolation circuit that, unlike directory scoping, must never leak.
func (s *Scope) CanAccessStorage(id int64) bool {
	if s == nil || s.IsSupertenant {
		return true
	}
	for _, sid := range s.StorageIDs {
		if sid == id {
			return true
		}
	}
	return false
}

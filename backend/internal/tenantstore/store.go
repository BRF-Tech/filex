// Package tenantstore wraps a db.Store so storage listings are confined to the
// request's tenant scope (see docs/MULTI-TENANCY.md).
//
// Only the storage-enumeration methods are overridden — the file-isolation
// layer's "what a tenant can see". Every other method passes through unchanged.
// Scoping is driven entirely by the context: a request carrying a tenant.Scope
// (set by auth.TenantResolver in multi-tenant mode) is filtered; a context with
// no scope — background workers, startup, single-tenant mode — passes through
// untouched. So wrapping is inert unless multi-tenant mode is on.
package tenantstore

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// Store is a db.Store whose storage listings honour the context tenant scope.
type Store struct {
	db.Store
}

// New wraps s. Safe everywhere; it only diverges from s when the context carries
// a tenant scope (i.e. an authenticated request in multi-tenant mode).
func New(s db.Store) *Store { return &Store{Store: s} }

// ListStorages returns only the storages the request's tenant may see.
func (s *Store) ListStorages(ctx context.Context) ([]*model.Storage, error) {
	out, err := s.Store.ListStorages(ctx)
	if err != nil {
		return nil, err
	}
	return confine(ctx, out), nil
}

// ListEnabledStorages returns only the enabled storages the tenant may see. This
// is the highest-leverage chokepoint — the explorer and many handlers list
// storages through it.
func (s *Store) ListEnabledStorages(ctx context.Context) ([]*model.Storage, error) {
	out, err := s.Store.ListEnabledStorages(ctx)
	if err != nil {
		return nil, err
	}
	return confine(ctx, out), nil
}

// confine drops storages the scope cannot reach. No scope (worker / single
// tenant) or a supertenant scope returns the list unchanged.
func confine(ctx context.Context, in []*model.Storage) []*model.Storage {
	scope, ok := tenant.FromContext(ctx)
	if !ok || scope.IsSupertenant {
		return in
	}
	out := make([]*model.Storage, 0, len(in))
	for _, st := range in {
		if scope.CanAccessStorage(st.ID) {
			out = append(out, st)
		}
	}
	return out
}

// ListUsers returns only the users in the request's tenant — the directory
// (layer-2) chokepoint behind every user picker: the file-permission / grant
// screen (GET /api/files/permissions/users), the share picker and the admin
// user list all list through it. A tenant sees only its own users; the
// supertenant (and single-tenant mode) sees all. This is the "users of other
// realms are invisible" guarantee. A leak here is only a name (file data is
// confined separately), but this is exactly the surface that must not leak.
func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	out, err := s.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	scope, ok := tenant.FromContext(ctx)
	if !ok || scope.IsSupertenant {
		return out, nil
	}
	filtered := make([]*model.User, 0, len(out))
	for _, u := range out {
		if u.ProviderID != nil && *u.ProviderID == scope.ProviderID {
			filtered = append(filtered, u)
		}
	}
	return filtered, nil
}

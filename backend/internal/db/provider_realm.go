package db

import (
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ProviderRealmOnCreate is the value a new tenant row's realm column gets
// (migration 00073), for every engine's CreateProvider: the realm the caller
// gave, else the slug — the same default the API offers and the migration
// used for the tenants that existed before it — lower-cased; NULL for a
// provider created as the supertenant, which has no realm.
//
// It does not validate: the API checks a realm before it gets here
// (tenant.CheckRealm), and a caller that creates rows directly (the cloud
// signup, a test) passes a slug that is already realm-shaped.
//
// ⚠ There is deliberately no counterpart for UpdateProvider. A realm is set
// once and never rewritten (package tenant).
func ProviderRealmOnCreate(p *model.Provider) any {
	if p == nil || p.IsSupertenant {
		return nil
	}
	r := tenant.NormalizeRealm(p.Realm)
	if r == "" {
		r = tenant.NormalizeRealm(p.Slug)
	}
	if r == "" {
		return nil
	}
	return r
}

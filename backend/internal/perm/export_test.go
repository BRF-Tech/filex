package perm

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
)

// UpgradeCatalogueWith is UpgradeCatalogue with another inheritance table:
// what the external tests use to show the mechanism with keys other than
// files.encrypt.
func UpgradeCatalogueWith(ctx context.Context, store db.Store, inherit map[Perm]Perm) (UpgradeReport, error) {
	return upgradeCatalogue(ctx, store, inherit, grantOnAdd)
}

// UpgradeCatalogueGranting is UpgradeCatalogue with another set of keys given
// to everyone: what the external tests use to show that mechanism with a key
// other than ai.assistant.
func UpgradeCatalogueGranting(ctx context.Context, store db.Store, grant Set) (UpgradeReport, error) {
	return upgradeCatalogue(ctx, store, inheritOnAdd, grant)
}

package perm

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
)

// UpgradeCatalogueWith is UpgradeCatalogue with another inheritance table:
// what the external tests use to show the mechanism with keys other than
// files.encrypt.
func UpgradeCatalogueWith(ctx context.Context, store db.Store, inherit map[Perm]Perm) (UpgradeReport, error) {
	return upgradeCatalogue(ctx, store, inherit)
}

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestRoleNamesOnEveryEngine round-trips a custom role's names and
// descriptions in other languages (migration 00071) through each driver's
// own INSERT, UPDATE and SELECT — the PostgreSQL half numbers its
// placeholders by hand, and MySQL keeps the columns as JSON — and pins that a
// row written without them (every role from before the migration) reads back
// with none.
func TestRoleNamesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			r, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
				Name:         "Accounting",
				Description:  "Invoices and payments",
				Names:        map[string]string{"tr": "Muhasebe", "es": "Contabilidad"},
				Descriptions: map[string]string{"tr": "Faturalar ve ödemeler"},
				Enabled:      true,
			})
			require.NoError(t, err)
			require.Equal(t, map[string]string{"tr": "Muhasebe", "es": "Contabilidad"}, r.Names)
			require.Equal(t, map[string]string{"tr": "Faturalar ve ödemeler"}, r.Descriptions)
			require.Equal(t, "Accounting", r.Name, "the role's own name is untouched")

			r.Names = map[string]string{"tr": "Muhasebe (AB)"}
			r.Descriptions = nil
			require.NoError(t, store.UpdatePermissionRule(ctx, r))
			reread, err := store.GetPermissionRule(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, map[string]string{"tr": "Muhasebe (AB)"}, reread.Names, "an update replaces the map")
			require.Nil(t, reread.Descriptions, "no translations read back as none, not as an empty map")

			_, err = sqlDB.ExecContext(ctx, `INSERT INTO permission_rules (name) VALUES ('Legacy')`)
			require.NoError(t, err)
			rules, err := store.ListPermissionRules(ctx)
			require.NoError(t, err)
			require.Len(t, rules, 2)
			require.Equal(t, "Legacy", rules[1].Name)
			require.Nil(t, rules[1].Names, "a row written without the columns has no translations")
			require.Nil(t, rules[1].Descriptions)
			require.Equal(t, "Legacy", rules[1].NameFor("tr"))
		})
	}
}

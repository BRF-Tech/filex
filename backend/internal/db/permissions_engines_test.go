package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestPermissionsOnEveryEngine round-trips everything migration 00069 stores:
// per-user overrides, permission rules (with their JSON columns, nullable
// tenant and creator) and SSO groups — and pins that deleting a user takes
// its rows along. The PostgreSQL half is a separate file with hand-numbered
// placeholders, and MySQL reaches the SQLite code through its upsert
// rewrite, so both only prove themselves here.
func TestPermissionsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			admin, err := store.CreateUser(ctx, "admin@example.test", "x", model.RoleAdmin, "en", "UTC")
			require.NoError(t, err)
			ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)

			// ── overrides ──
			got, err := store.GetUserPermissionOverrides(ctx, ada.ID)
			require.NoError(t, err)
			require.Empty(t, got, "no row is an empty map, not an error")

			want := map[string]string{"files.delete": model.PermDeny, "admin.audit": model.PermAllow}
			require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, want, &admin.ID))
			got, err = store.GetUserPermissionOverrides(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, want, got)

			// Second write goes through the upsert (MySQL: ON DUPLICATE KEY).
			want2 := map[string]string{"share.links": model.PermDeny}
			require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, want2, nil))
			got, err = store.GetUserPermissionOverrides(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, want2, got, "an upsert replaces, it does not merge")

			all, err := store.ListUserPermissionOverrides(ctx)
			require.NoError(t, err)
			require.Equal(t, map[int64]map[string]string{ada.ID: want2}, all)

			require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, nil, nil))
			all, err = store.ListUserPermissionOverrides(ctx)
			require.NoError(t, err)
			require.Empty(t, all, "an empty map removes the row")

			// ── rules ──
			tenant, err := store.GetSupertenant(ctx)
			require.NoError(t, err)
			require.NotNil(t, tenant, "00014 seeds the default provider")

			days := 30
			r, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
				Name:        "Contractors",
				Description: "outside staff",
				Enabled:     true,
				ProviderID:  &tenant.ID,
				Targets:     []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "contractors"}},
				Effects:     map[string]string{"files.delete": model.PermDeny},
				Settings:    model.PermRuleSettings{ShareLinkMaxDays: &days, BlockedExtensions: []string{"exe"}, Require2FA: true},
				CreatedBy:   &admin.ID,
			})
			require.NoError(t, err)
			require.NotZero(t, r.ID)
			require.Equal(t, "Contractors", r.Name)
			require.True(t, r.Enabled)
			require.NotNil(t, r.Permissions, "a role written without a list reads back with an empty one")
			require.Empty(t, r.Permissions)
			require.Equal(t, tenant.ID, *r.ProviderID)
			require.Equal(t, admin.ID, *r.CreatedBy)
			require.Len(t, r.Targets, 1)
			require.Equal(t, model.PermDeny, r.Effects["files.delete"])
			require.Equal(t, 30, *r.Settings.ShareLinkMaxDays)
			require.Equal(t, []string{"exe"}, r.Settings.BlockedExtensions)
			require.True(t, r.Settings.Require2FA)
			require.False(t, r.CreatedAt.IsZero())

			global, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
				Name:    "Everyone",
				Enabled: false,
				Effects: map[string]string{"ai.use": model.PermDeny},
			})
			require.NoError(t, err)
			require.Nil(t, global.ProviderID, "install-wide rule keeps a NULL tenant")
			require.Nil(t, global.CreatedBy)
			require.False(t, global.Enabled)

			r.Name = "Contractors (EU)"
			r.Enabled = false
			r.ProviderID = nil
			r.Effects = map[string]string{"files.delete": model.PermDeny, "files.purge": model.PermDeny}
			r.Settings = model.PermRuleSettings{}
			r.Conditions = model.PermRuleConditions{StorageIDs: []int64{3}, Paths: []string{"Archive/**"}}
			require.NoError(t, store.UpdatePermissionRule(ctx, r))
			// An update that changes nothing is not "missing" (MySQL reports
			// 0 affected rows for it).
			require.NoError(t, store.UpdatePermissionRule(ctx, r))

			reread, err := store.GetPermissionRule(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, "Contractors (EU)", reread.Name)
			require.False(t, reread.Enabled)
			require.Nil(t, reread.ProviderID)
			require.Len(t, reread.Effects, 2)
			require.Nil(t, reread.Settings.ShareLinkMaxDays)
			require.Equal(t, []int64{3}, reread.Conditions.StorageIDs, "conditions (migration 00069)")
			require.Equal(t, []string{"Archive/**"}, reread.Conditions.Paths)
			require.True(t, global.Conditions.Empty(), "a rule without conditions reads back empty")
			require.Equal(t, admin.ID, *reread.CreatedBy, "update leaves the creator alone")

			rules, err := store.ListPermissionRules(ctx)
			require.NoError(t, err)
			require.Len(t, rules, 2)
			require.Equal(t, r.ID, rules[0].ID, "id order")
			require.Equal(t, global.ID, rules[1].ID)

			require.ErrorIs(t, store.UpdatePermissionRule(ctx, &model.PermissionRule{ID: 99999, Name: "ghost"}), sql.ErrNoRows)
			_, err = store.GetPermissionRule(ctx, 99999)
			require.ErrorIs(t, err, sql.ErrNoRows)

			// ── a role's own list (migration 00069) ──
			r.Permissions = []string{"files.download", "files.create"}
			require.NoError(t, store.UpdatePermissionRule(ctx, r))
			reread, err = store.GetPermissionRule(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, []string{"files.download", "files.create"}, reread.Permissions)
			r.Permissions = []string{}
			require.NoError(t, store.UpdatePermissionRule(ctx, r))
			reread, err = store.GetPermissionRule(ctx, r.ID)
			require.NoError(t, err)
			require.NotNil(t, reread.Permissions, "an empty list is a role that may do nothing")
			require.Empty(t, reread.Permissions)

			// ── one custom role per person (migration 00069) ──
			held, err := store.GetUserCustomRole(ctx, admin.ID)
			require.NoError(t, err)
			require.Zero(t, held, "no role reads as 0, not an error")
			require.NoError(t, store.SetUserCustomRole(ctx, admin.ID, r.ID))
			require.NoError(t, store.SetUserCustomRole(ctx, admin.ID, global.ID), "a second role replaces the first")
			held, err = store.GetUserCustomRole(ctx, admin.ID)
			require.NoError(t, err)
			require.Equal(t, global.ID, held)
			members, err := store.ListUserCustomRoles(ctx)
			require.NoError(t, err)
			require.Equal(t, map[int64]int64{admin.ID: global.ID}, members)

			require.NoError(t, store.DeletePermissionRule(ctx, global.ID))
			held, err = store.GetUserCustomRole(ctx, admin.ID)
			require.NoError(t, err)
			require.Zero(t, held, "deleting a role takes it from its people")
			require.NoError(t, store.SetUserCustomRole(ctx, admin.ID, r.ID))
			require.NoError(t, store.SetUserCustomRole(ctx, admin.ID, 0))
			held, err = store.GetUserCustomRole(ctx, admin.ID)
			require.NoError(t, err)
			require.Zero(t, held, "0 takes the role away")
			rules, err = store.ListPermissionRules(ctx)
			require.NoError(t, err)
			require.Len(t, rules, 1)

			// ── SSO groups ──
			groups, err := store.ListUserSSOGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.Empty(t, groups)
			require.NoError(t, store.SetUserSSOGroups(ctx, ada.ID, []string{"staff", "contractors", "staff", ""}))
			groups, err = store.ListUserSSOGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, []string{"contractors", "staff"}, groups, "deduplicated, sorted, blanks dropped")
			require.NoError(t, store.SetUserSSOGroups(ctx, ada.ID, []string{"finance"}))
			groups, err = store.ListUserSSOGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, []string{"finance"}, groups, "replaced wholesale")

			// ── deleting a user takes its rows along; its rules' creator is nulled ──
			require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, want, &admin.ID))
			require.NoError(t, store.DeleteUser(ctx, ada.ID))
			all, err = store.ListUserPermissionOverrides(ctx)
			require.NoError(t, err)
			require.Empty(t, all)
			groups, err = store.ListUserSSOGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.Empty(t, groups)

			require.NoError(t, store.DeleteUser(ctx, admin.ID))
			reread, err = store.GetPermissionRule(ctx, r.ID)
			require.NoError(t, err)
			require.Nil(t, reread.CreatedBy)
		})
	}
}

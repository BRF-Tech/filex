package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Migrations 00082 and 00083 on every engine: a directory account's
// permanent id and the "switched off by the directory" mark (00082); a group
// that makes its members administrators, and the mark on an account a group
// made one (00083). PostgreSQL's BOOLEAN columns only prove themselves here.
func TestLDAPPeopleAndGroupAdminOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			ada, err := store.CreateUser(ctx, "ada@example.test", "", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			require.Empty(t, ada.DirectoryID)
			require.False(t, ada.DisabledByDirectory())
			require.False(t, ada.AdminByGroup)

			// The permanent id, and finding the account by it.
			_, err = store.GetUserByDirectoryID(ctx, "")
			require.Error(t, err, "no id finds nobody — not every account with none")
			_, err = store.GetUserByDirectoryID(ctx, "ldap:u-ada")
			require.Error(t, err)
			require.NoError(t, store.SetUserDirectoryID(ctx, ada.ID, "ldap:u-ada"))
			got, err := store.GetUserByDirectoryID(ctx, "ldap:u-ada")
			require.NoError(t, err)
			require.Equal(t, ada.ID, got.ID)
			require.Equal(t, "ldap:u-ada", got.DirectoryID)

			// Switched off by the directory, and by hand.
			require.NoError(t, store.SetUserEnabledByDirectory(ctx, ada.ID, false))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.False(t, got.Enabled)
			require.True(t, got.DisabledByDirectory())
			all, err := store.ListUsers(ctx)
			require.NoError(t, err)
			require.True(t, all[len(all)-1].DisabledByDirectory(), "the list reads it too")
			require.NoError(t, store.SetUserEnabledByDirectory(ctx, ada.ID, true))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.True(t, got.Enabled)
			require.False(t, got.DisabledByDirectory())
			require.NoError(t, store.SetUserEnabledByDirectory(ctx, ada.ID, false))
			require.NoError(t, store.SetUserEnabled(ctx, ada.ID, false))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.False(t, got.DisabledByDirectory(), "switched off by hand: the administrator's choice")

			// Administrator through a group; any other role change clears it.
			require.NoError(t, store.SetUserAdminByGroup(ctx, ada.ID))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, model.RoleAdmin, got.Role)
			require.True(t, got.AdminByGroup)
			require.NoError(t, store.UpdateUserRole(ctx, ada.ID, model.RoleUser))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, model.RoleUser, got.Role)
			require.False(t, got.AdminByGroup)

			// A group that makes its members administrators.
			g, err := store.CreateGroup(ctx, &model.Group{Name: "IT Admins", GivesAdmin: true,
				Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: "cn=it-admins,dc=example,dc=com"}}})
			require.NoError(t, err)
			require.True(t, g.GivesAdmin)
			g.GivesAdmin = false
			require.NoError(t, store.UpdateGroup(ctx, g))
			g, err = store.GetGroup(ctx, g.ID)
			require.NoError(t, err)
			require.False(t, g.GivesAdmin)
			g.GivesAdmin = true
			require.NoError(t, store.UpdateGroup(ctx, g))
			list, err := store.ListGroups(ctx)
			require.NoError(t, err)
			require.True(t, list[len(list)-1].GivesAdmin)
		})
	}
}

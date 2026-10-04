package db_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestGroupsOnEveryEngine round-trips everything migration 00074 stores:
// groups (nullable tenant and role, links), memberships and their sources,
// the link-sync that must leave manual rows alone, group folder grants (the
// upsert, the join through membership) and the cascades. db.GroupSQL is one
// implementation rebound per engine — PostgreSQL's `$n`, BOOLEAN and
// RETURNING only prove themselves here.
func TestGroupsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			admin, err := store.CreateUser(ctx, "admin@example.test", "x", model.RoleAdmin, "en", "UTC")
			require.NoError(t, err)
			ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			bob, err := store.CreateUser(ctx, "bob@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			tenant, err := store.GetSupertenant(ctx)
			require.NoError(t, err)
			role, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Look", Enabled: true, Permissions: []string{"files.download"}})
			require.NoError(t, err)
			other, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Other", Enabled: true, Permissions: []string{}})
			require.NoError(t, err)
			st, err := store.CreateStorage(ctx, &model.Storage{Name: "grp", Driver: "local", MountPath: "/grp", Enabled: true, ConfigJSON: []byte(`{"root":"/tmp"}`)})
			require.NoError(t, err)

			// ── groups ──
			fin, err := store.CreateGroup(ctx, &model.Group{
				Name: "Finance", Description: "money", ProviderID: &tenant.ID, RoleID: &role.ID,
				Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "finance"}}, CreatedBy: &admin.ID,
			})
			require.NoError(t, err)
			require.Equal(t, "Finance", fin.Name)
			require.Equal(t, tenant.ID, *fin.ProviderID)
			require.Equal(t, role.ID, *fin.RoleID)
			require.Equal(t, []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "finance"}}, fin.Links)
			legal, err := store.CreateGroup(ctx, &model.Group{Name: "Legal"})
			require.NoError(t, err)
			require.Nil(t, legal.ProviderID)
			require.Nil(t, legal.RoleID)
			require.Equal(t, []model.GroupLink{}, legal.Links)

			fin.Name, fin.Links = "Finance team", nil
			require.NoError(t, store.UpdateGroup(ctx, fin))
			got, err := store.GetGroup(ctx, fin.ID)
			require.NoError(t, err)
			require.Equal(t, "Finance team", got.Name)
			require.Empty(t, got.Links)
			// An update that changes nothing is not "missing" (MySQL reports
			// 0 rows affected for it).
			require.NoError(t, store.UpdateGroup(ctx, got))
			require.ErrorIs(t, store.UpdateGroup(ctx, &model.Group{ID: 999999, Name: "x"}), sql.ErrNoRows)
			_, err = store.GetGroup(ctx, 999999)
			require.ErrorIs(t, err, sql.ErrNoRows)

			all, err := store.ListGroups(ctx)
			require.NoError(t, err)
			require.Len(t, all, 2)
			require.Equal(t, fin.ID, all[0].ID, "id order")

			require.NoError(t, store.ReassignGroupRole(ctx, role.ID, other.ID))
			got, err = store.GetGroup(ctx, fin.ID)
			require.NoError(t, err)
			require.Equal(t, other.ID, *got.RoleID)
			require.NoError(t, store.DeletePermissionRule(ctx, other.ID))
			got, err = store.GetGroup(ctx, fin.ID)
			require.NoError(t, err)
			require.Nil(t, got.RoleID, "a deleted role leaves its groups with none")

			// ── members ──
			require.NoError(t, store.AddGroupMember(ctx, fin.ID, ada.ID))
			require.NoError(t, store.AddGroupMember(ctx, fin.ID, ada.ID), "adding twice is not an error")
			added, removed, err := store.SetUserLinkedGroups(ctx, ada.ID, model.GroupSourceSSO, []int64{fin.ID, legal.ID})
			require.NoError(t, err)
			require.Equal(t, []int64{legal.ID}, added, "Finance is already a manual membership")
			require.Empty(t, removed)
			ms, err := store.ListUserGroupMemberships(ctx, ada.ID)
			require.NoError(t, err)
			require.Len(t, ms, 2)
			require.Equal(t, model.GroupSourceManual, ms[0].Source)
			require.Equal(t, model.GroupSourceSSO, ms[1].Source)

			added, removed, err = store.SetUserLinkedGroups(ctx, ada.ID, model.GroupSourceSSO, nil)
			require.NoError(t, err)
			require.Empty(t, added)
			require.Equal(t, []int64{legal.ID}, removed, "only the link's own rows go")
			_, _, err = store.SetUserLinkedGroups(ctx, ada.ID, model.GroupSourceManual, nil)
			require.Error(t, err, "manual is not a link source")

			// A manual add over a link membership makes it manual.
			_, _, err = store.SetUserLinkedGroups(ctx, bob.ID, model.GroupSourceSSO, []int64{legal.ID})
			require.NoError(t, err)
			require.NoError(t, store.AddGroupMember(ctx, legal.ID, bob.ID))
			ms, err = store.ListGroupMembers(ctx, legal.ID)
			require.NoError(t, err)
			require.Len(t, ms, 1)
			require.Equal(t, model.GroupSourceManual, ms[0].Source)

			every, err := store.ListAllGroupMembers(ctx)
			require.NoError(t, err)
			require.Len(t, every, 2)

			// ── group grants ──
			gr, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "Reports", IsDir: true, GroupID: fin.ID, Level: model.GrantViewer, CreatedBy: &admin.ID})
			require.NoError(t, err)
			require.Equal(t, fin.ID, gr.GroupID)
			require.Zero(t, gr.UserID)
			require.True(t, gr.IsDir)
			again, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "Reports", IsDir: true, GroupID: fin.ID, Level: model.GrantEditor})
			require.NoError(t, err)
			require.Equal(t, gr.ID, again.ID, "an upsert on (storage, path, group)")
			require.Equal(t, model.GrantEditor, again.Level)
			// Paths compare exactly on every engine (MySQL: utf8mb4_0900_bin,
			// as file_grants since 00041): `reports` is another folder, and
			// granting it must not overwrite the grant on `Reports`.
			lower, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "reports", IsDir: true, GroupID: fin.ID, Level: model.GrantViewer})
			require.NoError(t, err)
			require.NotEqual(t, gr.ID, lower.ID, "a different folder")
			kept, err := store.GetGroupFileGrant(ctx, gr.ID)
			require.NoError(t, err)
			require.Equal(t, "Reports", kept.PathPrefix)
			require.Equal(t, model.GrantEditor, kept.Level, "the grant on Reports is untouched")
			require.NoError(t, store.DeleteGroupFileGrant(ctx, lower.ID))

			viaAda, err := store.ListGroupFileGrantsByStorageUser(ctx, st.ID, ada.ID)
			require.NoError(t, err)
			require.Len(t, viaAda, 1, "Ada is in Finance")
			viaBob, err := store.ListGroupFileGrantsByStorageUser(ctx, st.ID, bob.ID)
			require.NoError(t, err)
			require.Empty(t, viaBob, "Bob is not")

			require.NoError(t, store.UpdateGroupFileGrantLevel(ctx, gr.ID, model.GrantOwner))
			gr, err = store.GetGroupFileGrant(ctx, gr.ID)
			require.NoError(t, err)
			require.Equal(t, model.GrantOwner, gr.Level)
			byGroup, err := store.ListGroupFileGrantsByGroup(ctx, fin.ID)
			require.NoError(t, err)
			require.Len(t, byGroup, 1)
			byStorage, err := store.ListGroupFileGrantsByStorage(ctx, st.ID)
			require.NoError(t, err)
			require.Len(t, byStorage, 1)

			// ── cascades ──
			require.NoError(t, store.RemoveGroupMember(ctx, fin.ID, ada.ID))
			require.NoError(t, store.DeleteUser(ctx, bob.ID))
			every, err = store.ListAllGroupMembers(ctx)
			require.NoError(t, err)
			require.Empty(t, every, "a deleted account leaves its groups")
			require.NoError(t, store.DeleteGroup(ctx, fin.ID))
			allGrants, err := store.ListAllGroupFileGrants(ctx)
			require.NoError(t, err)
			require.Empty(t, allGrants, "a deleted group takes its grants along")
		})
	}
}

// TestGroupsIntegrityOnEveryEngine pins what the database itself holds for
// groups rather than the handlers: a name is unique within a tenant (two
// saves at once both pass the handler's check; the index is what refuses the
// second), moving an account to another tenant takes it out of the old
// tenant's groups, and the level a group's role moved is kept once.
func TestGroupsIntegrityOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			super, err := store.GetSupertenant(ctx)
			require.NoError(t, err)
			other, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
			require.NoError(t, err)

			// ── unique names, install-wide included ──
			_, err = store.CreateGroup(ctx, &model.Group{Name: "Finance"})
			require.NoError(t, err)
			_, err = store.CreateGroup(ctx, &model.Group{Name: "Finance"})
			require.Error(t, err, "two install-wide groups of one name (provider_id NULL never collides; tenant_key does)")
			_, err = store.CreateGroup(ctx, &model.Group{Name: "Finance", ProviderID: &other.ID})
			require.NoError(t, err, "another tenant may have its own Finance")
			legal, err := store.CreateGroup(ctx, &model.Group{Name: "Legal", ProviderID: &other.ID})
			require.NoError(t, err)
			legal.Name = "Finance"
			require.Error(t, store.UpdateGroup(ctx, legal), "renaming onto a name the tenant has")
			_, err = store.CreateGroup(ctx, &model.Group{Name: "finance"})
			require.NoError(t, err, "names compare exactly on every engine: finance is not Finance")

			// ── a move to another tenant ──
			ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(ctx, ada.ID, other.ID, ""))
			wide, err := store.CreateGroup(ctx, &model.Group{Name: "Everyone"})
			require.NoError(t, err)
			reloaded, err := store.GetGroup(ctx, legal.ID)
			require.NoError(t, err)
			require.NoError(t, store.AddGroupMember(ctx, reloaded.ID, ada.ID))
			require.NoError(t, store.AddGroupMember(ctx, wide.ID, ada.ID))
			require.NoError(t, store.SetUserProvider(ctx, ada.ID, super.ID, ""))
			ms, err := store.ListUserGroupMemberships(ctx, ada.ID)
			require.NoError(t, err)
			require.Len(t, ms, 1, "left the old tenant's group, kept the install-wide one")
			require.Equal(t, wide.ID, ms[0].GroupID)

			// ── the level before a group's role ──
			_, ok, err := store.GetUserGroupLevel(ctx, ada.ID)
			require.NoError(t, err)
			require.False(t, ok)
			require.NoError(t, store.SetUserGroupLevel(ctx, ada.ID, model.RoleUser))
			require.NoError(t, store.SetUserGroupLevel(ctx, ada.ID, model.RoleViewer), "a second move is not \"before\"")
			level, ok, err := store.GetUserGroupLevel(ctx, ada.ID)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, model.RoleUser, level)
			require.NoError(t, store.DeleteUserGroupLevel(ctx, ada.ID))
			_, ok, err = store.GetUserGroupLevel(ctx, ada.ID)
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

// Migration 00081: where an account comes from, and the groups of its last
// LDAP sign-in — a long DN and a name differing only by case included.
func TestLDAPGroupsAndAuthSourceOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			ada, err := store.CreateUser(ctx, "ada@example.test", "", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			require.Equal(t, model.AuthSourceLocal, ada.AuthSource, "a new account is Local until a directory says otherwise")
			require.NoError(t, store.SetUserAuthSource(ctx, ada.ID, model.AuthSourceLDAP))
			got, err := store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, model.AuthSourceLDAP, got.AuthSource)
			all, err := store.ListUsers(ctx)
			require.NoError(t, err)
			require.Equal(t, model.AuthSourceLDAP, all[len(all)-1].AuthSource)
			require.NoError(t, store.SetUserAuthDirectory(ctx, ada.ID, "ldap-partner"))
			got, err = store.GetUser(ctx, ada.ID)
			require.NoError(t, err)
			require.Equal(t, "ldap-partner", got.AuthDirectory)
			require.Equal(t, "ldap-partner", got.DirectoryOwner())

			// A removed directory's settings, and only its.
			for _, k := range []string{"auth.ldap-partner.url", "auth.ldap-partner.enabled", "auth.ldap-partnerx.url", "auth.ldap.url"} {
				require.NoError(t, store.UpsertSetting(ctx, k, "v"))
			}
			require.NoError(t, store.DeleteSettingsWithPrefix(ctx, "auth.ldap-partner."))
			left, err := store.ListSettings(ctx)
			require.NoError(t, err)
			require.NotContains(t, left, "auth.ldap-partner.url")
			require.NotContains(t, left, "auth.ldap-partner.enabled")
			require.Contains(t, left, "auth.ldap-partnerx.url")
			require.Contains(t, left, "auth.ldap.url")

			long := "cn=" + strings.Repeat("x", 400) + ",ou=groups,dc=example,dc=com"
			require.NoError(t, store.SetUserLDAPGroups(ctx, ada.ID, []string{"finance", "Finance", long, "finance", ""}))
			groups, err := store.ListUserLDAPGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"finance", "Finance", long}, groups, "blanks and duplicates dropped; case kept apart")

			require.NoError(t, store.SetUserLDAPGroups(ctx, ada.ID, nil))
			groups, err = store.ListUserLDAPGroups(ctx, ada.ID)
			require.NoError(t, err)
			require.Empty(t, groups)

			require.NoError(t, store.SetUserLDAPGroups(ctx, ada.ID, []string{"ops"}))
			require.NoError(t, store.DeleteUser(ctx, ada.ID))
			var n int
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_ldap_groups`).Scan(&n))
			require.Zero(t, n, "a deleted account takes its LDAP groups with it")
		})
	}
}

// Migration 00081's directory columns on user_groups, on every engine.
func TestGroupDirectoryOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			g, err := store.CreateGroup(ctx, &model.Group{Name: "dept-legal", DirectoryID: "ldap:e4191e09-5166-3785-9099-4557eb72e805", DirectoryName: "dept-legal"})
			require.NoError(t, err)
			require.Equal(t, "ldap:e4191e09-5166-3785-9099-4557eb72e805", g.DirectoryID)
			require.True(t, g.Synced())
			plain, err := store.CreateGroup(ctx, &model.Group{Name: "Plain"})
			require.NoError(t, err)
			require.Empty(t, plain.DirectoryID)

			require.NoError(t, store.SetGroupDirectory(ctx, g.ID, g.DirectoryID, "legal", model.GroupDirectoryRemoved))
			got, err := store.GetGroup(ctx, g.ID)
			require.NoError(t, err)
			require.Equal(t, "legal", got.DirectoryName)
			require.Equal(t, model.GroupDirectoryRemoved, got.DirectoryState)
			require.False(t, got.Synced())

			// UpdateGroup never touches where it comes from.
			got.Name = "Legal"
			require.NoError(t, store.UpdateGroup(ctx, got))
			got, err = store.GetGroup(ctx, g.ID)
			require.NoError(t, err)
			require.Equal(t, "ldap:e4191e09-5166-3785-9099-4557eb72e805", got.DirectoryID)

			require.NoError(t, store.SetGroupDirectory(ctx, g.ID, "", "", ""))
			all, err := store.ListGroups(ctx)
			require.NoError(t, err)
			for _, x := range all {
				require.Empty(t, x.DirectoryID)
			}
		})
	}
}

package perm_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

var testDBCounter atomic.Int64

func newTestStore(t *testing.T) db.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:perm_test_%d?mode=memory&cache=shared", testDBCounter.Add(1))
	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(context.Background(), drv, conn))
	return drv.NewStore(conn)
}

// TestLoaderFreshInstallChangesNobody is the upgrade promise end to end: a
// migrated database with no permission rows answers each role exactly what
// it could do before permissions existed.
func TestLoaderFreshInstallChangesNobody(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	l := perm.NewLoader(store)

	for role, want := range map[string]perm.Set{
		model.RoleUser:   perm.Standard,
		model.RoleViewer: perm.ReadOnly,
	} {
		u, err := store.CreateUser(ctx, role+"@example.test", "x", role, "en", "UTC")
		require.NoError(t, err)
		res, err := l.Load(ctx, u)
		require.NoError(t, err)
		require.Equal(t, want, res.Allowed, role)
	}

	res, err := l.Load(ctx, nil)
	require.NoError(t, err)
	require.Zero(t, res.Allowed.Len(), "no user, no permissions")
}

func TestLoaderAllLayers(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	l := perm.NewLoader(store)

	ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)

	// Defaults narrowed: no protocols for new users.
	require.NoError(t, perm.SaveDefaults(ctx, store, perm.Standard.Without(perm.AccessSFTP).Without(perm.AccessFTP)))
	// Her role.
	contractors, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Contractors", Enabled: true,
		Permissions: perm.Standard.Without(perm.AccessSFTP).Without(perm.AccessFTP).Without(perm.FilesDelete).Without(perm.FilesPurge).Strings(),
	})
	require.NoError(t, err)
	require.NoError(t, store.SetUserCustomRole(ctx, ada.ID, contractors.ID))
	perm.Invalidate()
	// Her own override brings SFTP back.
	require.NoError(t, store.SetUserPermissionOverrides(ctx, ada.ID, map[string]string{"access.sftp": model.PermAllow}, nil))

	res, err := l.Load(ctx, ada)
	require.NoError(t, err)
	require.False(t, res.Can(perm.AccessFTP), "her role leaves it off")
	require.Equal(t, perm.SourceRule, res.Why(perm.AccessFTP).Kind)
	require.True(t, res.Can(perm.AccessSFTP), "override")
	require.Equal(t, perm.SourceOverride, res.Why(perm.AccessSFTP).Kind)
	require.False(t, res.Can(perm.FilesDelete), "her role")
	require.Equal(t, "Contractors", res.Why(perm.FilesDelete).RuleName)
	require.True(t, res.Can(perm.FilesCreate))

	// Without the role, it no longer binds.
	require.NoError(t, store.SetUserCustomRole(ctx, ada.ID, 0))
	perm.Invalidate()
	res, err = l.Load(ctx, ada)
	require.NoError(t, err)
	require.True(t, res.Can(perm.FilesDelete))
}

func TestDefaultsRefuseAdminFull(t *testing.T) {
	store := newTestStore(t)
	err := perm.SaveDefaults(context.Background(), store, perm.Of(perm.AdminFull))
	require.ErrorIs(t, err, perm.ErrInvalid)
}

func TestCorruptDefaultsFailClosed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingPermissionDefaults, "{not json"))
	u, err := store.CreateUser(ctx, "u@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	_, err = perm.NewLoader(store).Load(ctx, u)
	require.Error(t, err, "an unreadable defaults row must deny, not fall back to Standard")
}

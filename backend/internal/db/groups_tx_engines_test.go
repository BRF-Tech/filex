package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The group methods join a transaction the context carries (db.Conn), like
// every other Store method (internal/db tx.go) — and the three that need one
// of their own join it rather than open a second.
//
// ⚠ Why it matters: on SQLite the pool has ONE connection and Store.WithTx
// holds it, so a statement sent to the pool instead waits for it until its
// deadline. Account moves call DropForeignMemberships from SetUserProvider,
// which runs on the context's connection — the two had to agree.
//
// RED PROOF (PR #78 as submitted): GroupSQL ran every statement on its own
// *sql.DB; inside WithTx on SQLite CreateGroup waited for the connection the
// transaction held and failed with "context deadline exceeded".
func TestGroupsJoinATransactionOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ada, err := store.CreateUser(ctx, "ada@example.test", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			st, err := store.CreateStorage(ctx, &model.Storage{Name: "grptx", Driver: "local", MountPath: "/grptx", Enabled: true, ConfigJSON: []byte(`{"root":"/tmp"}`)})
			require.NoError(t, err)

			undo := errors.New("roll it back")
			err = store.WithTx(ctx, func(ctx context.Context) error {
				g, err := store.CreateGroup(ctx, &model.Group{Name: "Finance"})
				if err != nil {
					return err
				}
				if err := store.AddGroupMember(ctx, g.ID, ada.ID); err != nil {
					return err
				}
				if _, _, err := store.SetUserLinkedGroups(ctx, ada.ID, model.GroupSourceSSO, []int64{g.ID}); err != nil {
					return err
				}
				if _, err := store.CreateGroupFileGrant(ctx, &model.FileGrant{StorageID: st.ID, PathPrefix: "docs", IsDir: true, GroupID: g.ID, Level: model.GrantViewer}); err != nil {
					return err
				}
				ms, err := store.ListGroupMembers(ctx, g.ID)
				if err != nil {
					return err
				}
				require.Len(t, ms, 1, "a membership written in the transaction is read back in it")
				return undo
			})
			require.ErrorIs(t, err, undo)

			groups, err := store.ListGroups(ctx)
			require.NoError(t, err)
			require.Empty(t, groups, "the rollback took the group along")
			grants, err := store.ListAllGroupFileGrants(ctx)
			require.NoError(t, err)
			require.Empty(t, grants, "and its grant")
		})
	}
}

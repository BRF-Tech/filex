package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The third request kind (operator decision 2026-10-03): a new-folder
// approval is kept and found on every engine as its own kind, apart from the
// in-place approval of the same folder (MySQL keeps `kind` in a VARCHAR(16)).
func TestE2ERequestsNewFolderKindOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "depo", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			ada, err := store.CreateUser(ctx, "ada@example.com", "hash", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			now := time.Now().UTC().Truncate(time.Second)
			week := now.Add(7 * 24 * time.Hour)

			r, err := store.CreateE2ERequest(ctx, &model.E2ERequest{
				UserID: ada.ID, Requester: "Ada", StorageID: st.ID, Path: "Muhasebe",
				Kind: model.E2ERequestNewFolder, Reason: "yeni şifreli klasör", ExpiresAt: week,
			})
			require.NoError(t, err)
			require.Equal(t, model.E2ERequestNewFolder, r.Kind)
			r.Status = model.E2ERequestApproved
			ok, err := store.UpdateE2ERequest(ctx, r, model.E2ERequestPending)
			require.NoError(t, err)
			require.True(t, ok)

			got, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, "Muhasebe", model.E2ERequestNewFolder, now)
			require.NoError(t, err)
			require.Equal(t, r.ID, got.ID)
			_, err = store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, "Muhasebe", model.E2ERequestFolder, now)
			require.Error(t, err, "a new-folder approval is not the folder's own")
		})
	}
}

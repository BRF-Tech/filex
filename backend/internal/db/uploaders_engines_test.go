package db_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestUploadersOnEveryEngine: a person's uploaders and their deletion links
// (migration 00136, db.UploaderSQL) - found by key hash, changed and given a
// new key (the old one is then nobody's), listed per account, deleted only
// by their owner; a deletion link is spent once even when two requests race,
// outlives its uploader, and goes with the account.
func TestUploadersOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			owner, err := store.CreateUser(ctx, "up-owner@test.local", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			other, err := store.CreateUser(ctx, "up-other@test.local", "x", model.RoleUser, "en", "UTC")
			require.NoError(t, err)
			key := strings.Repeat("a1", 32)

			made, err := store.CreateUploader(ctx, &model.Uploader{
				UserID: owner.ID, Name: "Screenshots", StorageID: 7, Folder: "uploads/shots",
				Visibility: model.UploaderPrivate, LinkDays: 30, TenantID: 3, KeyHash: key,
			})
			require.NoError(t, err)
			require.NotZero(t, made.ID)
			require.Equal(t, "Screenshots", made.Name)
			require.Equal(t, model.UploaderPrivate, made.Visibility)
			require.Equal(t, int64(3), made.TenantID)
			require.False(t, made.CreatedAt.IsZero())
			require.Nil(t, made.LastUsedAt)

			got, err := store.GetUploaderByKey(ctx, key)
			require.NoError(t, err)
			require.Equal(t, made.ID, got.ID)
			_, err = store.GetUploaderByKey(ctx, strings.Repeat("b2", 32))
			require.True(t, errors.Is(err, sql.ErrNoRows), "%v", err)
			_, err = store.GetUploaderByKey(ctx, "")
			require.True(t, errors.Is(err, sql.ErrNoRows), "an empty key is nobody's")

			// Changed: the same values again still answer "there" (MySQL counts
			// CHANGED rows).
			got.Name, got.Visibility, got.LinkDays = "Renamed", model.UploaderPublic, 0
			ok, err := store.UpdateUploader(ctx, got)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.UpdateUploader(ctx, got)
			require.NoError(t, err)
			require.True(t, ok)
			foreign := *got
			foreign.UserID = other.ID
			ok, err = store.UpdateUploader(ctx, &foreign)
			require.NoError(t, err)
			require.False(t, ok, "another account's uploader is not theirs to change")

			newKey := strings.Repeat("c3", 32)
			ok, err = store.SetUploaderKey(ctx, made.ID, owner.ID, newKey)
			require.NoError(t, err)
			require.True(t, ok)
			_, err = store.GetUploaderByKey(ctx, key)
			require.True(t, errors.Is(err, sql.ErrNoRows), "the old key still found the uploader")
			ok, err = store.SetUploaderKey(ctx, made.ID, other.ID, strings.Repeat("d4", 32))
			require.NoError(t, err)
			require.False(t, ok)

			require.NoError(t, store.TouchUploader(ctx, made.ID))
			got, err = store.GetUploader(ctx, made.ID)
			require.NoError(t, err)
			require.Equal(t, "Renamed", got.Name)
			require.Equal(t, model.UploaderPublic, got.Visibility)
			require.NotNil(t, got.LastUsedAt)

			_, err = store.CreateUploader(ctx, &model.Uploader{UserID: other.ID, Name: "Theirs", StorageID: 7, KeyHash: strings.Repeat("e5", 32)})
			require.NoError(t, err)
			mine, err := store.ListUploaders(ctx, owner.ID)
			require.NoError(t, err)
			require.Len(t, mine, 1)

			// A deletion link: spent once, by one of two racing requests.
			tok := strings.Repeat("f6", 32)
			require.NoError(t, store.CreateUploadDeletion(ctx, &model.UploadDeletion{
				TokenHash: tok, UserID: owner.ID, UploaderID: made.ID, StorageID: 7, NodeID: 42, ShareID: 9, TenantID: 3, Name: "abc123-shot.png",
			}))
			d, err := store.GetUploadDeletion(ctx, tok)
			require.NoError(t, err)
			require.Equal(t, int64(42), d.NodeID)
			require.Equal(t, int64(9), d.ShareID)
			require.Equal(t, "abc123-shot.png", d.Name)

			// The uploader goes; its deletion link stays.
			ok, err = store.DeleteUploader(ctx, made.ID, other.ID)
			require.NoError(t, err)
			require.False(t, ok, "another account deleted the uploader")
			ok, err = store.DeleteUploader(ctx, made.ID, owner.ID)
			require.NoError(t, err)
			require.True(t, ok)
			_, err = store.GetUploadDeletion(ctx, tok)
			require.NoError(t, err, "a deletion link outlives its uploader")

			var spent atomic.Int64
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if ok, err := store.DeleteUploadDeletion(ctx, d.ID); err == nil && ok {
						spent.Add(1)
					}
				}()
			}
			wg.Wait()
			require.Equal(t, int64(1), spent.Load())
			_, err = store.GetUploadDeletion(ctx, tok)
			require.True(t, errors.Is(err, sql.ErrNoRows))

			// The account goes; its uploaders and deletion links with it.
			require.NoError(t, store.CreateUploadDeletion(ctx, &model.UploadDeletion{
				TokenHash: strings.Repeat("07", 32), UserID: other.ID, StorageID: 7, NodeID: 43,
			}))
			require.NoError(t, store.DeleteUser(ctx, other.ID))
			theirs, err := store.ListUploaders(ctx, other.ID)
			require.NoError(t, err)
			require.Empty(t, theirs)
			_, err = store.GetUploadDeletion(ctx, strings.Repeat("07", 32))
			require.True(t, errors.Is(err, sql.ErrNoRows), "a deleted account's deletion link stayed")
		})
	}
}

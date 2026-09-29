package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestPluginRequestsOnEveryEngine walks the plugin_requests table (migration
// 00070) on every engine: a request is recorded with what the source answered
// frozen on it, read back by id and as the pending request for its source,
// listed by state, and closed exactly once — a second administrator's close of
// the same request is refused by the store itself (onlyIfPending). Written
// ONCE for every engine (db.PluginRequestSQL), so it is measured once for
// every engine; the timestamps are written by the store, so each engine's
// spelling of them is measured too.
func TestPluginRequestsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			admin, err := store.CreateUser(ctx, "admin@example.com", "hash", "admin", "tr", "UTC")
			require.NoError(t, err)
			agent, err := store.CreateUser(ctx, "agent@example.com", "hash", "admin", "tr", "UTC")
			require.NoError(t, err)
			tokID := int64(42)
			expires := time.Now().UTC().Add(14 * 24 * time.Hour).Truncate(time.Second)

			in := &model.PluginRequest{
				Kind: model.PluginRequestKindApp, Op: model.PluginRequestOpInstall, Name: "lang-eo",
				SourceKind: "url", SourceJSON: `{"manifest_url":"https://example.com/filex-app.json"}`,
				SourceKey: "k-lang-eo", Version: "1.0.0",
				ManifestJSON: `{"name":"lang-eo","label":{"tr":"Esperanto — Türkçe açıklama"}}`,
				ReviewJSON:   `{"kind":"language_pack"}`,
				SHA256:       "aa", ManifestSHA256: "bb", PermissionsJSON: `["files:read"]`,
				RequestedBy: &agent.ID, Requester: "agent@example.com", TokenID: &tokID, TokenLabel: "work-agent",
				Reason: "Esperanto kullanıcıları için — lütfen onaylayın", ExpiresAt: expires,
			}
			r, err := store.CreatePluginRequest(ctx, in)
			require.NoError(t, err)
			require.NotZero(t, r.ID)
			require.Len(t, r.Key, 32, "a key is minted when none is given")
			require.Equal(t, model.PluginRequestPending, r.Status, "a new request is pending")
			require.Equal(t, "lang-eo", r.Name)
			require.Equal(t, in.ManifestJSON, r.ManifestJSON, "the snapshot is kept byte for byte (Turkish text too)")
			require.Equal(t, in.Reason, r.Reason)
			require.Equal(t, `["files:read"]`, r.PermissionsJSON)
			require.NotNil(t, r.RequestedBy)
			require.Equal(t, agent.ID, *r.RequestedBy)
			require.NotNil(t, r.TokenID)
			require.EqualValues(t, 42, *r.TokenID)
			require.Nil(t, r.PluginID, "an install names no installed plugin")
			require.Nil(t, r.DecidedAt)
			require.WithinDuration(t, expires, r.ExpiresAt, time.Second, "the expiry reads back as the instant it was")
			require.False(t, r.CreatedAt.IsZero())

			got, err := store.GetPluginRequest(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, r.Key, got.Key)

			pending, err := store.PendingPluginRequestBySource(ctx, "k-lang-eo")
			require.NoError(t, err)
			require.Equal(t, r.ID, pending.ID)
			_, err = store.PendingPluginRequestBySource(ctx, "k-other")
			require.True(t, errors.Is(err, sql.ErrNoRows), "no pending request for a source: sql.ErrNoRows, got %v", err)
			_, err = store.GetPluginRequest(ctx, r.ID+1000)
			require.True(t, errors.Is(err, sql.ErrNoRows))

			// An upgrade of an installed plugin names it.
			pid := int64(7)
			up, err := store.CreatePluginRequest(ctx, &model.PluginRequest{
				Kind: model.PluginRequestKindStorage, Op: model.PluginRequestOpUpgrade, Name: "myfs", PluginID: &pid,
				SourceKind: "from_source", SourceKey: "k-myfs", Version: "1.1.0", FromVersion: "1.0.0",
				SHA256: "cc", ExpiresAt: expires,
			})
			require.NoError(t, err)
			require.NotNil(t, up.PluginID)
			require.EqualValues(t, 7, *up.PluginID)
			require.Equal(t, "[]", up.PermissionsJSON, "no permissions: an empty list, never NULL")
			require.Equal(t, "{}", up.SourceJSON)

			all, err := store.ListPluginRequests(ctx, "", 0)
			require.NoError(t, err)
			require.Len(t, all, 2)
			require.Equal(t, up.ID, all[0].ID, "newest first")

			// Closed once: the first close wins, the second is refused.
			now := time.Now().UTC().Truncate(time.Second)
			r.Status = model.PluginRequestApproved
			r.DecidedBy = &admin.ID
			r.Decider = "admin@example.com"
			r.DecidedAt = &now
			r.ResultJSON = `{"id":1}`
			ok, err := store.UpdatePluginRequest(ctx, r, true)
			require.NoError(t, err)
			require.True(t, ok)
			r.Status = model.PluginRequestRejected
			ok, err = store.UpdatePluginRequest(ctx, r, true)
			require.NoError(t, err)
			require.False(t, ok, "a request that is no longer pending is not closed again")

			got, err = store.GetPluginRequest(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, model.PluginRequestApproved, got.Status)
			require.NotNil(t, got.DecidedBy)
			require.Equal(t, admin.ID, *got.DecidedBy)
			require.NotNil(t, got.DecidedAt)
			require.WithinDuration(t, now, *got.DecidedAt, time.Second)
			require.Equal(t, `{"id":1}`, got.ResultJSON)

			_, err = store.PendingPluginRequestBySource(ctx, "k-lang-eo")
			require.True(t, errors.Is(err, sql.ErrNoRows), "an approved request is no longer the pending one for its source")

			onlyPending, err := store.ListPluginRequests(ctx, model.PluginRequestPending, 10)
			require.NoError(t, err)
			require.Len(t, onlyPending, 1)
			require.Equal(t, up.ID, onlyPending[0].ID)

			// A request outlives the account that made it (ON DELETE SET NULL).
			require.NoError(t, store.DeleteUser(ctx, agent.ID))
			got, err = store.GetPluginRequest(ctx, r.ID)
			require.NoError(t, err)
			require.Nil(t, got.RequestedBy, "the requester's row went; the request stays")
			require.Equal(t, "agent@example.com", got.Requester, "the name shown when it asked stays")
		})
	}
}

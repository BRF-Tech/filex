package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPOAuthRecordsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Now().Unix()
			const payload = `{"client_id":"registered","scope":"files:read"}`

			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "client", "registered", payload, now+600))
			// A fresh store instance must read persisted state, with immutable keys.
			got, err := drv.NewStore(sqlDB).GetMCPOAuthRecord(ctx, "client", "registered", now)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			require.Error(t, store.CreateMCPOAuthRecord(ctx, "client", "registered", "replacement", now+900))
			got, err = store.GetMCPOAuthRecord(ctx, "client", "registered", now)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			_, err = store.GetMCPOAuthRecord(ctx, "code", "registered", now)
			require.ErrorIs(t, err, sql.ErrNoRows)
			_, err = store.GetMCPOAuthRecord(ctx, "client", "registered", now+600)
			require.ErrorIs(t, err, sql.ErrNoRows)
			_, err = store.TakeMCPOAuthRecord(ctx, "client", "registered", now+600)
			require.ErrorIs(t, err, sql.ErrNoRows)

			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", "single-use", payload, now+600))
			start := make(chan struct{})
			results := make(chan error, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					got, err := store.TakeMCPOAuthRecord(ctx, "code", "single-use", now)
					if err == nil && got != payload {
						err = fmt.Errorf("wrong consumed payload: %q", got)
					}
					results <- err
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				} else {
					require.ErrorIs(t, err, sql.ErrNoRows)
				}
			}
			require.Equal(t, 1, wins, "only one caller may consume a code")

			rollback := errors.New("rollback OAuth exchange")
			err = store.WithTx(ctx, func(ctx context.Context) error {
				_, err := store.TakeMCPOAuthRecord(ctx, "client", "registered", now)
				require.NoError(t, err)
				require.NoError(t, store.CreateMCPOAuthRecord(ctx, "refresh", "rolled-back", payload, now+600))
				return rollback
			})
			require.ErrorIs(t, err, rollback)
			got, err = store.GetMCPOAuthRecord(ctx, "client", "registered", now)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			_, err = store.GetMCPOAuthRecord(ctx, "refresh", "rolled-back", now)
			require.ErrorIs(t, err, sql.ErrNoRows)
		})
	}
}

func TestMCPOAuthRecordCapacityOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Now().Unix()
			require.NoError(t, store.WithTx(ctx, func(ctx context.Context) error {
				for i := 0; i < 10000; i++ {
					if err := store.CreateMCPOAuthRecord(ctx, "code", fmt.Sprint(i), "{}", now+600); err != nil {
						return err
					}
				}
				return nil
			}))
			require.Error(t, store.CreateMCPOAuthRecord(ctx, "code", "over-capacity", "{}", now+600))
			// An expired row must free capacity when the next record is created.
			_, err := store.TakeMCPOAuthRecord(ctx, "code", "0", now)
			require.NoError(t, err)
			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", "expired", "{}", now-1))
			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", "after-prune", "{}", now+600))
			var count int
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_oauth_records`).Scan(&count))
			require.Equal(t, 10000, count)

			_, err = store.TakeMCPOAuthRecord(ctx, "code", "1", now)
			require.NoError(t, err)
			start := make(chan struct{})
			results := make(chan error, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					results <- store.CreateMCPOAuthRecord(ctx, "code", fmt.Sprintf("capacity-race-%d", i), "{}", now+600)
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				}
			}
			require.Equal(t, 1, wins, "racing creators must not exceed capacity")
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_oauth_records`).Scan(&count))
			require.Equal(t, 10000, count)
		})
	}
}

// MySQL's default repeatable-read snapshot can predate a later exchange.
// Take must consume and return the current row, even in that transaction.
func TestMCPOAuthMySQLCurrentTake(t *testing.T) {
	for _, e := range engines() {
		if e.name != "mysql" {
			continue
		}
		sqlDB, drv := openMigrated(t, e)
		store := drv.NewStore(sqlDB)
		ctx := context.Background()
		now := time.Now().Unix()
		require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", "reused", `{"version":1}`, now+600))
		require.NoError(t, store.WithTx(ctx, func(txctx context.Context) error {
			got, err := store.GetMCPOAuthRecord(txctx, "code", "reused", now)
			require.NoError(t, err)
			require.Equal(t, `{"version":1}`, got)
			// A different connection completes an exchange after this
			// transaction establishes its consistent-read snapshot.
			_, err = store.TakeMCPOAuthRecord(ctx, "code", "reused", now)
			require.NoError(t, err)
			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", "reused", `{"version":2}`, now+600))
			got, err = store.TakeMCPOAuthRecord(txctx, "code", "reused", now)
			require.NoError(t, err)
			require.Equal(t, `{"version":2}`, got)
			return nil
		}))
	}
}

// Filling unauthenticated registrations must leave room for existing grants.
func TestMCPOAuthClientCapacityOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			now := time.Now().Unix()
			require.NoError(t, store.WithTx(ctx, func(ctx context.Context) error {
				for i := 0; i < 1000; i++ {
					if err := store.CreateMCPOAuthRecord(ctx, "client", fmt.Sprint(i), "{}", now+600); err != nil {
						return err
					}
				}
				return nil
			}))
			require.Error(t, store.CreateMCPOAuthRecord(ctx, "client", "overflow", "{}", now+600))
			for _, kind := range []string{"request", "code", "refresh"} {
				require.NoError(t, store.CreateMCPOAuthRecord(ctx, kind, "existing-grant", "{}", now+600))
			}
			// Consuming and pruning clients both release registration capacity.
			_, err := store.TakeMCPOAuthRecord(ctx, "client", "0", now)
			require.NoError(t, err)
			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "client", "expired", "{}", now-1))
			require.NoError(t, store.CreateMCPOAuthRecord(ctx, "client", "after-prune", "{}", now+600))
			require.Error(t, store.CreateMCPOAuthRecord(ctx, "client", "overflow-again", "{}", now+600))
			var clients, total int
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_oauth_records WHERE kind='client'`).Scan(&clients))
			require.Equal(t, 1000, clients)
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_oauth_records`).Scan(&total))
			require.Equal(t, 1003, total)
		})
	}
}

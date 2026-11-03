package db_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCaptchaMarksOnEveryEngine: the bot check's shared state (migration
// 00134, db.CaptchaMarkSQL) - a mark is spent once while it lives, a racing
// second writer is told so, an ended one can be written again, the outage
// flag is written over, and pruning takes only the ended ones.
func TestCaptchaMarksOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			const now = int64(1_762_300_000)
			key := "captcha:local:used:" + strings.Repeat("ab", 32)

			_, ok, err := store.CaptchaMarkUntil(ctx, key)
			require.NoError(t, err)
			require.False(t, ok)

			added, err := store.AddCaptchaMark(ctx, key, now+600, now)
			require.NoError(t, err)
			require.True(t, added)
			added, err = store.AddCaptchaMark(ctx, key, now+600, now+1)
			require.NoError(t, err)
			require.False(t, added, "spent while it lives")
			until, ok, err := store.CaptchaMarkUntil(ctx, key)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, now+600, until)

			// Ended: the key can be written again.
			added, err = store.AddCaptchaMark(ctx, key, now+1200, now+600)
			require.NoError(t, err)
			require.True(t, added)

			// Racing writers on a new key: one wins.
			race := "captcha:token:" + strings.Repeat("cd", 32)
			var wins atomic.Int64
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if ok, err := store.AddCaptchaMark(ctx, race, now+300, now); err == nil && ok {
						wins.Add(1)
					}
				}()
			}
			wg.Wait()
			require.Equal(t, int64(1), wins.Load())

			// The flag is written over, the same value included (MySQL counts
			// changed rows).
			require.NoError(t, store.PutCaptchaMark(ctx, "captcha:fallback:active", now+120))
			require.NoError(t, store.PutCaptchaMark(ctx, "captcha:fallback:active", now+120))
			require.NoError(t, store.PutCaptchaMark(ctx, "captcha:fallback:active", now+240))
			until, _, err = store.CaptchaMarkUntil(ctx, "captcha:fallback:active")
			require.NoError(t, err)
			require.Equal(t, now+240, until)

			n, err := store.PruneCaptchaMarks(ctx, now+350)
			require.NoError(t, err)
			require.Equal(t, int64(2), n, "the flag and the token ended; the re-written solution lives")
			_, ok, err = store.CaptchaMarkUntil(ctx, key)
			require.NoError(t, err)
			require.True(t, ok)
		})
	}
}

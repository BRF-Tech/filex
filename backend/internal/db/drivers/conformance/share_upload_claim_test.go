package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A file request's total cap is enforced by one conditional UPDATE
// (ReserveShareUpload), on every engine: the database decides whether places
// are left, not a count the handler read earlier (GHSA-4pc6-66f4-q2x4).
func TestReserveShareUpload_TheDatabaseHoldsTheCap(t *testing.T) {
	each(t, uploadClaimHoldsTheCap)
}

func uploadClaimHoldsTheCap(t *testing.T, _ *sql.DB, store db.Store, _ string) {
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "upload-claim-" + suffix, Driver: "local", MountPath: "/u", Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	inbox, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "inbox", Path: "/inbox", PathHash: pathkey.Hash(st.ID, "/inbox"), Type: model.NodeTypeDirectory})
	require.NoError(t, err)
	links := 0
	link := func(max *int) *model.Share {
		// 32 characters, as every real token (PostgreSQL pads a CHAR(32)).
		links++
		token := fmt.Sprintf("u%d%030d", links, time.Now().UnixNano())
		sh, err := store.CreateShare(ctx, &model.Share{NodeID: inbox.ID, Token: token, Kind: model.ShareKindDrop, MaxUploads: max})
		require.NoError(t, err)
		return sh
	}
	count := func(id int64) int {
		cur, err := store.GetShareByID(ctx, id)
		require.NoError(t, err)
		return cur.UploadCount
	}
	claim := func(id int64, n int) bool {
		ok, err := store.ReserveShareUpload(ctx, id, n)
		require.NoError(t, err)
		return ok
	}

	three := 3
	capped := link(&three)
	require.True(t, claim(capped.ID, 2))
	require.False(t, claim(capped.ID, 2), "two where one is left")
	require.Equal(t, 2, count(capped.ID), "a refused claim takes nothing")
	require.True(t, claim(capped.ID, 1))
	require.False(t, claim(capped.ID, 1), "the cap is spent")
	require.False(t, claim(capped.ID, 0), "a claim of nothing is no claim")
	require.Equal(t, 3, count(capped.ID))

	require.NoError(t, store.ReleaseShareUpload(ctx, capped.ID, 2))
	require.Equal(t, 1, count(capped.ID))
	require.NoError(t, store.ReleaseShareUpload(ctx, capped.ID, 5))
	require.Equal(t, 0, count(capped.ID), "never below zero")

	open := link(nil)
	require.True(t, claim(open.ID, 5))
	require.True(t, claim(open.ID, 5))
	require.Equal(t, 10, count(open.ID), "a link with no cap still counts what it took")

	// Twenty claims at once against five places: five, whatever the order.
	five := 5
	contended := link(&five)
	var got atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := store.ReserveShareUpload(ctx, contended.ID, 1); err == nil && ok {
				got.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(5), got.Load())
	require.Equal(t, 5, count(contended.ID))
}

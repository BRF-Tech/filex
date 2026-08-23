package share

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func seedNode(t *testing.T, store interface {
	CreateStorage(context.Context, *model.Storage) (*model.Storage, error)
	CreateNode(context.Context, *model.Node) (*model.Node, error)
}, hash string) *model.Node {
	t.Helper()
	ctx := context.Background()
	stg, err := store.CreateStorage(ctx, &model.Storage{
		Name: "s-" + hash, Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: hash,
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

func TestClampExpiry(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	week := now.Add(7 * 24 * time.Hour)

	got, clamped := ClampExpiry(nil, 7, now)
	require.True(t, clamped, "no expiry must become now+max")
	require.True(t, got.Equal(week))

	month := now.Add(30 * 24 * time.Hour)
	got, clamped = ClampExpiry(&month, 7, now)
	require.True(t, clamped, "an expiry past the ceiling must be pulled back")
	require.True(t, got.Equal(week))

	day := now.Add(24 * time.Hour)
	got, clamped = ClampExpiry(&day, 7, now)
	require.False(t, clamped, "an expiry within the ceiling is honoured as is")
	require.True(t, got.Equal(day))

	got, clamped = ClampExpiry(nil, 0, now)
	require.False(t, clamped, "no ceiling: never stays never")
	require.Nil(t, got)
}

// ⚠ The behaviour this guards: before v0.25 a link with no expiry lived
// forever and so did its folder archive. The default ceiling gives a new link
// a week unless the admin says otherwise.
func TestCreate_ClampsToTheMaxTTLSetting(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	svc := NewService(store)
	n := seedNode(t, store, "h-ttl-1")

	require.Equal(t, DefaultMaxTTLDays, svc.MaxTTLDays(ctx), "unset setting = default")

	before := time.Now()
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	require.NotNil(t, sh.ExpiresAt, "a link minted with no expiry must still get one")
	limit := before.Add(time.Duration(DefaultMaxTTLDays) * 24 * time.Hour)
	require.WithinDuration(t, limit, *sh.ExpiresAt, time.Minute)

	month := time.Now().Add(30 * 24 * time.Hour)
	sh, err = svc.Create(ctx, CreateOpts{NodeID: n.ID, ExpiresAt: &month})
	require.NoError(t, err)
	require.True(t, sh.ExpiresAt.Before(month), "30 days must be shortened to the 7-day ceiling")
	require.WithinDuration(t, limit, *sh.ExpiresAt, time.Minute)

	// A shorter ceiling set by the admin wins over the default…
	require.NoError(t, store.UpsertSetting(ctx, SettingKeyMaxTTLDays, "2"))
	require.Equal(t, 2, svc.MaxTTLDays(ctx))
	sh, err = svc.Create(ctx, CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(48*time.Hour), *sh.ExpiresAt, time.Minute)

	// …and "0" means no ceiling at all.
	require.NoError(t, store.UpsertSetting(ctx, SettingKeyMaxTTLDays, "0"))
	sh, err = svc.Create(ctx, CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	require.Nil(t, sh.ExpiresAt, "with no ceiling a link with no expiry stays permanent")

	// A garbage value is the default, never "no ceiling".
	require.NoError(t, store.UpsertSetting(ctx, SettingKeyMaxTTLDays, "forever"))
	require.Equal(t, DefaultMaxTTLDays, svc.MaxTTLDays(ctx))
}

// Existing links are reported, not shortened: the count is what the operator
// sees in /api/admin/protection.
func TestCountOverMaxTTL_ReportsExistingLinksWithoutTouchingThem(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	svc := NewService(store)
	n := seedNode(t, store, "h-ttl-2")

	// Mint with no ceiling so the links can outlive the one set afterwards.
	require.NoError(t, store.UpsertSetting(ctx, SettingKeyMaxTTLDays, "0"))
	permanent, err := svc.Create(ctx, CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	month := time.Now().Add(30 * 24 * time.Hour)
	long, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, ExpiresAt: &month})
	require.NoError(t, err)
	day := time.Now().Add(24 * time.Hour)
	short, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, ExpiresAt: &day})
	require.NoError(t, err)

	over, err := svc.CountOverMaxTTL(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, 0, over, "no ceiling: nothing is over it")

	require.NoError(t, store.UpsertSetting(ctx, SettingKeyMaxTTLDays, strconv.Itoa(7)))
	over, err = svc.CountOverMaxTTL(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, 2, over, "the permanent link and the 30-day link outlive a 7-day ceiling")

	for _, sh := range []*model.Share{permanent, long, short} {
		again, err := store.GetShareByToken(ctx, sh.Token)
		require.NoError(t, err)
		if sh.ExpiresAt == nil {
			require.Nil(t, again.ExpiresAt, "an existing permanent link must stay permanent")
		} else {
			require.WithinDuration(t, *sh.ExpiresAt, *again.ExpiresAt, time.Second, "an existing link's expiry must not move")
		}
	}
}

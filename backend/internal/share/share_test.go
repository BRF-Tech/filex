package share

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestIsExpired covers the IsExpired contract — no expiry, expiry past,
// download cap reached.
func TestIsExpired(t *testing.T) {
	now := time.Now()

	// No expiry, no max → never expired.
	s := &model.Share{}
	assert.False(t, s.IsExpired(now))

	// Past expiry → expired.
	past := now.Add(-time.Minute)
	s.ExpiresAt = &past
	assert.True(t, s.IsExpired(now))

	// Future expiry → not expired.
	future := now.Add(time.Hour)
	s2 := &model.Share{ExpiresAt: &future}
	assert.False(t, s2.IsExpired(now))

	// Max downloads reached → expired.
	max := 3
	s3 := &model.Share{MaxDownloads: &max, DownloadCount: 3}
	assert.True(t, s3.IsExpired(now))

	// Max downloads NOT reached → not expired.
	s4 := &model.Share{MaxDownloads: &max, DownloadCount: 2}
	assert.False(t, s4.IsExpired(now))

	// Nil receiver
	var nilShare *model.Share
	assert.True(t, nilShare.IsExpired(now))
}

// TestPIN_BcryptRoundTrip — Service.Create stores a bcrypt hash, and the
// hash + plaintext must compare cleanly.
func TestPIN_BcryptRoundTrip(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()

	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0001",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)

	svc := NewService(store)
	sh, err := svc.Create(ctx, CreateOpts{
		NodeID: n.ID,
		PIN:    "1234",
	})
	require.NoError(t, err)
	require.NotEmpty(t, sh.PinHash)
	require.NotEqual(t, "1234", sh.PinHash, "pin must be hashed before persist")

	// Hash matches plaintext
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(sh.PinHash), []byte("1234")))
	// Wrong pin doesn't match
	require.Error(t, bcrypt.CompareHashAndPassword([]byte(sh.PinHash), []byte("9999")))
}

// TestService_Resolve_BadPIN returns ErrBadPIN.
func TestService_Resolve_BadPIN(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, _ := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0001",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	svc := NewService(store)
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, PIN: "secret"})
	require.NoError(t, err)

	_, err = svc.Resolve(ctx, sh.Token, "wrong")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBadPIN), "got %v", err)
}

// TestService_Resolve_OK returns the share when pin matches.
func TestService_Resolve_OK(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, _ := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0002",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	svc := NewService(store)
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, PIN: "1234"})
	require.NoError(t, err)

	got, err := svc.Resolve(ctx, sh.Token, "1234")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, sh.ID, got.ID)
}

// TestService_Resolve_Expired returns ErrExpired.
func TestService_Resolve_Expired(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, _ := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0003",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	svc := NewService(store)
	past := time.Now().Add(-time.Hour)
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, ExpiresAt: &past})
	require.NoError(t, err)

	_, err = svc.Resolve(ctx, sh.Token, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrExpired))
}

// TestService_Resolve_MaxDownloadsReached returns ErrExpired.
func TestService_Resolve_MaxDownloadsReached(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, _ := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0004",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	svc := NewService(store)
	max := 1
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID, MaxDownloads: &max})
	require.NoError(t, err)

	// Bump count past the cap.
	require.NoError(t, svc.IncrementDownload(ctx, sh.ID))

	_, err = svc.Resolve(ctx, sh.Token, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrExpired))
}

// TestService_Token_Uniqueness — over many creates, every token differs.
func TestService_Token_Uniqueness(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	svc := NewService(store)

	const N = 50
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		// Each call needs its own node row (FK), but the store doesn't enforce
		// distinct nodes for distinct shares — so reuse a single dummy node.
		hash := "h" + sprintf04x(i)
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: hash,
			Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
		})
		require.NoError(t, err)
		sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID})
		require.NoError(t, err)
		_, dup := seen[sh.Token]
		require.False(t, dup, "duplicate token: %s", sh.Token)
		seen[sh.Token] = struct{}{}
	}
}

// TestService_Create_MissingNodeID returns an explicit error.
func TestService_Create_MissingNodeID(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := NewService(store)
	_, err := svc.Create(context.Background(), CreateOpts{})
	require.Error(t, err)
}

// TestService_ListAndDelete round-trips a share through ListByNode +
// Delete.
func TestService_ListAndDelete(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	stg, _ := store.CreateStorage(ctx, &model.Storage{
		Name: "s", Driver: "local", MountPath: "/", SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	n, _ := store.CreateNode(ctx, &model.Node{
		StorageID: stg.ID, Name: "f.txt", Path: "/f.txt", PathHash: "h0009",
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced,
	})
	svc := NewService(store)
	sh, err := svc.Create(ctx, CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	list, err := svc.ListByNode(ctx, n.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, svc.Delete(ctx, sh.ID))
	list2, _ := svc.ListByNode(ctx, n.ID)
	assert.Empty(t, list2)
}

// sprintf04x is duplicated here on purpose — keep this test file
// self-contained without depending on internal helpers from the sqlite
// package (which lives behind a different package boundary in tests).
func sprintf04x(i int) string {
	const hex = "0123456789abcdef"
	out := []byte{
		hex[(i>>12)&0xF],
		hex[(i>>8)&0xF],
		hex[(i>>4)&0xF],
		hex[i&0xF],
	}
	// Pad to 32 chars total to match path_hash CHAR(32) constraint.
	pad := []byte("0000000000000000000000000000")
	return string(pad) + string(out)
}

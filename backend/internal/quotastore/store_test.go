package quotastore_test

// The rules from internal/quotastore's package comment, pinned one by one
// against a REAL sqlite store — not a fake. The bug this package exists to fix
// was that nothing anywhere called quota.AddUsage or Store.SetNodeOwner, so a
// mock that answers "yes, I was called" would have proved nothing; what has to
// be true is that `users.usage_bytes` and `nodes.owner_id` actually change.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// env is a store wrapped in the accounting decorator plus two users.
type env struct {
	store     db.Store
	acct      *quotastore.Store
	raw       db.Store
	storageID int64
	alice     int64
	bob       int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	acct := quotastore.New(raw)
	st, err := raw.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
	})
	require.NoError(t, err)
	return &env{
		store:     acct,
		acct:      acct,
		raw:       raw,
		storageID: st.ID,
		alice:     mkUser(t, raw, "alice@test.local"),
		bob:       mkUser(t, raw, "bob@test.local"),
	}
}

func mkUser(t *testing.T, store db.Store, email string) int64 {
	t.Helper()
	hash, err := authlocal.HashPassword("Passw0rd!123")
	require.NoError(t, err)
	u, err := store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	return u.ID
}

// asUser returns a context carrying an authenticated user, which is how every
// real request reaches the store.
func asUser(id int64) context.Context {
	return auth.WithUser(context.Background(), &model.User{ID: id})
}

func (e *env) usage(t *testing.T, userID int64) int64 {
	t.Helper()
	used, _, err := e.raw.GetUserUsage(context.Background(), userID)
	require.NoError(t, err)
	return used
}

func (e *env) owner(t *testing.T, nodeID int64) *int64 {
	t.Helper()
	o, err := e.raw.GetNodeOwner(context.Background(), nodeID)
	require.NoError(t, err)
	return o
}

func (e *env) file(t *testing.T, ctx context.Context, path string, size int64) *model.Node {
	t.Helper()
	n, err := e.store.CreateNode(ctx, &model.Node{
		StorageID: e.storageID,
		Name:      path,
		Path:      "/" + path,
		PathHash:  path,
		Type:      model.NodeTypeFile,
		Size:      size,
	})
	require.NoError(t, err)
	return n
}

// A write by a logged-in user stamps the owner and counts the bytes. On
// 6485c16 both halves were dead: usage stayed 0 and owner_id stayed NULL.
func TestWrite_CountsBytesAndStampsOwner(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "a.bin", 1000)

	assert.EqualValues(t, 1000, e.usage(t, e.alice))
	if o := e.owner(t, n.ID); assert.NotNil(t, o, "owner_id must be set — GetNodeOwner returning nil is what made the purge-time release unreachable") {
		assert.Equal(t, e.alice, *o)
	}
}

// A directory's `size` is a cached recursive total (sync.RecomputeFolderSizes),
// so counting it would bill every byte twice.
func TestWrite_DirectoriesAreNotCounted(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	d, err := e.store.CreateNode(ctx, &model.Node{
		StorageID: e.storageID, Name: "d", Path: "/d", PathHash: "d",
		Type: model.NodeTypeDirectory, Size: 4096,
	})
	require.NoError(t, err)

	assert.EqualValues(t, 0, e.usage(t, e.alice), "a folder's cached size is not storage of its own")
	require.NotNil(t, e.owner(t, d.ID), "the folder still records who made it")
}

// An unauthenticated/system write (the storage scanner) leaves the file
// unowned and uncounted: nobody uploaded it.
func TestWrite_NoActingUser_StaysUnowned(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, context.Background(), "found.bin", 5000)

	assert.Nil(t, e.owner(t, n.ID))
	assert.EqualValues(t, 0, e.usage(t, e.alice))
}

// Overwrite by the same owner moves usage by the DELTA, not by the new size.
func TestOverwrite_AppliesTheDelta(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	n := e.file(t, ctx, "a.bin", 1000)

	require.NoError(t, e.store.UpdateNodeMeta(ctx, n.ID, 1500, "", "", time.Now()))
	assert.EqualValues(t, 1500, e.usage(t, e.alice), "grew by 500, not by another 1500")

	require.NoError(t, e.store.UpdateNodeMeta(ctx, n.ID, 400, "", "", time.Now()))
	assert.EqualValues(t, 400, e.usage(t, e.alice), "shrinking an object gives the space back")
}

// The bytes belong to whoever wrote them last: an overwrite by another user
// hands the space back to the previous owner and charges the writer.
func TestOverwrite_ByAnotherUser_MovesTheBytes(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "shared.bin", 1000)
	require.EqualValues(t, 1000, e.usage(t, e.alice))

	require.NoError(t, e.store.UpdateNodeMeta(asUser(e.bob), n.ID, 3000, "", "", time.Now()))

	assert.EqualValues(t, 0, e.usage(t, e.alice), "alice is not billed for bob's bytes")
	assert.EqualValues(t, 3000, e.usage(t, e.bob))
	if o := e.owner(t, n.ID); assert.NotNil(t, o) {
		assert.Equal(t, e.bob, *o)
	}
}

// A file the scanner found (unowned, uncounted) starts counting the first time
// a user writes it — otherwise those bytes would never be attributable.
func TestOverwrite_AdoptsAnUnownedNode(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, context.Background(), "found.bin", 700)
	require.Nil(t, e.owner(t, n.ID))

	require.NoError(t, e.store.UpdateNodeMeta(asUser(e.alice), n.ID, 900, "", "", time.Now()))

	assert.EqualValues(t, 900, e.usage(t, e.alice))
	if o := e.owner(t, n.ID); assert.NotNil(t, o) {
		assert.Equal(t, e.alice, *o)
	}
}

// The scanner noticing a file changed on the backend corrects the owner's
// total but must NOT re-attribute the file to nobody.
func TestOverwrite_ScannerKeepsTheOwner(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "a.bin", 1000)

	require.NoError(t, e.store.UpdateNodeMeta(context.Background(), n.ID, 1200, "", "", time.Now()))

	assert.EqualValues(t, 1200, e.usage(t, e.alice))
	if o := e.owner(t, n.ID); assert.NotNil(t, o) {
		assert.Equal(t, e.alice, *o)
	}
}

// Trash is not a way to free space: the bytes are still on the storage, so
// they still count. Restore is therefore a no-op too.
func TestTrashAndRestore_DoNotChangeUsage(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	n := e.file(t, ctx, "a.bin", 1000)

	require.NoError(t, e.store.SoftDeleteNode(ctx, n.ID))
	assert.EqualValues(t, 1000, e.usage(t, e.alice), "trashed bytes still occupy the disk")

	require.NoError(t, e.store.RestoreNode(ctx, n.ID))
	assert.EqualValues(t, 1000, e.usage(t, e.alice), "they never stopped counting, so restore changes nothing")
}

// Moving/renaming is the same row, the same owner and the same bytes.
func TestMove_DoesNotChangeUsage(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	n := e.file(t, ctx, "a.bin", 1000)

	require.NoError(t, e.store.MoveNode(ctx, n.ID, nil, "b.bin", "/sub/b.bin", "subb"))
	assert.EqualValues(t, 1000, e.usage(t, e.alice))
}

// The purge is the ONLY release point.
func TestHardDelete_ReleasesTheBytes(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	n := e.file(t, ctx, "a.bin", 1000)
	require.NoError(t, e.store.SoftDeleteNode(ctx, n.ID))
	require.EqualValues(t, 1000, e.usage(t, e.alice))

	require.NoError(t, e.store.HardDeleteNode(ctx, n.ID))
	assert.EqualValues(t, 0, e.usage(t, e.alice))
}

// An explicit attribution beats the session — this is how the public file-drop
// bills the link's creator, and how the async copy worker bills the owner of
// the source file.
func TestWithOwner_OverridesTheSession(t *testing.T) {
	e := newEnv(t)
	ctx := quotastore.WithOwner(asUser(e.alice), e.bob)
	n := e.file(t, ctx, "dropped.bin", 2000)

	assert.EqualValues(t, 0, e.usage(t, e.alice))
	assert.EqualValues(t, 2000, e.usage(t, e.bob))
	if o := e.owner(t, n.ID); assert.NotNil(t, o) {
		assert.Equal(t, e.bob, *o)
	}
}

// The reconciler and the incremental accounting must agree, INCLUDING on
// trashed rows. Before this change RecomputeUserUsage filtered
// `deleted_at IS NULL`: a recompute forgave every trashed byte, and the
// release at purge then subtracted them a second time — clamped at zero, so
// the drift was silent.
func TestRecompute_AgreesWithTheIncrementalTotal(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)
	live := e.file(t, ctx, "live.bin", 1000)
	trashed := e.file(t, ctx, "trashed.bin", 250)
	require.NoError(t, e.store.SoftDeleteNode(ctx, trashed.ID))

	incremental := e.usage(t, e.alice)
	require.EqualValues(t, 1250, incremental)

	recomputed, err := e.acct.Quota().Recompute(ctx, e.alice)
	require.NoError(t, err)
	assert.EqualValues(t, incremental, recomputed,
		"a nightly recompute must not silently forgive trashed bytes")

	// And the purge still lands on zero rather than on a negative clamp.
	require.NoError(t, e.store.HardDeleteNode(ctx, trashed.ID))
	require.NoError(t, e.store.HardDeleteNode(ctx, live.ID))
	assert.EqualValues(t, 0, e.usage(t, e.alice))
}

// TestRawStore_CountsNothing is the regression witness for the bug itself.
//
// The raw driver store IS the tree at 6485c16: nothing there called
// quota.AddUsage or SetNodeOwner, so a user could write as much as they liked
// and the ceiling never saw a byte of it. This test asserts that old
// behaviour explicitly, right next to the tests that assert the new one, so
// the day someone hands a raw store to the handlers again the difference is
// visible in the suite rather than three months later on a full disk.
func TestRawStore_CountsNothing(t *testing.T) {
	e := newEnv(t)
	ctx := asUser(e.alice)

	n, err := e.raw.CreateNode(ctx, &model.Node{
		StorageID: e.storageID, Name: "unaccounted.bin", Path: "/unaccounted.bin",
		PathHash: "unaccounted", Type: model.NodeTypeFile, Size: 10 << 20,
	})
	require.NoError(t, err)

	assert.EqualValues(t, 0, e.usage(t, e.alice),
		"the unwrapped store is the 6485c16 behaviour: 10 MiB written, 0 counted")
	assert.Nil(t, e.owner(t, n.ID),
		"and with no owner the release at purge can never fire either")
}

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

// actor reads last_actor_id straight off the row — the decorator's claim is
// only worth anything if the column actually changed.
func (e *env) actor(t *testing.T, nodeID int64) *int64 {
	t.Helper()
	n, err := e.raw.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	return n.LastActorID
}

func (e *env) node(t *testing.T, nodeID int64) *model.Node {
	t.Helper()
	n, err := e.raw.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	return n
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
// ⚠⚠ This test used to be TestOverwrite_ByAnotherUser_MovesTheBytes and
// asserted the opposite: that bob became the owner and carried the bytes. The
// ownership model (migration 00038) changed the rule deliberately — an
// overwrite changes the BYTES, not whose file it is — because an Owner column
// whose answer changes every time a colleague edits a shared document cannot
// answer the only question it is asked.
//
// The consequence is real and is not hidden: alice is now billed for a file
// bob made bigger. Bob needs write access to alice's file to do it.
func TestOverwrite_ByAnotherUser_KeepsTheOwner_AndRecordsTheActor(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "shared.bin", 1000)
	require.EqualValues(t, 1000, e.usage(t, e.alice))

	require.NoError(t, e.store.UpdateNodeMeta(asUser(e.bob), n.ID, 3000, "", "", time.Now()))

	assert.EqualValues(t, 3000, e.usage(t, e.alice), "the owner carries their file's bytes")
	assert.EqualValues(t, 0, e.usage(t, e.bob), "writing somebody else's file does not make it yours")
	if o := e.owner(t, n.ID); assert.NotNil(t, o, "the owner must not move on an overwrite") {
		assert.Equal(t, e.alice, *o)
	}
	if a := e.actor(t, n.ID); assert.NotNil(t, a, "who wrote it has to be recorded somewhere") {
		assert.Equal(t, e.bob, *a)
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
	if a := e.actor(t, n.ID); assert.NotNil(t, a) {
		assert.Equal(t, e.alice, *a)
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

/* ── Ownership (migration 00038) ───────────────────────────────────────────
 *
 * owner_id says who PUT THE THING HERE; last_actor_id says who TOUCHED IT
 * LAST. Everything below pins one sentence of the package comment against the
 * real columns, because "the decorator calls SetNodeActor" is a claim about
 * the code and the only claim worth making is about the row.
 */

// A create stamps both: the person who made it is also the last person to have
// touched it.
func TestCreate_StampsOwnerAndActor(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "new.txt", 10)

	row := e.node(t, n.ID)
	require.NotNil(t, row.OwnerID)
	assert.Equal(t, e.alice, *row.OwnerID)
	require.NotNil(t, row.LastActorID)
	assert.Equal(t, e.alice, *row.LastActorID)
	assert.False(t, row.ExternalUpload, "an ordinary upload did not arrive from outside")
}

// A row the scanner found is SYSTEM on both counts. NULL is the answer, not a
// user invented to stand in for one.
func TestCreate_NoActingUser_IsSystemOnBothColumns(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, context.Background(), "found.bin", 700)

	row := e.node(t, n.ID)
	assert.Nil(t, row.OwnerID, "nobody put it here")
	assert.Nil(t, row.LastActorID, "nobody touched it")
}

// The public drop link: the OWNER is the person who created the link, and the
// row still says the bytes were handed in by somebody else.
func TestCreate_DropLink_OwnedByTheLinkCreator_AndMarkedExternal(t *testing.T) {
	e := newEnv(t)
	ctx := quotastore.WithExternalOrigin(quotastore.WithOwner(context.Background(), e.alice))
	n := e.file(t, ctx, "submitted.pdf", 500)

	row := e.node(t, n.ID)
	require.NotNil(t, row.OwnerID)
	assert.Equal(t, e.alice, *row.OwnerID, "the link's creator asked for the file; it is theirs")
	assert.True(t, row.ExternalUpload, "the row has to be able to say somebody else handed it in")
	assert.EqualValues(t, 500, e.usage(t, e.alice), "and it is on their quota")
	assert.Nil(t, row.LastActorID,
		"…but the link's creator did not TOUCH it — an anonymous visitor did, and that visitor is not a user")
}

// A move is the same file. Only the actor moves.
func TestMove_KeepsTheOwner_AndRecordsTheMover(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "doc.txt", 100)

	require.NoError(t, e.store.MoveNode(asUser(e.bob), n.ID, nil, "moved.txt", "/moved.txt", "moved.txt"))

	row := e.node(t, n.ID)
	require.NotNil(t, row.OwnerID)
	assert.Equal(t, e.alice, *row.OwnerID, "moving a file does not make it yours")
	require.NotNil(t, row.LastActorID)
	assert.Equal(t, e.bob, *row.LastActorID)
	assert.EqualValues(t, 100, e.usage(t, e.alice), "a move is quota-neutral")
	assert.EqualValues(t, 0, e.usage(t, e.bob))
}

// ⚠ A SYSTEM move — internal/sync repairing a row whose path drifted — is
// filex tidying its own catalogue, not a person moving a file. Blanking the
// last real actor there would destroy the only true thing the row knew.
func TestMove_BySystem_LeavesTheActorAlone(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "doc.txt", 100)

	require.NoError(t, e.store.MoveNode(context.Background(), n.ID, nil, "r.txt", "/r.txt", "r.txt"))

	if a := e.actor(t, n.ID); assert.NotNil(t, a) {
		assert.Equal(t, e.alice, *a)
	}
}

// An external change — the bucket side moved, a sync found new bytes — has
// nobody to name, and says so.
func TestOverwrite_BySystem_ClearsTheActor(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "a.bin", 1000)
	require.NotNil(t, e.actor(t, n.ID))

	require.NoError(t, e.store.UpdateNodeMeta(context.Background(), n.ID, 1200, "", "", time.Now()))

	assert.Nil(t, e.actor(t, n.ID), "a change from outside filex has no actor")
	if o := e.owner(t, n.ID); assert.NotNil(t, o, "…but it does not orphan the file") {
		assert.Equal(t, e.alice, *o)
	}
	assert.EqualValues(t, 1200, e.usage(t, e.alice))
}

// Restore is a person taking their file back out of the trash.
func TestRestore_RecordsTheActor(t *testing.T) {
	e := newEnv(t)
	n := e.file(t, asUser(e.alice), "t.txt", 10)
	require.NoError(t, e.store.SoftDeleteNode(context.Background(), n.ID))

	require.NoError(t, e.store.RestoreNode(asUser(e.bob), n.ID))

	row := e.node(t, n.ID)
	require.NotNil(t, row.OwnerID)
	assert.Equal(t, e.alice, *row.OwnerID)
	require.NotNil(t, row.LastActorID)
	assert.Equal(t, e.bob, *row.LastActorID)
}

// A copy is a NEW FILE, so the copier owns it. WithActor is how the ops worker
// says who asked, long after the request is gone.
func TestCopy_TheCopierOwnsTheCopy(t *testing.T) {
	e := newEnv(t)
	original := e.file(t, asUser(e.alice), "orig.txt", 400)

	// What ops.execute does with pending_ops.actor_id.
	ctx := quotastore.WithActor(quotastore.WithOwner(context.Background(), e.bob), e.bob)
	copyNode := e.file(t, ctx, "orig-copy.txt", 400)

	assert.Equal(t, e.alice, *e.node(t, original.ID).OwnerID, "the original is untouched")
	require.NotNil(t, e.node(t, copyNode.ID).OwnerID)
	assert.Equal(t, e.bob, *e.node(t, copyNode.ID).OwnerID)
	assert.EqualValues(t, 400, e.usage(t, e.alice))
	assert.EqualValues(t, 400, e.usage(t, e.bob), "a copy is a second set of real bytes")
}

// ActorFrom falls back to the explicit owner: a surface that named an owner and
// no actor (the drop link, an upload ticket) is acting AS that identity.
func TestActorFrom_FallsBackToTheNamedOwner(t *testing.T) {
	ctx := quotastore.WithOwner(context.Background(), 7)
	assert.EqualValues(t, 7, quotastore.ActorFrom(ctx))
	assert.EqualValues(t, 9, quotastore.ActorFrom(quotastore.WithActor(ctx, 9)))
	assert.EqualValues(t, 0, quotastore.ActorFrom(context.Background()))
	assert.EqualValues(t, 0, quotastore.ActorFrom(quotastore.WithExternalOrigin(ctx)),
		"an anonymous drop has an owner but no actor")
	assert.EqualValues(t, 7, quotastore.OwnerFrom(quotastore.WithExternalOrigin(ctx)),
		"…and the owner is untouched by that")
}

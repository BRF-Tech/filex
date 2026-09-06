package thumb_test

// The thumbnail cache reclaimer (issue #18).
//
// What the reporter saw: files deleted from S3, and the bytes still sitting in
// filex's own `thumbs` directory afterwards. The cause was that nothing in the
// tree ever removed a cached thumbnail — grep for a delete and there was none.
// The `thumbnails` ROW went away with its node through the FK cascade, so the
// catalogue looked clean while <data>/thumbs only ever grew.
//
// These tests measure the two properties that make the fix safe rather than
// merely effective:
//
//   - it reclaims what is genuinely dead (node purged, storage removed, an
//     orphan already on disk from before the fix shipped);
//   - it cannot reclaim anything that still has a referent — and specifically
//     not a TRASHED file, which is restorable and whose thumbnail must be there
//     the instant it comes back.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// aged writes a cache file and backdates it past the reaper's grace window, so
// the test measures the decision and not the clock.
func aged(t *testing.T, dir, name string, size int) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, make([]byte, size), 0o644))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(p, old, old))
	return p
}

func newNode(t *testing.T, store db.Store, storageID int64, name string) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: storageID,
		Name:      name,
		Path:      "/" + name,
		PathHash:  pathkey.Hash(storageID, "/"+name),
		Type:      model.NodeTypeFile,
		Size:      10,
	})
	require.NoError(t, err)
	return n
}

func reapFixture(t *testing.T) (db.Store, *thumb.Pipeline, string, int64) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true,
		ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	dir := t.TempDir()
	return store, thumb.New(store, dir, thumb.Capabilities{Image: true}), dir, st.ID
}

// The headline: an orphan goes, a live file's thumbnail stays.
func TestReapOrphans_RemovesCacheWithNoNodeAndKeepsTheRest(t *testing.T) {
	ctx := context.Background()
	store, p, dir, sid := reapFixture(t)

	live := newNode(t, store, sid, "live.png")
	livePath := aged(t, dir, strconv.FormatInt(live.ID, 10)+".jpg", 100)
	// An id that never had a row — what an install upgraded from v0.33.0 is
	// full of, and what removing a whole storage leaves behind.
	orphanPath := aged(t, dir, "999999.jpg", 250)

	res, err := p.ReapOrphans(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Removed)
	require.Equal(t, 1, res.Kept)
	require.EqualValues(t, 250, res.Freed)

	require.FileExists(t, livePath)
	require.NoFileExists(t, orphanPath)
}

// ⚠ The one that matters most. A trashed node still HAS a row; the file is
// restorable and its thumbnail has to survive, or a restore comes back as a
// blank tile and the "cleanup" has destroyed something the user still owns.
func TestReapOrphans_KeepsTheThumbnailOfATrashedFile(t *testing.T) {
	ctx := context.Background()
	store, p, dir, sid := reapFixture(t)

	n := newNode(t, store, sid, "trashed.png")
	path := aged(t, dir, strconv.FormatInt(n.ID, 10)+".jpg", 120)
	require.NoError(t, store.SoftDeleteNode(ctx, n.ID))

	res, err := p.ReapOrphans(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, res.Removed)
	require.Equal(t, 1, res.Kept)
	require.FileExists(t, path)
}

// Anything that is not `<digits>.jpg` is somebody else's file.
func TestReapOrphans_TouchesNothingItDoesNotOwn(t *testing.T) {
	_, p, dir, _ := reapFixture(t)

	keep := []string{"README.txt", "notes.jpg", "12ab.jpg", "7.png", ".hidden"}
	for _, name := range keep {
		aged(t, dir, name, 30)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "42.jpg.d"), 0o755))

	res, err := p.ReapOrphans(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, res.Removed)
	require.Equal(t, 0, res.Scanned)
	for _, name := range keep {
		require.FileExists(t, filepath.Join(dir, name))
	}
}

// A thumbnail written moments ago is never judged: generation writes the JPEG
// before it upserts the row, and a sweep that lands in that window would delete
// the file it is about to record.
func TestReapOrphans_SkipsFilesInsideTheGraceWindow(t *testing.T) {
	_, p, dir, _ := reapFixture(t)

	fresh := filepath.Join(dir, "888888.jpg")
	require.NoError(t, os.WriteFile(fresh, make([]byte, 50), 0o644))

	res, err := p.ReapOrphans(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, res.Removed)
	require.Equal(t, 1, res.Skipped)
	require.FileExists(t, fresh)
}

// ⚠⚠ The interlock. A database that cannot answer must abandon the pass, not
// treat silence as "the node is gone" — that is exactly how a sweep turns into
// data loss.
func TestReapOrphans_ADatabaseErrorDeletesNothing(t *testing.T) {
	store, _, dir, sid := reapFixture(t)
	n := newNode(t, store, sid, "live.png")
	livePath := aged(t, dir, strconv.FormatInt(n.ID, 10)+".jpg", 100)
	orphanPath := aged(t, dir, "999999.jpg", 100)

	p := thumb.New(brokenLookup{Store: store}, dir, thumb.Capabilities{Image: true})
	res, err := p.ReapOrphans(context.Background())
	require.Error(t, err)
	require.Equal(t, 0, res.Removed)
	require.FileExists(t, livePath)
	require.FileExists(t, orphanPath)
}

// Forget is the fast path: the space comes back when the user purges the file,
// not at the next sweep.
func TestForget_RemovesTheCachedFileAndTheRow(t *testing.T) {
	ctx := context.Background()
	store, p, dir, sid := reapFixture(t)

	n := newNode(t, store, sid, "gone.png")
	path := aged(t, dir, strconv.FormatInt(n.ID, 10)+".jpg", 64)
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: "ready"}))

	p.Forget(ctx, n.ID)

	require.NoFileExists(t, path)
	_, err := store.GetThumbnail(ctx, n.ID)
	require.Error(t, err)
}

// Forgetting a node that never had a thumbnail is a no-op, not an error: it is
// called on every purge and must never fail the deletion it hangs off.
func TestForget_IsSilentWhenThereIsNothingCached(t *testing.T) {
	_, p, _, _ := reapFixture(t)
	p.Forget(context.Background(), 4242)
}

// brokenLookup answers every ExistingNodeIDs with a failure while behaving
// normally otherwise — the "database blip" the interlock is written for.
type brokenLookup struct{ db.Store }

func (brokenLookup) ExistingNodeIDs(context.Context, []int64) (map[int64]bool, error) {
	return nil, errors.New("database is locked")
}

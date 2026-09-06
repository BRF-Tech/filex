package sync_test

// A file replaced UNDERNEATH filex, on a storage whose driver reports no etag.
//
// The storage sync exists to discover changes filex did not make. On S3 that
// works: the walk compares the etag the bucket reports against the one on the
// row. The local filesystem driver reports no etag at all, and neither do the
// FTP, SFTP and SMB drivers — so `etagDrift("", "")` was false on every pass,
// forever, and a file replaced under a local storage looked unchanged:
//
//   - its size stayed stale in the catalogue,
//   - its extracted content stayed stale in the search index,
//   - and it was never re-scanned for viruses, so a clean file could be
//     replaced with an infected one and nothing would ever look at it again.
//
// These tests are the red proof. They fail on the code as it was.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"

	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
)

// touchAt stamps a file with an explicit modification time, which is how a
// test says "this was written a moment ago" without sleeping for it.
func touchAt(t *testing.T, p string, mt time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(p, mt, mt))
}

// ---------------------------------------------------------------------------
// 1. the gap itself
// ---------------------------------------------------------------------------

// Someone replaces a file on the mounted disk. The walk must notice.
func TestSyncNoticesAFileReplacedUnderALocalStorage(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	p := filepath.Join(root, "rapor.txt")
	require.NoError(t, os.WriteFile(p, []byte("ilk surum"), 0o644))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)
	syncWithAV(t, store, st, job, qd)
	require.Equal(t, 1, drainScans(t, qd, job), "precondition: the first import scans it once")

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/rapor.txt"))
	require.NoError(t, err)
	require.NotNil(t, n)
	require.EqualValues(t, len("ilk surum"), n.Size)

	// Replaced out of band, with something longer and a later mtime — an
	// ordinary edit by any other process on the machine.
	waitPastSecondBoundary()
	require.NoError(t, os.WriteFile(p, []byte("ikinci surum, epeyce daha uzun"), 0o644))

	syncWithAV(t, store, st, job, qd)

	assert.Equal(t, []int64{n.ID}, pendingScans(t, qd),
		"the bytes on a local storage were replaced and nothing ever looked at them again")

	fresh, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.EqualValues(t, len("ikinci surum, epeyce daha uzun"), fresh.Size,
		"the catalogue kept the stale size of a file that had been replaced")
}

// The size alone is not what carries the detection: a same-size edit is the
// case a size-only check would miss, and it is the ordinary one (a spreadsheet
// re-saved, a config line changed for another of the same length).
func TestSyncNoticesASameSizeEditUnderALocalStorage(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	p := filepath.Join(root, "ayar.conf")
	require.NoError(t, os.WriteFile(p, []byte("mode = safe  "), 0o644))

	job := avJob(store, st, drv, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)
	require.Equal(t, 1, drainScans(t, qd, job))

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/ayar.conf"))
	require.NoError(t, err)
	require.NotNil(t, n)

	waitPastSecondBoundary()
	require.NoError(t, os.WriteFile(p, []byte("mode = unsafe"), 0o644)) // same length
	syncWithAV(t, store, st, job, qd)

	assert.Equal(t, []int64{n.ID}, pendingScans(t, qd),
		"a same-size rewrite on a local storage was never re-read")
}

// A file restored from a backup keeps an OLDER modification time than the row
// records. "Newer than what we saw" would miss it; the comparison has to be
// inequality, not ordering.
func TestSyncNoticesAFileWhoseMtimeWentBackwards(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	p := filepath.Join(root, "yedek.txt")
	require.NoError(t, os.WriteFile(p, []byte("bugunku surum"), 0o644))

	job := avJob(store, st, drv, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)
	require.Equal(t, 1, drainScans(t, qd, job))

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/yedek.txt"))
	require.NoError(t, err)
	require.NotNil(t, n)

	waitPastSecondBoundary()
	require.NoError(t, os.WriteFile(p, []byte("gecen haftaki"), 0o644))
	touchAt(t, p, time.Now().Add(-72*time.Hour))
	syncWithAV(t, store, st, job, qd)

	assert.Equal(t, []int64{n.ID}, pendingScans(t, qd),
		"a restore from backup moved the mtime backwards and the walk read it as unchanged")
}

// ---------------------------------------------------------------------------
// 2. the way this is most likely to go wrong: a re-scan storm
// ---------------------------------------------------------------------------

// Getting drift detection wrong in the other direction means every pass thinks
// every file changed. On an instance with antivirus enabled that is one scan
// per file per pass, forever — 96 full scans of the storage a day on the
// 15-minute default.
//
// Three consecutive passes over an unchanged local storage: zero drift.
func TestUnchangedLocalStorageDriftsOnNoPass(t *testing.T) {
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "belgeler"), 0o755))
	for i := 0; i < 20; i++ {
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "belgeler", fmt.Sprintf("dosya-%02d.txt", i)),
			[]byte(fmt.Sprintf("icerik %02d", i)), 0o644))
	}
	// A file with a sub-second modification time, which is what every write on
	// a modern filesystem has. If the comparison keeps a precision the DB does
	// not, this one alone drifts on every pass.
	sub := filepath.Join(root, "belgeler", "hassas.txt")
	require.NoError(t, os.WriteFile(sub, []byte("altsaniye"), 0o644))
	touchAt(t, sub, time.Unix(time.Now().Unix(), 523_456_789))

	sc := &cleanScanner{}
	job := avJob(store, st, drv, sc)

	syncWithAV(t, store, st, job, qd)
	require.Len(t, pendingScans(t, qd), 21, "pass 1 scans every newly catalogued file")
	require.Equal(t, 21, drainScans(t, qd, job))

	for pass := 2; pass <= 4; pass++ {
		waitPastSecondBoundary()
		run := syncWithAV(t, store, st, job, qd)
		assert.Emptyf(t, pendingScans(t, qd),
			"pass %d re-scanned an unchanged local storage: every sync interval would re-scan everything", pass)
		assert.Zerof(t, run.Updated,
			"pass %d reported drift on an unchanged local storage", pass)
	}
	assert.Equal(t, 21, sc.scanned, "the bytes must have been read exactly once each")
}

// The same blind spot, on the other drivers that report no etag. The plugin
// fixture stands in for them: it is the same `storage.Object` with an empty
// Etag that FTP, SFTP and SMB produce.
func TestEtaglessDriverDriftIsNoticedAndDoesNotStorm(t *testing.T) {
	ctx := context.Background()
	conn, store := dbtest.NewTestDB(t)
	st, drv, root := localStorage(t, store)
	qd := avQueue(t, conn)

	p := filepath.Join(root, "veri.bin")
	require.NoError(t, os.WriteFile(p, []byte("aaaa"), 0o644))

	job := avJob(store, st, drv, &cleanScanner{})
	syncWithAV(t, store, st, job, qd)
	require.Equal(t, 1, drainScans(t, qd, job))
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/veri.bin"))
	require.NoError(t, err)
	require.NotNil(t, n)

	// Two edits in a row, each followed by two quiet passes.
	for i, body := range []string{"bbbbbb", "cc"} {
		waitPastSecondBoundary()
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
		syncWithAV(t, store, st, job, qd)
		require.Equalf(t, []int64{n.ID}, pendingScans(t, qd), "edit %d was not noticed", i+1)
		require.Equal(t, 1, drainScans(t, qd, job))

		waitPastSecondBoundary()
		syncWithAV(t, store, st, job, qd)
		assert.Emptyf(t, pendingScans(t, qd), "the pass after edit %d drifted again", i+1)
	}

	fresh, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, fresh.Size)
}

package sync

// Issue #70: a directory's catalogue writes go in ONE transaction
// (applyListing), and nothing outside the database hears of its rows before it
// commits. These pin where the transaction starts and ends, what happens when
// it fails, and that the search index and the antivirus are told afterwards.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
)

// txWatch records every catalogue write and whether it ran inside a
// transaction on the lab's database. commitRefusals > 0 makes that many
// transactions fail after fn succeeded — a refused commit: rolled back.
type txWatch struct {
	db.Store
	pool *sql.DB

	mu             gosync.Mutex
	txs            int
	inside         map[string]int
	outside        map[string]int
	commitRefusals int
}

// newTxLab is a lazy lab whose catalogue writes through a txWatch.
func newTxLab(t *testing.T) (*lazyLab, *txWatch) {
	t.Helper()
	var w *txWatch
	l := newLazyLabOn(t, nil, func(s db.Store, pool *sql.DB) db.Store {
		w = &txWatch{Store: s, pool: pool, inside: map[string]int{}, outside: map[string]int{}}
		return w
	})
	return l, w
}

func (w *txWatch) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	w.mu.Lock()
	w.txs++
	refuse := w.commitRefusals > 0
	if refuse {
		w.commitRefusals--
	}
	w.mu.Unlock()
	return w.Store.WithTx(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		if refuse {
			return errors.New("commit refused")
		}
		return nil
	})
}

func (w *txWatch) note(ctx context.Context, what string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if db.InTx(ctx, w.pool) {
		w.inside[what]++
	} else {
		w.outside[what]++
	}
}

func (w *txWatch) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.txs = 0
	w.inside, w.outside = map[string]int{}, map[string]int{}
}

func (w *txWatch) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	w.note(ctx, "CreateNode")
	return w.Store.CreateNode(ctx, n)
}

func (w *txWatch) TouchNodeSeen(ctx context.Context, id int64) error {
	w.note(ctx, "TouchNodeSeen")
	return w.Store.TouchNodeSeen(ctx, id)
}

func (w *txWatch) UpdateNodeMeta(ctx context.Context, id int64, size int64, mime, etag string, mtime time.Time) error {
	w.note(ctx, "UpdateNodeMeta")
	return w.Store.UpdateNodeMeta(ctx, id, size, mime, etag, mtime)
}

func (w *txWatch) SoftDeleteNode(ctx context.Context, id int64) error {
	w.note(ctx, "SoftDeleteNode")
	return w.Store.SoftDeleteNode(ctx, id)
}

func (w *txWatch) DiscoverCatalogueFolders(ctx context.Context, storageID int64, folders []model.CatalogueFolder) error {
	w.note(ctx, "DiscoverCatalogueFolders")
	return w.Store.DiscoverCatalogueFolders(ctx, storageID, folders)
}

func (w *txWatch) RecordCatalogueFolder(ctx context.Context, f *model.CatalogueFolder) error {
	w.note(ctx, "RecordCatalogueFolder")
	return w.Store.RecordCatalogueFolder(ctx, f)
}

// A folder reconcile writes its entries, the folders they reveal, its own
// state row and its delete pass in ONE transaction.
//
// Break: in applyListing, call apply(ctx, false) instead of going through
// Store.WithTx — every write lands outside a transaction. Or run finish after
// the transaction — the state row and the delete pass land outside it.
func TestCatalogueTx_AFolderIsWrittenInOneTransaction(t *testing.T) {
	l, w := newTxLab(t)
	for i := 0; i < 5; i++ {
		l.write(t, fmt.Sprintf("klasör/belge-%d.txt", i), "x")
	}
	l.mkdir(t, "klasör/alt")
	l.reconcile(t, "/")
	l.seed(t, "/klasör/silinen.txt", model.NodeTypeFile) // catalogued once, gone from the disk now
	w.reset()

	res := l.reconcile(t, "/klasör")
	assert.Equal(t, 6, res.Added, "five files and a subfolder")
	assert.Equal(t, 1, res.Removed, "the row whose file is gone")
	assert.Equal(t, 1, w.txs, "one transaction for the folder")
	assert.Empty(t, w.outside, "no catalogue write outside it")
	for _, what := range []string{"CreateNode", "DiscoverCatalogueFolders", "RecordCatalogueFolder", "SoftDeleteNode"} {
		assert.Positive(t, w.inside[what], "%s ran inside the folder's transaction", what)
	}
	assert.Nil(t, l.row("/klasör/silinen.txt"))
	assert.True(t, l.folder(t, "/klasör").Catalogued())

	// Reconciled again after a file changed on the disk: the drift and the
	// "seen" stamps are the same one transaction.
	l.write(t, "klasör/belge-0.txt", "daha uzun bir içerik")
	w.reset()
	res = l.reconcile(t, "/klasör")
	assert.Equal(t, 1, res.Updated)
	assert.Equal(t, 1, w.txs)
	assert.Empty(t, w.outside)
	assert.Positive(t, w.inside["UpdateNodeMeta"])
	assert.Positive(t, w.inside["TouchNodeSeen"])
}

// The full scan walks the same way: one transaction per directory, every entry
// written inside the one of the directory that holds it.
//
// Break: as above, apply(ctx, false) in applyListing.
func TestCatalogueTx_AFullScanWritesEachDirectoryInOneTransaction(t *testing.T) {
	l, w := newTxLab(t)
	l.write(t, "a.txt", "a")
	l.write(t, "k/b.txt", "b")
	l.write(t, "k/alt/c.txt", "c")
	l.write(t, "k/alt/d.txt", "d")

	require.NoError(t, l.lc.s.RunOnce(l.ctx))
	assert.Equal(t, 3, w.txs, "/, /k and /k/alt: a transaction each")
	assert.Equal(t, 6, w.inside["CreateNode"], "every row created inside its directory's transaction")
	assert.Zero(t, w.outside["CreateNode"])
	for _, p := range []string{"/a.txt", "/k", "/k/b.txt", "/k/alt", "/k/alt/c.txt", "/k/alt/d.txt"} {
		assert.NotNil(t, l.row(p), p)
	}
}

// A directory bigger than one transaction's share is written in several, and
// the folder's state row (written in the last) still counts every entry.
//
// Break: hand finish only the last transaction's entries (drop `listed` from
// its append) — the folder records 1 entry instead of 7.
func TestCatalogueTx_ABigFolderIsWrittenInSeveralTransactions(t *testing.T) {
	was := catalogueTxEntries
	catalogueTxEntries = 3
	defer func() { catalogueTxEntries = was }()
	l, w := newTxLab(t)
	for i := 0; i < 7; i++ {
		l.write(t, fmt.Sprintf("çok/dosya-%d.txt", i), "x")
	}
	l.reconcile(t, "/")
	w.reset()

	res := l.reconcile(t, "/çok")
	assert.Equal(t, 3, w.txs, "7 entries, 3 a transaction")
	assert.Empty(t, w.outside)
	assert.Equal(t, 7, res.Added)
	assert.Equal(t, 7, res.Entries)
	assert.Equal(t, 7, l.folder(t, "/çok").Entries)
	assert.Zero(t, res.Removed)
}

// The search index and the antivirus hear of the new rows only after the
// transaction committed. Both enqueue jobs through the job queue, which shares
// the database but not the transaction: on SQLite, inside it, they would wait
// for ever for the one connection it holds.
//
// Break: call s.handOff(ctx, b) inside apply, before it returns — the hook
// and the scan see the transaction in their context.
func TestCatalogueTx_IndexAndAntivirusHearOfRowsOnlyAfterTheCommit(t *testing.T) {
	l := newLazyLab(t, nil)
	idx, err := search.Open(filepath.Join(t.TempDir(), "index"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	var (
		mu               gosync.Mutex
		hooked, scanned  int
		hookInTx, scanTx int
	)
	idx.SetContentHook(func(ctx context.Context, n *model.Node) {
		mu.Lock()
		defer mu.Unlock()
		hooked++
		if db.InTx(ctx, l.pool) {
			hookInTx++
		}
	})
	l.lc.s.index = idx
	l.lc.s.avScan = func(ctx context.Context, n *model.Node) {
		mu.Lock()
		defer mu.Unlock()
		scanned++
		if db.InTx(ctx, l.pool) {
			scanTx++
		}
	}
	for i := 0; i < 4; i++ {
		l.write(t, fmt.Sprintf("yeni/dosya-%d.txt", i), "içerik")
	}
	l.reconcile(t, "/")
	l.reconcile(t, "/yeni")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 4, hooked, "the content hook fired for each new file")
	assert.Equal(t, 4, scanned, "the antivirus was handed each new file")
	assert.Zero(t, hookInTx, "never from inside the transaction")
	assert.Zero(t, scanTx, "never from inside the transaction")
	assert.EqualValues(t, 5, idx.Stats().DocCount, "the folder and its four files, indexed in one batch")
}

// A transaction that fails — here its commit is refused — is rolled back and
// its entries are applied again one statement at a time: nothing is lost, and
// the rows are handed to the index and the antivirus once, not twice.
//
// Break: return the transaction's error instead of applying again — the
// reconcile fails and the folder has no rows. Or hand off the rolled-back
// attempt's rows too — every file is scanned twice.
func TestCatalogueTx_AFailedTransactionIsAppliedAgainOneByOne(t *testing.T) {
	l, w := newTxLab(t)
	scanned := 0
	l.lc.s.avScan = func(context.Context, *model.Node) { scanned++ }
	for i := 0; i < 3; i++ {
		l.write(t, fmt.Sprintf("geri/dosya-%d.txt", i), "x")
	}
	l.reconcile(t, "/")
	w.commitRefusals = 1
	scanned = 0

	res := l.reconcile(t, "/geri")
	assert.Equal(t, 3, res.Added)
	for i := 0; i < 3; i++ {
		assert.NotNil(t, l.row(fmt.Sprintf("/geri/dosya-%d.txt", i)))
	}
	assert.True(t, l.folder(t, "/geri").Catalogued())
	assert.Equal(t, 3, scanned, "handed over once each")
}

// Package ops manages the async queue of long-running file operations
// (copy/move/delete) so the HTTP request can return promptly while the
// actual byte movement happens in a worker goroutine.
//
// Persistence is in the pending_ops table — restart-safe so a server crash
// doesn't lose in-flight work. The single goroutine in Run() polls every
// few seconds, picks the oldest queued row, and executes it via the
// configured storage driver.
package ops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Op kinds.
const (
	OpCopy   = "copy"
	OpMove   = "move"
	OpDelete = "delete"
)

// Status values.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusFailed  = "failed"
	StatusPartial = "partial"
)

// Op is a queued file operation row.
type Op struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	StorageID  int64      `json:"storage_id"`
	Sources    []string   `json:"sources"`
	Dest       string     `json:"dest,omitempty"`
	Total      int        `json:"total"`
	Done       int        `json:"done"`
	Failed     int        `json:"failed"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Service is the queue + worker bundle.
type Service struct {
	db              *sql.DB
	storageResolver func(int64) (storage.Driver, error)
	dbsync          DBSync

	wakeup chan struct{}
	stopMu sync.Mutex
	stop   chan struct{}
	stopWg sync.WaitGroup
}

// DBSync mirrors a completed filesystem operation into the DB node index.
// Implemented by the manager HTTP handler and injected via SetSync once both
// are constructed.
//
// Without it the worker moves/deletes bytes on disk but leaves the DB cache
// stale. Directory listings read the DB (Store.ListNodesByParent), so a move
// would keep showing the file in its old folder and a delete would keep
// showing the file at all — the exact "move/delete doesn't work" bug. It also
// lets delete go through the trash (soft-delete) instead of hard-deleting.
type DBSync interface {
	// SyncMove updates the moved node's path/parent in the DB.
	SyncMove(ctx context.Context, storageID int64, src, dst string)
	// SyncSoftDelete flags the node deleted and retags it to the trash path
	// (storage_key keeps the original path so Restore works).
	SyncSoftDelete(ctx context.Context, storageID int64, src, trashRel string)
	// SyncHardDelete flags the node deleted when the driver could not move it
	// to trash and had to delete the bytes outright.
	SyncHardDelete(ctx context.Context, storageID int64, src string)
	// SyncCopy inserts a DB node for the freshly written copy.
	SyncCopy(ctx context.Context, storageID int64, src, dst string)
}

// SetSync wires the DB-sync hook. Call once at boot, before Run.
func (s *Service) SetSync(d DBSync) { s.dbsync = d }

// TrashPrefix is the in-storage dir soft-deleted files are moved into. It must
// match the manager handler's const so listings hide it and trash.Service can
// enumerate/restore. (A single shared const would be ideal, but the handler
// package imports ops — not the other way round — so we mirror the literal.)
const TrashPrefix = ".filex-trash"

func randHex6() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// trashRelFor builds `.filex-trash/<unix>-<rand>__<basename>` for `src`,
// matching the sync manager handler's trash-key format exactly.
func trashRelFor(src string) string {
	s := strings.TrimRight(src, "/")
	base := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		base = s[i+1:]
	}
	return fmt.Sprintf("%s/%d-%s__%s", TrashPrefix, time.Now().Unix(), randHex6(), base)
}

// New returns a Service that talks to the given *sql.DB.
//
// Callers must invoke Migrate before Submit/Status to ensure the
// pending_ops table exists. Run starts the worker goroutine.
func New(database *sql.DB, resolver func(int64) (storage.Driver, error)) *Service {
	return &Service{
		db:              database,
		storageResolver: resolver,
		wakeup:          make(chan struct{}, 1),
		stop:            make(chan struct{}),
	}
}

// Migrate ensures the pending_ops table exists. Idempotent.
//
// We don't drive this through goose because it's an internal queue table —
// the migration is tiny and would be the only one in the package.
func (s *Service) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS pending_ops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			storage_id INTEGER NOT NULL,
			sources_json TEXT NOT NULL,
			dest TEXT,
			total INTEGER NOT NULL DEFAULT 0,
			done INTEGER NOT NULL DEFAULT 0,
			failed INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			error TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		)`)
	if err != nil {
		return fmt.Errorf("ops: create table: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status, created_at)`)
	// On boot, any row left in `running` is from a previous crash — re-queue.
	if _, err := s.db.ExecContext(ctx, `UPDATE pending_ops SET status='pending', started_at=NULL WHERE status='running'`); err != nil {
		slog.Warn("ops: requeue stale running rows", slog.String("err", err.Error()))
	}
	return nil
}

// Submit enqueues a new op and pokes the worker.
func (s *Service) Submit(ctx context.Context, kind string, storageID int64, sources []string, dest string) (*Op, error) {
	switch kind {
	case OpCopy, OpMove, OpDelete:
	default:
		return nil, fmt.Errorf("ops: unknown kind %q", kind)
	}
	if storageID == 0 {
		return nil, errors.New("ops: missing storage_id")
	}
	if len(sources) == 0 {
		return nil, errors.New("ops: no sources")
	}
	if (kind == OpCopy || kind == OpMove) && dest == "" {
		return nil, errors.New("ops: dest required")
	}
	srcJSON, _ := json.Marshal(sources)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO pending_ops (kind, storage_id, sources_json, dest, total, status) VALUES (?,?,?,?,?,?)`,
		kind, storageID, string(srcJSON), dest, len(sources), StatusPending)
	if err != nil {
		return nil, fmt.Errorf("ops: insert: %w", err)
	}
	id, _ := res.LastInsertId()
	s.poke()
	return s.Get(ctx, id)
}

// Get returns the current state of an op.
func (s *Service) Get(ctx context.Context, id int64) (*Op, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, storage_id, sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at
		 FROM pending_ops WHERE id=?`, id)
	return scanOp(row)
}

// List returns ops, optionally filtered by status (e.g. "running").
// Empty status returns the most-recent rows across all statuses, capped
// at 200 to keep the polling payload small. Used by the SPA's
// PendingOpsTray which calls GET /api/files/ops?status=running every 2s.
func (s *Service) List(ctx context.Context, status string) ([]*Op, error) {
	const cols = `id, kind, storage_id, sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at`
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM pending_ops WHERE status=? ORDER BY id DESC LIMIT 200`, status)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+` FROM pending_ops ORDER BY id DESC LIMIT 200`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Op, 0, 16)
	for rows.Next() {
		op := &Op{}
		var srcJSON string
		if err := rows.Scan(&op.ID, &op.Kind, &op.StorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
		out = append(out, op)
	}
	return out, rows.Err()
}

// Run blocks until ctx is cancelled, draining the queue continuously.
//
// Spawn it in its own goroutine — typically once at server boot.
func (s *Service) Run(ctx context.Context) {
	s.stopMu.Lock()
	select {
	case <-s.stop:
		s.stop = make(chan struct{})
	default:
	}
	stop := s.stop
	s.stopWg.Add(1)
	s.stopMu.Unlock()
	defer s.stopWg.Done()

	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		s.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-s.wakeup:
		case <-t.C:
		}
	}
}

// Stop signals the worker to exit and waits for it.
func (s *Service) Stop() {
	s.stopMu.Lock()
	stop := s.stop
	s.stopMu.Unlock()
	if stop == nil {
		return
	}
	select {
	case <-stop:
		// already stopped
	default:
		close(stop)
	}
	s.stopWg.Wait()
}

func (s *Service) poke() {
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}

// drain pops queued rows one at a time and executes them.
func (s *Service) drain(ctx context.Context) {
	for {
		op, ok, err := s.claimNext(ctx)
		if err != nil {
			slog.Warn("ops: claim next", slog.String("err", err.Error()))
			return
		}
		if !ok {
			return
		}
		s.execute(ctx, op)
	}
}

// claimNext atomically picks the oldest pending row and marks it running.
//
// We do an UPDATE-then-SELECT rather than SELECT FOR UPDATE because SQLite
// doesn't support row-level locks. The single-writer constraint of SQLite
// makes this race-free for the common case (one filex node) — for
// MySQL/Postgres in HA setups this would need a SELECT FOR UPDATE SKIP
// LOCKED.
func (s *Service) claimNext(ctx context.Context) (*Op, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var id int64
	row := tx.QueryRowContext(ctx, `SELECT id FROM pending_ops WHERE status='pending' ORDER BY id ASC LIMIT 1`)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pending_ops SET status=?, started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'`, StatusRunning, id); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	op, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return op, true, nil
}

// execute runs a single Op against the storage driver and persists progress.
func (s *Service) execute(ctx context.Context, op *Op) {
	if s.storageResolver == nil {
		s.fail(ctx, op, "no storage resolver")
		return
	}
	drv, err := s.storageResolver(op.StorageID)
	if err != nil {
		s.fail(ctx, op, "storage: "+err.Error())
		return
	}

	var lastErr error
	for _, src := range op.Sources {
		if ctx.Err() != nil {
			break
		}
		if err := s.runOne(ctx, drv, op, src); err != nil {
			op.Failed++
			lastErr = err
			slog.Warn("ops: step failed",
				slog.Int64("op", op.ID),
				slog.String("kind", op.Kind),
				slog.String("src", src),
				slog.String("err", err.Error()))
		} else {
			op.Done++
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE pending_ops SET done=?, failed=? WHERE id=?`, op.Done, op.Failed, op.ID)
	}

	status := StatusOK
	errMsg := ""
	switch {
	case op.Failed == 0:
		status = StatusOK
	case op.Done == 0:
		status = StatusFailed
		errMsg = errMessage(lastErr)
	default:
		status = StatusPartial
		errMsg = errMessage(lastErr)
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE pending_ops SET status=?, error=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, errMsg, op.ID)
}

func (s *Service) runOne(ctx context.Context, drv storage.Driver, op *Op, src string) error {
	switch op.Kind {
	case OpDelete:
		// Soft-delete: rename the file into `.filex-trash/` and flag the DB
		// row so it's restorable AND leaves the listing. The async worker
		// used to hard-delete and never touch the DB, which left the file
		// both un-trashable and still visible (the listing reads the DB).
		if mover, ok := drv.(storage.Mover); ok {
			trashRel := trashRelFor(src)
			if err := mover.Move(ctx, src, trashRel); err != nil {
				if !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				// Source object already gone (stale index / out-of-band delete):
				// drop the cache row outright instead of trashing a phantom, so a
				// batch delete never fails on already-missing items.
				if s.dbsync != nil {
					s.dbsync.SyncHardDelete(ctx, op.StorageID, src)
				}
				return nil
			}
			if s.dbsync != nil {
				s.dbsync.SyncSoftDelete(ctx, op.StorageID, src, trashRel)
			}
			return nil
		}
		// Driver can't move — hard delete and just flag the DB row deleted.
		d, ok := drv.(storage.Deleter)
		if !ok {
			return errors.New("driver not deletable")
		}
		if err := d.Delete(ctx, src); err != nil {
			return err
		}
		if s.dbsync != nil {
			s.dbsync.SyncHardDelete(ctx, op.StorageID, src)
		}
		return nil
	case OpMove:
		m, ok := drv.(storage.Mover)
		if !ok {
			return errors.New("driver not movable")
		}
		dst := joinIntoDir(op.Dest, src)
		if err := m.Move(ctx, src, dst); err != nil {
			if !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			// Source already gone: the move can't happen, but the stale cache
			// row should not linger — drop it and treat the step as done rather
			// than failing a batch on a phantom.
			if s.dbsync != nil {
				s.dbsync.SyncHardDelete(ctx, op.StorageID, src)
			}
			return nil
		}
		if s.dbsync != nil {
			s.dbsync.SyncMove(ctx, op.StorageID, src, dst)
		}
		return nil
	case OpCopy:
		c, ok := drv.(storage.Copier)
		if !ok {
			return errors.New("driver not copyable")
		}
		dst := uniqueCopyDest(ctx, drv, src, joinIntoDir(op.Dest, src))
		if err := c.Copy(ctx, src, dst); err != nil {
			return err
		}
		if s.dbsync != nil {
			s.dbsync.SyncCopy(ctx, op.StorageID, src, dst)
		}
		return nil
	}
	return fmt.Errorf("unknown kind: %s", op.Kind)
}

// uniqueCopyDest resolves a non-colliding destination for a copy op.
//
// Two cases force a rename:
//
//  1. Self-copy: `dst == src`. Hetzner / S3 / most object stores reject
//     a CopyObject where the source and destination keys match (it
//     would be a no-op metadata-only edit and AWS rejects it as
//     `InvalidRequest: trying to copy an object to itself ...`). The
//     most common trigger is a "Duplicate" / "Make a copy" UI gesture
//     that drops the duplicate into the source's own directory.
//
//  2. Destination already exists: a paste into a directory that
//     already contains a file with that basename should not silently
//     overwrite — Finder/Nautilus/Explorer all auto-suffix instead.
//
// We probe with `Stat` and fall back to `<base>-copy<ext>`,
// `<base>-copy-2<ext>`, … up to a small bounded number of attempts so a
// pathological directory full of `-copy-N` siblings doesn't loop
// forever. If we somehow can't find a free name, we return the last
// candidate and let the underlying driver decide what to do.
//
// (sweep-2026-05-09 bug 25 — "Kopyasını Oluştur" was sending source ==
// destination and the S3 driver was 400ing the self-copy.)
func uniqueCopyDest(ctx context.Context, drv storage.Driver, src, dst string) string {
	if dst != src && !pathExists(ctx, drv, dst) {
		return dst
	}
	// Split base + ext for `<base>-copy<ext>` pattern. We rename the
	// *destination* basename rather than the parent dir, so a paste of
	// `users.csv` into `example/` becomes `example/users-copy.csv`,
	// not `example-copy/users.csv`.
	dir := ""
	base := dst
	if idx := strings.LastIndex(dst, "/"); idx >= 0 {
		dir = dst[:idx+1] // keep trailing slash
		base = dst[idx+1:]
	}
	stem := base
	ext := ""
	if dotIdx := strings.LastIndex(base, "."); dotIdx > 0 {
		stem = base[:dotIdx]
		ext = base[dotIdx:]
	}
	for i := 1; i <= 100; i++ {
		var candidate string
		if i == 1 {
			candidate = dir + stem + "-copy" + ext
		} else {
			candidate = fmt.Sprintf("%s%s-copy-%d%s", dir, stem, i, ext)
		}
		if candidate != src && !pathExists(ctx, drv, candidate) {
			return candidate
		}
	}
	// Saturated — let the driver surface the collision/self-copy error
	// instead of looping forever. Caller's error-message path will
	// surface this to the user via the failed-step log.
	return dst
}

// pathExists returns true if Stat resolves the path to anything other
// than ErrNotFound. Any other error is treated as "exists" out of an
// abundance of caution: better to pick the next candidate than to
// stomp a file we couldn't probe.
func pathExists(ctx context.Context, drv storage.Driver, p string) bool {
	if _, err := drv.Stat(ctx, p); err != nil {
		return !errors.Is(err, storage.ErrNotFound)
	}
	return true
}

func (s *Service) fail(ctx context.Context, op *Op, msg string) {
	_, _ = s.db.ExecContext(ctx,
		`UPDATE pending_ops SET status=?, error=?, finished_at=CURRENT_TIMESTAMP, failed=total WHERE id=?`,
		StatusFailed, msg, op.ID)
}

func errMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// joinIntoDir builds dest/<basename(src)> using forward slashes.
//
// Plain rename (single source, dest already a full path) is also supported
// — if dest doesn't end with `/` we treat it literally.
func joinIntoDir(dest, src string) string {
	if dest == "" {
		return src
	}
	if !strings.HasSuffix(dest, "/") {
		return dest
	}
	idx := strings.LastIndex(strings.TrimRight(src, "/"), "/")
	base := src
	if idx >= 0 {
		base = src[idx+1:]
	}
	return strings.TrimRight(dest, "/") + "/" + base
}

func scanOp(row *sql.Row) (*Op, error) {
	op := &Op{}
	var srcJSON string
	if err := row.Scan(&op.ID, &op.Kind, &op.StorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
	return op, nil
}

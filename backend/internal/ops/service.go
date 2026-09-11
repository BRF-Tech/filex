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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Op kinds.
const (
	OpCopy   = "copy"
	OpMove   = "move"
	OpDelete = "delete"
	// OpUploadCommit streams a staged upload from filex's staging area into the
	// storage driver. Its single "source" is the staged upload id, not a path —
	// the work is done by the injected UploadCommitter, because the DB mirror,
	// the search index, thumbnails and the writehook all live in the handler
	// layer (same reason DBSync is injected rather than reimplemented here).
	OpUploadCommit = "upload-commit"
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
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	StorageID int64  `json:"storage_id"`
	// DestStorageID is the storage the destination lives in. It equals
	// StorageID for the ordinary same-storage copy/move; a different value is
	// what makes an op CROSS-STORAGE (paste from one depo into another),
	// which the worker serves by streaming bytes between the two drivers.
	// Zero means "same as StorageID" — the shape every row written before
	// this column existed has.
	DestStorageID int64      `json:"dest_storage_id,omitempty"`
	Sources       []string   `json:"sources"`
	Dest          string     `json:"dest,omitempty"`
	Total         int        `json:"total"`
	Done          int        `json:"done"`
	Failed        int        `json:"failed"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// Service is the queue + worker bundle.
type Service struct {
	db *sql.DB
	// dialect is the engine `db` speaks. Every statement in this file is
	// written in the SQLite/MySQL `?` form and passed through q(), which is
	// the only thing that makes them legal on PostgreSQL. Empty means sqlite.
	dialect         string
	storageResolver func(int64) (storage.Driver, error)
	dbsync          DBSync
	uploadCommitter UploadCommitter

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
	// SyncCopyAcross inserts a DB node for a copy whose two ends live in
	// DIFFERENT storages. SyncCopy cannot serve this: it reads the source
	// node and writes the new one under ONE storage id, so a cross-storage
	// paste mirrored through it would either find no source node or attach
	// the copy to the wrong depo — the listing would then show the file
	// where it is not.
	SyncCopyAcross(ctx context.Context, srcStorageID int64, src string, dstStorageID int64, dst string)
}

// SetSync wires the DB-sync hook. Call once at boot, before Run.
func (s *Service) SetSync(d DBSync) { s.dbsync = d }

// UploadCommitter transfers one staged upload's bytes to the storage driver
// and runs the post-write side effects (node update, search index, thumbnail,
// writehook). Implemented by the staged-upload HTTP handler and injected via
// SetUploadCommitter once both are constructed.
//
// Without it an OpUploadCommit op fails loudly rather than silently doing
// nothing: a staged upload whose bytes never move is data the user believes
// is saved.
type UploadCommitter interface {
	CommitUpload(ctx context.Context, uploadID string) error
}

// SetUploadCommitter wires the staged-upload transfer hook. Call once at boot,
// before Run.
func (s *Service) SetUploadCommitter(c UploadCommitter) { s.uploadCommitter = c }

// TrashPrefix is the in-storage dir soft-deleted files are moved into.
// Re-exported from the trash package so there is exactly one definition of
// where the trash lives — the key minting itself is trash.Put's job now.
const TrashPrefix = trash.Prefix

// New returns a Service that talks to the given *sql.DB, assuming SQLite.
//
// Callers must invoke Migrate before Submit/Status. Run starts the worker
// goroutine.
func New(database *sql.DB, resolver func(int64) (storage.Driver, error)) *Service {
	return NewForDialect(database, "sqlite", resolver)
}

// NewForDialect is New for a server whose database is not SQLite.
//
// ⚠ Pass the real driver name. This queue is raw SQL on the application's own
// connection, not a db.Store, so nothing else in the process knows which
// dialect it is talking to: on PostgreSQL a Service built with the wrong
// dialect accepts every copy, move and delete and then fails each one at the
// first placeholder — which is precisely how issue #19's install behaved.
func NewForDialect(database *sql.DB, dialect string, resolver func(int64) (storage.Driver, error)) *Service {
	if dialect == "" {
		dialect = "sqlite"
	}
	return &Service{
		db:              database,
		dialect:         dialect,
		storageResolver: resolver,
		wakeup:          make(chan struct{}, 1),
		stop:            make(chan struct{}),
	}
}

// q rewrites the `?` placeholders these statements are written with into the
// `$1..$n` PostgreSQL insists on. A no-op on SQLite and MySQL.
//
// The statements here contain no string literal holding a `?`, which is what
// makes a plain scan safe; keep it that way.
func (s *Service) q(query string) string {
	if s.dialect != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$")
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Migrate prepares the queue for this boot.
//
// ⚠ It no longer creates pending_ops. The table is a goose migration like
// every other (db/migrations/*/00036_pending_ops.sql), because the hand-rolled
// CREATE TABLE that used to live here was SQLite DDL — INTEGER PRIMARY KEY
// AUTOINCREMENT — executed against whatever engine the operator had. On
// PostgreSQL it failed on every boot, the table never existed, and every
// copy, move and delete died on "relation pending_ops does not exist" while
// the server reported itself healthy (issue #19).
func (s *Service) Migrate(ctx context.Context) error {
	// Any row left in `running` is from a previous crash — re-queue it.
	if _, err := s.db.ExecContext(ctx, `UPDATE pending_ops SET status='pending', started_at=NULL WHERE status='running'`); err != nil {
		return fmt.Errorf("ops: requeue stale running rows: %w", err)
	}
	return nil
}

// Submit enqueues a same-storage op and pokes the worker.
//
// Kept as the narrow form because most callers (delete, upload-commit, and
// every copy/move that stays inside one storage) have nothing to say about a
// destination storage. It delegates to SubmitTo with dest == source.
func (s *Service) Submit(ctx context.Context, kind string, storageID int64, sources []string, dest string) (*Op, error) {
	return s.SubmitTo(ctx, kind, storageID, storageID, sources, dest)
}

// SubmitTo enqueues an op whose destination may live in ANOTHER storage.
//
// destStorageID == storageID (or 0) is the ordinary same-storage op. A
// different id makes the worker stream the bytes from one driver to the other
// — the "copy from one depo, paste into the next" gesture, which used to be
// accepted and then silently written into the SOURCE storage because the
// queue had nowhere to put the target's storage.
func (s *Service) SubmitTo(ctx context.Context, kind string, storageID, destStorageID int64, sources []string, dest string) (*Op, error) {
	switch kind {
	case OpCopy, OpMove, OpDelete, OpUploadCommit:
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
	if destStorageID == 0 {
		destStorageID = storageID
	}
	if kind == OpDelete || kind == OpUploadCommit {
		// Neither has a destination; a stray id here would only be able to lie.
		destStorageID = storageID
	}
	srcJSON, _ := json.Marshal(sources)
	id, err := s.insertOp(ctx, kind, storageID, destStorageID, string(srcJSON), dest, len(sources))
	if err != nil {
		return nil, fmt.Errorf("ops: insert: %w", err)
	}
	s.poke()
	return s.Get(ctx, id)
}

// insertOp writes the row and returns its id.
//
// ⚠ Two spellings, because there is no portable one: pgx's Result has no
// LastInsertId at all (it returns an error), so PostgreSQL has to ask the
// INSERT itself for the id with RETURNING, which in turn is not valid on
// MySQL.
func (s *Service) insertOp(ctx context.Context, kind string, storageID, destStorageID int64, srcJSON, dest string, total int) (int64, error) {
	const cols = `INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status) VALUES (?,?,?,?,?,?,?)`
	if s.dialect == "postgres" {
		var id int64
		err := s.db.QueryRowContext(ctx, s.q(cols+` RETURNING id`),
			kind, storageID, destStorageID, srcJSON, dest, total, StatusPending).Scan(&id)
		return id, err
	}
	res, err := s.db.ExecContext(ctx, cols,
		kind, storageID, destStorageID, srcJSON, dest, total, StatusPending)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get returns the current state of an op.
func (s *Service) Get(ctx context.Context, id int64) (*Op, error) {
	row := s.db.QueryRowContext(ctx, s.q(
		`SELECT id, kind, storage_id, COALESCE(dest_storage_id,0), sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at
		 FROM pending_ops WHERE id=?`), id)
	return scanOp(row)
}

// List returns ops, optionally filtered by status (e.g. "running").
// Empty status returns the most-recent rows across all statuses, capped
// at 200 to keep the polling payload small. Used by the SPA's
// PendingOpsTray which calls GET /api/files/ops?status=running every 2s.
func (s *Service) List(ctx context.Context, status string) ([]*Op, error) {
	return s.ListIn(ctx, status, nil)
}

// ListIn is List restricted to a set of storage ids — the multi-tenant form.
// A nil set means "no restriction" (single-tenant mode, the supertenant, and
// every background caller), which is why List delegates here rather than the
// other way round.
//
// ⚠ The predicate is in the SQL, not applied to List's result, and that is the
// whole point of the extra method: the 200-row cap is applied by the database.
// Filtering afterwards would hand a tenant an EMPTY queue tray whenever another
// tenant had 200 more recent ops — isolation that silently costs the neighbour
// their own feature. A row matches on either end, because a cross-storage copy
// belongs to the tenant on either side of it.
func (s *Service) ListIn(ctx context.Context, status string, storageIDs []int64) ([]*Op, error) {
	const cols = `id, kind, storage_id, COALESCE(dest_storage_id,0), sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at`
	q := `SELECT ` + cols + ` FROM pending_ops`
	var (
		where []string
		args  []any
	)
	if status != "" {
		where = append(where, `status=?`)
		args = append(args, status)
	}
	if storageIDs != nil {
		if len(storageIDs) == 0 {
			// A scope that reaches no storage sees no ops. An empty `IN ()` is
			// a syntax error on some drivers and, worse, an invitation to
			// "just skip the clause" — which is the unscoped query again.
			return []*Op{}, nil
		}
		ph := make([]string, len(storageIDs))
		for i, id := range storageIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		in := strings.Join(ph, ",")
		where = append(where, `(storage_id IN (`+in+`) OR COALESCE(dest_storage_id,0) IN (`+in+`))`)
		for _, id := range storageIDs {
			args = append(args, id)
		}
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, s.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Op, 0, 16)
	for rows.Next() {
		op := &Op{}
		var srcJSON string
		if err := rows.Scan(&op.ID, &op.Kind, &op.StorageID, &op.DestStorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
		if op.DestStorageID == 0 {
			op.DestStorageID = op.StorageID
		}
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
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE pending_ops SET status=?, started_at=CURRENT_TIMESTAMP WHERE id=? AND status='pending'`), StatusRunning, id); err != nil {
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
	// Cross-storage ops need BOTH drivers up front: failing the whole op here
	// is the honest answer when the target depo is offline, rather than
	// discovering it per item after some bytes already moved.
	dstDrv := drv
	if op.DestStorageID != 0 && op.DestStorageID != op.StorageID {
		dstDrv, err = s.storageResolver(op.DestStorageID)
		if err != nil {
			s.fail(ctx, op, "destination storage: "+err.Error())
			return
		}
	}

	var lastErr error
	for _, src := range op.Sources {
		if ctx.Err() != nil {
			break
		}
		if err := s.runOne(ctx, drv, dstDrv, op, src); err != nil {
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
		_, _ = s.db.ExecContext(ctx, s.q(`UPDATE pending_ops SET done=?, failed=? WHERE id=?`), op.Done, op.Failed, op.ID)
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
	_, _ = s.db.ExecContext(ctx, s.q(
		`UPDATE pending_ops SET status=?, error=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
		status, errMsg, op.ID)
}

func (s *Service) runOne(ctx context.Context, drv, dstDrv storage.Driver, op *Op, src string) error {
	switch op.Kind {
	case OpUploadCommit:
		// `src` is the staged upload id. The committer owns the driver write
		// and every post-write hook; this worker only owns the retry/progress
		// bookkeeping.
		if s.uploadCommitter == nil {
			return errors.New("ops: no upload committer wired")
		}
		return s.uploadCommitter.CommitUpload(ctx, src)
	case OpDelete:
		// Soft-delete: rename the file into `.filex-trash/` and flag the DB
		// row so it's restorable AND leaves the listing. The async worker
		// used to hard-delete and never touch the DB, which left the file
		// both un-trashable and still visible (the listing reads the DB).
		//
		// trash.Put is the one shared implementation of "put these bytes in the
		// trash" — the same call the web UI, WebDAV and the AI surface make — so
		// an async batch delete cannot drift from what a synchronous delete of
		// the same item does.
		out, terr := trash.Put(ctx, drv, src)
		switch {
		case terr == nil && out.Trashed:
			if s.dbsync != nil {
				s.dbsync.SyncSoftDelete(ctx, op.StorageID, src, out.Key)
			}
			return nil

		case terr == nil && out.Missing:
			// Source object already gone (stale index / out-of-band delete):
			// drop the cache row outright instead of trashing a phantom, so a
			// batch delete never fails on already-missing items.
			if s.dbsync != nil {
				s.dbsync.SyncHardDelete(ctx, op.StorageID, src)
			}
			return nil

		case errors.Is(terr, trash.ErrUnsupported):
			// Driver can neither move nor copy — nothing can be preserved, so
			// this is a real delete.
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

		default:
			return terr
		}
	case OpMove:
		if s.isCross(op) {
			return s.crossTransfer(ctx, drv, dstDrv, op, src, true)
		}
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
		if s.isCross(op) {
			return s.crossTransfer(ctx, drv, dstDrv, op, src, false)
		}
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
// (sweep-2026-05-09 bug 25 — "Kopyasını Oluştur" (Duplicate) was sending
// source == destination and the S3 driver was 400ing the self-copy.)
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
	_, _ = s.db.ExecContext(ctx, s.q(
		`UPDATE pending_ops SET status=?, error=?, finished_at=CURRENT_TIMESTAMP, failed=total WHERE id=?`),
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
// joinIntoDir resolves an operation's destination for one source.
//
// A dest that ends in "/" is a DIRECTORY: the source keeps its own basename
// inside it. Anything else is a literal target (a rename). An empty dest
// leaves the source path alone.
//
// ⚠ The storage root arrives here as "/", and path.Join is what keeps that
// case honest: "/" + "a/b.txt" must be "b.txt", not "/b.txt". A leading slash
// is harmless on a local disk and a real object on S3 — a key whose first
// path segment is empty, which no listing shows where the user expects it.
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
	return path.Join(strings.TrimRight(dest, "/"), base)
}

func scanOp(row *sql.Row) (*Op, error) {
	op := &Op{}
	var srcJSON string
	if err := row.Scan(&op.ID, &op.Kind, &op.StorageID, &op.DestStorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
	if op.DestStorageID == 0 {
		op.DestStorageID = op.StorageID
	}
	return op, nil
}

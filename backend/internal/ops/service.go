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
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// Op kinds.
const (
	OpCopy   = "copy"
	OpMove   = "move"
	OpDelete = "delete"
	// OpArchiveCreate materializes and compresses an archive through a handler-
	// supplied in-memory job. The persisted row intentionally contains only
	// safe metadata; archive passwords must never be written to pending_ops.
	OpArchiveCreate = "archive-create"
	// OpArchiveExtract expands an archive through a handler-supplied in-memory
	// job. As with creation, credentials stay in the closure and are never
	// persisted in pending_ops.
	OpArchiveExtract = "archive-extract"
	// OpUploadCommit streams a staged upload from filex's staging area into the
	// storage driver. Its single "source" is the staged upload id, not a path —
	// the work is done by the injected UploadCommitter, because the DB mirror,
	// the search index, thumbnails and the writehook all live in the handler
	// layer (same reason DBSync is injected rather than reimplemented here).
	OpUploadCommit = "upload-commit"
	// OpPluginAction runs one app-plugin action (internal/wasmplugin). The
	// row's Dest carries the job id (app_plugin_jobs) and Sources the input
	// paths; the work is done by the injected PluginRunner, which owns the
	// sandbox, the output commit and the job row. One op = one job, so
	// Done/Failed move as a unit rather than per source.
	OpPluginAction = "plugin-action"
)

// Status values.
const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusOK         = "ok"
	StatusFailed     = "failed"
	StatusPartial    = "partial"
	StatusCancelling = "cancelling"
	// StatusCancelled is a row Cancel ended: a pending row that never ran,
	// or a running one whose context was cancelled.
	StatusCancelled = "cancelled"
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
	DestStorageID int64    `json:"dest_storage_id,omitempty"`
	Sources       []string `json:"sources"`
	Dest          string   `json:"dest,omitempty"`
	Total         int      `json:"total"`
	Done          int      `json:"done"`
	// BytesTotal / BytesDone are a running cross-storage transfer's byte
	// counters (issue #27), merged in from memory by Get/List — never stored.
	// BytesTotal 0 with BytesDone > 0 means the total is not known (yet).
	BytesTotal int64  `json:"bytes_total,omitempty"`
	BytesDone  int64  `json:"bytes_done,omitempty"`
	Failed     int    `json:"failed"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	// ErrorCode / ErrorEngine classify a failed APP job for the client, which
	// says it in the person's language (lib/errorWords `jobFailure`) and keeps
	// `Error` — English, sometimes plumbing — for an administrator's second
	// line. Filled by the plugin Decorator, never stored.
	ErrorCode   string     `json:"error_code,omitempty"`
	ErrorEngine string     `json:"error_engine,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	// Plugin fields are filled by the Decorator for OpPluginAction rows and
	// never stored here: which plugin/action ran, the action's label in the
	// caller's locale, the last progress message, and the committed outputs.
	Plugin  string     `json:"plugin,omitempty"`
	Action  string     `json:"action,omitempty"`
	Label   string     `json:"label,omitempty"`
	Message string     `json:"message,omitempty"`
	Outputs []OpOutput `json:"outputs,omitempty"`
	// ActorID is who asked for this op. The worker runs on a server-lifetime
	// context long after the request that queued the work is gone, so the only
	// way a pasted file can be attributed to the person who pasted it is for
	// the queue row to carry them (migration 00038). nil is SYSTEM — a row
	// queued before the column existed, or by something that is not a person.
	ActorID *int64 `json:"actor_id,omitempty"`
	// Cancellable is advertised rather than inferred from the operation kind.
	Cancellable bool `json:"cancellable"`

	// An OpTrashEmpty row's request (trash_empty.go): kept out of the
	// answers, which carry its counts only.
	trash  trashParams
	tenant string
}

// shape finishes a scanned row. An OpTrashEmpty row stores its request where
// the other kinds store paths; that is read into the private fields and
// taken out of the public ones.
func shape(op *Op) {
	if op.DestStorageID == 0 {
		op.DestStorageID = op.StorageID
	}
	if op.Kind != OpTrashEmpty {
		return
	}
	p, err := decodeTrashParams(op.Sources)
	if err != nil {
		slog.Warn("ops: trash-empty row unreadable; it reaches nothing", slog.Int64("op", op.ID), slog.String("err", err.Error()))
	}
	op.trash, op.tenant = p, op.Dest
	op.Sources, op.Dest = nil, ""
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
	// locks is the live app-lock table of a storage (acl.Resolver.Locks),
	// wired by the router; nil judges names only (writegate.Check).
	locks           func(ctx context.Context, storageID int64) writegate.Locks
	uploadCommitter UploadCommitter
	pluginRunner    PluginRunner
	decorator       func(ctx context.Context, rows []*Op)

	// cancels holds the cancel handle of every running op (Cancel).
	cancels sync.Map
	jobsMu  sync.Mutex
	jobs    map[int64]queuedJob

	// live holds the byte counters of running cross-storage ops (progress.go).
	live sync.Map

	// deleteWorkers bounds how many items of ONE delete job are trashed at
	// the same time (SetDeleteWorkers; delete_pool.go).
	deleteWorkers int

	// "Empty the trash now" (trash_empty.go): the purge, the lock that makes
	// a tenant's second press see its first, and the runs in flight.
	trashEmptier TrashEmptier
	trashMu      sync.Mutex
	trashDone    sync.Map // op id -> chan struct{}, closed when the run ends

	// life is what background runs live in; Stop ends it and waits (bg).
	lifeMu     sync.Mutex
	life       context.Context
	lifeCancel context.CancelFunc
	bg         sync.WaitGroup

	wakeup chan struct{}
	stopMu sync.Mutex
	stop   chan struct{}
	stopWg sync.WaitGroup
}

// Job is handler-owned background work. progress reports completed units;
// callers choose the units when submitting (archive creation uses one per
// staged member plus one for compression and the destination write).
type Job func(ctx context.Context, progress func(done int)) error

type queuedJob struct {
	run     Job
	cleanup func()
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

// SetLocks wires the app-lock lookup SubmitTo judges every operation
// against (see Targets).
func (s *Service) SetLocks(fn func(ctx context.Context, storageID int64) writegate.Locks) {
	s.locks = fn
}

func (s *Service) lockView(ctx context.Context, storageID int64) writegate.Locks {
	if s.locks == nil {
		return nil
	}
	return s.locks(ctx, storageID)
}

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

// OpOutput is one file a plugin job produced.
type OpOutput struct {
	Path string `json:"path"`
}

// PluginRunner executes an OpPluginAction row: reads the job behind op.Dest,
// runs the plugin in its sandbox, commits the outputs and records the job's
// outcome. live receives progress the plugin reports (done/total), which the
// tray draws through the same byte counters a cross-storage copy uses.
// Implemented by wasmplugin.Registry, injected via SetPluginRunner.
type PluginRunner interface {
	RunPluginAction(ctx context.Context, op *Op, live func(done, total int64)) error
}

// SetPluginRunner wires the app-plugin executor. Without it an
// OpPluginAction row fails loudly.
func (s *Service) SetPluginRunner(r PluginRunner) { s.pluginRunner = r }

// SetDecorator installs the hook Get/List call to fill the plugin fields of
// rows (Op.Plugin, Label, Outputs, …) from the job table.
func (s *Service) SetDecorator(fn func(ctx context.Context, rows []*Op)) { s.decorator = fn }

// Cancel ends an op. A pending row is marked cancelled without running; a
// running row has its context cancelled, which tears down the plugin
// instance (or stops the transfer loop), and is marked cancelled when the
// worker returns. Unknown or finished rows are a no-op with ok=false.
func (s *Service) Cancel(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, s.q(
		`UPDATE pending_ops SET status=?, finished_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`),
		StatusCancelled, id, StatusPending)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		// A queued trash empty already has its goroutine, waiting for its
		// turn: tell it to stop waiting.
		if v, ok := s.cancels.Load(id); ok {
			v.(*cancelHandle).cancel()
		}
		s.jobsMu.Lock()
		queued := s.jobs[id]
		delete(s.jobs, id)
		s.jobsMu.Unlock()
		if queued.cleanup != nil {
			queued.cleanup()
		}
		return true, nil
	}
	if v, ok := s.cancels.Load(id); ok {
		v.(*cancelHandle).cancel()
		return true, nil
	}
	return false, nil
}

type cancelHandle struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

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
		deleteWorkers:   DefaultDeleteWorkers,
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
	if _, err := s.db.ExecContext(ctx, `UPDATE pending_ops SET status='cancelled', finished_at=CURRENT_TIMESTAMP WHERE status='cancelling'`); err != nil {
		return fmt.Errorf("ops: finish stale cancelling rows: %w", err)
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

// SubmitJob queues handler-owned work without persisting its executable
// payload. This is important for encrypted archives: sources and destination
// are useful durable operation metadata, but the password stays exclusively
// in the closure and therefore in process memory.
func (s *Service) SubmitJob(ctx context.Context, kind string, storageID int64, sources []string, dest string, total int, job Job) (*Op, error) {
	return s.SubmitJobWithCleanup(ctx, kind, storageID, sources, dest, total, job, nil)
}

// SubmitJobWithCleanup is SubmitJob with an idempotent cleanup callback. The
// callback is also invoked when a still-pending job is cancelled, which is
// essential for request-prepared temporary inputs that the job never gets a
// chance to defer-remove itself.
func (s *Service) SubmitJobWithCleanup(ctx context.Context, kind string, storageID int64, sources []string, dest string, total int, job Job, cleanup func()) (*Op, error) {
	if kind != OpArchiveCreate && kind != OpArchiveExtract {
		return nil, fmt.Errorf("ops: unsupported job kind %q", kind)
	}
	if storageID == 0 {
		return nil, errors.New("ops: missing storage_id")
	}
	if len(sources) == 0 {
		return nil, errors.New("ops: no sources")
	}
	if dest == "" {
		return nil, errors.New("ops: dest required")
	}
	if total <= 0 || job == nil {
		return nil, errors.New("ops: invalid job")
	}
	srcJSON, _ := json.Marshal(sources)

	// Hold the same lock the worker uses to take the closure. That closes the
	// small timer-driven race between INSERT and registering the in-memory job.
	s.jobsMu.Lock()
	id, err := s.insertOp(ctx, kind, storageID, storageID, string(srcJSON), dest, total)
	if err == nil {
		if s.jobs == nil {
			s.jobs = make(map[int64]queuedJob)
		}
		s.jobs[id] = queuedJob{run: job, cleanup: cleanup}
	}
	s.jobsMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("ops: insert job: %w", err)
	}
	s.poke()
	return s.Get(ctx, id)
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
	case OpCopy, OpMove, OpDelete, OpUploadCommit, OpPluginAction:
	default:
		return nil, fmt.Errorf("ops: unknown kind %q", kind)
	}
	if storageID == 0 {
		return nil, errors.New("ops: missing storage_id")
	}
	if len(sources) == 0 {
		return nil, errors.New("ops: no sources")
	}
	if (kind == OpCopy || kind == OpMove || kind == OpPluginAction) && dest == "" {
		return nil, errors.New("ops: dest required")
	}
	if destStorageID == 0 {
		destStorageID = storageID
	}
	srcT, dstT := Targets(kind, sources, dest)
	if err := writegate.Check(s.lockView(ctx, storageID), 0, srcT...); err != nil {
		return nil, err
	}
	if err := writegate.Check(s.lockView(ctx, destStorageID), 0, dstT...); err != nil {
		return nil, err
	}
	if kind == OpCopy || kind == OpMove {
		if err := refuseSelfDescendant(storageID, destStorageID, sources, dest); err != nil {
			return nil, err
		}
	}
	if kind == OpDelete || kind == OpUploadCommit || kind == OpPluginAction {
		// None of these has a destination storage; a stray id here would only
		// be able to lie.
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
	// Read the acting identity HERE, where a request context still exists. By
	// the time the worker picks the row up there is nobody to ask.
	var actor *int64
	if u := auth.UserFrom(ctx); u != nil && u.ID > 0 {
		id := u.ID
		actor = &id
	}
	const cols = `INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status, actor_id) VALUES (?,?,?,?,?,?,?,?)`
	if s.dialect == "postgres" {
		var id int64
		err := s.db.QueryRowContext(ctx, s.q(cols+` RETURNING id`),
			kind, storageID, destStorageID, srcJSON, dest, total, StatusPending, actor).Scan(&id)
		return id, err
	}
	res, err := s.db.ExecContext(ctx, cols,
		kind, storageID, destStorageID, srcJSON, dest, total, StatusPending, actor)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get returns the current state of an op.
func (s *Service) Get(ctx context.Context, id int64) (*Op, error) {
	row := s.db.QueryRowContext(ctx, s.q(
		`SELECT id, kind, storage_id, COALESCE(dest_storage_id,0), sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at, actor_id
		 FROM pending_ops WHERE id=?`), id)
	op, err := scanOp(row)
	if err == nil {
		s.attachLive(op)
		if s.decorator != nil {
			s.decorator(ctx, []*Op{op})
		}
		op.Cancellable = op.Status == StatusPending || op.Status == StatusRunning
	}
	return op, err
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
	if storageIDs == nil {
		return s.ListFor(ctx, status, Viewer{All: true})
	}
	return s.ListFor(ctx, status, Viewer{StorageIDs: storageIDs})
}

// ListFor is List as v sees it (Viewer.Sees, in the SQL).
func (s *Service) ListFor(ctx context.Context, status string, v Viewer) ([]*Op, error) {
	const cols = `id, kind, storage_id, COALESCE(dest_storage_id,0), sources_json, COALESCE(dest,''), total, done, failed, status, COALESCE(error,''), created_at, started_at, finished_at, actor_id`
	q := `SELECT ` + cols + ` FROM pending_ops`
	var (
		where []string
		args  []any
	)
	if status != "" {
		where = append(where, `status=?`)
		args = append(args, status)
	}
	if !v.All {
		var either []string
		if len(v.StorageIDs) > 0 {
			ph := make([]string, len(v.StorageIDs))
			for i, id := range v.StorageIDs {
				ph[i] = "?"
				args = append(args, id)
			}
			in := strings.Join(ph, ",")
			either = append(either, `storage_id IN (`+in+`)`, `COALESCE(dest_storage_id,0) IN (`+in+`)`)
			for _, id := range v.StorageIDs {
				args = append(args, id)
			}
		}
		if v.Tenant != "" {
			either = append(either, `(kind=? AND COALESCE(dest,'')=?)`)
			args = append(args, OpTrashEmpty, v.Tenant)
		}
		if len(either) == 0 {
			// A scope that reaches no storage sees no ops. An empty `IN ()` is
			// a syntax error on some drivers and, worse, an invitation to
			// "just skip the clause" — which is the unscoped query again.
			return []*Op{}, nil
		}
		where = append(where, `(`+strings.Join(either, ` OR `)+`)`)
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
		if err := rows.Scan(&op.ID, &op.Kind, &op.StorageID, &op.DestStorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt, &op.ActorID); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
		shape(op)
		s.attachLive(op)
		op.Cancellable = op.Status == StatusPending || op.Status == StatusRunning
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.decorator != nil && len(out) > 0 {
		s.decorator(ctx, out)
	}
	return out, nil
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

	// A trash empty the previous process left behind carries on.
	s.resumeTrashEmpties(ctx)

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
	s.stopBackground()
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
	// A trash empty is never the worker's: it runs in its own goroutine
	// (trash_empty.go) and would hold the whole queue for as long as it runs.
	row := tx.QueryRowContext(ctx, `SELECT id FROM pending_ops WHERE status='pending' AND kind <> 'trash-empty' ORDER BY id ASC LIMIT 1`)
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
//
// ⚠ The first thing it does is put the person who asked back on the context.
// Everything downstream — the DB mirror that creates the pasted row, the actor
// stamp on a moved one — resolves identity from the context, and this worker
// has none of its own. Both keys are set: WithOwner because a COPY is a new
// file and the copier owns it, WithActor because a MOVE is the same file being
// moved BY somebody without becoming theirs.
func (s *Service) execute(ctx context.Context, op *Op) {
	if op != nil && op.ActorID != nil && *op.ActorID > 0 {
		ctx = quotastore.WithOwner(ctx, *op.ActorID)
		ctx = quotastore.WithActor(ctx, *op.ActorID)
	}
	if op.Kind == OpArchiveCreate || op.Kind == OpArchiveExtract {
		ctx, cancelOp := context.WithCancel(ctx)
		defer cancelOp()
		ch := &cancelHandle{}
		ch.cancel = func() { ch.cancelled.Store(true); cancelOp() }
		s.cancels.Store(op.ID, ch)
		defer s.cancels.Delete(op.ID)
		s.executeJob(ctx, op)
		return
	}
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

	// Every running op can be cancelled (Cancel): the handle ends this
	// context, and a plugin instance or a transfer loop stops with it.
	parent := ctx
	ctx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()
	ch := &cancelHandle{}
	ch.cancel = func() { ch.cancelled.Store(true); cancelOp() }
	s.cancels.Store(op.ID, ch)
	defer s.cancels.Delete(op.ID)

	if op.Kind == OpPluginAction {
		s.executePlugin(ctx, parent, op, ch)
		return
	}

	if s.isCross(op) {
		lp := &liveProgress{}
		s.live.Store(op.ID, lp)
		defer s.live.Delete(op.ID)
		// Measured beside the transfer, not before it: the bytes start moving
		// at once, and the tray shows a spinner until the total is known.
		go func(srcs []string) {
			if total, ok := measureSources(ctx, drv, srcs); ok && total > 0 {
				lp.total.Store(total)
			}
		}(append([]string(nil), op.Sources...))
	}

	var lastErr error
	// left gathers what the cross-storage transfers deliberately did not carry,
	// across every source of this op (cross.go, Skipped). A source that left
	// entries behind is DONE — everything it could carry arrived and was
	// verified — but the op must not read as a clean success, because the one
	// place the person learns what was left out is this row: the tray and the
	// toast draw a `partial` row's error text, and draw nothing for an `ok`.
	var left *SkipsError
	if op.Kind == OpDelete {
		// Several items at once, within this one job (delete_pool.go). The
		// job itself still runs in queue order, like every other.
		lastErr = s.runDeletes(ctx, drv, dstDrv, op)
	} else {
		for _, src := range op.Sources {
			if ctx.Err() != nil {
				break
			}
			err := s.runOne(ctx, drv, dstDrv, op, src)
			var skips *SkipsError
			switch {
			case err == nil:
				op.Done++
			case errors.As(err, &skips):
				op.Done++
				if left == nil {
					left = &SkipsError{}
				}
				left.Skipped = append(left.Skipped, skips.Skipped...)
				left.SourceKept = left.SourceKept || skips.SourceKept
			default:
				op.Failed++
				lastErr = err
				slog.Warn("ops: step failed",
					slog.Int64("op", op.ID),
					slog.String("kind", op.Kind),
					slog.String("src", src),
					slog.String("err", err.Error()))
			}
			_, _ = s.db.ExecContext(ctx, s.q(`UPDATE pending_ops SET done=?, failed=? WHERE id=?`), op.Done, op.Failed, op.ID)
		}
	}

	status := StatusOK
	errMsg := ""
	switch {
	case ch.cancelled.Load():
		status = StatusCancelled
	case op.Failed == 0 && left != nil:
		status = StatusPartial
		errMsg = left.Error()
	case errors.Is(ctx.Err(), context.Canceled):
		status = StatusCancelled
	case op.Failed == 0:
		status = StatusOK
	case op.Done == 0:
		status = StatusFailed
		errMsg = errMessage(lastErr)
	default:
		status = StatusPartial
		errMsg = errMessage(lastErr)
		if left != nil {
			errMsg += "; " + left.Error()
		}
	}
	// The counters ride along: a delete job writes its progress at most once
	// a second, so the last item's count may not be on the row yet.
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), s.q(
		`UPDATE pending_ops SET status=?, error=?, done=?, failed=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
		status, errMsg, op.Done, op.Failed, op.ID)
}

// executePlugin runs an OpPluginAction row as one unit through the injected
// PluginRunner. Progress arrives through the live counters; the outcome is
// the row's status, with cancelled kept apart from failed so the tray can say
// which it was.
func (s *Service) executePlugin(ctx, parent context.Context, op *Op, ch *cancelHandle) {
	if s.pluginRunner == nil {
		s.fail(ctx, op, "ops: no plugin runner wired")
		return
	}
	lp := &liveProgress{}
	s.live.Store(op.ID, lp)
	defer s.live.Delete(op.ID)
	err := s.pluginRunner.RunPluginAction(ctx, op, func(done, total int64) {
		lp.done.Store(done)
		if total > 0 {
			lp.total.Store(total)
		}
	})
	wctx := context.WithoutCancel(parent)
	switch {
	case err == nil:
		_, _ = s.db.ExecContext(wctx, s.q(
			`UPDATE pending_ops SET status=?, done=total, error='', finished_at=CURRENT_TIMESTAMP WHERE id=?`),
			StatusOK, op.ID)
	case ch.cancelled.Load():
		_, _ = s.db.ExecContext(wctx, s.q(
			`UPDATE pending_ops SET status=?, error='', finished_at=CURRENT_TIMESTAMP WHERE id=?`),
			StatusCancelled, op.ID)
	default:
		slog.Warn("ops: plugin action failed", slog.Int64("op", op.ID), slog.String("job", op.Dest), slog.String("err", err.Error()))
		_, _ = s.db.ExecContext(wctx, s.q(
			`UPDATE pending_ops SET status=?, error=?, failed=total, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
			StatusFailed, errMessage(err), op.ID)
	}
}

func (s *Service) executeJob(ctx context.Context, op *Op) {
	s.jobsMu.Lock()
	queued := s.jobs[op.ID]
	delete(s.jobs, op.ID)
	s.jobsMu.Unlock()
	if queued.run == nil {
		s.fail(ctx, op, "archive job was interrupted by a server restart; create the archive again")
		return
	}
	if queued.cleanup != nil {
		defer queued.cleanup()
	}
	progress := func(done int) {
		if done < 0 {
			done = 0
		}
		if done > op.Total {
			done = op.Total
		}
		op.Done = done
		_, _ = s.db.ExecContext(ctx, s.q(`UPDATE pending_ops SET done=? WHERE id=?`), done, op.ID)
	}
	if err := queued.run(ctx, progress); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_, _ = s.db.ExecContext(context.WithoutCancel(ctx), s.q(
				`UPDATE pending_ops SET status=?, error='', finished_at=CURRENT_TIMESTAMP WHERE id=?`),
				StatusCancelled, op.ID)
			return
		}
		op.Failed = 1
		_, _ = s.db.ExecContext(ctx, s.q(
			`UPDATE pending_ops SET status=?, error=?, failed=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
			StatusFailed, err.Error(), op.Failed, op.ID)
		return
	}
	progress(op.Total)
	_, _ = s.db.ExecContext(ctx, s.q(
		`UPDATE pending_ops SET status=?, error='', finished_at=CURRENT_TIMESTAMP WHERE id=?`),
		StatusOK, op.ID)
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
		dst, err := MoveDest(ctx, drv, src, joinIntoDir(op.Dest, src))
		if err != nil {
			return err
		}
		if normOpPath(dst) == normOpPath(src) {
			return nil
		}
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
		dst, err := uniqueCopyDest(ctx, drv, src, joinIntoDir(op.Dest, src))
		if err != nil {
			return err
		}
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
func uniqueCopyDest(ctx context.Context, drv storage.Driver, src, dst string, taken ...Taken) (string, error) {
	occupied := func(p string) bool {
		if storage.Exists(ctx, drv, p) {
			return true
		}
		for _, t := range taken {
			if t != nil && t(p) {
				return true
			}
		}
		return false
	}
	if dst != src && caseOnlyRename(src, dst) {
		// ⚠ On a case-insensitive disk (Windows, macOS) Stat of `C.txt` finds
		// `c.txt` — the item being renamed — and the rename would be turned
		// into `C-copy.txt`. Only an entry spelled EXACTLY like the new name
		// is another item.
		if free, ok := caseOnlyFree(ctx, drv, src, dst, taken); ok && free {
			return dst, nil
		}
	}
	if dst != src && !occupied(dst) {
		return dst, nil
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
		if candidate != src && !occupied(candidate) {
			return candidate, nil
		}
	}
	// ⚠⚠ Saturated: an ERROR, never `dst`. It used to hand back the taken
	// name "to let the driver surface the collision" — but every driver's
	// Move and Copy REPLACE an occupied destination (a local rename does, an
	// object store's copy-then-delete does), so a folder already holding
	// `a-copy.txt` … `a-copy-100.txt` had its `a.txt` silently overwritten.
	return "", fmt.Errorf("%w: %s", ErrNoFreeName, dst)
}

// ErrNoFreeName means every name a move or copy would give an item beside a
// taken destination is taken as well. Nothing was written.
var ErrNoFreeName = errors.New("no free name left beside the destination")

// Taken is an extra "is this name taken?" a caller can add to a destination
// check — the catalogue's live rows, which the driver cannot see when a row's
// bytes went missing. Moving onto such a name would make the catalogue drop
// that row, and its history with it.
type Taken func(rel string) bool

// caseOnlyRename reports a rename that changes nothing but the case.
func caseOnlyRename(src, dst string) bool {
	s, d := normOpPath(src), normOpPath(dst)
	return s != "" && s != d && strings.EqualFold(s, d)
}

// caseOnlyFree lists dst's folder and reports whether an entry spelled
// exactly like dst exists (then it is another item and the name is taken).
// ok is false when the folder cannot be listed; the caller then falls back
// to the ordinary check.
func caseOnlyFree(ctx context.Context, drv storage.Driver, src, dst string, taken []Taken) (free, ok bool) {
	lister, isLister := drv.(interface {
		List(ctx context.Context, p string) ([]storage.Object, error)
	})
	if !isLister {
		return false, false
	}
	d := normOpPath(dst)
	objs, err := lister.List(ctx, "/"+path.Dir(d))
	if err != nil {
		return false, false
	}
	base := path.Base(d)
	for _, o := range objs {
		if o.Name == base {
			return false, true
		}
	}
	for _, t := range taken {
		if t != nil && t(dst) {
			return false, true
		}
	}
	return true, true
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

// ErrIntoOwnDescendant is "you asked to put this folder inside itself".
//
// ⚠ Measured on a local storage 2026-09-13, before this guard existed: the
// submit answered 202, the row went to the queue, and the WORKER failed a
// moment later with `rename /srv/data/x /srv/data/x/child/x: invalid argument`.
// Three things wrong with that. The person is told nothing at click time; the
// reason they eventually see is an OS errno and a SERVER path, which is both
// meaningless to them and more than they should be shown; and the refusal is
// the local driver's, not ours — an object store has no rename, so the same
// request there walks the tree and copies it into a destination that is inside
// the tree it is walking.
//
// The answer belongs here, synchronously, in the one funnel both the unified
// endpoint and the per-verb wrappers pass through. A destination picker can
// (and does) grey the folder out, but a picker is a courtesy: the API is
// reachable without it.
var ErrIntoOwnDescendant = errors.New("ops: a folder cannot be moved or copied into itself or into one of its own subfolders")

// Targets is what one queued operation writes, for writegate.Check: on the
// source storage and on the destination storage. SubmitTo judges every
// operation by it — the explorer's copy, move and delete, the unified
// /api/files/ops endpoint, an app's action and a scheduled one — and the
// handlers ask the same function earlier, so the two cannot disagree about
// what an operation touches.
//
//   - move and delete TAKE their sources (and everything under a folder): a
//     frozen document, or the folder around it, does not move or go.
//   - copy and an app's action only READ their sources: named, not changed.
//     (An app's output is judged where it is written, handlers.AppPlugins.)
//   - a copy or move destination is named, not replaced: the worker
//     de-collides (UniqueDest / MoveDest), so nothing already there is
//     overwritten.
//
// Every one of them is judged on filex's own names — a copy INTO
// `.filex-trash` is a file nobody can find, a move OUT of `.filex-open` takes
// a document from under the desktop that is about to write it back.
// Upload commits are not judged here: their sources are staged-upload ids,
// and the target was judged when the upload began (StagedUpload.Begin).
func Targets(kind string, sources []string, dest string) (src, dst []writegate.Target) {
	if kind == OpUploadCommit {
		return nil, nil
	}
	for _, raw := range sources {
		rel := opRel(raw)
		switch kind {
		case OpMove, OpDelete:
			src = append(src, writegate.Writes(rel))
		default:
			src = append(src, writegate.Names(rel))
		}
		if kind == OpCopy || kind == OpMove {
			dst = append(dst, writegate.Names(opRel(dest)), writegate.Names(opRel(joinIntoDir(dest, raw))))
		}
	}
	return src, dst
}

// opRel is a storage-relative path from any spelling a queued operation
// carries (`adapter://a/b`, `/a/b/`, `a/b`) — the key app locks are stored
// under.
func opRel(p string) string {
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+3:]
	}
	return normOpPath(p)
}

// refuseSelfDescendant rejects a copy/move whose real destination lands inside
// one of its own sources.
//
// It computes the FINAL target with joinIntoDir — the same function the worker
// uses — rather than comparing the raw dest, because "into this directory"
// (trailing slash) and "to this exact path" (no slash) mean different things
// and only one of them appends the basename.
//
// Two deliberate narrowings:
//   - Only within ONE storage. `a://x` into `b://x/sub` is two different trees
//     that happen to share a name; refusing it would break a legitimate paste.
//   - Only a STRICT descendant. A target equal to its source is a rename to
//     the same name (a no-op) or a self-copy, both of which the existing
//     de-collision path already handles.
func refuseSelfDescendant(storageID, destStorageID int64, sources []string, dest string) error {
	if storageID != destStorageID {
		return nil
	}
	for _, src := range sources {
		s := normOpPath(src)
		if s == "" {
			// The storage root. Everything is inside it, so a copy of the
			// root into any path under it is the same trap.
			if normOpPath(joinIntoDir(dest, src)) != "" {
				return ErrIntoOwnDescendant
			}
			continue
		}
		if strings.HasPrefix(normOpPath(joinIntoDir(dest, src))+"/", s+"/") &&
			normOpPath(joinIntoDir(dest, src)) != s {
			return ErrIntoOwnDescendant
		}
	}
	return nil
}

// normOpPath puts a source or destination into one comparable form: no leading
// or trailing slash, traversal collapsed, "" for the storage root.
func normOpPath(p string) string {
	p = strings.Trim(path.Clean("/"+strings.Trim(p, "/")), "/")
	if p == "." {
		return ""
	}
	return p
}

func scanOp(row *sql.Row) (*Op, error) {
	op := &Op{}
	var srcJSON string
	if err := row.Scan(&op.ID, &op.Kind, &op.StorageID, &op.DestStorageID, &srcJSON, &op.Dest, &op.Total, &op.Done, &op.Failed, &op.Status, &op.Error, &op.CreatedAt, &op.StartedAt, &op.FinishedAt, &op.ActorID); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(srcJSON), &op.Sources)
	shape(op)
	return op, nil
}

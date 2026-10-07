package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"
)

// Replication mirrors writes/deletes/moves/copies from a primary
// Driver to a replica. Reads fall back to the replica when the
// primary errors. All write paths go through a path-based RuleEngine
// so operators can override mirror semantics (mirror vs append-only
// vs skip).
//
// The wrapper itself implements Driver + the optional Write/Move/
// Copy/Delete/Mkdir/RangeReader sub-interfaces so callers can substitute it
// transparently for the underlying primary. The primary's Toucher, Presigner
// and PartUploader are carried by Shaped (replicated_shape.go): filex decides
// what a storage can do by type-asserting those, so the wrapper must have them
// exactly when the primary does.
//
// ⚠⚠ Until 0.53 nothing built one outside the tests: the server's resolver
// handed out the bare driver, so a storage linked to a target replicated
// nothing and said nothing (#186, GitHub Discussion #91). The resolver's cache
// (internal/server storage_cache.go) is what wraps a linked storage now.

// Replication mode constants live in model.ReplicaMode* — referenced
// by the rule engine and config. Surface them again here only when
// the storage package needs them at the type level.

// FailureRecorder is the persistence sink for replica errors.
//
// The implementation lives in the replica/recorder.go subpackage and
// wraps db.Store; storage uses only this thin interface so it can
// avoid pulling the DB driver in tests. One recorder belongs to one
// storage: the rows it writes carry that storage's id.
type FailureRecorder interface {
	Record(ctx context.Context, path, op, errCode, errMsg string) error
	Resolve(ctx context.Context, path, op string) error
}

// RuleEngine returns a ReplicaMode for the given path. Concrete impl
// is in rules.go.
type RuleEngine interface {
	Match(path string) ReplicaMode
}

// EventNotifier emits replica events to the notify subsystem. We
// keep this as a tiny interface so the storage package doesn't import
// notify (and notify can't import storage either — there's already a
// dep in the other direction via handlers).
type EventNotifier interface {
	NotifyReplicaFail(ctx context.Context, path, op string, err error, attempt int)
	NotifyPrimaryReadFail(ctx context.Context, path string, err error)
}

// ReplicaMode is the return type of RuleEngine.Match.
type ReplicaMode string

// Mode values, mirroring model.ReplicaMode* but typed for the rules
// engine to avoid cross-package magic strings.
const (
	ModeMirror     ReplicaMode = "mirror"
	ModeAppendOnly ReplicaMode = "append_only"
	ModeSkip       ReplicaMode = "skip"
)

// ReplicatedDriver is the wrapper Driver. Construct via NewReplicated.
type ReplicatedDriver struct {
	primary  Driver
	replica  Driver // may be nil
	rules    RuleEngine
	failures FailureRecorder
	notifier EventNotifier
	logger   *slog.Logger
	// folder is the storage's own folder on the replica (WithFolder): every
	// path the wrapper hands the replica is inside it, so storages sharing a
	// target never meet. "" = the replica's root.
	folder string

	wg     sync.WaitGroup
	stopMu sync.Mutex
	stop   chan struct{}

	// closeOnce/closed: Close releases the drivers once, after the fan-outs
	// in flight have finished; closed is shut when that has happened.
	closeOnce sync.Once
	closed    chan struct{}
}

// errReplaced is recorded for a change that reached a wrapper after it was
// retired (Close/Stop): its storage was edited, relinked or unlinked while
// the request was in flight. The failure row is what Fix all replays through
// the wrapper that replaced it.
var errReplaced = errors.New("replica: the storage's replication was reconfigured while this change was in flight")

// NewReplicated wires a wrapper. replica may be nil — the wrapper
// then degenerates to a pure passthrough on writes (no fan-out).
//
// rules MUST be non-nil; pass DefaultRules() if you don't have a
// configured engine yet (it returns mirror for everything).
func NewReplicated(primary, replica Driver, rules RuleEngine, failures FailureRecorder, notifier EventNotifier) *ReplicatedDriver {
	return &ReplicatedDriver{
		primary:  primary,
		replica:  replica,
		rules:    rules,
		failures: failures,
		notifier: notifier,
		logger:   slog.Default(),
		stop:     make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

// WithFolder puts everything the wrapper writes to, reads from and compares
// on the replica inside folder (a storage's own folder on a target several
// storages share, internal/replica folder.go). Paths in failure rows stay the
// storage's own; the folder is applied on the way to the replica. Call it
// before the wrapper is used.
func (r *ReplicatedDriver) WithFolder(folder string) *ReplicatedDriver {
	r.folder = strings.Trim(strings.ReplaceAll(folder, "\\", "/"), "/")
	return r
}

// Folder is the storage's folder on the replica ("" = its root).
func (r *ReplicatedDriver) Folder() string { return r.folder }

// rp is p as the replica knows it: inside the storage's folder.
func (r *ReplicatedDriver) rp(p string) string {
	if r.folder == "" {
		return p
	}
	return path.Join("/", r.folder, strings.TrimLeft(p, "/"))
}

// Stop retires the wrapper and waits for the fan-outs in flight. A change
// that reaches it afterwards is not fanned out: it is recorded as a failure
// (REPLICA_DRIVER_REPLACED) so Fix all can replay it.
func (r *ReplicatedDriver) Stop() {
	r.halt()
	r.wg.Wait()
}

// halt retires the wrapper without waiting.
func (r *ReplicatedDriver) halt() {
	r.stopMu.Lock()
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	r.stopMu.Unlock()
}

// Halted reports whether the wrapper was retired (Stop or Close).
func (r *ReplicatedDriver) Halted() bool {
	select {
	case <-r.stop:
		return true
	default:
		return false
	}
}

// Close retires the wrapper at once and, in the background, waits for the
// fan-outs in flight before closing the primary and the replica (connections,
// a plugin's instance). It is what the resolver's cache calls when it drops
// the wrapper: the caller - an admin saving a storage - does not wait for a
// large file still on its way to the backup. Done says when it is over.
func (r *ReplicatedDriver) Close() error {
	r.halt()
	go r.release()
	return nil
}

// release waits for the fan-outs, then closes both drivers, once.
func (r *ReplicatedDriver) release() {
	r.wg.Wait()
	r.closeOnce.Do(func() {
		CloseDriver(r.primary)
		if r.replica != nil {
			CloseDriver(r.replica)
		}
		close(r.closed)
	})
}

// Done is closed once Close has drained the fan-outs and released the drivers.
func (r *ReplicatedDriver) Done() <-chan struct{} { return r.closed }

// replicated lets AsReplicated find the wrapper inside a shape.
func (r *ReplicatedDriver) replicated() *ReplicatedDriver { return r }

// AsReplicated returns the replication wrapper d is, or carries (Shaped), and
// false for a bare driver.
func AsReplicated(d Driver) (*ReplicatedDriver, bool) {
	h, ok := d.(interface{ replicated() *ReplicatedDriver })
	if !ok || h == nil {
		return nil, false
	}
	r := h.replicated()
	return r, r != nil
}

// Primary returns the underlying primary Driver — used by reconcile
// + report jobs that need direct access (e.g. counting objects).
func (r *ReplicatedDriver) Primary() Driver { return r.primary }

// Replica returns the underlying replica or nil.
func (r *ReplicatedDriver) Replica() Driver { return r.replica }

// HasReplica reports whether replication is configured.
func (r *ReplicatedDriver) HasReplica() bool { return r.replica != nil }

// Mode is the replica mode the rules give path. A leading "/" is not part of
// the match: filex writes paths both ways ("/docs/a.txt" from the catalogue,
// "docs/a.txt" from an upload key) and a rule `docs/**` means both. filex's
// own folders are ModeSkip before any rule (InternalPath).
func (r *ReplicatedDriver) Mode(p string) ReplicaMode {
	if InternalPath(p) {
		return ModeSkip
	}
	if r.rules == nil {
		return ModeMirror
	}
	return r.rules.Match(strings.TrimLeft(p, "/"))
}

// ─── Driver interface ─────────────────────────────────────────

// Init is a no-op — primary and replica are already initialized by
// the caller (server bootstrap).
func (r *ReplicatedDriver) Init(_ context.Context, _ map[string]any) error { return nil }

// Name returns the underlying primary's name (visible to operators).
func (r *ReplicatedDriver) Name() string { return r.primary.Name() }

// Capabilities mirrors the primary's capabilities — replica fan-out
// is invisible to clients above the wrapper.
func (r *ReplicatedDriver) Capabilities() Capabilities { return r.primary.Capabilities() }

// List always reads from primary (source of truth). On primary error
// we DO NOT fall back to the replica because a replica list could
// disagree with primary's state (e.g. mid-replication holes).
func (r *ReplicatedDriver) List(ctx context.Context, path string) ([]Object, error) {
	return r.primary.List(ctx, path)
}

// Stat reads from primary, with replica fallback on error so the
// admin UI doesn't 404 if the primary is briefly down.
func (r *ReplicatedDriver) Stat(ctx context.Context, path string) (Object, error) {
	o, err := r.primary.Stat(ctx, path)
	if err == nil {
		return o, nil
	}
	if !r.fallsBack(ctx, err) {
		return Object{}, err
	}
	o2, err2 := r.replica.Stat(ctx, r.rp(path))
	if err2 != nil {
		return Object{}, err
	}
	// The replica answers for its own path (inside the storage's folder);
	// the caller asked for its own.
	o2.Path = path
	return o2, nil
}

// Read tries primary, falls back to replica on error and emits a
// primary_read_fail notification.
func (r *ReplicatedDriver) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	rc, err := r.primary.Read(ctx, path)
	if err == nil {
		return rc, nil
	}
	if !r.fallsBack(ctx, err) {
		return nil, err
	}
	rc2, err2 := r.replica.Read(ctx, r.rp(path))
	if err2 != nil {
		return nil, err
	}
	if r.notifier != nil {
		r.notifier.NotifyPrimaryReadFail(ctx, path, err)
	}
	return rc2, nil
}

// fallsBack reports whether a primary read error is one the replica may
// answer for: the primary failed, not the request.
//
// ⚠ Never ErrNotFound. A file the primary does not have is not a primary
// outage: in `append_only` the backup keeps every deleted file, and a fallback
// on "not found" would serve deleted files again, make a deleted name read as
// taken (an upload is refused, a new file is checked as a modification) and
// raise primary_read_fail for every miss. A request the caller cancelled is
// not one either.
func (r *ReplicatedDriver) fallsBack(ctx context.Context, err error) bool {
	if r.replica == nil || err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) || ctx.Err() != nil {
		return false
	}
	return true
}

// ReadRange serves a byte window from the primary (falling back to the replica
// like Read). A driver without a RangeReader of its own is read from the start
// and the bytes before off are skipped: slower, never wrong.
func (r *ReplicatedDriver) ReadRange(ctx context.Context, path string, off, length int64) (io.ReadCloser, error) {
	rc, err := rangeOf(ctx, r.primary, path, off, length)
	if err == nil {
		return rc, nil
	}
	if !r.fallsBack(ctx, err) {
		return nil, err
	}
	rc2, err2 := rangeOf(ctx, r.replica, r.rp(path), off, length)
	if err2 != nil {
		return nil, err
	}
	if r.notifier != nil {
		r.notifier.NotifyPrimaryReadFail(ctx, path, err)
	}
	return rc2, nil
}

// rangeOf is d's ranged read, or the same window cut from its full read.
func rangeOf(ctx context.Context, d Driver, path string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("storage: negative range offset %d", off)
	}
	if rr, ok := d.(RangeReader); ok {
		return rr.ReadRange(ctx, path, off, length)
	}
	if length == 0 {
		return EmptyReadCloser(), nil
	}
	rc, err := d.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if _, err := io.CopyN(io.Discard, rc, off); err != nil {
			_ = rc.Close()
			if errors.Is(err, io.EOF) {
				// At or past the end: an empty answer, as the contract says.
				return EmptyReadCloser(), nil
			}
			return nil, err
		}
	}
	return LimitReadCloser(rc, length), nil
}

// ─── Writer ───────────────────────────────────────────────────

// Write writes to primary synchronously. On success, an async
// goroutine reads back from primary and writes to replica.
//
// We deliberately do not buffer the request body to allow a single
// fan-out write — replica writes happen via a primary read-back so
// the streaming primary write doesn't have to fork.
func (r *ReplicatedDriver) Write(ctx context.Context, path string, body io.Reader, size int64) error {
	w, ok := r.primary.(Writer)
	if !ok {
		return ErrUnsupported
	}
	if err := w.Write(ctx, path, body, size); err != nil {
		return err
	}
	r.afterWrite(path)
	return nil
}

// afterWrite fans a write that landed on the primary out to the replica.
// Shared by Write and CompleteMultipart (replicated_shape.go).
func (r *ReplicatedDriver) afterWrite(path string) {
	if r.replica == nil || r.Mode(path) == ModeSkip {
		return
	}
	r.dispatch(path, "write", func() { r.replicateWrite(path) })
}

// ─── Deleter ──────────────────────────────────────────────────

// Delete removes from primary. On success, an async goroutine
// removes from replica unless the path's rule is append_only or skip.
func (r *ReplicatedDriver) Delete(ctx context.Context, path string) error {
	d, ok := r.primary.(Deleter)
	if !ok {
		return ErrUnsupported
	}
	if err := d.Delete(ctx, path); err != nil {
		return err
	}
	if r.replica == nil {
		return nil
	}
	mode := r.Mode(path)
	if mode == ModeSkip || mode == ModeAppendOnly {
		return nil
	}
	r.dispatch(path, "delete", func() { r.replicateDelete(path) })
	return nil
}

// ─── Mover ────────────────────────────────────────────────────

// Move renames on primary, then re-issues the same move on replica
// asynchronously. Falls back to delete+write when the replica's
// driver doesn't support Mover.
func (r *ReplicatedDriver) Move(ctx context.Context, src, dst string) error {
	m, ok := r.primary.(Mover)
	if !ok {
		return ErrUnsupported
	}
	if err := m.Move(ctx, src, dst); err != nil {
		return err
	}
	if r.replica == nil {
		return nil
	}
	srcIn, dstIn := InternalPath(src), InternalPath(dst)
	switch {
	case srcIn && dstIn:
		return nil
	case dstIn:
		// Into filex's own folder - the trash: the file left the live tree,
		// and on the backup that is a delete (which its rule may keep).
		if r.Mode(src) == ModeMirror {
			r.dispatch(src, "delete", func() { r.replicateDelete(src) })
		}
		return nil
	case srcIn:
		// Out of it - a restore from the trash: a new file (or folder) in the
		// live tree, copied from the primary.
		if r.Mode(dst) != ModeSkip {
			r.dispatch(dst, "write", func() { r.replicateTree(dst) })
		}
		return nil
	}
	if r.Mode(dst) == ModeSkip {
		return nil
	}
	r.dispatch(dst, "move", func() { r.replicateMove(src, dst) })
	return nil
}

// ─── Copier ───────────────────────────────────────────────────

// Copy clones on primary, then async clones on replica.
func (r *ReplicatedDriver) Copy(ctx context.Context, src, dst string) error {
	c, ok := r.primary.(Copier)
	if !ok {
		return ErrUnsupported
	}
	if err := c.Copy(ctx, src, dst); err != nil {
		return err
	}
	if r.replica == nil {
		return nil
	}
	if r.Mode(dst) == ModeSkip {
		return nil
	}
	if InternalPath(src) {
		// From filex's own folder (a version restored over a file): the
		// replica has no source to copy from, so the result is copied.
		r.dispatch(dst, "write", func() { r.replicateTree(dst) })
		return nil
	}
	r.dispatch(dst, "copy", func() { r.replicateCopy(src, dst) })
	return nil
}

// ─── Mkdirer ──────────────────────────────────────────────────

// Mkdir creates the directory on primary, then on replica.
func (r *ReplicatedDriver) Mkdir(ctx context.Context, path string) error {
	mk, ok := r.primary.(Mkdirer)
	if !ok {
		return ErrUnsupported
	}
	if err := mk.Mkdir(ctx, path); err != nil {
		return err
	}
	if r.replica == nil {
		return nil
	}
	if r.Mode(path) == ModeSkip {
		return nil
	}
	r.dispatch(path, "", func() {
		if rmk, ok := r.replica.(Mkdirer); ok {
			ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = rmk.Mkdir(ctx2, r.rp(path))
		}
	})
	return nil
}

// ─── async dispatch helpers ──────────────────────────────────

// dispatch starts a goroutine for an async fan-out and tracks it on
// the wrapper's WaitGroup. Stop() drains the group via wg.Wait so
// every dispatched fn() runs to completion — we deliberately do NOT
// short-circuit a fan-out that was already dispatched, otherwise a Stop()
// called immediately after a Write+goroutine-scheduled-but-not-yet-running
// window would drop replica work without recording the failure.
//
// A change that reaches a RETIRED wrapper (Stop/Close: its storage was edited
// or relinked while the request was in flight) is not dispatched: the replica
// it would go to may be closing. It is recorded as a failure under (path, op)
// instead, so Fix all replays it through the wrapper that replaced this one.
// op "" (mkdir, mtime) is best effort and simply dropped.
//
// The check and wg.Add happen under stopMu, the lock halt closes the channel
// under, so no Add can follow the Wait that Stop or release starts.
func (r *ReplicatedDriver) dispatch(path, op string, fn func()) {
	r.stopMu.Lock()
	select {
	case <-r.stop:
		r.stopMu.Unlock()
		if op != "" {
			r.recordFail(context.Background(), path, op, "REPLICA_DRIVER_REPLACED", errReplaced)
		}
		return
	default:
	}
	r.wg.Add(1)
	r.stopMu.Unlock()
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

// replicateWrite reads back from primary and writes to replica.
func (r *ReplicatedDriver) replicateWrite(path string) {
	const op = "write"
	ctx := context.Background()
	code, err := r.copyOne(ctx, path)
	if err != nil {
		r.recordFail(ctx, path, op, code, err)
		return
	}
	r.markResolved(ctx, path, op)
}

// copyTimeout bounds one file's copy to the replica: five minutes, plus the
// time the file takes at 256 KiB/s. A flat five minutes (what the fan-out had
// before) failed every file larger than a slow link moves in that time.
func copyTimeout(size int64) time.Duration {
	if size < 0 {
		size = 0
	}
	return 5*time.Minute + time.Duration(size/(256<<10))*time.Second
}

// copyOne copies path from the primary to the replica as it is now, and
// carries its modification time across when the replica can keep one. It
// returns the failure code with the error ("" on success).
func (r *ReplicatedDriver) copyOne(ctx context.Context, path string) (string, error) {
	if r.replica == nil {
		return "REPLICA_NONE", errors.New("replica: no replica configured")
	}
	if u, ok := r.replica.(*unavailableDriver); ok {
		return "REPLICA_UNAVAILABLE", u.err
	}
	stat, err := r.primary.Stat(ctx, path)
	if err != nil {
		return "PRIMARY_STAT_FAIL", err
	}
	ctx, cancel := context.WithTimeout(ctx, copyTimeout(stat.Size))
	defer cancel()
	rc, err := r.primary.Read(ctx, path)
	if err != nil {
		return "PRIMARY_READBACK_FAIL", err
	}
	defer rc.Close()
	rw, ok := r.replica.(Writer)
	if !ok {
		return "REPLICA_NO_WRITER", fmt.Errorf("replica driver lacks Writer interface")
	}
	// Same reason as replicateCopy: the replica is where a file-onto-folder
	// collision does the real damage, because the prefix stops listing and its
	// objects leave the backup without a word. Record it instead.
	if err := EnsureFileTarget(ctx, r.replica, r.rp(path)); err != nil {
		return "REPLICA_KIND_CONFLICT", err
	}
	if err := rw.Write(ctx, r.rp(path), rc, stat.Size); err != nil {
		return "REPLICA_WRITE_FAIL", err
	}
	// The file's own time, not the moment it reached the backup: what a
	// restore gives back, and what the initial copy compares (ReplicaHolds).
	// Best effort - an object store keeps no settable time.
	if t, ok := r.replica.(Toucher); ok && !stat.Mtime.IsZero() {
		if err := t.SetMtime(ctx, r.rp(path), stat.Mtime); err != nil {
			r.logger.Debug("replica: modification time not kept",
				slog.String("path", path), slog.String("err", err.Error()))
		}
	}
	return "", nil
}

// CopyNow copies one file to the replica synchronously - the initial copy's
// step. On success any failure recorded for (path, write) is resolved; on
// failure the code and error are returned and nothing is recorded (the caller
// decides, without a notification per file).
func (r *ReplicatedDriver) CopyNow(ctx context.Context, path string) (code string, err error) {
	code, err = r.copyOne(ctx, path)
	if err == nil {
		r.markResolved(ctx, path, "write")
	}
	return code, err
}

// RecordFailure writes a failure row for (path, op) without a notification -
// for a caller that reports in bulk (the initial copy).
func (r *ReplicatedDriver) RecordFailure(ctx context.Context, path, op, code string, err error) {
	if r.failures == nil || err == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = r.failures.Record(rctx, path, op, code, err.Error())
}

// ReplicaHolds reports whether the replica already has the file o describes:
// the same size and a modification time that is the same (within two
// seconds: SMB and FTP keep coarse times) - or, on a replica that cannot keep
// a modification time (an object store), not older than the primary's.
// Anything it cannot tell reads as "not there", so the file is copied.
func (r *ReplicatedDriver) ReplicaHolds(ctx context.Context, o Object) bool {
	if r.replica == nil {
		return false
	}
	if _, bad := r.replica.(*unavailableDriver); bad {
		return false
	}
	got, err := r.replica.Stat(ctx, r.rp(o.Path))
	if err != nil || got.Kind == KindDirectory || got.Size != o.Size {
		return false
	}
	if o.Mtime.IsZero() || got.Mtime.IsZero() {
		return false
	}
	const slack = 2 * time.Second
	d := got.Mtime.Sub(o.Mtime)
	if d < slack && d > -slack {
		return true
	}
	if _, keepsTime := r.replica.(Toucher); !keepsTime {
		return d >= 0
	}
	return false
}

// ProbeReplica asks the replica one cheap question. nil means it answered -
// "not found" is an answer; an error means it could not be reached.
func (r *ReplicatedDriver) ProbeReplica(ctx context.Context) error {
	if r.replica == nil {
		return errors.New("replica: no replica configured")
	}
	if u, ok := r.replica.(*unavailableDriver); ok {
		return u.err
	}
	// The storage's folder, or the root: "not found" (a folder nothing was
	// written to yet) is an answer too.
	if _, err := r.replica.Stat(ctx, r.rp("/")); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Repair re-runs one failed fan-out against the replica: a write, move or
// copy re-copies the path from the primary, a delete removes it from the
// replica. On success the failure row for (path, op) is resolved. A path the
// primary no longer has leaves nothing to copy: that is resolved too (the
// delete that removed it fans out on its own). On failure the row is updated
// with the new error, without a notification.
//
// ⚠ A repaired move writes the new name; the old name stays on the backup
// (the failure row keeps only the destination).
func (r *ReplicatedDriver) Repair(ctx context.Context, path, op string) error {
	if r.replica == nil {
		return errors.New("replica: no replica configured")
	}
	switch op {
	case "write", "move", "copy":
		code, err := r.copyOne(ctx, path)
		if err != nil {
			if errors.Is(err, ErrNotFound) && (code == "PRIMARY_STAT_FAIL" || code == "PRIMARY_READBACK_FAIL") {
				r.markResolved(ctx, path, op)
				return nil
			}
			r.RecordFailure(ctx, path, op, code, err)
			return fmt.Errorf("%s: %w", code, err)
		}
		r.markResolved(ctx, path, op)
		return nil
	case "delete":
		if u, bad := r.replica.(*unavailableDriver); bad {
			return fmt.Errorf("REPLICA_UNAVAILABLE: %w", u.err)
		}
		rd, ok := r.replica.(Deleter)
		if !ok {
			return fmt.Errorf("replica driver lacks Deleter interface")
		}
		if err := rd.Delete(ctx, r.rp(path)); err != nil && !errors.Is(err, ErrNotFound) {
			r.RecordFailure(ctx, path, op, "REPLICA_DELETE_FAIL", err)
			return fmt.Errorf("REPLICA_DELETE_FAIL: %w", err)
		}
		r.markResolved(ctx, path, op)
		return nil
	default:
		return fmt.Errorf("replica: unknown op %q", op)
	}
}

// replicateDelete removes from replica.
func (r *ReplicatedDriver) replicateDelete(path string) {
	const op = "delete"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if u, bad := r.replica.(*unavailableDriver); bad {
		r.recordFail(ctx, path, op, "REPLICA_UNAVAILABLE", u.err)
		return
	}
	rd, ok := r.replica.(Deleter)
	if !ok {
		r.recordFail(ctx, path, op, "REPLICA_NO_DELETER", fmt.Errorf("replica driver lacks Deleter interface"))
		return
	}
	if err := rd.Delete(ctx, r.rp(path)); err != nil {
		r.recordFail(ctx, path, op, "REPLICA_DELETE_FAIL", err)
		return
	}
	r.markResolved(ctx, path, op)
}

// replicateMove first tries Mover; falls back to copy+delete when
// the replica's driver doesn't expose one.
func (r *ReplicatedDriver) replicateMove(src, dst string) {
	const op = "move"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if u, bad := r.replica.(*unavailableDriver); bad {
		r.recordFail(ctx, dst, op, "REPLICA_UNAVAILABLE", u.err)
		return
	}
	if mv, ok := r.replica.(Mover); ok {
		if err := mv.Move(ctx, r.rp(src), r.rp(dst)); err != nil {
			r.recordFail(ctx, dst, op, "REPLICA_MOVE_FAIL", err)
			return
		}
		r.markResolved(ctx, dst, op)
		return
	}
	// Fallback: copy then delete src.
	cp, okCp := r.replica.(Copier)
	dl, okDel := r.replica.(Deleter)
	if !okCp || !okDel {
		r.recordFail(ctx, dst, op, "REPLICA_NO_MOVER", fmt.Errorf("replica driver lacks Mover and Copier+Deleter fallback"))
		return
	}
	if err := cp.Copy(ctx, r.rp(src), r.rp(dst)); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_MOVE_COPY_FAIL", err)
		return
	}
	if err := dl.Delete(ctx, r.rp(src)); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_MOVE_DELETE_FAIL", err)
		return
	}
	r.markResolved(ctx, dst, op)
}

// replicateCopy mirrors a copy; falls back to read+write when the
// replica lacks a Copier (no server-side clone).
func (r *ReplicatedDriver) replicateCopy(src, dst string) {
	const op = "copy"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if u, bad := r.replica.(*unavailableDriver); bad {
		r.recordFail(ctx, dst, op, "REPLICA_UNAVAILABLE", u.err)
		return
	}
	if cp, ok := r.replica.(Copier); ok {
		if err := cp.Copy(ctx, r.rp(src), r.rp(dst)); err != nil {
			r.recordFail(ctx, dst, op, "REPLICA_COPY_FAIL", err)
			return
		}
		r.markResolved(ctx, dst, op)
		return
	}
	// Fallback: stream src→dst through Go.
	wr, ok := r.replica.(Writer)
	if !ok {
		r.recordFail(ctx, dst, op, "REPLICA_NO_COPIER", fmt.Errorf("replica driver lacks Copier and Writer fallback"))
		return
	}
	rc, err := r.replica.Read(ctx, r.rp(src))
	if err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_READ_FAIL", err)
		return
	}
	defer rc.Close()
	stat, err := r.replica.Stat(ctx, r.rp(src))
	if err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_STAT_FAIL", err)
		return
	}
	// Do not reproduce a file-onto-folder collision on the replica: that is the
	// side where it does the real damage — a prefix that stops listing takes
	// its objects out of the backup silently. Recorded as a failure so it is
	// visible instead of quietly corrupting the mirror.
	if err := EnsureFileTarget(ctx, r.replica, r.rp(dst)); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_KIND_CONFLICT", err)
		return
	}
	if err := wr.Write(ctx, r.rp(dst), rc, stat.Size); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_WRITE_FAIL", err)
		return
	}
	r.markResolved(ctx, dst, op)
}

// recordFail writes a failure row + emits a webhook+in-app event.
// Logged at warn level — production audit lives in DB and bell.
//
// The row is written on a context of its own: a fan-out that failed because
// its own deadline passed must still be able to say so.
func (r *ReplicatedDriver) recordFail(ctx context.Context, path, op, code string, err error) {
	r.logger.Warn("replica fan-out failed",
		slog.String("op", op),
		slog.String("path", path),
		slog.String("code", code),
		slog.String("err", err.Error()))
	r.RecordFailure(ctx, path, op, code, err)
	if r.notifier != nil {
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		r.notifier.NotifyReplicaFail(nctx, path, op, err, 1)
	}
}

// markResolved is the success counterpart — clears any prior failure
// row for the same (path, op).
func (r *ReplicatedDriver) markResolved(ctx context.Context, path, op string) {
	if r.failures == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = r.failures.Resolve(rctx, path, op)
}

// fanOutMtime carries a modification time set on the primary to the replica,
// best effort (Toucher on the shapes, replicated_shape.go).
func (r *ReplicatedDriver) fanOutMtime(path string, mtime time.Time) {
	if r.replica == nil || r.Mode(path) == ModeSkip {
		return
	}
	t, ok := r.replica.(Toucher)
	if !ok {
		return
	}
	r.dispatch(path, "", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = t.SetMtime(ctx, r.rp(path), mtime)
	})
}

// replicateTree copies p from the primary to the replica: the file, or - for
// a folder (restored from the trash) - every file under it that is not left
// out, one failure row per file that does not go.
func (r *ReplicatedDriver) replicateTree(p string) {
	ctx := context.Background()
	o, err := r.primary.Stat(ctx, p)
	if err != nil || o.Kind != KindDirectory {
		r.replicateWrite(p)
		return
	}
	r.copyTree(ctx, p, 0)
}

func (r *ReplicatedDriver) copyTree(ctx context.Context, dir string, depth int) {
	if depth >= MaxWalkDepth {
		return
	}
	objs, err := r.primary.List(ctx, dir)
	if err != nil {
		r.recordFail(ctx, dir, "write", "PRIMARY_LIST_FAIL", err)
		return
	}
	for _, o := range objs {
		name := o.Name
		if name == "" {
			name = path.Base(strings.TrimRight(o.Path, "/"))
		}
		child := path.Join(dir, name)
		if r.Mode(child) == ModeSkip {
			continue
		}
		switch o.Kind {
		case KindDirectory:
			r.copyTree(ctx, child, depth+1)
		case KindFile:
			r.replicateWrite(child)
		}
	}
}

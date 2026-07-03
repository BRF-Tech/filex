package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
// Copy/Delete/Mkdir sub-interfaces so callers can substitute it
// transparently for the underlying primary.

// Replication mode constants live in model.ReplicaMode* — referenced
// by the rule engine and config. Surface them again here only when
// the storage package needs them at the type level.

// FailureRecorder is the persistence sink for replica errors.
//
// The implementation lives in the replica/recorder.go subpackage and
// wraps db.Store; storage uses only this thin interface so it can
// avoid pulling the DB driver in tests.
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

	wg     sync.WaitGroup
	stopMu sync.Mutex
	stop   chan struct{}
}

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
	}
}

// Stop waits for in-flight async fan-outs.
func (r *ReplicatedDriver) Stop() {
	r.stopMu.Lock()
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	r.stopMu.Unlock()
	r.wg.Wait()
}

// Primary returns the underlying primary Driver — used by reconcile
// + report jobs that need direct access (e.g. counting objects).
func (r *ReplicatedDriver) Primary() Driver { return r.primary }

// Replica returns the underlying replica or nil.
func (r *ReplicatedDriver) Replica() Driver { return r.replica }

// HasReplica reports whether replication is configured.
func (r *ReplicatedDriver) HasReplica() bool { return r.replica != nil }

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
	if r.replica == nil {
		return Object{}, err
	}
	o2, err2 := r.replica.Stat(ctx, path)
	if err2 != nil {
		return Object{}, err
	}
	return o2, nil
}

// Read tries primary, falls back to replica on error and emits a
// primary_read_fail notification.
func (r *ReplicatedDriver) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	rc, err := r.primary.Read(ctx, path)
	if err == nil {
		return rc, nil
	}
	if r.replica == nil {
		return nil, err
	}
	rc2, err2 := r.replica.Read(ctx, path)
	if err2 != nil {
		return nil, err
	}
	if r.notifier != nil {
		r.notifier.NotifyPrimaryReadFail(ctx, path, err)
	}
	return rc2, nil
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
	if r.replica == nil {
		return nil
	}
	mode := r.rules.Match(path)
	if mode == ModeSkip {
		return nil
	}
	r.dispatch(func() { r.replicateWrite(path) })
	return nil
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
	mode := r.rules.Match(path)
	if mode == ModeSkip || mode == ModeAppendOnly {
		return nil
	}
	r.dispatch(func() { r.replicateDelete(path) })
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
	mode := r.rules.Match(dst)
	if mode == ModeSkip {
		return nil
	}
	r.dispatch(func() { r.replicateMove(src, dst) })
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
	mode := r.rules.Match(dst)
	if mode == ModeSkip {
		return nil
	}
	r.dispatch(func() { r.replicateCopy(src, dst) })
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
	mode := r.rules.Match(path)
	if mode == ModeSkip {
		return nil
	}
	r.dispatch(func() {
		if rmk, ok := r.replica.(Mkdirer); ok {
			ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = rmk.Mkdir(ctx2, path)
		}
	})
	return nil
}

// ─── async dispatch helpers ──────────────────────────────────

// dispatch starts a goroutine for an async fan-out and tracks it on
// the wrapper's WaitGroup. Stop() drains the group via wg.Wait so
// every queued fn() runs to completion — we deliberately do NOT
// short-circuit on r.stop, otherwise a Stop() called immediately
// after a Write+goroutine-scheduled-but-not-yet-running window would
// drop replica work without recording the failure.
func (r *ReplicatedDriver) dispatch(fn func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		fn()
	}()
}

// replicateWrite reads back from primary and writes to replica.
func (r *ReplicatedDriver) replicateWrite(path string) {
	const op = "write"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rc, err := r.primary.Read(ctx, path)
	if err != nil {
		r.recordFail(ctx, path, op, "PRIMARY_READBACK_FAIL", err)
		return
	}
	defer rc.Close()
	stat, err := r.primary.Stat(ctx, path)
	if err != nil {
		r.recordFail(ctx, path, op, "PRIMARY_STAT_FAIL", err)
		return
	}
	rw, ok := r.replica.(Writer)
	if !ok {
		r.recordFail(ctx, path, op, "REPLICA_NO_WRITER", fmt.Errorf("replica driver lacks Writer interface"))
		return
	}
	if err := rw.Write(ctx, path, rc, stat.Size); err != nil {
		r.recordFail(ctx, path, op, "REPLICA_WRITE_FAIL", err)
		return
	}
	r.markResolved(ctx, path, op)
}

// replicateDelete removes from replica.
func (r *ReplicatedDriver) replicateDelete(path string) {
	const op = "delete"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rd, ok := r.replica.(Deleter)
	if !ok {
		r.recordFail(ctx, path, op, "REPLICA_NO_DELETER", fmt.Errorf("replica driver lacks Deleter interface"))
		return
	}
	if err := rd.Delete(ctx, path); err != nil {
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
	if mv, ok := r.replica.(Mover); ok {
		if err := mv.Move(ctx, src, dst); err != nil {
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
	if err := cp.Copy(ctx, src, dst); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_MOVE_COPY_FAIL", err)
		return
	}
	if err := dl.Delete(ctx, src); err != nil {
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
	if cp, ok := r.replica.(Copier); ok {
		if err := cp.Copy(ctx, src, dst); err != nil {
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
	rc, err := r.replica.Read(ctx, src)
	if err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_READ_FAIL", err)
		return
	}
	defer rc.Close()
	stat, err := r.replica.Stat(ctx, src)
	if err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_STAT_FAIL", err)
		return
	}
	if err := wr.Write(ctx, dst, rc, stat.Size); err != nil {
		r.recordFail(ctx, dst, op, "REPLICA_COPY_WRITE_FAIL", err)
		return
	}
	r.markResolved(ctx, dst, op)
}

// recordFail writes a failure row + emits a webhook+in-app event.
// Logged at warn level — production audit lives in DB and bell.
func (r *ReplicatedDriver) recordFail(ctx context.Context, path, op, code string, err error) {
	r.logger.Warn("replica fan-out failed",
		slog.String("op", op),
		slog.String("path", path),
		slog.String("err", err.Error()))
	if r.failures != nil {
		_ = r.failures.Record(ctx, path, op, code, err.Error())
	}
	if r.notifier != nil {
		r.notifier.NotifyReplicaFail(ctx, path, op, err, 1)
	}
}

// markResolved is the success counterpart — clears any prior failure
// row for the same (path, op).
func (r *ReplicatedDriver) markResolved(ctx context.Context, path, op string) {
	if r.failures != nil {
		_ = r.failures.Resolve(ctx, path, op)
	}
}

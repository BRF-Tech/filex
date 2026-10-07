package replica

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// THE INITIAL COPY (#186, the maintainers' decision, 2026-10-06).
//
// The fan-out only sees changes made after a storage is linked to a target.
// What the storage ALREADY held is copied by this: in the background, from
// the queue, in short slices (TypeReplicaInitialCopy) that each walk part of
// the storage and queue the next one. Rules apply (`skip` paths are left
// out), a file the target already has with the same size and modification
// time is left alone (ReplicatedDriver.ReplicaHolds), and the walk's place is
// saved after every few files (replica_initial_copies.walk_cursor), so a
// restart - or a second instance - picks it up where it stopped.
//
// Two walks: one that only counts (so the page can say "1,234 of 5,000"),
// then the copy. Both are walkFiles: one folder listing per level in memory,
// never the storage.
//
// A failed file is a row in replica_failures (Fix all replays it) and the
// copy goes on. A RUN of failures with a target that does not answer is not
// the files' fault: the copy rewinds to before them, waits (phase `waiting`,
// the reason in last_error, one notification) and resumes on its own.

const (
	// defaultSlice is how long one slice works before it saves and queues
	// the next: short enough that other queued work is not held up for long.
	defaultSlice = 45 * time.Second
	// defaultRetryAfter is how long a copy waits when the target or the
	// primary stopped answering.
	defaultRetryAfter = 10 * time.Minute
	// leaseMargin is how long past its slice a claim holds, for a large file
	// still on its way when the slice's time is up. Saves renew it.
	leaseMargin = 10 * time.Minute
	// saveEvery is how often a slice writes its place down while it works.
	saveEvery = 10 * time.Second
	// failRun is how many files in a row may fail before the target is asked
	// whether it is there at all.
	failRun = 10
	// initialCopyPriority is below the default: a backup catching up is the
	// least urgent thing in the queue.
	initialCopyPriority = 10
)

// errLeaseLost: another worker holds the copy now (the claim lapsed, or the
// copy was restarted); this slice stops without a word.
var errLeaseLost = errors.New("replica: initial copy claimed elsewhere")

// targetDown stops a slice whose target does not answer.
type targetDown struct{ err error }

func (t *targetDown) Error() string { return "replication target unreachable: " + t.err.Error() }
func (t *targetDown) Unwrap() error { return t.err }

// InitialCopyDedupKey keeps at most one waiting slice per storage.
func InitialCopyDedupKey(storageID int64) string {
	return queue.TypeReplicaInitialCopy + ":" + strconv.FormatInt(storageID, 10)
}

// SetSlice shortens or lengthens a slice (tests; files <= 0 means no file
// limit).
func (s *Service) SetSlice(d time.Duration, files int) {
	if d > 0 {
		s.slice = d
	}
	s.sliceFiles = files
}

// SetClock replaces the clock the initial copy reads (tests).
func (s *Service) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// StartInitialCopy makes sure the storage has an initial copy to targetID and
// queues its next slice. restart=false keeps a copy already there for that
// target (a finished one is not run again); restart=true begins it anew.
func (s *Service) StartInitialCopy(ctx context.Context, storageID, targetID int64, restart bool) (*model.ReplicaInitialCopy, error) {
	c, _, err := s.store.StartReplicaInitialCopy(ctx, storageID, targetID, s.now().Unix(), restart)
	if err != nil {
		return nil, err
	}
	if c.Phase == model.ReplicaCopyDone {
		return c, nil
	}
	if err := s.enqueueSlice(ctx, storageID, targetID, time.Time{}); err != nil {
		return c, err
	}
	return c, nil
}

// LinkChanged brings the storage's initial copy in line with its link as the
// row says now: linked to an enabled target - a copy to that target (a new
// one when the target changed); not linked - none.
func (s *Service) LinkChanged(ctx context.Context, storageID int64) error {
	return s.syncInitialCopy(ctx, storageID, false)
}

// RestartInitialCopy is LinkChanged that begins the copy anew even when one
// to the same target finished: the target's configuration changed (another
// bucket, another share), or it was switched back on after the changes made
// while it was off went nowhere.
func (s *Service) RestartInitialCopy(ctx context.Context, storageID int64) error {
	return s.syncInitialCopy(ctx, storageID, true)
}

// syncInitialCopy brings the storage's folder on its target and its initial
// copy in line with the row:
//   - not linked (or the storage or its target is gone): no folder, no copy;
//   - linked to a target that is switched off: the folder is KEPT (a target
//     switched on again must not hand the name to another storage), no copy;
//   - linked to an enabled target: the folder, and the copy.
func (s *Service) syncInitialCopy(ctx context.Context, storageID int64, restart bool) error {
	st, targetID, enabled, err := s.linkOf(ctx, storageID)
	if err != nil {
		return err
	}
	if targetID == 0 {
		if err := s.store.DeleteReplicaLink(ctx, storageID); err != nil {
			return err
		}
		return s.store.DeleteReplicaInitialCopy(ctx, storageID)
	}
	if _, err := EnsureFolder(ctx, s.store, st, targetID); err != nil {
		return err
	}
	if !enabled {
		return s.store.DeleteReplicaInitialCopy(ctx, storageID)
	}
	_, err = s.StartInitialCopy(ctx, storageID, targetID, restart)
	return err
}

// linkOf is the storage and the target it is linked to (0 for none, the
// storage or the target gone), and whether that target is enabled.
func (s *Service) linkOf(ctx context.Context, storageID int64) (*model.Storage, int64, bool, error) {
	st, err := s.store.GetStorage(ctx, storageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	if st == nil || st.ReplicaTargetID == nil {
		return st, 0, false, nil
	}
	t, err := s.store.GetReplicationTarget(ctx, *st.ReplicaTargetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return st, 0, false, nil
		}
		return st, 0, false, err
	}
	if t == nil {
		return st, 0, false, nil
	}
	return st, t.ID, t.Enabled, nil
}

// ResumeInitialCopies runs once at start: every storage that replicates gets
// its initial copy - a new one when it has none (the upgrade from a version
// whose replication never ran: the copy runs ONCE, for links that never had
// one) - and every copy still under way gets its next slice queued. Rows of
// storages that no longer replicate are dropped.
func (s *Service) ResumeInitialCopies(ctx context.Context) error {
	if s.queue == nil {
		return nil
	}
	linked, err := s.Linked(ctx)
	if err != nil {
		return err
	}
	keep := map[int64]bool{}
	for _, st := range linked {
		keep[st.ID] = true
		if _, err := EnsureFolder(ctx, s.store, st, *st.ReplicaTargetID); err != nil {
			slog.Warn("replica: no folder on the target for a linked storage",
				slog.String("storage", st.Name), slog.String("err", err.Error()))
			continue
		}
		if _, err := s.StartInitialCopy(ctx, st.ID, *st.ReplicaTargetID, false); err != nil {
			slog.Warn("replica: initial copy could not be queued",
				slog.String("storage", st.Name), slog.String("err", err.Error()))
		}
	}
	rows, err := s.store.ListReplicaInitialCopies(ctx)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if !keep[c.StorageID] {
			_ = s.store.DeleteReplicaInitialCopy(ctx, c.StorageID)
		}
	}
	// A folder kept for a link the storage no longer has (unlinked, relinked,
	// deleted while this process was down). One for a target that is only
	// switched off stays.
	if links, err := s.store.ListReplicaLinks(ctx); err == nil {
		target := map[int64]int64{}
		if storages, err := s.store.ListStorages(ctx); err == nil {
			for _, st := range storages {
				if st.ReplicaTargetID != nil {
					target[st.ID] = *st.ReplicaTargetID
				}
			}
			for _, l := range links {
				if target[l.StorageID] != l.TargetID {
					_ = s.store.DeleteReplicaLink(ctx, l.StorageID)
				}
			}
		}
	}
	return nil
}

// InitialCopyStatus is a storage's initial copy as the Replication page shows
// it: the row, with the storage's and the target's names.
type InitialCopyStatus struct {
	*model.ReplicaInitialCopy
	StorageName string `json:"storage_name"`
	TargetName  string `json:"target_name"`
	// Done is Copied+Present+Excluded+Failed.
	Done int64 `json:"done"`
}

// InitialCopies lists every storage's initial copy, for the page.
func (s *Service) InitialCopies(ctx context.Context) ([]InitialCopyStatus, error) {
	rows, err := s.store.ListReplicaInitialCopies(ctx)
	if err != nil {
		return nil, err
	}
	names, targets := s.pageNames(ctx)
	out := make([]InitialCopyStatus, 0, len(rows))
	for _, c := range rows {
		out = append(out, InitialCopyStatus{
			ReplicaInitialCopy: c,
			StorageName:        storageLabel(c.StorageID, names[c.StorageID]),
			TargetName:         targets[c.TargetID],
			Done:               c.Done(),
		})
	}
	return out, nil
}

// enqueueSlice queues the next slice; one already waiting absorbs it.
func (s *Service) enqueueSlice(ctx context.Context, storageID, targetID int64, notBefore time.Time) error {
	if s.queue == nil {
		return fmt.Errorf("replica initial copy: queue not configured")
	}
	op := queue.Op{
		Type: queue.TypeReplicaInitialCopy,
		Payload: map[string]any{
			"storage_id": storageID,
			"target_id":  targetID,
		},
		Priority:    initialCopyPriority,
		MaxAttempts: 5,
		DedupKey:    InitialCopyDedupKey(storageID),
	}
	if !notBefore.IsZero() {
		op.NotBefore = &notBefore
	}
	if _, err := s.queue.Enqueue(ctx, op); err != nil && !errors.Is(err, queue.ErrDuplicate) {
		return err
	}
	return nil
}

func newLeaseOwner() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// HandleInitialCopy is the queue.Handler for TypeReplicaInitialCopy: one
// slice of one storage's initial copy.
func (s *Service) HandleInitialCopy(ctx context.Context, op queue.Op) error {
	if s.wrappers == nil {
		return fmt.Errorf("replica initial copy: %w", ErrNoReplica)
	}
	storageID := payloadInt(op.Payload["storage_id"])
	targetID := payloadInt(op.Payload["target_id"])
	row, err := s.store.GetReplicaInitialCopy(ctx, storageID)
	if err != nil {
		return err
	}
	if row == nil || row.Phase == model.ReplicaCopyDone {
		return nil
	}
	if targetID != 0 && row.TargetID != targetID {
		return nil // a slice of an earlier link; the new link's copy has its own
	}
	link, err := s.wrappers.Replicated(ctx, storageID)
	if errors.Is(err, ErrNotLinked) {
		return s.store.DeleteReplicaInitialCopy(ctx, storageID)
	}
	if err != nil {
		return err
	}
	if link.TargetID != row.TargetID || link.Driver == nil {
		return nil
	}
	now := s.now()
	owner := newLeaseOwner()
	ok, err := s.store.ClaimReplicaInitialCopy(ctx, storageID, row.TargetID, owner, now.Unix(), now.Add(s.slice+leaseMargin).Unix())
	if err != nil {
		return err
	}
	if !ok {
		return s.comeBack(ctx, storageID)
	}
	row, err = s.store.GetReplicaInitialCopy(ctx, storageID)
	if err != nil || row == nil {
		return err
	}
	return s.runSlice(ctx, link.Driver, row, owner)
}

// comeBack is a slice that found the copy held by another worker's lease.
// That worker queues the next slice when it finishes - unless it died holding
// the lease (a crash, a killed instance), and then nobody would. So this one
// queues a slice of its own for when the lease runs out, but at most a minute
// from now: a live holder that finished meanwhile finds it waiting (its own
// next slice is absorbed by it), and the copy loses a minute, not its way.
func (s *Service) comeBack(ctx context.Context, storageID int64) error {
	row, err := s.store.GetReplicaInitialCopy(ctx, storageID)
	if err != nil || row == nil || row.Phase == model.ReplicaCopyDone {
		return err
	}
	now := s.now()
	at := time.Unix(row.LeaseUntil+1, 0)
	if limit := now.Add(time.Minute); at.After(limit) {
		at = limit
	}
	if at.Before(now) {
		at = now
	}
	return s.enqueueSlice(ctx, storageID, row.TargetID, at)
}

// runSlice does one slice of work on c, which `owner` holds.
func (s *Service) runSlice(ctx context.Context, rd *storage.ReplicatedDriver, c *model.ReplicaInitialCopy, owner string) error {
	start := s.now()
	deadline := start.Add(s.slice)
	lastSave := start
	files := 0
	wasWaiting := c.Phase == model.ReplicaCopyWaiting
	if c.Counted {
		c.Phase = model.ReplicaCopyCopying
	} else {
		c.Phase = model.ReplicaCopyCounting
	}

	save := func(sctx context.Context, release bool) error {
		now := s.now()
		if c.Done() > c.Total {
			c.Total = c.Done()
		}
		c.UpdatedUnix = now.Unix()
		if release {
			c.LeaseOwner, c.LeaseUntil = "", 0
		} else {
			c.LeaseOwner, c.LeaseUntil = owner, now.Add(s.slice+leaseMargin).Unix()
		}
		ok, err := s.store.SaveReplicaInitialCopy(sctx, c, owner)
		if err != nil {
			return err
		}
		if !ok {
			return errLeaseLost
		}
		return nil
	}
	over := func() bool {
		if s.sliceFiles > 0 && files >= s.sliceFiles {
			return true
		}
		return !s.now().Before(deadline)
	}
	checkpoint := func() error {
		if now := s.now(); now.Sub(lastSave) >= saveEvery {
			lastSave = now
			return save(ctx, false)
		}
		return nil
	}

	var walkErr error
	if !c.Counted {
		walkErr = walkFiles(ctx, rd.Primary(), c.Cursor, func(o storage.Object) error {
			if over() {
				return errSliceOver
			}
			files++
			c.Total++
			c.Cursor = o.Path
			return checkpoint()
		})
		if walkErr == nil {
			c.Counted = true
			c.Cursor = ""
			c.Phase = model.ReplicaCopyCopying
		}
	}
	if c.Counted && walkErr == nil {
		walkErr = s.copyWalk(ctx, rd, c, &files, over, checkpoint)
	}

	// The slice's outcome is written on a context of its own: a server
	// stopping mid-slice still records how far it got.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var down *targetDown
	switch {
	case walkErr == nil:
		c.Phase = model.ReplicaCopyDone
		c.Cursor = ""
		c.LastError = ""
		c.FinishedUnix = s.now().Unix()
		c.Total = c.Done()
		if err := save(wctx, true); err != nil && !errors.Is(err, errLeaseLost) {
			return err
		}
		slog.Info("replica: initial copy finished",
			slog.Int64("storage_id", c.StorageID), slog.Int64("copied", c.Copied),
			slog.Int64("present", c.Present), slog.Int64("excluded", c.Excluded), slog.Int64("failed", c.Failed))
		return nil
	case errors.Is(walkErr, errLeaseLost):
		return nil
	case errors.Is(walkErr, errSliceOver):
		c.LastError = ""
		if err := save(wctx, true); err != nil {
			if errors.Is(err, errLeaseLost) {
				return nil
			}
			return err
		}
		return s.enqueueSlice(wctx, c.StorageID, c.TargetID, time.Time{})
	case errors.As(walkErr, &down), ctx.Err() == nil:
		// The target (or, during a listing, the primary) stopped answering:
		// wait, then go on from the cursor - which a run of failures has
		// already been rewound to the start of.
		c.Phase = model.ReplicaCopyWaiting
		c.LastError = walkErr.Error()
		if err := save(wctx, true); err != nil {
			if errors.Is(err, errLeaseLost) {
				return nil
			}
			return err
		}
		if !wasWaiting {
			s.tellWaiting(wctx, c, walkErr)
		}
		return s.enqueueSlice(wctx, c.StorageID, c.TargetID, s.now().Add(s.retryAfter))
	default:
		// The server is stopping: keep the place. The queue gives the op back
		// at the next start, and ResumeInitialCopies queues one anyway.
		if err := save(wctx, true); err != nil && !errors.Is(err, errLeaseLost) {
			return err
		}
		return walkErr
	}
}

// copyWalk is the second walk: each file is left out by a `skip` rule, found
// on the target already, or copied.
func (s *Service) copyWalk(ctx context.Context, rd *storage.ReplicatedDriver, c *model.ReplicaInitialCopy, files *int, over func() bool, checkpoint func() error) error {
	runStart := c.Cursor
	runLen := 0
	return walkFiles(ctx, rd.Primary(), c.Cursor, func(o storage.Object) error {
		if over() {
			return errSliceOver
		}
		*files++
		switch {
		case rd.Mode(o.Path) == storage.ModeSkip:
			c.Excluded++
			runLen = 0
		case rd.ReplicaHolds(ctx, o):
			c.Present++
			runLen = 0
		default:
			code, err := rd.CopyNow(ctx, o.Path)
			switch {
			case err == nil:
				c.Copied++
				c.CopiedBytes += o.Size
				runLen = 0
			case ctx.Err() != nil:
				return ctx.Err() // stopping: this file is not done
			case errors.Is(err, storage.ErrNotFound) && (code == "PRIMARY_STAT_FAIL" || code == "PRIMARY_READBACK_FAIL"):
				// Deleted since it was listed: nothing to copy.
				runLen = 0
			default:
				if runLen == 0 {
					runStart = c.Cursor
				}
				runLen++
				c.Failed++
				rd.RecordFailure(ctx, o.Path, "write", code, err)
				if runLen >= failRun {
					if perr := rd.ProbeReplica(ctx); perr != nil {
						c.Failed -= int64(runLen)
						c.Cursor = runStart
						return &targetDown{err: perr}
					}
					runLen = 0 // the target answers: these files fail on their own
				}
			}
		}
		c.Cursor = o.Path
		return checkpoint()
	})
}

// tellWaiting raises one notification when a copy starts waiting.
func (s *Service) tellWaiting(ctx context.Context, c *model.ReplicaInitialCopy, why error) {
	if s.notifier == nil {
		return
	}
	name := ""
	if st, err := s.store.GetStorage(ctx, c.StorageID); err == nil && st != nil {
		name = st.Name
	}
	label := storageLabel(c.StorageID, name)
	_, _ = s.notifier.Send(ctx, notify.Event{
		Event:    notify.EventReplicaFail,
		Severity: notify.SeverityWarning,
		Title:    "Replica copy failed",
		Body: fmt.Sprintf("The initial copy of %s to its replication target is waiting and will resume on its own: %s",
			label, why.Error()),
		Meta: map[string]any{
			"path":       label,
			"op":         "copy",
			"error":      why.Error(),
			"storage_id": c.StorageID,
			"storage":    label,
			"initial":    true,
		},
	})
}

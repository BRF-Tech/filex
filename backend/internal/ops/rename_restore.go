package ops

// A rename, a restore from the trash and a permanent delete of a trash entry
// as jobs of this queue.
//
// ⚠⚠ Why here (2026-09-26). Both ran inside the request. A folder renamed, or
// brought back from the trash, on an object store is one request per object,
// and the dialog waited with nothing on screen until the proxy gave up (nginx
// after 60 s, Cloudflare after 100 s) — then said it had failed while the
// server carried on. As jobs they answer at once, show in the operations
// centre, and run one at a time, so a second press cannot overlap the first.
//
// ⚠⚠ On a lane of their own (runFinishingLane), never on the queue's single
// worker: each lasts as long as its folder does, and behind a large folder
// renamed, restored or purged on an object store every copy, move, delete and
// staged-upload commit of the instance used to wait (lesson #444).
//
// All three finish what they start. A running one has no cancel handle (a
// pending one can still be cancelled), and its storage work runs on a context
// the worker's shutdown does not cut. A rename or a restore stopped half-way
// leaves a folder in two places, and the retry is refused because the half
// that arrived holds the name; a purge stopped half-way leaves a folder half
// gone with its row still in the trash. A process that dies in the middle of
// one anyway leaves its row `running`, and the next start requeues it: a
// rename then carries its move on (runRename).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
)

const (
	// OpRename gives ONE item a new name within its storage: Sources holds it
	// and Dest the exact path it becomes. It is not a move. A person chose
	// the name, so a destination that is taken fails the op (ErrNameTaken)
	// instead of landing beside it as `name-copy`, and nothing is replaced.
	OpRename = "rename"
	// OpRestore brings trash entries back where they came from. Sources holds
	// their node ids; the Restorer does the work, the same call
	// POST /api/files/manager/restore makes.
	OpRestore = "restore"
	// OpPurge takes trash entries out for good. Sources holds their node ids;
	// the Purger does the work, the same call DELETE /api/admin/trash/{id}
	// makes. It ran inside that request, and a folder is purged one object and
	// one row at a time: the admin page's client gave up after 30 s.
	OpPurge = "purge"
)

// ErrNameTaken is a rename refused because something already has the name.
// Nothing moved.
var ErrNameTaken = errors.New("something with that name already exists here")

// Restorer brings one trash entry back: the bytes to their original path and
// the row out of the trash. handlers.Trash implements it; wired with
// SetRestorer. An entry whose place is taken is an error, and nothing of it
// moves.
//
// storageID is the storage the job was queued for (Op.StorageID): an entry
// that lives in another one is refused, not restored. The handler that queued
// the job judged its entries on that storage, and a row is the only thing the
// worker has to go on.
type Restorer interface {
	RestoreNode(ctx context.Context, storageID, nodeID int64) error
}

// SetRestorer wires the restore behind an OpRestore row. Without it the row
// fails rather than claiming to have restored anything.
func (s *Service) SetRestorer(r Restorer) { s.restorer = r }

// Purger takes one trash entry out for good: its bytes, its rows and its
// descendants'. handlers.Trash implements it; wired with SetPurger. storageID
// is the job's, as for Restorer.
type Purger interface {
	PurgeNode(ctx context.Context, storageID, nodeID int64) error
}

// SetPurger wires the purge behind an OpPurge row. Without it the row fails
// rather than claiming to have purged anything.
func (s *Service) SetPurger(p Purger) { s.purger = p }

// RenameSync is what a DBSync adds to serve a rename (handlers.Manager does):
// the one rule for whether a name is taken — the catalogue's live rows as well
// as the storage, and a change of case alone is not the item colliding with
// itself — and the catalogue's side of the rename, announced as a rename.
type RenameSync interface {
	NameTaken(ctx context.Context, storageID int64, src, dst string) (bool, error)
	SyncRename(ctx context.Context, storageID int64, src, dst string)
}

// finishesOnceStarted is a kind that runs to the end once the worker took it.
func finishesOnceStarted(kind string) bool {
	return kind == OpRename || kind == OpRestore || kind == OpPurge
}

// finishingKinds is the SQL list of the kinds finishesOnceStarted names: the
// finishing lane's.
const finishingKinds = `'` + OpRename + `', '` + OpRestore + `', '` + OpPurge + `'`

// runFinishingLane runs renames, restores and purges, one at a time, beside
// the main worker and the archive lane (the file comment says why). One at a
// time among themselves: two renames of the same folder, or a restore and a
// purge of the same entry, must not overlap — and finishMu holds that across
// a Stop and the next Run as well.
func (s *Service) runFinishingLane(ctx context.Context, stop <-chan struct{}) {
	// The lane's jobs see Stop as well as the server's own shutdown: a job
	// stops between two of its entries when either comes (execute).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		for s.runOneFinishing(ctx, stop) {
		}
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-s.finishWake:
		case <-t.C:
		}
	}
}

// runOneFinishing claims and runs the oldest waiting finishing job. It
// reports whether it ran one.
func (s *Service) runOneFinishing(ctx context.Context, stop <-chan struct{}) bool {
	s.finishMu.Lock()
	defer s.finishMu.Unlock()
	select {
	case <-stop:
		return false
	default:
	}
	if ctx.Err() != nil {
		return false
	}
	op, ok, err := s.claimWhere(ctx, `kind IN (`+finishingKinds+`)`)
	if err != nil {
		slog.Warn("ops: claim next rename, restore or purge", slog.String("err", err.Error()))
		return false
	}
	if !ok {
		return false
	}
	s.execute(ctx, op)
	return true
}

// checkRename refuses a rename SubmitTo cannot run.
func checkRename(sources []string, dest string) error {
	if len(sources) != 1 {
		return errors.New("ops: a rename takes exactly one item")
	}
	if strings.HasSuffix(dest, "/") || normOpPath(dest) == "" {
		return errors.New("ops: a rename needs the item's new path")
	}
	return nil
}

// trashIDs reads the sources of an OpRestore or OpPurge row: trash entries by
// node id.
func trashIDs(sources []string) ([]int64, error) {
	ids := make([]int64, 0, len(sources))
	for _, raw := range sources {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ops: a restore or a purge names trash entries by id, not %q", raw)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Service) runRename(ctx context.Context, drv storage.Driver, op *Op, src string) error {
	m, ok := drv.(storage.Mover)
	if !ok {
		return errors.New("driver not movable")
	}
	rs, ok := s.dbsync.(RenameSync)
	if !ok {
		return errors.New("ops: no rename sync wired")
	}
	work := context.WithoutCancel(ctx)
	dst := normOpPath(op.Dest)
	if err := s.refuseUnsettledEncryption(work, drv, op, src, dst); err != nil {
		return err
	}
	// ⚠ Asked again, although the handler asked when it queued the rename:
	// other work may have run in between, and every driver's Move replaces
	// what holds the name.
	taken, err := rs.NameTaken(work, op.StorageID, src, dst)
	if err != nil {
		return fmt.Errorf("could not tell whether the name is free: %w", err)
	}
	if taken && !op.resumed {
		return ErrNameTaken
	}
	// ⚠ The storage's row gate, from the first byte that moves to the last row
	// that follows (internal/rowgate, issue #192). Without it a scan running
	// beside the rename saw the folder half way - listed in its parent before
	// the bytes moved, listed itself after - judged everything in it gone, and
	// dropped the rows a moment before SyncRename re-homed them: the renamed
	// folder opened empty.
	release := rowgate.Move(op.StorageID)
	defer release()
	if taken {
		// ⚠⚠ Carried on after a restart (op.resumed: this row had begun when
		// the previous process stopped). The name is held by the rename's own
		// half: an object store moves a folder one object at a time, and what
		// had arrived is at the new name while the rest is still at the old.
		// Failing here — as it did — left the folder in two places and told
		// the person to rename part of it themselves.
		if !storage.Exists(work, drv, src) {
			// Every object had moved; the catalogue's side had not (or its
			// end was never written). Do that part.
			rs.SyncRename(work, op.StorageID, src, dst)
			return nil
		}
		// What is left at the old name joins the rest: a folder move walks the
		// objects still under src, and each lands where its moved siblings
		// already are.
	}
	if err := m.Move(work, src, dst); err != nil {
		return err
	}
	rs.SyncRename(work, op.StorageID, src, dst)
	return nil
}

func (s *Service) runRestore(ctx context.Context, op *Op, src string) error {
	if s.restorer == nil {
		return errors.New("ops: no restorer wired")
	}
	ids, err := trashIDs([]string{src})
	if err != nil {
		return err
	}
	// A restore is a two-step change too: the bytes come out of the trash,
	// then the rows do. A scan between the two would find the bytes back at
	// their path with no live row and catalogue them as a new file, which
	// would then hold the place the restored row is going back to (rowgate).
	release := rowgate.Move(op.StorageID)
	defer release()
	return s.restorer.RestoreNode(context.WithoutCancel(ctx), op.StorageID, ids[0])
}

func (s *Service) runPurge(ctx context.Context, op *Op, src string) error {
	if s.purger == nil {
		return errors.New("ops: no purger wired")
	}
	ids, err := trashIDs([]string{src})
	if err != nil {
		return err
	}
	return s.purger.PurgeNode(context.WithoutCancel(ctx), op.StorageID, ids[0])
}

// cancellable is what a row advertises: a row waiting in the queue can be
// cancelled, and a running one unless it finishes what it starts.
func cancellable(op *Op) bool {
	return op.Status == StatusPending || (op.Status == StatusRunning && !finishesOnceStarted(op.Kind))
}

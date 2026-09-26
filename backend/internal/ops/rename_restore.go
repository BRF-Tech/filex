package ops

// A rename and a restore from the trash as jobs of this queue.
//
// ⚠⚠ Why here (2026-09-26). Both ran inside the request. A folder renamed, or
// brought back from the trash, on an object store is one request per object,
// and the dialog waited with nothing on screen until the proxy gave up (nginx
// after 60 s, Cloudflare after 100 s) — then said it had failed while the
// server carried on. As jobs they answer at once, show in the operations
// centre, and run on the one worker, so a second press cannot overlap the
// first.
//
// Both finish what they start. A running rename or restore has no cancel
// handle (a pending one can still be cancelled), and its storage work runs on
// a context the worker's shutdown does not cut: stopped half-way, a folder is
// left in two places, and the retry is refused because the half that arrived
// holds the name.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
)

// ErrNameTaken is a rename refused because something already has the name.
// Nothing moved.
var ErrNameTaken = errors.New("something with that name already exists here")

// Restorer brings one trash entry back: the bytes to their original path and
// the row out of the trash. handlers.Trash implements it; wired with
// SetRestorer. An entry whose place is taken is an error, and nothing of it
// moves.
type Restorer interface {
	RestoreNode(ctx context.Context, nodeID int64) error
}

// SetRestorer wires the restore behind an OpRestore row. Without it the row
// fails rather than claiming to have restored anything.
func (s *Service) SetRestorer(r Restorer) { s.restorer = r }

// RenameSync is what a DBSync adds to serve a rename (handlers.Manager does):
// the one rule for whether a name is taken — the catalogue's live rows as well
// as the storage, and a change of case alone is not the item colliding with
// itself — and the catalogue's side of the rename, announced as a rename.
type RenameSync interface {
	NameTaken(ctx context.Context, storageID int64, src, dst string) (bool, error)
	SyncRename(ctx context.Context, storageID int64, src, dst string)
}

// finishesOnceStarted is a kind that runs to the end once the worker took it.
func finishesOnceStarted(kind string) bool { return kind == OpRename || kind == OpRestore }

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

// restoreIDs reads an OpRestore row's sources.
func restoreIDs(sources []string) ([]int64, error) {
	ids := make([]int64, 0, len(sources))
	for _, raw := range sources {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("ops: a restore names trash entries by id, not %q", raw)
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
	// ⚠ Asked again, although the handler asked when it queued the rename:
	// other work may have run in between, and every driver's Move replaces
	// what holds the name.
	taken, err := rs.NameTaken(work, op.StorageID, src, dst)
	if err != nil {
		return fmt.Errorf("could not tell whether the name is free: %w", err)
	}
	if taken {
		return ErrNameTaken
	}
	if err := m.Move(work, src, dst); err != nil {
		return err
	}
	rs.SyncRename(work, op.StorageID, src, dst)
	return nil
}

func (s *Service) runRestore(ctx context.Context, src string) error {
	if s.restorer == nil {
		return errors.New("ops: no restorer wired")
	}
	ids, err := restoreIDs([]string{src})
	if err != nil {
		return err
	}
	return s.restorer.RestoreNode(context.WithoutCancel(ctx), ids[0])
}

// cancellable is what a row advertises: a row waiting in the queue can be
// cancelled, and a running one unless it finishes what it starts.
func cancellable(op *Op) bool {
	return op.Status == StatusPending || (op.Status == StatusRunning && !finishesOnceStarted(op.Kind))
}

package protocolsync

// The protocol servers' two-step changes, from the first byte to the last row,
// under the storage's row gate (internal/rowgate, issues #192 and #201).
//
// A rename, a delete into the trash and a delete for good change the storage
// first and the catalogue after. Between the two steps the catalogue and the
// storage disagree, and the storage scan cannot tell that apart from a change
// made outside filex: a walk run there catalogued a renamed folder's bytes as
// NEW files, or the tombstone pass confirmed a moved or trashed row gone by a
// Stat of its old path and dropped it, with its shares, versions, comments and
// the trash entry a person could have restored. The explorer and the
// operations queue have held the gate since 0.54 (#192); WebDAV, SFTP, FTPS,
// NFS and the S3 gateway did their two steps bare, and the scan's re-reading
// of a row (internal/sync stillAsListed) was all that stood between them.
//
// So every protocol server's rename, trash and purge goes through the three
// methods below, which hold the gate (rowgate.Move, shared) around both steps.
// The steps themselves are the ones each server always took; only their frame
// is shared, so a sixth protocol cannot be the one that forgets it.
//
// ⚠ rowgate's rules hold here: never call these with the gate already held on
// the same goroutine (the queue's job, a handler that took rowgate.Change), and
// never from inside a database transaction.
//
// # Waiting, then detaching, then asking again (sec055)
//
// ctx is the protocol request's own context (a WebDAV request's, an SFTP or
// FTP session's). The gate is waited for on it (rowgate.MoveCtx): while a
// storage judgement holds the gate the verb waits, and a client that gives up
// in the meantime takes nothing and changes nothing. Once the gate is held the
// change detaches from the client (storage.DetachMutation) and runs to its
// end. Then, before the first byte moves, what the verb checked before it
// asked for the gate is asked again (checks: StillFree, StillAsSeen): a late
// MOVE or rename onto a name that was free refuses a file that landed there
// meanwhile instead of replacing it, and a late DELETE does not trash a new
// file put where the old one was.
//
// # A folder on an object store: a fence, not the gate
//
// Moving or trashing a folder on an object store is a copy and a delete for
// every object under it, and held the whole storage's scan off for as long as
// it ran. Relocate and Discard fence the folder's prefixes instead (fenceOf,
// rowgate.FenceCtx): its path and, for a move, its destination's. The scan
// leaves those alone and goes on everywhere else; the fence opens once the
// rows have followed. Purge keeps the gate (one call), and PurgeTree holds it
// a batch at a time.

import (
	"context"
	"errors"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Check is one thing a verb checked before it asked for the row gate, asked
// again once it holds it, on the detached context the change runs on.
type Check func(ctx context.Context) error

// StillFree is the check that dst, found free, still is (storage.StillFree:
// storage.ErrTakenMeanwhile, an os.ErrExist). src is the verb's own source,
// so a change of case alone does not find itself in the way.
func StillFree(drv storage.Driver, dst, src string) Check {
	return func(ctx context.Context) error { return storage.StillFree(ctx, drv, dst, src) }
}

// StillAsSeen is the check that rel is still the object the verb stat'ed
// (storage.StillAsSeen: storage.ErrChangedMeanwhile).
func StillAsSeen(drv storage.Driver, rel string, seen storage.Object) Check {
	return func(ctx context.Context) error { return storage.StillAsSeen(ctx, drv, rel, seen) }
}

// hold waits for st's gate on ctx and detaches: it answers the release, the
// context the change runs on and its cancel, or ctx's error when the client
// went first. Then it asks checks again. With fence (a folder moved or
// trashed object by object: fenceOf) it holds those prefixes instead of the
// gate (rowgate.HoldCtx): the storage's scan goes on everywhere else.
func hold(ctx context.Context, st *model.Storage, fence []string, checks []Check) (release func(), work context.Context, cancel context.CancelFunc, err error) {
	release, err = rowgate.HoldCtx(ctx, st.ID, fence...)
	if err != nil {
		return release, nil, func() {}, err
	}
	work, cancel = storage.DetachMutation(ctx)
	for _, c := range checks {
		if c == nil {
			continue
		}
		if err := c(work); err != nil {
			cancel()
			release()
			return func() {}, nil, func() {}, err
		}
	}
	return release, work, cancel, nil
}

// Relocate is a protocol's rename or move within one storage: move changes the
// storage (srcRel's bytes to dstRel), then the rows follow (Move), both under
// st's row gate - or, for a folder on an object store, under a fence on
// srcRel and dstRel (fenceOf) - after checks are asked again. The rows follow only when move
// succeeded; Relocate answers move's error as it is, so the caller maps it the
// way it always did. move is given the detached context the change runs on.
func (s *Syncer) Relocate(ctx context.Context, st *model.Storage, srcRel, dstRel string, move func(ctx context.Context) error, checks ...Check) error {
	release, work, cancel, err := hold(ctx, st, s.fenceOf(ctx, st, nil, srcRel, dstRel), checks)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	if err := move(work); err != nil {
		return err
	}
	s.Move(work, st, srcRel, dstRel)
	return nil
}

// Discard sends rel to st's trash (trash.Put, the one implementation every
// delete surface shares) and lets the rows follow, both under st's row gate -
// or, for a folder on an object store, under a fence on rel (fenceOf) - after
// checks are asked again:
//
//   - the bytes were trashed: the row is retagged into the trash (Trash);
//   - nothing was there: the rows are dropped (Delete);
//   - anything else (trash.ErrUnsupported included): nothing in the catalogue
//     changes, and the caller decides - a storage without a trash deletes for
//     good only for a person who may purge (Purge).
//
// It answers trash.Put's outcome and error as they are.
func (s *Syncer) Discard(ctx context.Context, st *model.Storage, drv storage.Driver, rel string, checks ...Check) (trash.Outcome, error) {
	release, work, cancel, err := hold(ctx, st, s.fenceOf(ctx, st, drv, rel), checks)
	if err != nil {
		return trash.Outcome{}, err
	}
	defer release()
	defer cancel()
	out, err := trash.Put(work, drv, rel)
	if err != nil {
		return out, err
	}
	switch {
	case out.Trashed:
		s.Trash(work, st, rel, out.Key)
	case out.Missing:
		s.Delete(work, st, rel)
	}
	return out, nil
}

// Purge deletes rel for good: del removes the bytes, then the rows are dropped
// (Delete), both under st's row gate, after checks are asked again. The rows
// go only when del succeeded; a del that tolerates "already gone" says so by
// answering nil. Purge answers del's error as it is. del is given the detached
// context the change runs on.
func (s *Syncer) Purge(ctx context.Context, st *model.Storage, rel string, del func(ctx context.Context) error, checks ...Check) error {
	release, work, cancel, err := hold(ctx, st, nil, checks)
	if err != nil {
		return err
	}
	defer release()
	defer cancel()
	if err := del(work); err != nil {
		return err
	}
	s.Delete(work, st, rel)
	return nil
}

// PurgeBatch is how many objects PurgeTree deletes under one hold of the row
// gate.
var PurgeBatch = 100

// PurgeTree deletes the folder rel for good on a storage that keeps no real
// object for a folder (an object store): its objects, as list answers under
// the first hold of the gate, in batches of PurgeBatch, each batch's bytes and then its rows under one hold
// of st's row gate, and last the folder's own markers and every row left under
// it. The gate is held a batch at a time, not for the whole folder: a storage
// scan between two batches sees files gone with their rows gone and files
// still there with their rows, which is the truth, and a folder whose objects
// are all gone, which is what the purge is making it. A delete that finds an
// object already gone carries on. The first hold waits on ctx (a client gone
// by then changes nothing); once the first batch has started, the rest runs
// detached to the end.
func (s *Syncer) PurgeTree(ctx context.Context, st *model.Storage, del storage.Deleter, rel string, list func(ctx context.Context) ([]string, error), checks ...Check) error {
	release, work, cancel, err := hold(ctx, st, nil, checks)
	if err != nil {
		return err
	}
	defer cancel()
	// Whichever hold is current is let go however this returns (a release
	// is safe to call twice).
	defer func() { release() }()
	files, err := list(work)
	if err != nil {
		return err
	}
	batch := PurgeBatch
	if batch < 1 {
		batch = 1
	}
	for start := 0; ; start += batch {
		end := min(start+batch, len(files))
		for _, fp := range files[start:end] {
			if derr := del.Delete(work, fp); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
				return derr
			}
			// The file's rows go with its bytes, quietly: the folder is
			// announced once, when its own rows go below.
			s.DeleteRows(work, st, fp)
		}
		if end >= len(files) {
			break
		}
		release()
		release = rowgate.Move(st.ID)
	}
	_ = del.Delete(work, rel)
	_ = del.Delete(work, strings.TrimRight(rel, "/")+"/")
	s.Delete(work, st, rel)
	return nil
}

// fenceOf answers the prefixes a change of rels (the source first) fences on
// st instead of holding its row gate: rels when rels[0] is a folder on a
// storage that moves and trashes a folder object by object (storage.FenceFor),
// nil otherwise - a file, a storage with real folders, no driver to ask - and
// the change holds the gate as before. drv may be nil: the Syncer's Resolver
// is asked.
//
// The catalogue answers first (one row, and only on an object store). A
// source with no row yet - a folder of a lazily catalogued storage nobody has
// opened, objects written to the bucket by another tool - is asked of the
// storage (storage.FenceAt: one Stat; on an object store a HEAD and, for a
// folder, a one-key listing). A Stat that fails answers nil: the gate, as
// before, which is never wrong, only slower for the scan.
func (s *Syncer) fenceOf(ctx context.Context, st *model.Storage, drv storage.Driver, rels ...string) []string {
	if st == nil || len(rels) == 0 {
		return nil
	}
	if drv == nil {
		if s.Resolver == nil {
			return nil
		}
		d, err := s.Resolver(st.ID)
		if err != nil {
			return nil
		}
		drv = d
	}
	if !storage.ObjectByObject(drv) {
		return nil
	}
	src := NormalizePath(rels[0])
	node, _ := s.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, src))
	if node == nil {
		return storage.FenceAt(ctx, drv, rels...)
	}
	return storage.FenceFor(drv, node.Type == model.NodeTypeDirectory, rels...)
}

package sync

// unavailable.go - an entry the storage could not answer for (issue #104).
//
// The tombstone pass drops a row only once the storage confirms its object is
// gone: the walk did not list it AND its own Stat answers "not found"
// (confirmGone). Any other answer - a plugin that cannot Stat a folder, a
// permission it lacks, a backend error - used to keep the row exactly as it
// was: live, clickable, and saying nothing about the fact that nothing about it
// could be trusted any more. On a plugin storage that answers so for a folder
// deleted outside filex, the folder stayed in the catalogue for good.
//
// Now such a row is MARKED (Store.MarkNodeUnavailable, migration 00078) and
// left in the catalogue - a drop cannot be undone, and "I could not check"
// never reads as "it is gone". The explorer shows it with a warning, and every
// operation on it, or below it, is refused with 409 ENTRY_UNAVAILABLE
// (handlers.refuseUnavailable) until the storage answers for it again:
//
//   - its Stat succeeds, or the walk lists it again: the mark is lifted;
//   - its Stat answers "not found": the row is dropped, as before.
//
// Each answer is told to Worker.AttachEntryState after the batch commits: the
// server writes it to the storage plugin's log (internal/pluginlog, which
// counts a repeated line instead of writing it again and reaches the server
// log at most once every five minutes) and, when the row's state changed, to
// the audit trail.

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/model"
)

// EntryState is one answer about an entry the storage could not answer for,
// or answered for again.
type EntryState struct {
	Storage *model.Storage
	NodeID  int64
	Path    string
	Kind    model.NodeType
	// Reason is what the storage answered; empty when it answered for the
	// entry again and the mark was lifted.
	Reason string
	// Changed: the row's state changed (it was not marked, or carried another
	// reason; or it was marked and is not any more). False: the same answer as
	// last time.
	Changed bool
}

// AttachEntryState wires what hears about entries the storage could not
// answer for (EntryState). Called after the batch committed; nil leaves the
// marks in the catalogue and tells nobody.
func (w *Worker) AttachEntryState(fn func(ctx context.Context, e EntryState)) {
	w.entryState = fn
}

// unanswered marks n: the storage's Stat answered err, which is neither "it is
// there" nor "not found".
func (s *storageSyncer) unanswered(ctx context.Context, n *model.Node, err error, b *entryBatch) {
	reason := err.Error()
	changed, merr := s.store.MarkNodeUnavailable(ctx, n.ID, reason)
	if merr != nil {
		slog.Warn("sync: could not record that the storage gave no answer for an entry",
			slog.Int64("node", n.ID), slog.String("path", n.Path),
			slog.String("storage", s.storage.Name), slog.String("err", merr.Error()))
		return
	}
	b.states = append(b.states, EntryState{
		Storage: s.storage, NodeID: n.ID, Path: n.Path, Kind: n.Type, Reason: reason, Changed: changed,
	})
}

// answered lifts n's mark, when it carries one: the storage answered for it
// again.
func (s *storageSyncer) answered(ctx context.Context, n *model.Node, b *entryBatch) {
	if n == nil || !n.Unavailable {
		return
	}
	changed, err := s.store.ClearNodeUnavailable(ctx, n.ID)
	if err != nil || !changed {
		return
	}
	n.Unavailable, n.UnavailableReason, n.UnavailableAt = false, "", nil
	b.states = append(b.states, EntryState{
		Storage: s.storage, NodeID: n.ID, Path: n.Path, Kind: n.Type, Changed: true,
	})
}

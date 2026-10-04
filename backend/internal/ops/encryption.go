package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ── who may encrypt, when the queue runs the work ───────────────────────
//
// A rename, a move or a copy onto a LITERAL destination named like an
// encrypted folder's key file or a `.fxe` is a new encryption unless it
// carries what is encrypted already: a folder under any name, a `.fxe` that
// stays one, a key file that stays its own folder's (internal/e2epolicy,
// RelocationEncrypts). The handler that queues it asks the rule; the worker
// runs as nobody and cannot. But part of the handler's answer rests on what a
// source WAS: a folder lands on the name unasked. A folder replaced by a file
// before the worker reaches the row would land as an encryption nobody was
// asked about (an upload of a `.fxe` holding a key file's bytes, say).
//
// So the handler tells the queue which sources it settled without leaning on
// "it is a folder" (WithEncryptionSettled): the ones it asked the rule about,
// and the ones whose names free them. Any other source that is a FILE when
// the worker reaches it fails, with nothing moved. What the queue was told is
// kept in memory, as an archive job's work is: a row the server restarts
// under has been told nothing. The worker asks the names half of the rule
// itself (RelocationEncrypts with no driver, which counts a source as a
// file), so after a restart a relocation the names alone free still runs, and
// a file source of any other fails the same way: the person queues it again.

type settledKey struct{}

// WithEncryptionSettled is ctx for the Submit or SubmitTo of a rename, a move
// or a copy onto a literal destination: sources are the ones the handler
// settled with the encryption rule (asked about and allowed, or free by their
// names). A source left out may still land as a folder, never as a file.
func WithEncryptionSettled(ctx context.Context, sources []string) context.Context {
	return context.WithValue(ctx, settledKey{}, append([]string(nil), sources...))
}

// ErrEncryptionUnasked fails a source that is a file now, that the rule's
// names do not free, and that the queue cannot confirm the handler checked
// when it queued the work: it was a folder then (a folder lands on the name
// unasked), or the server has restarted since and the queue was told nothing.
// Under that name a file is a new end-to-end encryption.
var ErrEncryptionUnasked = errors.New("the source could not be confirmed as checked against the end-to-end encryption policy when it was queued, and a file under this name is a new encryption; queue it again")

// settledFrom reads what WithEncryptionSettled put on ctx, and whether
// anything was.
func settledFrom(ctx context.Context) (map[string]bool, bool) {
	srcs, ok := ctx.Value(settledKey{}).([]string)
	if !ok {
		return nil, false
	}
	set := make(map[string]bool, len(srcs))
	for _, s := range srcs {
		set[opRel(s)] = true
	}
	return set, true
}

// refuseUnsettledEncryption fails moving, copying or renaming src to dst
// (the op's destination for src) when dst is a literal destination whose name
// makes it a new encryption, src is a FILE now, and the handler did not settle
// src when it queued the op.
//
// The names are asked first, as the handler asks them: a relocation they free
// (a `.fxe` that stays a `.fxe`, a key file that stays its own folder's)
// runs whatever the queue was told, a restart included. A source that is gone
// is left to the step itself; one the storage cannot describe fails, rather
// than land unasked.
func (s *Service) refuseUnsettledEncryption(ctx context.Context, drv storage.Driver, op *Op, src, dst string) error {
	if strings.HasSuffix(op.Dest, "/") {
		return nil
	}
	// No driver: src counts as a file, which is the names' own answer. What
	// src IS now is looked at below, once nothing else has settled it.
	if !e2epolicy.RelocationEncrypts(ctx, nil, opRel(src), opRel(dst), !s.isCross(op)) {
		return nil
	}
	s.settledMu.Lock()
	settled := s.settled[op.ID][opRel(src)]
	s.settledMu.Unlock()
	if settled {
		return nil
	}
	obj, err := drv.Stat(ctx, opRel(src))
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("could not tell whether the item is a file or a folder: %w", err)
	case obj.Kind == storage.KindDirectory:
		return nil
	}
	return ErrEncryptionUnasked
}

// forgetSettled drops what the queue was told about op id.
func (s *Service) forgetSettled(id int64) {
	s.settledMu.Lock()
	delete(s.settled, id)
	s.settledMu.Unlock()
}

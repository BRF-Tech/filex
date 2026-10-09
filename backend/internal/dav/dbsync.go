package dav

// The DB node-cache / search-index / thumbnail / write-hook bookkeeping that
// follows a WebDAV mutation.
//
// The logic itself lives in internal/protocolsync, shared with every other
// protocol server: a write is not just bytes, and a copy of this per protocol
// means the fifth copy is the one somebody forgets — silently, because the
// file is there and only the index, the thumbnail and the quota are missing.
//
// What stays here are the names the rest of this package already calls, so the
// call sites read exactly as they did.

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
)

// normalizeDBPath canonicalises a path the way the shared pathkey.Hash key
// expects.
func normalizeDBPath(rel string) string { return protocolsync.NormalizePath(rel) }

func (h *Handler) syncWrite(ctx context.Context, st *model.Storage, rel string, size int64, mime string) {
	h.sync.Write(ctx, st, rel, size, mime)
}

func (h *Handler) syncMkdir(ctx context.Context, st *model.Storage, rel string) {
	h.sync.Mkdir(ctx, st, rel)
}

// A delete and a MOVE are not here: their bytes and their rows go together
// under the storage's row gate, so fs.go calls h.sync.Discard, Purge and
// Relocate directly (protocolsync/gated.go, issue #201).

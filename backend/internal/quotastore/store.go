// Package quotastore is the single place where per-user storage usage is
// accounted. It wraps a db.Store and keeps `users.usage_bytes` in step with
// the node rows, so every write surface — browser upload, staged upload,
// staged ingest, WebDAV PUT, the public file drop, ShareX, the AI/REST API,
// save-text, archive extract, copy — is counted without any of them knowing
// that quotas exist.
//
// # Why here, and not in nine handlers
//
// Before this package, `quota.AddUsage` and `Store.SetNodeOwner` had no
// callers anywhere in the tree: `usage_bytes` was never incremented,
// `GetNodeOwner` always returned nil, and the `SubUsage` at trash-purge —
// the only call site — therefore never ran either. Nothing was counted, so
// nothing was ever refused, and the two features standing on this (chunk 4's
// quota reservation at `begin`, and "trashed bytes still count against
// quota") were theory.
//
// The write surfaces do not share a funnel: `writehook` comes closest, but it
// fires AFTER the row is already updated, so the previous size — the thing an
// overwrite delta needs — is gone by then, and it deliberately imports no db
// package. What every surface DOES share is the store: a node's bytes cannot
// begin, change or stop existing without `CreateNode`, `UpdateNodeMeta` or
// `HardDeleteNode`. Wrapping those three is therefore the one place that is
// both complete and future-proof — a write path added next month is counted
// on the day it is written, with no line of its own.
//
// The pattern is the one internal/tenantstore already uses: embed db.Store,
// override the handful of methods that matter, pass everything else through.
//
// # The rule (docs/QUOTAS.md is the prose version)
//
//	usage_bytes(u) == SUM(nodes.size) WHERE owner_id=u AND type='file'
//	                  — trashed rows INCLUDED
//
// Everything else follows from that identity:
//
//   - bytes land   → owner set, size added
//   - overwrite    → the delta is applied; on a user-attributed write the
//     owner becomes the writer, so old owner -= old size and
//     new owner += new size
//   - trash        → nothing (the bytes still exist and still count)
//   - restore      → nothing (they never stopped counting)
//   - move/rename  → nothing (same row, same owner, same bytes)
//   - copy         → a new row, so the bytes are counted again — they are
//     genuinely a second copy on the disk
//   - purge / permanent delete → subtracted, because the bytes are gone
//
// # Attribution
//
// The owner is the acting identity, resolved in this order:
//
//  1. an explicit owner put on the context with WithOwner — used by surfaces
//     with no logged-in user whose bytes still belong to someone (the public
//     file-drop link bills the link's creator; the async copy worker bills
//     the owner of the source file);
//  2. auth.UserFrom(ctx) — every authenticated surface, including WebDAV
//     Basic auth and AI/REST tokens;
//  3. nobody. A node discovered by the storage scanner was not uploaded by
//     anyone, so it stays unowned and uncounted — until a user overwrites it,
//     at which point it becomes theirs.
package quotastore

import (
	"context"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
)

// ownerCtxKey carries an explicit attribution for surfaces that have no
// authenticated user in context but whose bytes still belong to someone.
type ownerCtxKey struct{}

// WithOwner attributes every node written under the returned context to
// userID, overriding auth.UserFrom. Pass 0 to attribute to nobody.
//
// Used by the public file-drop handler (bytes land in the link creator's
// storage, so they are the link creator's bytes) and by the async copy worker
// (which runs on a server-lifetime context long after the request is gone).
func WithOwner(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ownerCtxKey{}, userID)
}

// OwnerFrom returns the effective owner for a write on this context: the
// explicit attribution if one was set, otherwise the authenticated user,
// otherwise 0 ("nobody").
func OwnerFrom(ctx context.Context) int64 {
	if v, ok := ctx.Value(ownerCtxKey{}).(int64); ok {
		return v
	}
	if u := auth.UserFrom(ctx); u != nil {
		return u.ID
	}
	return 0
}

// Metrics is the optional counter sink. Nil in tests and in any build that
// does not want metrics; the accounting itself never depends on it.
type Metrics interface {
	QuotaUsageDelta(userID int64, delta int64)
}

// Store is a db.Store that keeps users.usage_bytes true.
type Store struct {
	db.Store
	q       *quota.Service
	metrics Metrics
}

// Ensure the decorator still satisfies the full interface.
var _ db.Store = (*Store)(nil)

// New wraps s. The quota service is built on the UNWRAPPED store on purpose:
// AddUsage/SubUsage must reach the real UPDATE, and routing them back through
// the wrapper would be a needless loop (and, if the wrapper ever grew a user
// override, a real one).
func New(s db.Store) *Store {
	return &Store{Store: s, q: quota.New(s)}
}

// Quota returns the service the wrapper accounts through, so the bootstrap
// does not build a second one over the same tables.
func (s *Store) Quota() *quota.Service { return s.q }

// AttachMetrics installs the optional counter sink.
func (s *Store) AttachMetrics(m Metrics) { s.metrics = m }

// add applies a signed delta to a user's usage and reports it.
func (s *Store) add(ctx context.Context, userID, delta int64) {
	if userID <= 0 || delta == 0 {
		return
	}
	var err error
	if delta > 0 {
		err = s.q.AddUsage(ctx, userID, delta)
	} else {
		err = s.q.SubUsage(ctx, userID, -delta)
	}
	if err != nil {
		// Accounting must never fail a write that already landed — the bytes
		// ARE on storage. Log loudly instead; `filex admin quota recompute`
		// (and quota.Recompute) rebuild the truth from the node rows.
		slog.Warn("quota: usage accounting",
			slog.Int64("user", userID),
			slog.Int64("delta", delta),
			slog.String("err", err.Error()))
		return
	}
	if s.metrics != nil {
		s.metrics.QuotaUsageDelta(userID, delta)
	}
}

// CreateNode stamps the acting identity onto the new row and counts its bytes.
//
// Directories get an owner too (it is useful provenance and costs one UPDATE),
// but only files move the counter — a directory's `size` column is a cached
// recursive total (internal/sync.RecomputeFolderSizes), so counting it would
// bill every byte twice.
func (s *Store) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	created, err := s.Store.CreateNode(ctx, n)
	if err != nil || created == nil {
		return created, err
	}
	owner := OwnerFrom(ctx)
	if owner <= 0 {
		return created, nil
	}
	if serr := s.Store.SetNodeOwner(ctx, created.ID, &owner); serr != nil {
		slog.Warn("quota: set node owner",
			slog.Int64("node", created.ID),
			slog.Int64("owner", owner),
			slog.String("err", serr.Error()))
		return created, nil
	}
	if created.Type == model.NodeTypeFile {
		s.add(ctx, owner, created.Size)
	}
	return created, nil
}

// UpdateNodeMeta applies the size delta of an overwrite, and re-attributes the
// row when a user wrote it.
//
// The previous size has to be read here: this is the last moment it exists.
// It is one primary-key SELECT in front of an UPDATE that was already going
// to run, on a path that has just finished moving the bytes themselves.
func (s *Store) UpdateNodeMeta(ctx context.Context, id int64, size int64, mime, etag string, mtime time.Time) error {
	before, _ := s.Store.GetNode(ctx, id)
	if err := s.Store.UpdateNodeMeta(ctx, id, size, mime, etag, mtime); err != nil {
		return err
	}
	if before == nil || before.Type != model.NodeTypeFile {
		return nil
	}
	prevOwner, _ := s.Store.GetNodeOwner(ctx, id)
	writer := OwnerFrom(ctx)

	// No acting user (storage scanner noticing the file changed on the
	// backend): keep the owner, just correct their total.
	if writer <= 0 {
		if prevOwner != nil {
			s.add(ctx, *prevOwner, size-before.Size)
		}
		return nil
	}
	// Same owner: a plain delta.
	if prevOwner != nil && *prevOwner == writer {
		s.add(ctx, writer, size-before.Size)
		return nil
	}
	// A different user (or nobody) owned it: the bytes now on disk are the
	// writer's, so the old owner gives back what they were carrying and the
	// writer takes on the new size. This is also how a node the scanner found
	// — unowned, uncounted — starts counting the first time someone writes it.
	if prevOwner != nil {
		s.add(ctx, *prevOwner, -before.Size)
	}
	if err := s.Store.SetNodeOwner(ctx, id, &writer); err != nil {
		slog.Warn("quota: re-attribute node",
			slog.Int64("node", id), slog.Int64("owner", writer), slog.String("err", err.Error()))
		return nil
	}
	s.add(ctx, writer, size)
	return nil
}

// HardDeleteNode releases the bytes. This is the ONLY release point: a soft
// delete into the trash keeps counting, which is the documented rule and the
// reason a user cannot free space by filling the trash.
//
// Called by the trash purge, by the retention loop, and by the permanent-delete
// paths on drivers that cannot preserve the bytes (WebDAV/ai_ops on a driver
// with no Mover) — all of them genuine destruction.
func (s *Store) HardDeleteNode(ctx context.Context, id int64) error {
	before, _ := s.Store.GetNode(ctx, id)
	var owner *int64
	if before != nil && before.Type == model.NodeTypeFile {
		owner, _ = s.Store.GetNodeOwner(ctx, id)
	}
	if err := s.Store.HardDeleteNode(ctx, id); err != nil {
		return err
	}
	if owner != nil && before != nil {
		s.add(ctx, *owner, -before.Size)
	}
	return nil
}

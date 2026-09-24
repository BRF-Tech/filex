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
// This package is also where a node LEARNS WHOSE IT IS, for the same reason it
// is where the bytes are counted: the store is the one thing every write
// surface has in common. `nodes.owner_id`, `nodes.last_actor_id` and
// `nodes.external_upload` (migrations 00004 and 00038) are written here and
// nowhere else.
//
// The acting identity is resolved in this order:
//
//  1. an explicit identity put on the context — WithOwner for surfaces with no
//     logged-in user whose bytes still belong to someone (the public file-drop
//     link bills the link's creator; an upload ticket bills its minter), and
//     WithActor for a background worker running long after the request that
//     asked for the work is gone (the async copy/move queue, which carries the
//     requesting user in `pending_ops.actor_id`);
//  2. auth.UserFrom(ctx) — every authenticated surface, including WebDAV,
//     FTPS, SFTP, NFS and S3 (internal/protocolauth stamps the principal on
//     the connection context) and every API token;
//  3. nobody. A node the storage scanner discovered was not put there by
//     anyone, so it stays SYSTEM — and NULL is the honest way to say that.
//     No user is invented to stand in for it.
//
// # The two columns say different things
//
//	owner_id      — who PUT THE THING HERE
//	last_actor_id — who TOUCHED IT LAST
//
// and they move independently:
//
//   - upload / new folder / save / create   → owner = actor = the writer
//   - a write over WebDAV/FTPS/S3/CLI/sync  → the account whose token was used
//   - a drop-link upload                    → owner = the LINK'S CREATOR, plus
//     external_upload=1. The uploader is anonymous by design and is not a user;
//     nothing invents one for them.
//   - copy                                  → a new file, so the COPIER owns it
//   - move / rename                         → the same file: owner UNCHANGED,
//     actor = the mover
//   - edit / overwrite / restore            → owner UNCHANGED, actor = the actor
//   - an external change (the bucket side moved, a sync found new bytes)
//     → actor = NULL (system); the owner is left alone
//
// ⚠ The one place an owner still moves is ADOPTION: a SYSTEM row (owner NULL)
// that a user writes becomes that user's. "Nobody's" is not "somebody else's",
// and without it a scanner-found file could be filled with gigabytes that no
// quota ever counted.
//
// ⚠⚠ This changed a quota behaviour, deliberately, and the change is worth
// stating plainly: before, an overwrite by another user MOVED the bytes to the
// writer. Now a file that already has an owner keeps it, so the owner carries
// the new size. The identity `usage_bytes(u) == SUM(size) WHERE owner_id=u`
// still holds exactly — but it is now possible for user B to grow the total
// user A is billed for, by overwriting A's file with a bigger one. B needs
// write access to A's file to do it, and the alternative (ownership that
// silently changes hands every time somebody edits a shared document) makes
// the Owner column unable to answer the only question it is asked.
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
// otherwise 0 ("nobody" — SYSTEM).
func OwnerFrom(ctx context.Context) int64 {
	if v, ok := ctx.Value(ownerCtxKey{}).(int64); ok {
		return v
	}
	if u := auth.UserFrom(ctx); u != nil {
		return u.ID
	}
	return 0
}

// actorCtxKey carries an explicit "who is doing this" for background work that
// runs long after the request that asked for it.
type actorCtxKey struct{}

// WithActor names the person on whose behalf the work under the returned
// context is being done, for surfaces with no authenticated user in context.
//
// It is separate from WithOwner because the two answer different questions and
// a move proves it: the ops worker's move is DONE BY the person who dragged the
// file, but it does not make the file theirs. Pass 0 to attribute to nobody.
func WithActor(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, userID)
}

// ActorFrom returns who is acting on this context: the explicit actor if one
// was set, otherwise the explicit owner (a surface that named an owner and no
// actor — an upload ticket, the async copy worker — is acting as that
// identity), otherwise the authenticated user, otherwise 0 (SYSTEM).
//
// ⚠⚠ An anonymous drop is the exception, and it is the whole reason the two
// questions are asked separately. The link's creator OWNS what lands — they
// asked for it, it is in their storage, it is on their quota — but they did
// not TOUCH it: a visitor did, and that visitor is not a user. Falling back to
// the owner here would write "last changed by Ada" onto a file Ada has never
// seen, which is a lie the UI cannot see through. Measured before this branch
// existed: a drop-link upload came back last_actor = the link's creator.
//
// So an external drop has no actor. What actually happened is recorded by
// external_upload, which says "somebody else handed this in" without inventing
// a person to have done it.
func ActorFrom(ctx context.Context) int64 {
	if v, ok := ctx.Value(actorCtxKey{}).(int64); ok {
		return v
	}
	if ExternalOriginFrom(ctx) {
		return 0
	}
	return OwnerFrom(ctx)
}

// ExplicitActorFrom returns the actor a background surface named with
// WithActor, or 0 when it named nobody.
//
// ⚠ It never falls back: not to the owner, not to the authenticated user. It
// answers the narrower question writehook asks for a notification — "whom is
// this event FROM" — and ActorFrom's owner fallback is the wrong answer there.
// The copy mirror bills the SOURCE file's owner when an old queue row names
// nobody (handlers/manager_opsync.go); that person did not make the copy, and
// the copy may sit in a folder they cannot open, so an event addressed to them
// would tell them about it.
func ExplicitActorFrom(ctx context.Context) int64 {
	if v, ok := ctx.Value(actorCtxKey{}).(int64); ok && v > 0 {
		return v
	}
	return 0
}

// externalCtxKey marks writes that arrived from outside filex through an
// anonymous drop link.
type externalCtxKey struct{}

// WithExternalOrigin marks every node written under the returned context as
// having arrived through an anonymous drop link / file request.
//
// The OWNER is still a real account — the person who created the link, set
// separately with WithOwner — and this only records that the bytes were handed
// over by somebody else. That somebody has no identity here: they are anonymous
// by design, and inventing a user row for them would put a person in the
// account list who cannot log in and was never invited.
func WithExternalOrigin(ctx context.Context) context.Context {
	return context.WithValue(ctx, externalCtxKey{}, true)
}

// ExternalOriginFrom reports whether this context is an anonymous drop.
func ExternalOriginFrom(ctx context.Context) bool {
	v, _ := ctx.Value(externalCtxKey{}).(bool)
	return v
}

// ptr is nil for 0 ("nobody" — SYSTEM) and a pointer otherwise, which is how
// both columns spell the same distinction in the database.
func ptr(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	v := id
	return &v
}

// sameID compares two nullable ids.
func sameID(a *int64, b *int64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
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
// Directories get an owner too — somebody made the folder, and that is worth
// saying — but only files move the counter: a directory's `size` column is a
// cached recursive total (internal/sync.RecomputeFolderSizes), so counting it
// would bill every byte twice.
//
// ⚠ The attribution is written ONTO THE MODEL, before the INSERT, rather than
// as a follow-up UPDATE the way it used to be. An archive extract, a desktop
// sync or a scanner walk creates rows in bulk, and a second round trip per row
// is a cost the feature does not need to have: the drivers name the three
// columns in the INSERT itself.
func (s *Store) CreateNode(ctx context.Context, n *model.Node) (*model.Node, error) {
	owner := OwnerFrom(ctx)
	actor := ActorFrom(ctx)
	if n != nil {
		// The context wins when it knows something; a caller that pre-filled
		// the model (nothing does today) keeps its value when it does not.
		if owner > 0 || n.OwnerID == nil {
			n.OwnerID = ptr(owner)
		}
		if actor > 0 || n.LastActorID == nil {
			n.LastActorID = ptr(actor)
		}
		if ExternalOriginFrom(ctx) {
			n.ExternalUpload = true
		}
	}
	created, err := s.Store.CreateNode(ctx, n)
	if err != nil || created == nil {
		return created, err
	}
	if owner > 0 && created.Type == model.NodeTypeFile {
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
	prevOwner := before.OwnerID
	writer := OwnerFrom(ctx)
	s.stampActor(ctx, before, ptr(ActorFrom(ctx)))

	// No acting user — the storage scanner noticing the file changed on the
	// backend. The owner is left alone and their total is corrected; the actor
	// was already set to NULL just above, because a change that arrived from
	// outside filex has nobody to name.
	if writer <= 0 {
		if prevOwner != nil {
			s.add(ctx, *prevOwner, size-before.Size)
		}
		return nil
	}
	// Somebody already owns it: an overwrite changes the BYTES, not whose file
	// it is. The owner carries the delta; who did the writing is recorded in
	// last_actor_id and nowhere else.
	if prevOwner != nil {
		s.add(ctx, *prevOwner, size-before.Size)
		return nil
	}
	// Nobody owned it — a row the scanner discovered. "Nobody's" is not
	// "somebody else's", so the writer adopts it, and that is what makes a
	// found file start counting against a quota at all.
	if err := s.Store.SetNodeOwner(ctx, id, &writer); err != nil {
		slog.Warn("quota: adopt unowned node",
			slog.Int64("node", id), slog.Int64("owner", writer), slog.String("err", err.Error()))
		return nil
	}
	s.add(ctx, writer, size)
	return nil
}

// stampActor writes last_actor_id when it actually changes.
//
// `before` was already read by the caller, so the comparison is free and the
// common case — the same person editing their own file twice — costs nothing.
func (s *Store) stampActor(ctx context.Context, before *model.Node, actor *int64) {
	if before == nil || sameID(before.LastActorID, actor) {
		return
	}
	if err := s.Store.SetNodeActor(ctx, before.ID, actor); err != nil {
		slog.Warn("quota: set node actor",
			slog.Int64("node", before.ID), slog.String("err", err.Error()))
	}
}

// MoveNode records who moved it. A move or a rename is the SAME file: the
// owner does not change and neither does the size, so there is nothing to
// count — only somebody to name.
//
// ⚠ One UPDATE per row, on top of the move's own. A subtree move
// (protocolsync.MoveRows) calls this once per descendant, so the bookkeeping
// cost of a move doubles. It is DB-cache work that runs after the driver has
// already moved the bytes, and the alternative — widening MoveNode's signature
// through five protocol servers — buys back a round trip at the price of a
// cross-cutting change to code this feature has no other business in.
func (s *Store) MoveNode(ctx context.Context, id int64, parentID *int64, name, fullPath, pathHash string) error {
	if err := s.Store.MoveNode(ctx, id, parentID, name, fullPath, pathHash); err != nil {
		return err
	}
	s.stampActorByID(ctx, id)
	return nil
}

// RestoreNode records who took it back out of the trash. Usage does not move:
// a trashed file never stopped counting.
func (s *Store) RestoreNode(ctx context.Context, id int64) error {
	if err := s.Store.RestoreNode(ctx, id); err != nil {
		return err
	}
	s.stampActorByID(ctx, id)
	return nil
}

// RestoreNodeAt is RestoreNode with the original path put back.
func (s *Store) RestoreNodeAt(ctx context.Context, id int64, parentID *int64, origPath string) error {
	if err := s.Store.RestoreNodeAt(ctx, id, parentID, origPath); err != nil {
		return err
	}
	s.stampActorByID(ctx, id)
	return nil
}

// stampActorByID is stampActor for the paths that have no `before` row in hand.
//
// ⚠ A system move does NOT blank the actor. internal/sync.repairStalePath
// moves a row whose path drifted, and that is filex tidying its own catalogue,
// not a person moving a file — overwriting the last real actor with NULL there
// would destroy the only true thing the row knew.
func (s *Store) stampActorByID(ctx context.Context, id int64) {
	actor := ptr(ActorFrom(ctx))
	if actor == nil {
		return
	}
	if err := s.Store.SetNodeActor(ctx, id, actor); err != nil {
		slog.Warn("quota: set node actor",
			slog.Int64("node", id), slog.String("err", err.Error()))
	}
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

// Package slotchange is what the server does when a write changed a KEY SLOT
// of something end-to-end encrypted - the password or the recovery slot of an
// encrypted folder's key file (`.filex-e2e.json`, e2e/keyfilewatch) or of a
// single encrypted file's header (`.fxe`, e2e/fxewatch): who may retire the
// old copies that still open it with the old secret, and who is told.
//
// Both watches decide here, so a folder and a single file follow one rule.
//
// # Who may retire the old copies
//
// Each earlier version of a key file (or of a `.fxe` over the same file key)
// opens today's contents with the secret that was just changed. Deleting them
// is what makes a password change stick - and it cannot be undone. So it is
// done only for a write the server can attribute to someone who may delete
// that history anyway:
//
//   - the OWNER of the encrypted folder (for a `.fxe`, of the file), or
//   - an ADMINISTRATOR.
//
// Anyone else's rewrite keeps every version, and the owner is told (a
// warning). The bytes alone prove nothing: anybody who may write the folder
// can upload a file under the key file's name, and before 0.54 such an upload
// was enough to erase the folder's key history - every way back to a key file
// that opens it. The password itself cannot be checked by the server (it never
// sees one, and the key file holds nothing it could check one against), so
// ownership is the proof it can ask for.
//
// # Who is told
//
// Every key-slot change is recorded and announced by the server from what it
// saw change, never from what a client said: an audit row
// `e2e.password_change` and the notification `e2e.password_changed` to the
// owner (to the administrators when nobody owns it). Up to 0.53 both came from
// the browser's announcement (POST /api/files/e2e/password-changed), which a
// client could send without changing anything; that door now records nothing
// (api/handlers/e2e_password.go). docs/E2E-ENCRYPTION.md → "Who is told".
package slotchange

import (
	"context"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
)

// Store is the narrow store surface the decision needs (db.Store satisfies
// it): the encrypted root's node and its owner, the writer's account, the
// storage's name.
type Store interface {
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	GetNodeOwner(ctx context.Context, nodeID int64) (*int64, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	GetStorage(ctx context.Context, id int64) (*model.Storage, error)
}

// Audit is the narrow audit surface.
type Audit interface {
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// Subject is the encrypted thing whose slot changed.
type Subject struct {
	StorageID int64
	// Root is the encrypted folder - or the single encrypted file, which is
	// its own root - storage-relative, without a leading slash ("" is the
	// storage's top).
	Root string
	// File: Root is a single encrypted file (`.fxe`).
	File bool
}

// Writer is who made the write, as the server knows it: the signed-in
// account on the request, or the person a background write is done for (the
// staged upload's commit runs in the ops worker, quotastore.ActorFrom).
type Writer struct {
	ID   int64       // 0: nobody the server can name
	User *model.User // nil when the account could not be read
}

// WriterOf names the writer of the write ctx carries.
func WriterOf(ctx context.Context, store Store) Writer {
	if u := auth.UserFrom(ctx); u != nil && u.ID > 0 {
		return Writer{ID: u.ID, User: u}
	}
	id := quotastore.ActorFrom(ctx)
	if id <= 0 {
		return Writer{}
	}
	w := Writer{ID: id}
	if store != nil {
		if u, err := store.GetUser(ctx, id); err == nil && u != nil {
			w.User = u
		}
	}
	return w
}

// Verdict is the decision for one write.
type Verdict struct {
	// Owner is the recorded owner of the encrypted root, nil when nobody
	// owns it.
	Owner  *int64
	Writer Writer
	// MayRetire: the writer may delete the copies that open the root with
	// the old secret (its owner, or an administrator).
	MayRetire bool
}

// ByOwner reports whether the owner made the write.
func (v Verdict) ByOwner() bool {
	return v.Owner != nil && v.Writer.ID > 0 && *v.Owner == v.Writer.ID
}

// OwnerOf is the recorded owner of the encrypted root, or nil.
//
// ⚠ For a folder it is the FOLDER's owner: a key file that nobody owned is
// adopted by the first person who overwrites it (quotastore.UpdateNodeMeta),
// a folder never is.
func OwnerOf(ctx context.Context, store Store, s Subject) *int64 {
	if store == nil {
		return nil
	}
	n, err := store.GetNodeByPath(ctx, s.StorageID, pathkey.Hash(s.StorageID, s.Root))
	if err != nil || n == nil {
		return nil
	}
	owner, err := store.GetNodeOwner(ctx, n.ID)
	if err != nil {
		return nil
	}
	return owner
}

// Judge decides whether the write ctx carries may retire the old copies of s.
func Judge(ctx context.Context, store Store, s Subject) Verdict {
	v := Verdict{Writer: WriterOf(ctx, store), Owner: OwnerOf(ctx, store, s)}
	v.MayRetire = v.ByOwner() || (v.Writer.User != nil && v.Writer.User.IsAdmin())
	return v
}

// Change is what one rewrite did, as the watch measured it.
type Change struct {
	Subject
	// Changes are the diff's, sorted ("password", "recovery_key", "rekey", ...).
	Changes []string
	// Origin is the surface the write came through (writehook origin).
	Origin string
	// Deleted is how many old copies were deleted; Kept how many were kept
	// because the writer may not retire them.
	Deleted int
	Kept    int
}

func (c Change) has(name string) bool {
	for _, x := range c.Changes {
		if x == name {
			return true
		}
	}
	return false
}

// Teller records a key-slot change and tells the owner. Every field may be
// nil; that part is then skipped.
type Teller struct {
	Store  Store
	Audit  Audit
	Notify notify.Service
}

// Tell writes the audit row `e2e.password_change` and sends
// `e2e.password_changed` - to the owner, or as a broadcast the administrators
// read when nobody owns it. A warning when somebody other than the owner made
// the change. The words are the server's (internal/notify say.go,
// server.notify.e2e.password_changed); this writes facts only.
func (t *Teller) Tell(ctx context.Context, c Change, v Verdict) {
	if t == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	root := strings.Trim(c.Root, "/")
	name := path.Base("/" + root)
	if name == "/" || name == "." {
		name = root
	}
	meta := map[string]any{
		"changes": c.Changes,
		"rekey":   c.has("rekey"),
		"origin":  c.Origin,
	}
	if c.File {
		meta["file"] = root
		meta["kind"] = "file"
	} else {
		meta["folder"] = root
	}
	if t.Store != nil {
		if st, err := t.Store.GetStorage(ctx, c.StorageID); err == nil && st != nil {
			meta["storage"] = st.Name
		}
	}
	if c.Deleted > 0 {
		meta["versions_deleted"] = c.Deleted
	}
	if c.Kept > 0 {
		meta["versions_kept"] = c.Kept
	}
	var actorID *int64
	var actor *notify.ActorRef
	if v.Writer.ID > 0 {
		id := v.Writer.ID
		actorID = &id
		actor = &notify.ActorRef{ID: id}
		if v.Writer.User != nil {
			meta["actor_email"] = v.Writer.User.Email
			actor.Email = v.Writer.User.Email
		}
	}

	if t.Audit != nil {
		target := ""
		if t.Store != nil {
			if n, err := t.Store.GetNodeByPath(ctx, c.StorageID, pathkey.Hash(c.StorageID, root)); err == nil && n != nil {
				target = strconv.FormatInt(n.ID, 10)
			}
		}
		if err := t.Audit.InsertAuditEntry(ctx, &model.AuditEntry{
			UserID:     actorID,
			Action:     "e2e.password_change",
			TargetType: "node",
			TargetID:   target,
			Metadata:   meta,
		}); err != nil {
			slog.Warn("e2e: could not audit a key-slot change",
				slog.Int64("storage", c.StorageID), slog.String("path", root), slog.String("err", err.Error()))
		}
	}

	if t.Notify == nil {
		return
	}
	severity := notify.SeverityInfo
	if !v.ByOwner() {
		severity = notify.SeverityWarning
	}
	ev := notify.Event{
		Event:    notify.EventE2EPasswordChanged,
		Severity: severity,
		Body:     root,
		Node:     &notify.NodeRef{StorageID: c.StorageID, Path: root, Name: name},
		Target:   notify.DirTarget(root),
		Meta:     meta,
		Actor:    actor,
		// The OWNER (who may not be the person who changed it); nobody owns
		// it: a broadcast, which the administrators read (notify bell.go).
		UserID: v.Owner,
	}
	if c.File {
		ev.Target = notify.FileTarget(root)
	}
	sink := t.Notify
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("notify: key-slot change panic", slog.Any("recover", rec))
			}
		}()
		if _, err := sink.Send(ctx, ev); err != nil {
			slog.Warn("notify: key-slot change send", slog.String("err", err.Error()))
		}
	}()
}

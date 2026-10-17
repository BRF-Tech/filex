package officesave

// Writing an office save that does not go over its document (beside.go): an
// edit in another format than the file's, kept beside it (`rapor.doc` stays,
// the edit is `rapor.docx`), and a save of a document that changed while it
// was open, kept as a conflict copy beside it.
//
// ONE writer for every editor: the Document Server's callback
// (internal/onlyoffice callback_format.go, session_base.go) and an app's save
// door (handlers app_ui_beside.go). The name it is written under, who it is
// written for and whether they may create it there, the gate, the catalogue,
// the people told and the audit row are the same whichever editor made the
// save; only what the caller answers differs (the callback's {"error": n},
// the app's {saved, name, conflict}).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Store is what a save written beside its document reads and writes in the
// catalogue (db.Store has every method).
type Store interface {
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
	GetStorage(ctx context.Context, id int64) (*model.Storage, error)
	GetUser(ctx context.Context, id int64) (*model.User, error)
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
}

// Env is where a save is written and who answers for what.
type Env struct {
	Store  Store
	Driver storage.Driver
	Writer storage.Writer
	// Locks is the storage's lock view from the SHARED resolver (api.Deps.ACL):
	// a fresh acl.New does not know the vaults, and a name inside a vault
	// must be refused here as everywhere.
	Locks writegate.Locks
	// App is the app writing it (an app's own lock lets it through); 0 for the
	// Document Server.
	App int64
	// MayCreate reports whether the person may create a file at rel now
	// (files.create there: role, folder exceptions, grants, a blocked file
	// type, an app's lock).
	MayCreate func(ctx context.Context, uid int64, rel string) bool
	// Sync records the new file in the catalogue; Write emits the one event
	// itself, for the people it was written for, with the notice.
	Sync *protocolsync.Syncer
	// Origin is the event's origin (writehook.OriginOnlyOffice,
	// writehook.OriginPlugin).
	Origin string
}

// Save is one save written beside its document.
type Save struct {
	// Target is the name it is written under (Env.BesideTarget,
	// Env.ConflictTarget).
	Target string
	// Format is the type the bytes are in.
	Format string
	Mime   string
	// TitleKey, BodyKey and Vars are what the people it was written for are
	// told (srvtext keys, every built-in language into the notice's meta).
	TitleKey string
	BodyKey  string
	Vars     srvtext.Vars
	// Action is the audit row's action.
	Action string
	// Why is the log line's reason.
	Why string
}

// Written is a save written beside its document.
type Written struct {
	// Node is the new file's catalogue row.
	Node *model.Node
	Size int64
	// For are the people it was written for, the one who may create it first.
	For []int64
}

// The refusals of a save Write does not write.
var (
	// ErrNoEditor: nobody is known to have made the save.
	ErrNoEditor = errors.New("officesave: no editor of the save is known")
	// ErrNoCreate: none of the people who made it may create a file there.
	ErrNoCreate = errors.New("officesave: none of the editors may create a file beside the document")
)

// GateError is a save writegate refused at its new name: a lock, a name
// filex keeps, a vault.
type GateError struct{ Err error }

func (e *GateError) Error() string { return "officesave: beside the document: " + e.Err.Error() }
func (e *GateError) Unwrap() error { return e.Err }

// FreeName is want, or the next free `name (2).ext` beside it when want is
// taken on the storage or in the catalogue (ops.UniqueDestNumbered: the New
// document dialog's numbering).
func (e Env) FreeName(ctx context.Context, storageID int64, want string) (string, error) {
	taken := func(rel string) bool {
		n, err := e.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel))
		return err == nil && n != nil
	}
	return ops.UniqueDestNumbered(ctx, e.Driver, want, taken)
}

// BesideTarget is where an edit of node saved as ext goes: the file's own
// name with that extension (BesideName), or the next free `name (2).ext`.
func (e Env) BesideTarget(ctx context.Context, node *model.Node, ext string) (string, error) {
	return e.FreeName(ctx, node.StorageID, BesideName(node.Path, ext))
}

// ConflictTarget is where a save of node made while the file changed goes:
// its conflict copy (ConflictName), in ext when the save is in another format
// than the file's ("" or the file's own: its own type), free.
func (e Env) ConflictTarget(ctx context.Context, node *model.Node, ext string, now time.Time) (string, error) {
	name := node.Name
	if ext != "" && ext != DocExt(node.Name) {
		name = path.Base(BesideName(node.Name, ext))
	}
	return e.FreeName(ctx, node.StorageID, path.Join(path.Dir(node.Path), ConflictName(name, now)))
}

// Write writes src at s.Target, beside node, which is left as it was, for the
// first of who that may create it there, checked now, and tells them. The
// gate is asked for the new name as for every save (syspath.SaveVerb: a
// draft's owner, a desktop working copy). err is ErrNoEditor, ErrNoCreate, a
// *GateError, or a failure to write.
func Write(ctx context.Context, env Env, node *model.Node, src io.Reader, length int64, who []int64, s Save) (*Written, error) {
	gate := writegate.Writes(s.Target).As(syspath.SaveVerb(s.Target))
	if owner, ok := syspath.DraftOwner(s.Target); ok {
		gate = gate.By(owner)
	}
	if err := writegate.Check(env.Locks, env.App, gate); err != nil {
		return nil, &GateError{Err: err}
	}
	editors, err := creatorFirst(ctx, env, who, s.Target)
	if err != nil {
		return &Written{For: editors}, err
	}
	if err := env.Writer.Write(ctx, s.Target, src, length); err != nil {
		return nil, fmt.Errorf("write beside: %w", err)
	}
	mime := s.Mime
	var size int64
	if obj, err := env.Driver.Stat(ctx, s.Target); err == nil {
		size = obj.Size
		if obj.Mime != "" {
			mime = obj.Mime
		}
	}
	meta, _, _ := NoticeMeta(s.TitleKey, s.BodyKey, s.Vars)
	meta["saved_beside"] = node.Path
	meta["filetype"] = s.Format
	var saved *model.Node
	if st, err := env.Store.GetStorage(ctx, node.StorageID); err == nil && st != nil && env.Sync != nil {
		env.Sync.WriteWithoutNotification(ctx, st, s.Target, size, mime)
		saved, _ = env.Store.GetNodeByPath(ctx, node.StorageID, pathkey.Hash(node.StorageID, s.Target))
	}
	if saved == nil {
		saved = &model.Node{StorageID: node.StorageID, Name: path.Base(s.Target), Path: s.Target, Size: size, Mime: mime, Type: model.NodeTypeFile}
	}
	writehook.EmitWrittenFor(ctx, node.StorageID, saved, env.Origin, writehook.Created, editors, meta)
	slog.Info("office save: written beside the document",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
		slog.String("saved", s.Target), slog.String("filetype", s.Format), slog.String("why", s.Why))
	Audit(ctx, env.Store, s.Action, editors, saved.ID, map[string]any{
		"target_name": s.Target, "storage_id": node.StorageID, "original": node.Path, "filetype": s.Format,
	})
	return &Written{Node: saved, Size: size, For: editors}, nil
}

// creatorFirst is who with the first of them that may create rel leading
// (the audit row names them), ErrNoEditor for nobody and ErrNoCreate when
// none of them may.
func creatorFirst(ctx context.Context, env Env, who []int64, rel string) ([]int64, error) {
	if len(who) == 0 {
		return nil, ErrNoEditor
	}
	for i, id := range who {
		if env.MayCreate != nil && env.MayCreate(ctx, id, rel) {
			out := make([]int64, 0, len(who))
			out = append(out, id)
			out = append(out, who[:i]...)
			return append(out, who[i+1:]...), nil
		}
	}
	return who, ErrNoCreate
}

// NoticeMeta is a notice's words in every built-in language (`title_<lang>`,
// `body_<lang>`), and the row's own title and body in the instance's language
// (the webhook and the admin history read those).
func NoticeMeta(titleKey, bodyKey string, vars srvtext.Vars) (meta map[string]any, title, body string) {
	meta = map[string]any{}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = srvtext.Text(lang, titleKey, vars)
		meta["body_"+lang] = srvtext.Text(lang, bodyKey, vars)
	}
	lang := srvtext.Pick()
	return meta, srvtext.Text(lang, titleKey, vars), srvtext.Text(lang, bodyKey, vars)
}

// Audit writes one row about a file, best-effort: a row the store refuses is
// logged, never a reason to undo what already happened. users are the
// editors, the row's user the first of them while that account is still there
// (the column is a reference to it).
func Audit(ctx context.Context, store Store, action string, users []int64, targetID int64, meta map[string]any) {
	if store == nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	e := &model.AuditEntry{
		Action: action, TargetType: "file", TargetID: strconv.FormatInt(targetID, 10),
		Metadata: meta, CreatedAt: time.Now().UTC(),
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if len(users) > 0 {
		meta["editors"] = users
		if u, err := store.GetUser(actx, users[0]); err == nil && u != nil {
			uid := users[0]
			e.UserID = &uid
		}
	}
	if err := store.InsertAuditEntry(actx, e); err != nil {
		slog.Warn("office save: audit row not written", slog.String("action", action), slog.Any("err", err))
	}
}

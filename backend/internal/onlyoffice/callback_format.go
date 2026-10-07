package onlyoffice

// A save that comes back in another format than the file's name says
// (filex 0.51).
//
// The document server writes a document back in its own format when it can
// (`assemblyFormatAsOrigin`, on by default) and in OOXML when it cannot or is
// told not to. Measured on ONLYOFFICE Docs 9.4 with its defaults: an edited
// .xls comes back as XLSX (`filetype: "xlsx"`), a .doc as DOCX - it writes
// neither old binary format. The callback used to write those bytes under the
// old name: a "rapor.doc" that is a DOCX inside, which some readers open and
// some refuse, and a desktop sync client cannot tell apart.
//
// Now the old file is NOT touched. The edit is written BESIDE it, in the
// format it is in: rapor.doc stays as it was and rapor.docx is the edit
// (`rapor (2).docx` when that name is taken: ops.UniqueDestNumbered, the New
// document dialog's numbering). The people who edited it are told, in their
// language (srvtext `server.onlyoffice.saved_beside_*`), and an audit row
// records it. A CSV is the exception: it is converted back and written in
// place (callback_csv.go).
//
// What is never written: a type the document server should not have sent for
// a document (anything but the OOXML and ODF formats below), or bytes that are
// not the package the type names. Those are refused - logged, the server told
// error 1, the editors told (`server.onlyoffice.save_refused_*`), an audit row.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Audit actions (web/tests/lib/auditLabel.test.ts holds their labels).
const (
	// AuditActionSavedBeside: an edit the document server saved in another
	// format was written beside the file, which was left as it was.
	AuditActionSavedBeside = "file.office_saved_beside"
	// AuditActionSaveRefused: the document server's save was not written.
	AuditActionSaveRefused = "file.office_save_refused"
)

// The server catalogue's words for the people told (srvtext, every built-in
// language into the notice's meta: packages/core notificationText `noticed`).
const (
	savedBesideTitle = "server.onlyoffice.saved_beside_title"
	savedBesideBody  = "server.onlyoffice.saved_beside_body"
	saveRefusedTitle = "server.onlyoffice.save_refused_title"
	saveRefusedBody  = "server.onlyoffice.save_refused_body"

	refusedTooLarge     = "server.onlyoffice.refused_too_large"
	refusedNotConverted = "server.onlyoffice.refused_not_converted"
	refusedOtherType    = "server.onlyoffice.refused_other_type"
	refusedPackage      = "server.onlyoffice.refused_package"
	refusedBadBytes     = "server.onlyoffice.refused_bad_bytes"
	refusedNoCreate     = "server.onlyoffice.refused_no_create"
	refusedNoEditor     = "server.onlyoffice.refused_no_editor"
)

// besideTypes are the formats a save may come back in that are kept, beside
// the file: the document server's OOXML and ODF documents.
var besideTypes = map[string]bool{
	"docx": true, "xlsx": true, "pptx": true,
	"docm": true, "xlsm": true, "pptm": true,
	"odt": true, "ods": true, "odp": true,
}

// errNotWritten is a save that is not written, and why, as a catalogue key
// and its values: the person reads it in their language, the log in English.
type errNotWritten struct {
	key  string
	vars srvtext.Vars
}

func notWritten(key string, vars srvtext.Vars) *errNotWritten {
	return &errNotWritten{key: key, vars: vars}
}

func (e *errNotWritten) Error() string { return srvtext.Text("en", e.key, e.vars) }

// asNotWritten reports whether err is a save refused, and why.
func asNotWritten(err error) (*errNotWritten, bool) {
	var e *errNotWritten
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// srvtextVars is a Vars from name/value pairs.
func srvtextVars(kv ...string) srvtext.Vars {
	v := srvtext.Vars{}
	for i := 0; i+1 < len(kv); i += 2 {
		v[kv[i]] = kv[i+1]
	}
	return v
}

// formatName is a type as a person reads it: "DOCX".
func formatName(ext string) string { return strings.ToUpper(ext) }

// editorIDs are the callback's `users` as filex account ids, each once.
func editorIDs(users []string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, u := range users {
		id, err := strconv.ParseInt(strings.TrimSpace(u), 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// noticeMeta is a notice's words in every built-in language
// (`title_<lang>`, `body_<lang>`), and the row's own title and body in the
// instance's language (the webhook and the admin history read those).
func noticeMeta(titleKey, bodyKey string, vars srvtext.Vars) (meta map[string]any, title, body string) {
	meta = map[string]any{}
	for _, lang := range srvtext.BuiltinLanguages() {
		meta["title_"+lang] = srvtext.Text(lang, titleKey, vars)
		meta["body_"+lang] = srvtext.Text(lang, bodyKey, vars)
	}
	lang := srvtext.Pick()
	return meta, srvtext.Text(lang, titleKey, vars), srvtext.Text(lang, bodyKey, vars)
}

// refusals is how many saves of the document were not written in place since
// this process started (Service.refused): refused, or written beside it.
func (s *Service) refusals(nodeID int64) int {
	s.refusedMu.Lock()
	defer s.refusedMu.Unlock()
	return s.refused[nodeID]
}

// newSessionKey makes the next opening of the document a new editing session
// (Service.refused): its bytes did not change, so its key would not either.
func (s *Service) newSessionKey(nodeID int64) {
	s.refusedMu.Lock()
	if s.refused == nil {
		s.refused = map[int64]int{}
	}
	s.refused[nodeID]++
	s.refusedMu.Unlock()
}

// audit writes one row, best-effort: a row the store refuses is logged, never
// a reason to undo what already happened.
func (s *Service) audit(ctx context.Context, action string, users []int64, targetID int64, meta map[string]any) {
	if s.Store == nil {
		return
	}
	e := &model.AuditEntry{
		Action: action, TargetType: "file", TargetID: strconv.FormatInt(targetID, 10),
		Metadata: meta, CreatedAt: time.Now().UTC(),
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// The editors as the callback named them; the row's user only when that
	// account is still there (the column is a reference to it).
	if len(users) > 0 {
		meta["editors"] = users
		if u, err := s.Store.GetUser(actx, users[0]); err == nil && u != nil {
			uid := users[0]
			e.UserID = &uid
		}
	}
	if err := s.Store.InsertAuditEntry(actx, e); err != nil {
		slog.Warn("onlyoffice callback: audit row not written", slog.String("action", action), slog.Any("err", err))
	}
}

// refuseSave records a save that was not written and tells the people who
// edited it (the session's editors, callback_identity.go), in their language:
// the bell's "did not reach the storage" notice (file.upload_failed), with the
// server's words. The next opening of the document gets a new key.
func (s *Service) refuseSave(ctx context.Context, node *model.Node, editors []int64, got string, why *errNotWritten) {
	s.newSessionKey(node.ID)
	slog.Warn("onlyoffice callback refused: not written",
		slog.Int64("storage", node.StorageID),
		slog.String("path", node.Path),
		slog.String("filetype", got),
		slog.String("why", why.Error()))
	vars := srvtext.Vars{"name": node.Name}
	meta := map[string]any{}
	for _, lang := range srvtext.BuiltinLanguages() {
		v := srvtext.Vars{"name": node.Name, "why": srvtext.Text(lang, why.key, why.vars)}
		meta["title_"+lang] = srvtext.Text(lang, saveRefusedTitle, v)
		meta["body_"+lang] = srvtext.Text(lang, saveRefusedBody, v)
	}
	lang := srvtext.Pick()
	vars["why"] = srvtext.Text(lang, why.key, why.vars)
	title := srvtext.Text(lang, saveRefusedTitle, vars)
	meta["filetype"] = got
	if len(editors) == 0 {
		writehook.OnUploadFailed(ctx, node.StorageID, 0, node.Path, node.Name, writehook.OriginOnlyOffice, why.Error(), meta)
	} else {
		writehook.OnUploadFailedFor(ctx, node.StorageID, editors, node.Path, node.Name, writehook.OriginOnlyOffice, title, why.Error(), meta)
	}
	s.audit(ctx, AuditActionSaveRefused, editors, node.ID, map[string]any{
		"target_name": node.Path, "storage_id": node.StorageID, "filetype": got, "reason": why.Error(),
	})
}

// besideTarget is the name the edit is written under: the file's own name
// with the format's extension, or the next free `name (2).ext` beside it.
func (s *Service) besideTarget(ctx context.Context, drv storage.Driver, node *model.Node, got string) (string, error) {
	base := strings.TrimSuffix(node.Name, path.Ext(node.Name))
	return s.freeBeside(ctx, drv, node, path.Join(path.Dir(node.Path), base+"."+got))
}

// freeBeside is want, or the next free `name (2).ext` beside it when want is
// taken on the storage or in the catalogue.
func (s *Service) freeBeside(ctx context.Context, drv storage.Driver, node *model.Node, want string) (string, error) {
	taken := func(rel string) bool {
		n, err := s.Store.GetNodeByPath(ctx, node.StorageID, pathkey.Hash(node.StorageID, rel))
		return err == nil && n != nil
	}
	return ops.UniqueDestNumbered(ctx, drv, want, taken)
}

// besideSave is one save written beside the document instead of over it:
// where, in which format, what the editors are told and what the audit row
// says. Two kinds: a save in another format (saveBeside) and a save of a
// session the document moved on from (session_base.go saveConflict).
type besideSave struct {
	target string
	// got is the format the bytes are in (the refusal's words).
	got      string
	mime     string
	titleKey string
	bodyKey  string
	vars     srvtext.Vars
	action   string
	// why is the log line's reason.
	why string
	// noCreate, when set, is the reason said instead of refusedNoCreate when
	// none of the editors may create the file there.
	noCreate string
}

// saveBeside writes an edit the document server saved as got (not the
// file's own format) beside the file, which is left as it was, for one of the
// session's editors who may create it there (callback_identity.go). The map is
// the callback's answer; err an *errNotWritten for a save that must not be
// written, else a failure to say as such.
func (s *Service) saveBeside(ctx context.Context, drv storage.Driver, writer storage.Writer, node *model.Node, src io.Reader, length int64, got, key string, users []string) (map[string]any, error) {
	// The bytes are the package the type names, or nothing is written.
	br := bufio.NewReader(src)
	head, _ := br.Peek(4)
	if !bytes.Equal(head, []byte("PK\x03\x04")) {
		return nil, notWritten(refusedBadBytes, srvtext.Vars{"format": formatName(got)})
	}
	target, err := s.besideTarget(ctx, drv, node, got)
	if err != nil {
		return map[string]any{"error": 1, "message": "no free name beside the document"}, nil
	}
	return s.writeBeside(ctx, drv, writer, node, br, length, key, users, besideSave{
		target:   target,
		got:      got,
		mime:     assoc.MimeOf(got),
		titleKey: savedBesideTitle,
		bodyKey:  savedBesideBody,
		vars:     srvtext.Vars{"name": node.Name, "saved": path.Base(target), "format": formatName(got), "ext": docExt(node.Name)},
		action:   AuditActionSavedBeside,
		why:      "saved in another format",
	})
}

// writeBeside writes src at w.target, beside the document, which is left as it
// was, for one of the session's editors who may create it there
// (callback_identity.go), and tells them. The map is the callback's answer;
// err an *errNotWritten for a save that must not be written.
func (s *Service) writeBeside(ctx context.Context, drv storage.Driver, writer storage.Writer, node *model.Node, src io.Reader, length int64, key string, users []string, w besideSave) (map[string]any, error) {
	target := w.target
	// The same gate the document's own save went through, for the new name.
	gate := writegate.Writes(target).As(syspath.PutWorkCopy)
	if owner, ok := syspath.DraftOwner(node.Path); ok {
		gate = writegate.Writes(target).As(syspath.OwnDraft).By(owner)
	}
	if gerr := writegate.Check(acl.New(s.Store).Locks(ctx, node.StorageID), 0, gate); gerr != nil {
		slog.Warn("onlyoffice callback refused: beside", slog.Int64("storage", node.StorageID),
			slog.String("path", target), slog.String("why", gerr.Error()))
		return map[string]any{"error": 1, "message": syspath.ErrReserved.Error()}, nil
	}
	// Who it is written for, and whether they may create it there - checked
	// now, on the file's own name.
	editors, why := s.creatorOf(ctx, node, key, users, target, w.got)
	if why != nil {
		if w.noCreate != "" && why.key == refusedNoCreate {
			why = notWritten(w.noCreate, w.vars)
		}
		return nil, why
	}
	if err := writer.Write(ctx, target, src, length); err != nil {
		return map[string]any{"error": 1, "message": "write beside: " + err.Error()}, nil
	}
	mime := w.mime
	var size int64
	if obj, err := drv.Stat(ctx, target); err == nil {
		size = obj.Size
		if obj.Mime != "" {
			mime = obj.Mime
		}
	}
	meta, _, _ := noticeMeta(w.titleKey, w.bodyKey, w.vars)
	meta["saved_beside"] = node.Path
	meta["filetype"] = w.got
	var saved *model.Node
	if st, err := s.Store.GetStorage(ctx, node.StorageID); err == nil && st != nil {
		sy := s.syncer()
		sy.WriteWithoutNotification(ctx, st, target, size, mime)
		saved, _ = s.Store.GetNodeByPath(ctx, node.StorageID, pathkey.Hash(node.StorageID, target))
	}
	if saved == nil {
		saved = &model.Node{StorageID: node.StorageID, Name: path.Base(target), Path: target, Size: size, Mime: mime, Type: model.NodeTypeFile}
	}
	writehook.EmitWrittenFor(ctx, node.StorageID, saved, writehook.OriginOnlyOffice, writehook.Created, editors, meta)
	s.newSessionKey(node.ID)
	slog.Info("onlyoffice callback: saved beside the document",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
		slog.String("saved", target), slog.String("filetype", w.got), slog.String("why", w.why))
	s.audit(ctx, w.action, editors, saved.ID, map[string]any{
		"target_name": target, "storage_id": node.StorageID, "original": node.Path, "filetype": w.got,
	})
	return map[string]any{"error": 0}, nil
}

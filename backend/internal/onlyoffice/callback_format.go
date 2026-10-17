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
// a document (anything but the OOXML and ODF formats of
// officesave.BesideFormats), or bytes that are
// not the package the type names. Those are refused - logged, the server told
// error 1, the editors told (`server.onlyoffice.save_refused_*`), an audit row.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/officesave"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
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

	// A CSV save (officesave.Refusal): a character the file's encoding cannot
	// hold; bytes that are not text in an encoding filex can read for it.
	refusedCSVEncoding   = "server.onlyoffice.refused_csv_encoding"
	refusedCSVUnreadable = "server.onlyoffice.refused_csv_unreadable"
)

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

// audit writes one row, best-effort (officesave.Audit: the editors as the
// callback named them).
func (s *Service) audit(ctx context.Context, action string, users []int64, targetID int64, meta map[string]any) {
	if s.Store == nil {
		return
	}
	officesave.Audit(ctx, s.Store, action, users, targetID, meta)
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

// besideEnv is where the callback writes a save beside its document
// (officesave.Write, the writer an app's save door shares): the shared
// resolver's lock view, the document's editors who may create files there
// (callback_identity.go mayCreate), the event as the Document Server's.
func (s *Service) besideEnv(ctx context.Context, drv storage.Driver, writer storage.Writer, node *model.Node) officesave.Env {
	return officesave.Env{
		Store: s.Store, Driver: drv, Writer: writer,
		Locks: s.aclResolver().Locks(ctx, node.StorageID),
		MayCreate: func(ctx context.Context, uid int64, rel string) bool {
			return s.mayCreate(ctx, node, uid, rel)
		},
		Sync: s.syncer(), Origin: writehook.OriginOnlyOffice,
	}
}

// besideSave is one save written beside the document instead of over it:
// the shared writer's Save, and what the callback says when none of the
// editors may create it there. Two kinds: a save in another format
// (saveBeside) and a save of a session the document moved on from
// (session_base.go saveConflict).
type besideSave struct {
	officesave.Save
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
	if !officesave.BesideBytesOK(head) {
		return nil, notWritten(refusedBadBytes, srvtext.Vars{"format": officesave.FormatName(got)})
	}
	env := s.besideEnv(ctx, drv, writer, node)
	target, err := env.BesideTarget(ctx, node, got)
	if err != nil {
		return map[string]any{"error": 1, "message": "no free name beside the document"}, nil
	}
	return s.writeBeside(ctx, env, node, br, length, key, users, besideSave{Save: officesave.Save{
		Target:   target,
		Format:   got,
		Mime:     assoc.MimeOf(got),
		TitleKey: savedBesideTitle,
		BodyKey:  savedBesideBody,
		Vars:     srvtext.Vars{"name": node.Name, "saved": path.Base(target), "format": officesave.FormatName(got), "ext": officesave.DocExt(node.Name)},
		Action:   AuditActionSavedBeside,
		Why:      "saved in another format",
	}})
}

// writeBeside writes src at w.Target, beside the document, which is left as
// it was, for one of the session's editors who may create it there
// (callback_identity.go), and tells them - through officesave.Write, the
// writer an app's save door shares. The map is the callback's answer; err an
// *errNotWritten for a save that must not be written.
func (s *Service) writeBeside(ctx context.Context, env officesave.Env, node *model.Node, src io.Reader, length int64, key string, users []string, w besideSave) (map[string]any, error) {
	editors := s.sessionEditors(key, users)
	_, err := officesave.Write(ctx, env, node, src, length, editors, w.Save)
	var gerr *officesave.GateError
	switch {
	case err == nil:
	case errors.As(err, &gerr):
		slog.Warn("onlyoffice callback refused: beside", slog.Int64("storage", node.StorageID),
			slog.String("path", w.Target), slog.String("why", gerr.Err.Error()))
		return gateRefusal(gerr.Err), nil
	case errors.Is(err, officesave.ErrNoEditor):
		return nil, notWritten(refusedNoEditor, nil)
	case errors.Is(err, officesave.ErrNoCreate):
		if w.noCreate != "" {
			return nil, notWritten(w.noCreate, w.Vars)
		}
		return nil, notWritten(refusedNoCreate, srvtext.Vars{"ext": officesave.DocExt(node.Name), "format": officesave.FormatName(w.Format)})
	default:
		return map[string]any{"error": 1, "message": err.Error()}, nil
	}
	s.newSessionKey(node.ID)
	return map[string]any{"error": 0}, nil
}

package handlers

// A .csv saved and read through an app's interface (task #255, task #268;
// docs/APP-PLUGINS-API.md → CSV files: how they are written).
//
// The Office editor app edits a CSV in a spreadsheet and hands back the whole
// table as CSV text, written its own way: its delimiter, its line ends, UTF-8.
// The ONLYOFFICE Document Server does the same, and its callback has written
// the file's own way back since 0.51; an app's save is written by the same
// rules now (internal/officesave): the file's delimiter, byte order mark,
// line ends and encoding, every record and cell nobody changed as the file
// has it. What the app's bytes are is read from them; what it declares about
// them (`csv_delimiter`, `csv_encoding`, `csv_bom`, `csv_eol` beside the save)
// is checked against them, and a save they contradict is refused with nothing
// written. A file of an encrypted folder is not read here: the server holds
// its ciphertext only, and the browser writes it.
//
// The read (UIRead) says how the file is written, so the app opens it right:
// the TextDialectHeader, `{"delimiter", "bom", "crlf", "encoding"}`, on the
// answer of a .csv's read.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/officesave"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// isCSV: a name the CSV rules are for.
func isCSV(rel string) bool { return officesave.DocExt(rel) == "csv" }

// csvDeclaredOf reads what the app declared about its CSV from the save's
// query, nil when it said nothing.
func csvDeclaredOf(r *http.Request) (*officesave.Declared, error) {
	q := r.URL.Query()
	return officesave.ParseDeclared(q.Get("csv_delimiter"), q.Get("csv_encoding"), q.Get("csv_bom"), q.Get("csv_eol"))
}

// writeCSVDeclarationRefused answers a declaration filex cannot read.
func writeCSVDeclarationRefused(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusBadRequest, "csv_declaration", nil)
}

// csvSaveOver turns what an interface saved over a .csv into the bytes to
// write (officesave.SaveCSV). size is the file's size as the driver reported
// it. On refusal the answer is written and ok is false: nothing may be
// written.
func (h *AppPlugins) csvSaveOver(w http.ResponseWriter, r *http.Request, drv storage.Driver, storageID int64, rel string, size int64, body io.Reader) (out []byte, ok bool) {
	decl, err := csvDeclaredOf(r)
	if err != nil {
		writeCSVDeclarationRefused(w, r)
		return nil, false
	}
	saved, err := io.ReadAll(io.LimitReader(body, officesave.MaxSaveBytes+1))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", apierr.Params{"max": srvtext.Bytes(langOf(r), h.Registry.MaxOutputBytes())})
			return nil, false
		}
		writeError(w, r, http.StatusBadRequest, "bad_body", nil)
		return nil, false
	}
	if int64(len(saved)) > officesave.MaxSaveBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", apierr.Params{"max": srvtext.Bytes(langOf(r), officesave.MaxSaveBytes)})
		return nil, false
	}
	out, res, err := officesave.SaveCSV(h.csvOriginal(r, drv, storageID, rel, size), saved, decl)
	if err != nil {
		writeCSVRefusal(w, r, rel, err)
		return nil, false
	}
	if !res.Applied && res.Why != "empty" {
		at := []any{slog.Int64("storage", storageID), slog.String("path", rel), slog.String("why", res.Why)}
		if res.Why == "panic" {
			at = append(at, slog.String("panic", res.Panic))
		}
		slog.Info("app ui save: CSV cells not kept", at...)
	}
	return out, true
}

// csvOriginal reads the file a save is about to replace, the way the
// interface's read of it did (the body resolver: the staged copy while an
// upload is still transferring).
func (h *AppPlugins) csvOriginal(r *http.Request, drv storage.Driver, storageID int64, rel string, size int64) officesave.Original {
	rc, err := h.openBody(r, drv, storageID, rel)
	o, err := officesave.ReadOriginalFrom(rc, err, size)
	if err != nil {
		slog.Warn("app ui save: reading the CSV before its save", slog.Int64("storage", storageID), slog.String("path", rel), slog.String("err", err.Error()))
	}
	return o
}

// openBody opens a file's bytes the way the body resolver serves them.
func (h *AppPlugins) openBody(r *http.Request, drv storage.Driver, storageID int64, rel string) (io.ReadCloser, error) {
	src, err := h.Body.Resolve(r.Context(), drv, storageID, rel, nil)
	if err != nil {
		return nil, err
	}
	return src.Open(r.Context())
}

// writeCSVRefusal answers a CSV save officesave refused: 422, a code a
// program branches on (`csv_dialect`, `csv_encoding`, `csv_not_text`, with
// the declaration's `field`), and the reason in the reader's language.
func writeCSVRefusal(w http.ResponseWriter, r *http.Request, rel string, err error) {
	name := path.Base(rel)
	rf, ok := officesave.AsRefusal(err)
	if !ok {
		slog.Warn("app ui save: CSV", slog.String("path", rel), slog.String("err", err.Error()))
		writeError(w, r, http.StatusInternalServerError, "save_failed", nil)
		return
	}
	switch rf.Why {
	case "unencodable":
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "csv_encoding", "app_csv_unencodable",
			apierr.Params{"name": name, "encoding": rf.Encoding, "char": string(rf.Char)}, "encoding", rf.Encoding)
	case "package":
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "csv_not_text", "app_csv_not_text", apierr.Params{"name": name})
	case "unreadable":
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "csv_dialect", "app_csv_unreadable", apierr.Params{"name": name}, "field", "encoding")
	case "encoding", "bom":
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "csv_dialect", "app_csv_"+rf.Why,
			apierr.Params{"name": name, "declared": rf.Declared}, "field", rf.Why)
	default:
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "csv_dialect", "app_csv_"+rf.Why,
			apierr.Params{"name": name, "declared": rf.Declared, "found": rf.Found}, "field", rf.Why)
	}
}

// csvCheckDeclared holds a NEW .csv ("save as") to what the app declared
// about it: there is no file to write it the way of, so the bytes are written
// as they came, and only a declaration they contradict is refused.
func csvCheckDeclared(w http.ResponseWriter, r *http.Request, name string, body io.Reader) (io.Reader, bool) {
	decl, err := csvDeclaredOf(r)
	if err != nil {
		writeCSVDeclarationRefused(w, r)
		return nil, false
	}
	if decl == nil {
		return body, true
	}
	saved, err := io.ReadAll(io.LimitReader(body, officesave.MaxSaveBytes+1))
	if err != nil || int64(len(saved)) > officesave.MaxSaveBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", apierr.Params{"max": srvtext.Bytes(langOf(r), officesave.MaxSaveBytes)})
		return nil, false
	}
	if _, _, err := officesave.SaveCSV(officesave.Original{Why: "unreadable"}, saved, decl); err != nil {
		writeCSVRefusal(w, r, name, err)
		return nil, false
	}
	return bytes.NewReader(saved), true
}

// okHeader is the read's answer with one more header on it - a .csv's
// dialect (officesave.TextDialectHeader), the date of the version handed over
// (uiModifiedHeader): set when the answer is the file (2xx), never on a
// refusal - a person who may not read the file learns nothing of it.
type okHeader struct {
	http.ResponseWriter
	wrote bool
	name  string
	value func() (string, bool)
}

func (d *okHeader) WriteHeader(code int) {
	if !d.wrote {
		d.wrote = true
		if code/100 == 2 {
			if v, ok := d.value(); ok {
				d.Header().Set(d.name, v)
			}
		}
	}
	d.ResponseWriter.WriteHeader(code)
}

func (d *okHeader) Write(b []byte) (int, error) {
	if !d.wrote {
		d.WriteHeader(http.StatusOK)
	}
	return d.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer below (Flush).
func (d *okHeader) Unwrap() http.ResponseWriter { return d.ResponseWriter }

// csvDialectOf is the dialect of the .csv at target as JSON, read from its
// first bytes the way the body resolver serves them.
func (h *AppPlugins) csvDialectOf(r *http.Request, target string) (string, bool) {
	storageID, rels, err := h.resolvePaths(r.Context(), 0, []string{target})
	if err != nil || len(rels) != 1 {
		return "", false
	}
	drv, err := h.StorageResolver(storageID)
	if err != nil {
		return "", false
	}
	rc, err := h.openBody(r, drv, storageID, rels[0])
	if err != nil {
		return "", false
	}
	defer rc.Close()
	var head bytes.Buffer
	if _, err := head.ReadFrom(io.LimitReader(rc, officesave.CSVSniffBytes)); err != nil {
		return "", false
	}
	b, err := json.Marshal(officesave.SniffCSV(head.Bytes()).Text())
	if err != nil {
		return "", false
	}
	return string(b), true
}

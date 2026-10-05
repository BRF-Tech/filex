package onlyoffice

// Saving a .csv edited in ONLYOFFICE (filex 0.51; csv.go says what was
// measured, docs/ONLYOFFICE.md → CSV files says it for the operator).
//
// ⚠⚠ The callback used to write whatever the document server handed back to
// the document's own path without reading `filetype`. A document server set to
// `assemblyFormatAsOrigin: false` hands back an XLSX for an edited CSV
// (measured on Docs 9.4: `filetype: "xlsx"`, a zip), and filex would have put
// those bytes under the .csv name: a file every CSV reader then refuses. Here
// a CSV is only ever written as CSV text - converted from the spreadsheet the
// server saved when it is one, refused (logged, the editors told) when it is
// anything else. The other office kinds are callback_format.go's.
//
// The CSV text is then compared with the file it replaces, so that the cells
// nobody changed keep their text (csv_keep.go): 0.51.0 wrote every value as
// ONLYOFFICE's spreadsheet shows it, `007` as `7`.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// csvSaveMaxBytes bounds a saved CSV (and the spreadsheet it is converted
// from): it is read whole, to put the file's own delimiter back. A larger one
// is not written. The comparison that keeps the untouched cells has a bound
// of its own, csvKeepMaxBytes, and a save over that one is still written.
const csvSaveMaxBytes = 256 << 20

// csvSaveTimeout bounds the conversion of a spreadsheet the server saved
// instead of a CSV.
const csvSaveTimeout = 2 * time.Minute

// csvConvertParams names what the conversion asks for, in its key.
const csvConvertParams = "csv-save-v1"

// csvFromTypes are the spreadsheet types a document server may save an
// edited CSV as, and filex converts back.
var csvFromTypes = map[string]bool{"xlsx": true, "xlsm": true, "xlsb": true, "xls": true, "ods": true}

// docExt is a document's type as its name says it: the extension, lower
// case, no dot.
func docExt(name string) string {
	return strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
}

// binaryPackage reports whether b starts like a spreadsheet package and not
// like text: a zip (XLSX, ODS) or an OLE compound file (XLS). Such bytes are
// never written under a .csv name.
func binaryPackage(b []byte) bool {
	return bytes.HasPrefix(b, []byte("PK\x03\x04")) ||
		bytes.HasPrefix(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

// csvSave turns what the document server saved for a .csv into the bytes to
// write: CSV text in the file's own dialect, with the text of the cells
// nobody changed as the file on storage has it (KeepCSV). When the cells
// cannot be kept - the file could not be read, is not UTF-8, is too large -
// the save is written all the same, with the file's own delimiter, byte order
// mark and line ends and ONLYOFFICE's values (RewriteCSV), and the log says
// why (csvNotKept). got is the callback's `filetype` ("" from a server that
// does not say).
// An *errNotWritten is a save that must not be written.
func (s *Service) csvSave(ctx context.Context, drv storage.Driver, node *model.Node, src io.Reader, got string) ([]byte, error) {
	saved, err := io.ReadAll(io.LimitReader(src, csvSaveMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch saved doc: %w", err)
	}
	if int64(len(saved)) > csvSaveMaxBytes {
		return nil, notWritten(refusedTooLarge, srvtext.Vars{"mb": fmt.Sprint(csvSaveMaxBytes >> 20)})
	}
	if got == "" && binaryPackage(saved) {
		// A server that does not name the type: the bytes do.
		got = "xlsx"
	}
	switch {
	case got == "" || got == "csv":
	case csvFromTypes[got]:
		conv, err := s.spreadsheetToCSV(ctx, saved, got, node.Name)
		if err != nil {
			slog.Warn("onlyoffice callback: converting the saved spreadsheet to CSV",
				slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
				slog.String("filetype", got), slog.String("err", err.Error()))
			return nil, notWritten(refusedNotConverted, srvtext.Vars{"format": formatName(got)})
		}
		saved = conv
	default:
		return nil, notWritten(refusedOtherType, srvtext.Vars{"format": formatName(got), "ext": "csv"})
	}
	if binaryPackage(saved) {
		return nil, notWritten(refusedPackage, nil)
	}
	d, original, why := s.csvOriginal(ctx, drv, node)
	if why != "" {
		csvNotKept(node, CSVKept{Why: why})
		return RewriteCSV(saved, d), nil
	}
	// Not kept: out is RewriteCSV's all the same.
	out, kept := KeepCSV(original, saved, d)
	if !kept.Applied {
		csvNotKept(node, kept)
		return out, nil
	}
	slog.Debug("onlyoffice callback: CSV cells kept",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
		slog.Int("records", kept.Records), slog.Int("unchanged", kept.Unchanged),
		slog.Int("restored", kept.Restored), slog.Int("new", kept.New), slog.Int("deleted", kept.Deleted))
	return out, nil
}

// csvNotKept logs why a saved CSV was written with ONLYOFFICE's values. Never
// a cell's text.
//
// ⚠ "check_failed" and "panic" are not a file's doing but filex's: it was
// about to write something that is not the save, or failed lining the records
// up. Those are a warning, the panic with what it said and where, so that the
// bug can be found; the others are what the file is, and say so once per save.
func csvNotKept(node *model.Node, kept CSVKept) {
	const msg = "onlyoffice callback: CSV cells not kept"
	at := []any{slog.Int64("storage", node.StorageID), slog.String("path", node.Path), slog.String("why", kept.Why)}
	switch kept.Why {
	case "empty":
		// An empty file has no cell to keep: nothing to say.
	case "check_failed":
		slog.Warn(msg, at...)
	case "panic":
		slog.Warn(msg, append(at, slog.String("panic", kept.Panic))...)
	default:
		slog.Info(msg, at...)
	}
}

// csvOriginal reads the CSV on storage - the file the save is about to
// replace - the way the document server's download of it does (the body
// resolver: the staged copy while an upload is still transferring). It
// answers how the file is written, sniffed from its first CSVSniffBytes as
// when it was opened, and the whole file for KeepCSV to compare the save
// with. why says when there is no file to compare: "not_utf8" or "too_large"
// (over csvKeepMaxBytes), of which only the head is read, or "unreadable". A
// file whose head could not be read keeps the server's own way (comma, a byte
// order mark): nothing is guessed about a file nobody could read. One whose
// rest could not be read keeps the dialect its head showed.
func (s *Service) csvOriginal(ctx context.Context, drv storage.Driver, node *model.Node) (d CSVDialect, original []byte, why string) {
	unread := CSVDialect{Comma: ',', BOM: true, UTF8: true}
	var rc io.ReadCloser
	src, err := s.Body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err == nil {
		rc, err = src.Open(ctx)
	}
	if err != nil {
		slog.Warn("onlyoffice callback: reading the CSV before its save",
			slog.Int64("storage", node.StorageID), slog.String("path", node.Path), slog.String("err", err.Error()))
		return unread, nil, "unreadable"
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(rc, CSVSniffBytes)); err != nil {
		return unread, nil, "unreadable"
	}
	d = SniffCSV(buf.Bytes())
	if !d.UTF8 {
		// Its text cannot be compared with a UTF-8 save: the rest is not read.
		return d, nil, "not_utf8"
	}
	if node.Size > csvKeepMaxBytes {
		return d, nil, "too_large"
	}
	if rest := node.Size - int64(buf.Len()); rest > 0 {
		buf.Grow(int(rest) + bytes.MinRead)
	}
	// One byte past the bound says the file is longer than its row claims.
	if _, err := buf.ReadFrom(io.LimitReader(rc, csvKeepMaxBytes+1-int64(buf.Len()))); err != nil {
		slog.Warn("onlyoffice callback: reading the CSV before its save",
			slog.Int64("storage", node.StorageID), slog.String("path", node.Path), slog.String("err", err.Error()))
		return d, nil, "unreadable"
	}
	if int64(buf.Len()) > csvKeepMaxBytes {
		return d, nil, "too_large"
	}
	return d, buf.Bytes(), ""
}

// spreadsheetToCSV converts a spreadsheet the document server saved back to
// CSV through its own conversion service: the file is offered to it for this
// one conversion (OfferFile), comma-separated UTF-8 comes back.
func (s *Service) spreadsheetToCSV(ctx context.Context, saved []byte, from, name string) ([]byte, error) {
	conv, err := s.Converter(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, csvSaveTimeout)
	defer cancel()
	f, err := os.CreateTemp("", "filex-oo-csv-*."+from)
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(saved); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(name, path.Ext(name))
	fetch, withdraw, err := s.OfferFile(ctx, tmp, base+"."+from)
	if err != nil {
		return nil, err
	}
	defer withdraw()
	sum := sha256.Sum256(saved)
	// A fresh key for every save: the document server keeps a key's answer,
	// a failure included, for a day (lesson #935).
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	key := ConvertKey(PurposeConvert, csvConvertParams, hex.EncodeToString(sum[:]), from, "csv", hex.EncodeToString(nonce[:]))
	res, err := conv.Convert(ctx, ConvertRequest{
		FetchURL: fetch, FileType: from, OutputType: "csv", Title: base + ".csv", Key: key,
		CodePage: csvCodePageUTF8, Delimiter: csvDelimiterCodes[','],
	})
	if err != nil {
		return nil, err
	}
	return conv.FetchResult(ctx, res, csvSaveMaxBytes)
}

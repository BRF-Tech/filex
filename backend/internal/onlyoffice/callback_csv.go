package onlyoffice

// Saving a .csv edited in ONLYOFFICE (filex 0.51; docs/ONLYOFFICE.md → CSV
// files says it for the operator).
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
// The CSV text is then written in the file's own way (internal/officesave,
// the same for the Office editor app's saves): its delimiter, byte order
// mark, line ends and encoding, the cells nobody changed as the file has
// them (0.51.0 wrote every value as ONLYOFFICE's spreadsheet shows it, `007`
// as `7`). How the server wrote its save is read from the bytes, never
// assumed: Docs 9.4 writes commas and a byte order mark, Docs 10 the file's
// own delimiter (task #255).

import (
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
	"github.com/brf-tech/filex/backend/internal/officesave"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// csvSaveTimeout bounds the conversion of a spreadsheet the server saved
// instead of a CSV.
const csvSaveTimeout = 2 * time.Minute

// csvConvertParams names what the conversion asks for, in its key.
const csvConvertParams = "csv-save-v1"

// csvFromTypes are the spreadsheet types a document server may save an
// edited CSV as, and filex converts back.
var csvFromTypes = map[string]bool{"xlsx": true, "xlsm": true, "xlsb": true, "xls": true, "ods": true}

// csvSave turns what the document server saved for a .csv into the bytes to
// write: CSV text in the file's own dialect and encoding, with the text of
// the cells nobody changed as the file on storage has it
// (officesave.SaveCSV). When the cells cannot be kept - the file could not be
// read whole, is too large - the save is written all the same, in the file's
// way with ONLYOFFICE's values, and the log says why (csvNotKept). got is the
// callback's `filetype` ("" from a server that does not say).
// An *errNotWritten is a save that must not be written.
func (s *Service) csvSave(ctx context.Context, drv storage.Driver, node *model.Node, src io.Reader, got string) ([]byte, error) {
	saved, err := io.ReadAll(io.LimitReader(src, officesave.MaxSaveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch saved doc: %w", err)
	}
	if int64(len(saved)) > officesave.MaxSaveBytes {
		return nil, notWritten(refusedTooLarge, srvtext.Vars{"mb": fmt.Sprint(officesave.MaxSaveBytes >> 20)})
	}
	if got == "" && officesave.IsPackage(saved) {
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
			return nil, notWritten(refusedNotConverted, srvtext.Vars{"format": officesave.FormatName(got)})
		}
		saved = conv
	default:
		return nil, notWritten(refusedOtherType, srvtext.Vars{"format": officesave.FormatName(got), "ext": "csv"})
	}
	out, res, err := officesave.SaveCSV(s.csvOriginal(ctx, drv, node), saved, nil)
	if err != nil {
		return nil, csvRefusal(err)
	}
	if !res.Applied {
		csvNotKept(node, res.CSVKept)
		return out, nil
	}
	slog.Debug("onlyoffice callback: CSV cells kept",
		slog.Int64("storage", node.StorageID), slog.String("path", node.Path),
		slog.String("saved_delimiter", officesave.DelimiterName(res.Saved.Comma)),
		slog.Int("records", res.Records), slog.Int("unchanged", res.Unchanged),
		slog.Int("restored", res.Restored), slog.Int("new", res.New), slog.Int("deleted", res.Deleted))
	return out, nil
}

// csvRefusal is a CSV save officesave refused, in the words the editors are
// told it with.
func csvRefusal(err error) *errNotWritten {
	r, ok := officesave.AsRefusal(err)
	if !ok {
		return notWritten(refusedCSVUnreadable, nil)
	}
	switch r.Why {
	case "package":
		return notWritten(refusedPackage, nil)
	case "unencodable":
		return notWritten(refusedCSVEncoding, srvtext.Vars{"encoding": r.Encoding, "char": string(r.Char)})
	}
	return notWritten(refusedCSVUnreadable, nil)
}

// csvNotKept logs why a saved CSV was written with ONLYOFFICE's values. Never
// a cell's text.
//
// ⚠ "check_failed" and "panic" are not a file's doing but filex's: it was
// about to write something that is not the save, or failed lining the records
// up. Those are a warning, the panic with what it said and where, so that the
// bug can be found; the others are what the file is, and say so once per save.
func csvNotKept(node *model.Node, kept officesave.CSVKept) {
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
// resolver: the staged copy while an upload is still transferring): its head,
// which says how it is written, and the whole file up to
// officesave.KeepMaxBytes for the save to be compared with. A file whose head
// could not be read is one nobody could read: the save is written as the
// server wrote it, nothing guessed. One whose rest could not be read keeps
// the way its head showed.
func (s *Service) csvOriginal(ctx context.Context, drv storage.Driver, node *model.Node) officesave.Original {
	var rc io.ReadCloser
	src, err := s.Body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err == nil {
		rc, err = src.Open(ctx)
	}
	o, err := officesave.ReadOriginalFrom(rc, err, node.Size)
	if err != nil {
		slog.Warn("onlyoffice callback: reading the CSV before its save",
			slog.Int64("storage", node.StorageID), slog.String("path", node.Path), slog.String("err", err.Error()))
	}
	return o
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
	return conv.FetchResult(ctx, res, officesave.MaxSaveBytes)
}

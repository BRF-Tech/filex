package officesave

// SaveCSV: a save over a .csv, for both editors - the Document Server's
// callback (internal/onlyoffice callback_csv.go) and an app's interface
// (api/handlers app_ui.go, editing together included).
//
//	the file the save replaces ─ ReadOriginal ─┐
//	                                           ├─ SaveCSV ─ the bytes to write
//	what the editor saved (+ its declaration) ─┘    or a Refusal: nothing is written
//
// What is written is the save, in the FILE's way: its delimiter, its byte
// order mark or none, its line ends, its encoding; every record nobody
// changed byte for byte, every cell nobody changed as its text was
// (KeepCSV). What the editor's bytes are is read from them (dialect.go).

import (
	"bytes"
	"errors"
	"io"
)

// MaxSaveBytes bounds a saved CSV: it is read whole, to be written in the
// file's own way. A larger one is not written.
const MaxSaveBytes = 256 << 20

// Original is the file a save replaces, as far as it could be read.
type Original struct {
	// Head is its first bytes (CSVSniffBytes at most): how it is written is
	// read from them. nil when nothing of it could be read.
	Head []byte
	// Body is the whole file, nil when it is not held (Why says why).
	Body []byte
	// Why it is not held: "too_large" (over KeepMaxBytes), "unreadable" (the
	// read broke off).
	Why string
}

// ReadOriginal reads the file a save is about to replace: its head, and the
// whole of it up to KeepMaxBytes. size is its size as the catalogue says it
// (the buffer's first guess, and a file larger than the bound is not read
// past its head). The error is a read that broke off, for the log; what was
// read is in the Original all the same.
func ReadOriginal(r io.Reader, size int64) (Original, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(r, CSVSniffBytes)); err != nil {
		return Original{Why: "unreadable"}, err
	}
	// An empty file is read all the same: it has a way (the default one),
	// only no cell.
	head := append([]byte{}, buf.Bytes()...)
	if size > KeepMaxBytes {
		return Original{Head: head, Why: "too_large"}, nil
	}
	if rest := size - int64(buf.Len()); rest > 0 {
		buf.Grow(int(rest) + bytes.MinRead)
	}
	// One byte past the bound says the file is longer than its row claims.
	if _, err := buf.ReadFrom(io.LimitReader(r, KeepMaxBytes+1-int64(buf.Len()))); err != nil {
		return Original{Head: head, Why: "unreadable"}, err
	}
	all := buf.Bytes()
	if all == nil {
		all = []byte{}
	}
	if int64(len(all)) > KeepMaxBytes {
		return Original{Head: all[:min(len(all), CSVSniffBytes)], Why: "too_large"}, nil
	}
	return Original{Head: all[:min(len(all), CSVSniffBytes)], Body: all}, nil
}

// ReadOriginalFrom is ReadOriginal of a file the caller opened: err is the
// opening's (the file could not be opened: nothing of it is read). The error
// returned is the opening's or the read's, for the caller's log.
func ReadOriginalFrom(rc io.ReadCloser, err error, size int64) (Original, error) {
	if err != nil {
		return Original{Why: "unreadable"}, err
	}
	defer rc.Close()
	return ReadOriginal(rc, size)
}

// CSVSave says what SaveCSV did, for the log and the tests.
type CSVSave struct {
	CSVKept
	// File is how the written file is written: the file's own dialect (its
	// encoding may be another code page that reads the file's bytes alike,
	// one that holds the save's letters: alikePages).
	File CSVDialect
	// Saved is how the editor wrote its save.
	Saved CSVDialect
}

// SaveCSV turns what an editor saved over a .csv into the bytes to write.
// decl is what the editor declared about its bytes (nil: nothing). An error
// is a *Refusal - the save must not be written - when the bytes are not what
// was declared, cannot be read for this file, or hold a character the file's
// encoding cannot.
//
// When the cells cannot be kept (the file is too large, could not be read
// whole, ...) the save is still written in the file's way, with the editor's
// values, and CSVSave.Why says why (P3, csv_keep.go). When nothing of the
// file could be read, the save is written as the editor wrote it: nothing is
// guessed about a file nobody could read.
func SaveCSV(o Original, saved []byte, decl *Declared) ([]byte, CSVSave, error) {
	if IsPackage(saved) {
		return nil, CSVSave{}, &Refusal{Why: "package"}
	}
	if int64(len(saved)) > MaxSaveBytes {
		return nil, CSVSave{}, errors.New("officesave: a save over the size bound")
	}
	var declComma byte
	if decl != nil {
		declComma = decl.Delimiter
	}
	if o.Head == nil {
		// Nothing of the file was read: the save as it was written, once it
		// reads as what it says it is.
		st, err := readSaved(saved, DefaultCSVDialect, decl)
		if err != nil {
			return nil, CSVSave{}, err
		}
		comma, err := savedComma(st.text, DefaultCSVDialect, nil, declComma)
		if err != nil {
			return nil, CSVSave{}, err
		}
		sd := savedDialect(st, comma)
		return saved, CSVSave{CSVKept: CSVKept{Why: "unreadable"}, File: sd, Saved: sd}, nil
	}
	f := fileOf(o)
	st, err := readSaved(saved, f.d, decl)
	if err != nil {
		return nil, CSVSave{}, err
	}
	work, d, err := f.inFileForm(st.text)
	if err != nil {
		return nil, CSVSave{}, err
	}
	comma, err := savedComma(work, d, f.sample, declComma)
	if err != nil {
		return nil, CSVSave{}, err
	}
	res := CSVSave{File: d, Saved: savedDialect(st, comma)}
	// The text in the file's dialect, without a byte order mark: the mark is
	// the encoding's, put on below.
	plain := CSVDialect{Comma: d.Comma, CRLF: d.CRLF, UTF8: true}
	rewrite := func() []byte { return rewriteCSV(work, comma, plain) }
	var out []byte
	if f.body != nil {
		as := csvKeepAs{from: comma, page: f.page != nil}
		out, res.CSVKept = keepOr(func() ([]byte, CSVKept) { return keepCSVAs(f.body, work, plain, as) }, rewrite)
	} else {
		out, res.CSVKept = rewrite(), CSVKept{Why: f.why}
	}
	return f.encode(out, d), res, nil
}

// savedDialect is how the editor wrote its save.
func savedDialect(st savedText, comma byte) CSVDialect {
	d := CSVDialect{Comma: comma, BOM: st.bom, CRLF: st.crlf, UTF8: st.enc == EncUTF8}
	if !d.UTF8 {
		d.Encoding = st.enc
	}
	return d
}

// fileForm is the file a save replaces, in the form it is compared in: UTF-8
// for a UTF-8 or UTF-16 file, its own bytes for one in a code page (whose
// delimiters, quotes and digits are ASCII all the same).
type fileForm struct {
	d CSVDialect
	// page is its code page, nil for UTF-8 and UTF-16.
	page *codePage
	// body is the whole file in that form, without its byte order mark; nil
	// when it is not held, and why says why.
	body []byte
	why  string
	// sample is what of it is held: body, or its head.
	sample []byte
	// raw is the file's bytes after the mark, as they are (for the code
	// pages that read them alike).
	raw []byte
}

// fileOf reads the file a save replaces. How it is written is read from its
// head, its encoding from all of it when it is held: a code page after 64 KiB
// of ASCII is the file's encoding too.
func fileOf(o Original) fileForm {
	f := fileForm{d: SniffCSV(o.Head), why: o.Why}
	held := o.Body
	if held == nil {
		held = o.Head
	}
	if f.d.Encoding == "" && o.Body != nil {
		// The head read as UTF-8: is the rest?
		if whole := SniffCSV(o.Body); !whole.UTF8 && whole.Encoding != EncUTF16LE && whole.Encoding != EncUTF16BE {
			f.d.UTF8, f.d.Encoding = false, whole.Encoding
		}
	}
	switch f.d.Encoding {
	case EncUTF16LE, EncUTF16BE:
		body := held
		if f.d.BOM {
			body = held[2:]
		}
		text := decodeUTF16(body[:len(body)&^1], f.d.Encoding == EncUTF16BE)
		f.sample = text
		if o.Body != nil {
			// Its records are written back from this text: only when it reads
			// back as the very same bytes.
			if len(body)%2 == 0 && bytes.Equal(encodeUTF16(text, f.d.Encoding == EncUTF16BE), body) {
				f.body = text
			} else {
				f.why = "not_text"
			}
		}
	case "":
		f.sample = bytes.TrimPrefix(held, utf8BOM)
		if o.Body != nil {
			f.body = bytes.TrimPrefix(o.Body, utf8BOM)
		}
	default:
		f.page = pageOf(f.d.Encoding)
		f.raw = bytes.TrimPrefix(held, utf8BOM)
		f.sample = f.raw
		if o.Body != nil {
			f.body = bytes.TrimPrefix(o.Body, utf8BOM)
		}
	}
	return f
}

// inFileForm is the save's text (UTF-8) in the form the file is compared in,
// and the file's dialect it is written in. For a file in a code page that is
// the save encoded in it - or in another page that reads every byte of the
// file alike and holds the save's letters - and a character none of them
// holds refuses the save.
func (f *fileForm) inFileForm(text []byte) ([]byte, CSVDialect, error) {
	if f.page == nil {
		return text, f.d, nil
	}
	var first rune
	for i, p := range alikePages(f.raw, f.page) {
		out, bad, ok := encodePage(text, p)
		if ok {
			d := f.d
			d.Encoding = p.label
			return out, d, nil
		}
		if i == 0 {
			first = bad
		}
	}
	return nil, f.d, &Refusal{Why: "unencodable", Encoding: f.page.label, Char: first}
}

// encode writes text (the file's form, no byte order mark) in the file's
// encoding, with its mark when it had one.
func (f *fileForm) encode(text []byte, d CSVDialect) []byte {
	switch d.Encoding {
	case EncUTF16LE, EncUTF16BE:
		be := d.Encoding == EncUTF16BE
		out := encodeUTF16(text, be)
		if !d.BOM {
			return out
		}
		mark := []byte{0xFF, 0xFE}
		if be {
			mark = []byte{0xFE, 0xFF}
		}
		return append(mark, out...)
	}
	if !d.BOM {
		return text
	}
	out := make([]byte, 0, len(utf8BOM)+len(text))
	return append(append(out, utf8BOM...), text...)
}

package onlyoffice

// CSV files in ONLYOFFICE (filex 0.51, docs/ONLYOFFICE.md → CSV files).
//
// A .csv opens in ONLYOFFICE's spreadsheet editor. Two things about that are
// measured against the document server (Docs 9.4) and are why this file
// exists:
//
//   - Opening. ONLYOFFICE asks for the encoding and the delimiter in a dialog
//     ("Choose CSV options") before it shows anything, unless the editor's
//     config carries them in `document.options` (`codePage` and `delimiter`,
//     or `delimiterChar`; sdkjs spreadsheet_api._onNeedParams). filex reads
//     the start of the file and passes what it finds (OpenOptions), so a CSV
//     opens straight away. A file that is not UTF-8 gets no options: the
//     dialog asks, because guessing a legacy code page wrong would turn every
//     non-ASCII letter into another one and the save would keep it.
//
//   - Saving. With the server's default (`assemblyFormatAsOrigin: true`) the
//     callback hands back a CSV (`filetype: "csv"`), but ONLYOFFICE writes it
//     its own way whatever the file was: comma-separated, a UTF-8 byte order
//     mark in front, "\n" line ends. A semicolon file came back with commas.
//     And it writes every cell as its spreadsheet shows it: a cell that reads
//     as a number or a date comes back as one, edited or not (`007` as `7`).
//     KeepCSV (csv_keep.go) lines the saved records up with the file the save
//     replaces and writes the file's own bytes back for what nobody changed;
//     RewriteCSV, which 0.51.0 wrote alone, puts back only the file's own
//     way (its delimiter, its byte order mark or none, its line ends) and is
//     what is written whenever the cells cannot be kept. With
//     `assemblyFormatAsOrigin: false` the callback hands back an XLSX
//     (`filetype: "xlsx"`); the callback converts that to CSV first
//     (callback_csv.go csvSave) and never writes it under the .csv name.

import (
	"bytes"
	"unicode/utf8"
)

// CSVSniffBytes is how much of a CSV is read to learn how it is written.
const CSVSniffBytes = 64 << 10

// csvSniffLines bounds the lines the delimiter is judged on.
const csvSniffLines = 50

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// CSVDialect is how a CSV file is written.
type CSVDialect struct {
	// Comma is the delimiter: ',', ';', '\t' or '|'.
	Comma byte
	// BOM: the file starts with a UTF-8 byte order mark.
	BOM bool
	// CRLF: its lines end in "\r\n" (else "\n").
	CRLF bool
	// UTF8: its bytes are UTF-8 (ASCII included). False for UTF-16 and for a
	// legacy code page.
	UTF8 bool
}

// DefaultCSVDialect is a CSV with nothing to say about itself: an empty file,
// or one filex could not read. Comma-separated UTF-8, no byte order mark,
// "\n" line ends.
var DefaultCSVDialect = CSVDialect{Comma: ',', UTF8: true}

// csvCandidates are the delimiters a CSV is judged for, in the order a tie is
// broken.
var csvCandidates = []byte{',', ';', '\t', '|'}

// SniffCSV reads how a CSV is written from its first bytes (CSVSniffBytes of
// them is plenty; fewer is fine). The delimiter is the candidate that splits
// the first lines (outside quotes) into the same number of fields, the most
// of them; with none consistent, the most frequent; with none at all, a
// comma.
func SniffCSV(head []byte) CSVDialect {
	d := DefaultCSVDialect
	if len(head) >= 2 && ((head[0] == 0xFF && head[1] == 0xFE) || (head[0] == 0xFE && head[1] == 0xFF)) {
		// UTF-16: every other byte is zero, nothing below reads it.
		d.UTF8 = false
		return d
	}
	body := head
	if bytes.HasPrefix(body, utf8BOM) {
		d.BOM = true
		body = body[len(utf8BOM):]
	}
	d.UTF8 = utf8.Valid(trimPartialRune(body))
	if i := bytes.IndexByte(body, '\n'); i > 0 && body[i-1] == '\r' {
		d.CRLF = true
	}
	d.Comma = sniffComma(body)
	return d
}

// trimPartialRune drops a rune cut in half at the end of b (the head of a
// file read up to a byte count), so a valid UTF-8 file is not judged invalid
// for where the read stopped.
func trimPartialRune(b []byte) []byte {
	for i := 1; i <= utf8.UTFMax && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < utf8.RuneSelf {
			return b
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// sniffComma counts each candidate per line, outside quotes, over the first
// complete lines.
func sniffComma(b []byte) byte {
	counts := make([][]int, len(csvCandidates))
	line := make([]int, len(csvCandidates))
	lines := 0
	quoted := false
	ended := func() {
		for i := range line {
			counts[i] = append(counts[i], line[i])
			line[i] = 0
		}
		lines++
	}
	for _, c := range b {
		if lines >= csvSniffLines {
			break
		}
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\n':
			ended()
		default:
			for i, cand := range csvCandidates {
				if c == cand {
					line[i]++
				}
			}
		}
	}
	if lines == 0 {
		// One line and no line end (a short file, or a header alone): it is
		// what there is.
		ended()
	}
	best, bestN, consistent := byte(','), 0, false
	for i, cand := range csvCandidates {
		n, same, total := counts[i][0], true, 0
		for _, k := range counts[i] {
			total += k
			if k != n {
				same = false
			}
		}
		switch {
		case same && n > 0 && (!consistent || n > bestN):
			best, bestN, consistent = cand, n, true
		case !consistent && total > bestN:
			best, bestN = cand, total
		}
	}
	return best
}

// csvDelimiterCodes are ONLYOFFICE's numbers for the delimiters it names
// (sdkjs c_oAscCsvDelimiter). Any other one goes as `delimiterChar`.
var csvDelimiterCodes = map[byte]int{'\t': 1, ';': 2, ':': 3, ',': 4, ' ': 5}

// csvCodePageUTF8 is UTF-8's code page, as ONLYOFFICE takes it.
const csvCodePageUTF8 = 65001

// OpenOptions is the editor config's `document.options` that opens the CSV
// without ONLYOFFICE's "Choose CSV options" dialog: UTF-8 and its delimiter.
// Nil for a file that is not UTF-8: the dialog asks which encoding it is.
func (d CSVDialect) OpenOptions() map[string]any {
	if !d.UTF8 {
		return nil
	}
	opts := map[string]any{"codePage": csvCodePageUTF8}
	if code, ok := csvDelimiterCodes[d.Comma]; ok {
		opts["delimiter"] = code
	} else {
		opts["delimiterChar"] = string(rune(d.Comma))
	}
	return opts
}

// KeepsBOM reports whether the saved file starts with a byte order mark: when
// the file had one, and when it was not UTF-8 (the save is UTF-8 now, and the
// mark is what tells a reader so).
func (d CSVDialect) KeepsBOM() bool { return d.BOM || !d.UTF8 }

// RewriteCSV turns the CSV the document server saved (comma-separated, a
// UTF-8 byte order mark, "\n" line ends) back into the file's own way of
// writing: its delimiter, its byte order mark or none, its line ends. The
// values are not touched: they stay ONLYOFFICE's, `7` for a cell that held
// `007`. KeepCSV is what keeps the file's own text, and this is what is
// written when it cannot. A field is quoted when the delimiter, a quote or a
// line end is in it.
func RewriteCSV(saved []byte, d CSVDialect) []byte {
	body := bytes.TrimPrefix(saved, utf8BOM)
	if d.Comma != ',' || d.CRLF {
		comma := d.Comma
		if comma == 0 {
			comma = ','
		}
		body = redelimit(body, comma, d.CRLF)
	}
	if !d.KeepsBOM() {
		return body
	}
	out := make([]byte, 0, len(utf8BOM)+len(body))
	return append(append(out, utf8BOM...), body...)
}

// redelimit reads comma-separated records (csvReader) and writes them with
// comma as the delimiter and "\r\n" or "\n" between records. Records keep
// their fields, empty lines included (encoding/csv would drop those). A field
// is written as it is read: a line of a million delimiters is a million
// fields, and holding them all would cost a hundred times the line.
func redelimit(in []byte, comma byte, crlf bool) []byte {
	var out bytes.Buffer
	out.Grow(len(in) + len(in)/16)
	eol := "\n"
	if crlf {
		eol = "\r\n"
	}
	r := csvReader{in: in, comma: ','}
	for r.more() {
		for {
			f, last, _, term := r.field()
			writeCSVField(&out, f.val, comma)
			if !last {
				out.WriteByte(comma)
				continue
			}
			if term > 0 {
				out.WriteString(eol)
			}
			break
		}
	}
	return out.Bytes()
}

// writeCSVField writes one value, in quotes when the delimiter, a quote or a
// line end is in it.
func writeCSVField(out *bytes.Buffer, val []byte, comma byte) {
	if bytes.IndexByte(val, comma) < 0 && bytes.IndexByte(val, '"') < 0 &&
		bytes.IndexByte(val, '\n') < 0 && bytes.IndexByte(val, '\r') < 0 {
		out.Write(val)
		return
	}
	out.WriteByte('"')
	for {
		i := bytes.IndexByte(val, '"')
		if i < 0 {
			break
		}
		out.Write(val[:i+1])
		out.WriteByte('"')
		val = val[i+1:]
	}
	out.Write(val)
	out.WriteByte('"')
}

// csvField is one field of a record: as it is written, and what it says.
type csvField struct {
	// raw is the field's bytes in the file, its quotes included.
	raw []byte
	// val is its value: the quotes gone, a doubled quote one.
	val []byte
}

// csvReader reads the records of a CSV one after another: the one reader of
// the package, for the file on storage (its own delimiter) and for what the
// document server saved (a comma). RFC 4180: a field in quotes may hold the
// delimiter, quotes doubled, and line ends; a record ends at "\n" or "\r\n"
// outside quotes. A lone "\r" ends nothing.
//
// Odd input is read, never repaired and never lost: what stands between a
// closing quote and the delimiter belongs to the value, a quote nobody closed
// runs to the end of the file, and an empty line is a record of one empty
// field.
//
// A record is read whole (next) or a field at a time (field). ⚠ next holds
// every field of the record, 48 bytes each, and a record may be a whole file
// of delimiters: it is for records known to be narrow (csvKeepMaxFields).
type csvReader struct {
	in    []byte
	comma byte
	// pos is where the next field starts. Set it to a record's start to read
	// that record again.
	pos int

	fields []csvField
	// buf holds the values that are not a piece of in (a doubled quote, text
	// after a closing quote).
	buf []byte
}

// more reports whether a record is left. A final line end starts none. Asked
// between records: inside one, field says when it is over.
func (r *csvReader) more() bool { return r.pos < len(r.in) }

// next reads the record at pos. Its bytes are in[start:end] for the pos it was
// called at, and its line end the term bytes after them: 2 ("\r\n"), 1 ("\n")
// or 0 at the end of a file that has none. The fields are valid until the
// next call.
func (r *csvReader) next() (fields []csvField, end, term int) {
	r.fields, r.buf = r.fields[:0], r.buf[:0]
	for {
		f, last, end, term := r.step()
		r.fields = append(r.fields, f)
		if last {
			return r.fields, end, term
		}
	}
}

// field reads the field at pos, valid until the next call. last: the record
// ends with it, at end and with a line end of term bytes, as next says them;
// otherwise a delimiter followed, and the record's next field is at pos even
// at the end of the file (a delimiter there is followed by an empty field).
func (r *csvReader) field() (f csvField, last bool, end, term int) {
	r.buf = r.buf[:0]
	return r.step()
}

// step reads one field, its value appended to buf when it is not a piece of
// in.
func (r *csvReader) step() (f csvField, last bool, end, term int) {
	in, n, comma := r.in, len(r.in), r.comma
	i := r.pos
	start := i
	// tail is where a "\r" may be the line end's: after the closing quote, or
	// at the field's start.
	tail := i
	var val []byte
	if i < n && in[i] == '"' {
		i++
		open, doubled := i, false
		for i < n {
			q := bytes.IndexByte(in[i:], '"')
			if q < 0 {
				i = n
				break
			}
			i += q
			if i+1 < n && in[i+1] == '"' {
				doubled = true
				i += 2
				continue
			}
			break
		}
		inner := in[open:i]
		if i < n {
			i++
		}
		tail = i
		// Anything between the closing quote and the delimiter is kept as it
		// stands (a malformed field is not repaired, nor lost).
		after := false
		for i < n && in[i] != comma && in[i] != '\n' {
			if in[i] != '\r' {
				after = true
			}
			i++
		}
		val = inner
		if doubled || after {
			at := len(r.buf)
			for {
				q := bytes.Index(inner, []byte{'"', '"'})
				if q < 0 {
					break
				}
				r.buf = append(r.buf, inner[:q+1]...)
				inner = inner[q+2:]
			}
			r.buf = append(r.buf, inner...)
			for _, c := range in[tail:i] {
				if c != '\r' {
					r.buf = append(r.buf, c)
				}
			}
			val = r.buf[at:]
		}
	} else {
		for i < n && in[i] != comma && in[i] != '\n' {
			i++
		}
		val = in[start:i]
		if len(val) > 0 && val[len(val)-1] == '\r' && (i == n || in[i] == '\n') {
			val = val[:len(val)-1]
		}
	}
	if i < n && in[i] == comma {
		// A delimiter: one more field, an empty one if nothing follows.
		r.pos = i + 1
		return csvField{raw: in[start:i], val: val}, false, 0, 0
	}
	// A line end, or the end of the file: the record is over.
	end = i
	if i < n {
		term = 1
		if i > tail && in[i-1] == '\r' {
			end, term = i-1, 2
		}
	}
	r.pos = end + term
	return csvField{raw: in[start:end], val: val}, true, end, term
}

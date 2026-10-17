package officesave

// CSV files edited in an office editor (docs/ONLYOFFICE.md → CSV files;
// docs/APP-PLUGINS-API.md → CSV files: how they are written).
//
// A .csv is edited in a spreadsheet: the ONLYOFFICE Document Server's, or the
// Office editor app's (the same engine, in the browser). Both hand back the
// whole table as CSV text when it is saved, and neither writes it the way
// the file was written. What was measured, and why this package exists:
//
//   - Docs 9.4 writes every CSV comma-separated, a UTF-8 byte order mark in
//     front, "\n" line ends, whatever the file was: a semicolon file came
//     back with commas. Docs 10.0 keeps the file's own delimiter (the one the
//     editor was opened with). filex 0.51 to 0.56 assumed the comma, read a
//     Docs 10 save of a semicolon file as one column, and wrote every row as
//     one quoted field (task #255). So nothing about the saved bytes is
//     assumed any more: their encoding, byte order mark, line ends and
//     delimiter are read from them (dialect.go), and what the producer
//     declares about them (an app's `csv` save option) is checked against
//     them; a declaration the bytes contradict is refused, never written.
//
//   - The editor writes every cell as its spreadsheet shows it: a cell that
//     reads as a number or a date comes back as one, edited or not (`007` as
//     `7`). KeepCSV (csv_keep.go) lines the saved records up with the file
//     the save replaces and writes the file's own bytes back for what nobody
//     changed; RewriteCSV puts back only the file's own way (its delimiter,
//     its byte order mark or none, its line ends) and is what is written
//     whenever the cells cannot be kept.
//
//   - The file keeps its encoding (encoding.go): UTF-8, UTF-16, or a
//     single-byte code page filex recognises from its bytes. SaveCSV
//     (save.go) is the whole of it, for both editors.

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
	// BOM: the file starts with a byte order mark (UTF-8's, or UTF-16's for a
	// UTF-16 file).
	BOM bool
	// CRLF: its lines end in "\r\n" (else "\n").
	CRLF bool
	// UTF8: its bytes are UTF-8 (ASCII included). False for UTF-16 and for a
	// code page.
	UTF8 bool
	// Encoding is what it is written in when it is not UTF-8: "utf-16le",
	// "utf-16be", or a code page ("windows-1254"), as a browser's TextDecoder
	// names them (encoding.go). Empty for UTF-8, and for a file whose encoding
	// nobody read (a dialect written by hand).
	Encoding string
}

// Charset is the dialect's encoding by its name: "utf-8" for a UTF-8 file,
// "" when it is not UTF-8 and nobody said what it is.
func (d CSVDialect) Charset() string {
	if d.UTF8 && d.Encoding == "" {
		return EncUTF8
	}
	return d.Encoding
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
// comma. The encoding: UTF-16 by its byte order mark (or by the zero bytes
// of its ASCII), UTF-8 when the bytes are, else the code page its letters
// read best in (detectPage).
func SniffCSV(head []byte) CSVDialect {
	d := DefaultCSVDialect
	if enc, bom := utf16Of(head); enc != "" {
		d.UTF8, d.Encoding, d.BOM = false, enc, bom
		body := head
		if bom {
			body = head[2:]
		}
		text := decodeUTF16(body[:len(body)&^1], enc == EncUTF16BE)
		d.CRLF = firstLineCRLF(text)
		d.Comma = sniffComma(text, 0)
		return d
	}
	body := head
	if bytes.HasPrefix(body, utf8BOM) {
		d.BOM = true
		body = body[len(utf8BOM):]
	}
	d.UTF8 = utf8.Valid(trimPartialRune(body))
	if !d.UTF8 {
		d.Encoding = detectPage(body)
	}
	d.CRLF = firstLineCRLF(body)
	d.Comma = sniffComma(body, 0)
	return d
}

// firstLineCRLF: the first line of b ends in "\r\n".
func firstLineCRLF(b []byte) bool {
	i := bytes.IndexByte(b, '\n')
	return i > 0 && b[i-1] == '\r'
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

// csvLineCounts counts each of cands per line, outside quotes, over the first
// complete lines (csvSniffLines of them): counts[i][l] is cands[i] on line l.
// One line and no line end (a short file, or a header alone) is what there
// is.
func csvLineCounts(b []byte, cands []byte) [][]int {
	counts := make([][]int, len(cands))
	line := make([]int, len(cands))
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
			for i, cand := range cands {
				if c == cand {
					line[i]++
				}
			}
		}
	}
	if lines == 0 {
		ended()
	}
	return counts
}

// sniffComma counts each candidate per line, outside quotes, over the first
// complete lines. prefer, when it is not 0, wins a tie: the file's own
// delimiter, when the lines of a save split as well by another.
func sniffComma(b []byte, prefer byte) byte {
	counts := csvLineCounts(b, csvCandidates)
	best, bestN, consistent := byte(','), 0, false
	if prefer != 0 {
		best = prefer
	}
	for i, cand := range csvCandidates {
		n, same, total := counts[i][0], true, 0
		for _, k := range counts[i] {
			total += k
			if k != n {
				same = false
			}
		}
		switch {
		case same && n > 0 && (!consistent || n > bestN || (n == bestN && cand == prefer)):
			best, bestN, consistent = cand, n, true
		case !consistent && (total > bestN || (total == bestN && total > 0 && cand == prefer)):
			best, bestN = cand, total
		}
	}
	return best
}

// KeepsBOM reports whether the saved file starts with a UTF-8 byte order
// mark: when the file had one, and when it was not UTF-8 and nobody read what
// it was (the save is UTF-8 then, and the mark is what tells a reader so).
// SaveCSV writes a file in its own encoding, with its own mark or none.
func (d CSVDialect) KeepsBOM() bool { return d.BOM || !d.UTF8 }

// RewriteCSV turns a comma-separated UTF-8 save (what Docs 9.4 writes: a
// UTF-8 byte order mark, "\n" line ends) into the file's own way of writing:
// its delimiter, its byte order mark or none, its line ends. The values are
// not touched: they stay the editor's, `7` for a cell that held `007`.
// KeepCSV is what keeps the file's own text, and this is what is written when
// it cannot. A field is quoted when the delimiter, a quote or a line end is
// in it.
func RewriteCSV(saved []byte, d CSVDialect) []byte {
	return rewriteCSV(saved, ',', d)
}

// rewriteCSV is RewriteCSV for a save whose records are separated by from.
func rewriteCSV(saved []byte, from byte, d CSVDialect) []byte {
	body := bytes.TrimPrefix(saved, utf8BOM)
	comma := d.Comma
	if comma == 0 {
		comma = ','
	}
	if from != comma || d.CRLF || bytes.IndexByte(body, '\r') >= 0 {
		body = redelimit(body, from, comma, d.CRLF)
	}
	if !d.KeepsBOM() {
		return body
	}
	out := make([]byte, 0, len(utf8BOM)+len(body))
	return append(append(out, utf8BOM...), body...)
}

// redelimit reads records separated by from (csvReader) and writes them with
// comma as the delimiter and "\r\n" or "\n" between records. Records keep
// their fields, empty lines included (encoding/csv would drop those). A field
// is written as it is read: a line of a million delimiters is a million
// fields, and holding them all would cost a hundred times the line.
func redelimit(in []byte, from, comma byte, crlf bool) []byte {
	var out bytes.Buffer
	out.Grow(len(in) + len(in)/16)
	eol := "\n"
	if crlf {
		eol = "\r\n"
	}
	r := csvReader{in: in, comma: from}
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
// editor saved (its delimiter). RFC 4180: a field in quotes may hold the
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

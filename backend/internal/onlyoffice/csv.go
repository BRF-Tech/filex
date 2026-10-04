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
//     RewriteCSV puts the file's own way back (its delimiter, its byte order
//     mark or none, its line ends) before it is written. With
//     `assemblyFormatAsOrigin: false` the callback hands back an XLSX
//     (`filetype: "xlsx"`); the callback converts that to CSV first
//     (callback.go csvSave) and never writes it under the .csv name.

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
// values are not touched; a field is quoted when the delimiter, a quote or a
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

// redelimit reads comma-separated records (RFC 4180: a field in quotes may
// hold commas, quotes doubled, and line ends) and writes them with comma as
// the delimiter and "\r\n" or "\n" between records. Records keep their
// fields, empty lines included (encoding/csv would drop those).
func redelimit(in []byte, comma byte, crlf bool) []byte {
	var out bytes.Buffer
	out.Grow(len(in) + len(in)/16)
	eol := "\n"
	if crlf {
		eol = "\r\n"
	}
	var field []byte
	first := true
	flush := func() {
		if !first {
			out.WriteByte(comma)
		}
		first = false
		if bytes.IndexByte(field, comma) >= 0 || bytes.IndexByte(field, '"') >= 0 ||
			bytes.IndexByte(field, '\n') >= 0 || bytes.IndexByte(field, '\r') >= 0 {
			out.WriteByte('"')
			out.Write(bytes.ReplaceAll(field, []byte{'"'}, []byte{'"', '"'}))
			out.WriteByte('"')
		} else {
			out.Write(field)
		}
		field = field[:0]
	}
	i, n := 0, len(in)
	for i < n {
		// One field.
		if in[i] == '"' {
			i++
			for i < n {
				if in[i] == '"' {
					if i+1 < n && in[i+1] == '"' {
						field = append(field, '"')
						i += 2
						continue
					}
					i++
					break
				}
				field = append(field, in[i])
				i++
			}
			// Anything between the closing quote and the delimiter is kept
			// as it stands (a malformed field is not repaired, nor lost).
			for i < n && in[i] != ',' && in[i] != '\n' {
				if in[i] != '\r' {
					field = append(field, in[i])
				}
				i++
			}
		} else {
			for i < n && in[i] != ',' && in[i] != '\n' {
				field = append(field, in[i])
				i++
			}
			if len(field) > 0 && field[len(field)-1] == '\r' && (i == n || in[i] == '\n') {
				field = field[:len(field)-1]
			}
		}
		flush()
		if i >= n {
			break
		}
		if in[i] == ',' {
			i++
			if i == n {
				// A trailing delimiter: one more, empty field.
				flush()
			}
			continue
		}
		// A line end: the record is over.
		out.WriteString(eol)
		first = true
		i++
	}
	return out.Bytes()
}

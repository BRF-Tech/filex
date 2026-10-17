package officesave

// How the saved bytes are written: read from them, never assumed (task #255).
//
// Two editors save a CSV: the ONLYOFFICE Document Server, which says nothing
// about its bytes, and an app's interface, which may say (`csv` in its save:
// docs/APP-PLUGINS-API.md → CSV files: how they are written). What they wrote
// is read the same way for both:
//
//   - the encoding: a byte order mark says it (UTF-8's, UTF-16's); else UTF-8
//     when the bytes are; else the file's own code page, when the file is in
//     one. Anything else is not read at all: a save the file's encoding cannot
//     be told for is refused (Refusal "unreadable"), not guessed;
//   - the line ends, outside quotes;
//   - the delimiter: the one under which the save reads as the file's table
//     (savedComma).
//
// What the producer declared is checked against what the bytes say. A
// declaration they contradict is refused: the bytes would be read as one
// table and written as another, which is how #255 turned a six-column file
// into a one-column one without a word.

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// TextDialectHeader is the response header an app's read of a .csv carries
// its dialect in (TextDialect, JSON): GET /api/files/plugins/ui/{plugin}/{view}/read.
const TextDialectHeader = "X-Filex-Text-Dialect"

// TextDialect is how a CSV is written, as an app's interface is told it
// (`text_dialect`).
type TextDialect struct {
	// Delimiter is one character: ",", ";", "\t" or "|".
	Delimiter string `json:"delimiter"`
	// BOM: the file starts with a byte order mark.
	BOM bool `json:"bom"`
	// CRLF: its lines end in "\r\n".
	CRLF bool `json:"crlf"`
	// Encoding is its encoding's name as a browser's TextDecoder takes it:
	// "utf-8", "utf-16le", "utf-16be", "windows-1254"...
	Encoding string `json:"encoding"`
}

// Text is the dialect as an app's interface is told it.
func (d CSVDialect) Text() TextDialect {
	comma := d.Comma
	if comma == 0 {
		comma = ','
	}
	enc := d.Charset()
	if enc == "" {
		enc = EncUTF8
	}
	return TextDialect{Delimiter: string(rune(comma)), BOM: d.BOM, CRLF: d.CRLF, Encoding: enc}
}

// Declared is what the producer of a save says about how it is written (an
// app's `csv` save option). A field left out is not said.
type Declared struct {
	// Delimiter is its one character, 0 when not said.
	Delimiter byte
	// Encoding is its name (EncodingLabel's), "" when not said.
	Encoding string
	// BOM: it starts with a byte order mark; nil when not said.
	BOM *bool
	// EOL is "crlf" or "lf", "" when not said.
	EOL string
}

// ErrDeclaration is a declaration filex cannot take: a delimiter that is not
// one character of `,;\t|`, an encoding it does not know, a line end other
// than crlf or lf.
var ErrDeclaration = errors.New("officesave: a csv declaration filex cannot take")

// ParseDeclared reads a declaration from its four values as an app's save
// sends them (query csv_delimiter, csv_encoding, csv_bom, csv_eol). nil when
// none is given.
func ParseDeclared(delimiter, encoding, bom, eol string) (*Declared, error) {
	if delimiter == "" && encoding == "" && bom == "" && eol == "" {
		return nil, nil
	}
	d := &Declared{}
	if delimiter != "" {
		if len(delimiter) != 1 || bytes.IndexByte(csvCandidates, delimiter[0]) < 0 {
			return nil, fmt.Errorf("%w: delimiter %q", ErrDeclaration, delimiter)
		}
		d.Delimiter = delimiter[0]
	}
	if encoding != "" {
		label, ok := EncodingLabel(encoding)
		if !ok {
			return nil, fmt.Errorf("%w: encoding %q", ErrDeclaration, encoding)
		}
		d.Encoding = label
	}
	switch strings.ToLower(bom) {
	case "":
	case "1", "true":
		v := true
		d.BOM = &v
	case "0", "false":
		v := false
		d.BOM = &v
	default:
		return nil, fmt.Errorf("%w: bom %q", ErrDeclaration, bom)
	}
	switch e := strings.ToLower(eol); e {
	case "", "crlf", "lf":
		d.EOL = e
	default:
		return nil, fmt.Errorf("%w: eol %q", ErrDeclaration, eol)
	}
	return d, nil
}

// Refusal is a CSV save that is not written, and why. The file does not
// change; the caller tells the person who saved it.
type Refusal struct {
	// Why:
	//   - "delimiter", "encoding", "bom", "eol": the producer declared it one
	//     way and the bytes are written another (Declared, Found);
	//   - "unreadable": bytes that are not text in an encoding filex can read
	//     for the file (Found);
	//   - "unencodable": the file is in Encoding, which cannot hold Char;
	//   - "package": a spreadsheet package (XLSX, ODS, XLS) where CSV text
	//     belongs.
	Why string
	// Declared and Found, as a person reads them: `;`, `TAB`, `utf-8`,
	// `CRLF`, `yes`/`no` for a byte order mark.
	Declared, Found string
	// Encoding is the file's encoding, for "unencodable".
	Encoding string
	// Char is the first character the file's encoding cannot hold.
	Char rune
}

func (r *Refusal) Error() string {
	switch r.Why {
	case "unencodable":
		return fmt.Sprintf("officesave: the file is %s, which cannot hold %q", r.Encoding, r.Char)
	case "unreadable":
		return "officesave: the save is not text in an encoding filex can read for the file: " + r.Found
	case "package":
		return "officesave: a spreadsheet package where CSV text belongs"
	}
	return fmt.Sprintf("officesave: the save's %s was declared %s and is %s", r.Why, r.Declared, r.Found)
}

// AsRefusal reports whether err is a save refused, and why.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// DelimiterName is a delimiter as a person reads it: the character, `TAB`
// for a tab.
func DelimiterName(c byte) string {
	if c == '\t' {
		return "TAB"
	}
	return string(rune(c))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func eolName(crlf bool) string {
	if crlf {
		return "CRLF"
	}
	return "LF"
}

// savedText is a save read: its text as UTF-8 without a byte order mark, and
// how it was written.
type savedText struct {
	text []byte
	enc  string
	bom  bool
	crlf bool
}

// readSaved reads how the save is written (the header of this file) and hands
// its text back as UTF-8. file is how the file the save replaces is written:
// a save that is not UTF-8 and says nothing is read in the file's own code
// page, when the file is in one.
func readSaved(saved []byte, file CSVDialect, decl *Declared) (savedText, error) {
	if decl == nil {
		decl = &Declared{}
	}
	u8 := bytes.HasPrefix(saved, utf8BOM)
	enc16, bom16 := "", false
	if !u8 {
		enc16, bom16 = utf16Of(saved)
	}
	refuse := func(found string) error {
		return &Refusal{Why: "encoding", Declared: decl.Encoding, Found: found}
	}
	var st savedText
	switch want := decl.Encoding; {
	case want == EncUTF8 || (want == "" && enc16 == "" && (u8 || utf8.Valid(saved))):
		if enc16 != "" {
			return st, refuse(enc16)
		}
		body := saved
		if u8 {
			body = saved[len(utf8BOM):]
		}
		if !utf8.Valid(body) {
			return st, refuse("not " + EncUTF8)
		}
		st = savedText{text: body, enc: EncUTF8, bom: u8}
	case want == EncUTF16LE || want == EncUTF16BE || (want == "" && enc16 != ""):
		if u8 {
			return st, refuse(EncUTF8)
		}
		if want == "" {
			want = enc16
		}
		// Its mark, or the zero bytes of its ASCII, say which UTF-16 it is:
		// bytes that say another one, or none, are not what was declared.
		if enc16 != want {
			found := enc16
			if found == "" {
				found = "not " + want
			}
			return st, refuse(found)
		}
		body := saved
		if bom16 {
			body = saved[2:]
		}
		if len(body)%2 != 0 {
			return st, refuse("not " + want)
		}
		st = savedText{text: decodeUTF16(body, want == EncUTF16BE), enc: want, bom: bom16}
	case want != "":
		p := pageOf(want)
		if p == nil {
			return st, refuse("unknown")
		}
		if u8 || bom16 {
			return st, refuse("a byte order mark")
		}
		st = savedText{text: decodePage(saved, p), enc: want}
	default:
		// Not UTF-8, no mark, nothing said: the file's own code page, the
		// one the producer read it in. A file in UTF-8 or UTF-16 has none
		// to read the save in, and nothing is guessed for it.
		p := pageOf(file.Encoding)
		if p == nil {
			return st, &Refusal{Why: "unreadable", Found: "not " + EncUTF8}
		}
		st = savedText{text: decodePage(saved, p), enc: p.label}
	}
	if decl.BOM != nil && *decl.BOM != st.bom {
		return st, &Refusal{Why: "bom", Declared: yesNo(*decl.BOM), Found: yesNo(st.bom)}
	}
	crlfN, lfN := csvLineEnds(st.text)
	st.crlf = crlfN > 0 && lfN == 0
	switch {
	case decl.EOL == "crlf" && lfN > 0:
		return st, &Refusal{Why: "eol", Declared: "CRLF", Found: "LF"}
	case decl.EOL == "lf" && crlfN > 0:
		return st, &Refusal{Why: "eol", Declared: "LF", Found: "CRLF"}
	}
	return st, nil
}

// csvLineEnds counts the record ends of b outside quotes: "\r\n" ones and
// bare "\n" ones.
func csvLineEnds(b []byte) (crlf, lf int) {
	quoted := false
	for i, c := range b {
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\n':
			if i > 0 && b[i-1] == '\r' {
				crlf++
			} else {
				lf++
			}
		}
	}
	return crlf, lf
}

// What of the two sides the delimiter of a save is judged on.
const (
	commaJudgedFileBytes  = 1 << 20
	commaJudgedSavedBytes = 256 << 10
)

// savedComma is the delimiter the records of a save are separated by. saved
// and sample (the file, or its head) are in the same form: UTF-8, or both in
// the file's code page. declared is the producer's word for it (0: none).
//
// It is the candidate under which the save reads most like the file: the most
// of its filled cells are cells of the file (csvCanon, so `7` counts as the
// file's `007`), the file's own delimiter on a tie. A save that shares no
// cell with the file (all of it typed anew, or a file with nothing in it)
// says nothing that way: then the candidate that splits its lines alike
// (sniffComma), the file's own first.
//
// ⚠ Docs 9.4 writes a comma whatever the file, Docs 10 the file's own
// delimiter, an app what it likes: none of them is assumed. Two of the
// measured cases a count of delimiters gets wrong: Docs 10's semicolon save
// of a file whose cells hold decimal commas (`elma;1,5`: as many commas as
// semicolons on every line), and Docs 9.4's comma save of a semicolon file
// whose cells hold semicolons (`x;y,1`: one of each). The cells tell both.
//
// A declared delimiter is taken unless another one reads the save as the
// file and it does not: at least two of the file's cells under the other,
// fewer than half as many under it. Then the save is refused.
func savedComma(saved []byte, file CSVDialect, sample []byte, declared byte) (byte, error) {
	comma := file.Comma
	if comma == 0 {
		comma = ','
	}
	tabs := comma != '\t'
	cells := csvCellSet(sample, comma, tabs)
	cands := append([]byte(nil), csvCandidates...)
	for _, c := range []byte{comma, declared} {
		if c != 0 && bytes.IndexByte(cands, c) < 0 {
			cands = append(cands, c)
		}
	}
	score := make(map[byte]int, len(cands))
	best, bestN := byte(0), 0
	for _, c := range cands {
		n := csvSharedCells(saved, c, tabs, cells)
		score[c] = n
		if n > bestN || (n == bestN && n > 0 && c == comma) {
			best, bestN = c, n
		}
	}
	if declared != 0 {
		other, otherN := byte(0), 0
		for _, c := range cands {
			if c != declared && score[c] > otherN {
				other, otherN = c, score[c]
			}
		}
		if otherN >= 2 && 2*score[declared] < otherN {
			return 0, &Refusal{Why: "delimiter", Declared: DelimiterName(declared), Found: DelimiterName(other)}
		}
		if otherN < 2 {
			// The cells say nothing (a new file, a table typed anew): the
			// lines may.
			if other, ok := csvDeclaredContradicted(saved, declared); ok {
				return 0, &Refusal{Why: "delimiter", Declared: DelimiterName(declared), Found: DelimiterName(other)}
			}
		}
		return declared, nil
	}
	if bestN > 0 {
		// Among candidates as good as each other, the file's own, then the one
		// its lines split alike by.
		if score[comma] == bestN {
			return comma, nil
		}
		if s := sniffComma(saved, comma); score[s] == bestN {
			return s, nil
		}
		return best, nil
	}
	return sniffComma(saved, comma), nil
}

// csvDeclaredContradicted judges a declared delimiter by the save's lines
// alone: it is contradicted when it stands nowhere outside quotes in the
// first lines and another candidate splits two or more of them, every one
// alike. other is that one. A save of one column, or of lines nothing splits
// alike, contradicts nothing.
func csvDeclaredContradicted(saved []byte, declared byte) (other byte, contradicted bool) {
	cands := append([]byte{declared}, csvCandidates...)
	counts := csvLineCounts(saved, cands)
	for _, k := range counts[0] {
		if k > 0 {
			return 0, false
		}
	}
	for i, c := range cands[1:] {
		line := counts[i+1]
		if c == declared || len(line) < 2 || line[0] == 0 {
			continue
		}
		alike := true
		for _, k := range line {
			alike = alike && k == line[0]
		}
		if alike {
			return c, true
		}
	}
	return 0, false
}

// csvCellSet is the canonical forms (csvCanon) of the filled cells of the
// first records of a file (at most commaJudgedFileBytes of it).
func csvCellSet(b []byte, comma byte, tabs bool) map[string]struct{} {
	set := map[string]struct{}{}
	if len(b) == 0 {
		return set
	}
	r := csvReader{in: b, comma: comma}
	for r.more() && r.pos < commaJudgedFileBytes {
		for {
			f, last, _, _ := r.field()
			if len(f.val) > 0 {
				set[string(csvCanon(nil, f.val, tabs))] = struct{}{}
			}
			if last {
				break
			}
		}
	}
	return set
}

// csvSharedCells counts the filled cells of the first records of saved (at
// most commaJudgedSavedBytes of it), read with comma as the delimiter, that
// are cells of the file (cells).
func csvSharedCells(saved []byte, comma byte, tabs bool, cells map[string]struct{}) int {
	if len(cells) == 0 {
		return 0
	}
	r := csvReader{in: saved, comma: comma}
	n := 0
	for r.more() && r.pos < commaJudgedSavedBytes {
		for {
			f, last, _, _ := r.field()
			if len(f.val) > 0 {
				if _, ok := cells[string(csvCanon(nil, f.val, tabs))]; ok {
					n++
				}
			}
			if last {
				break
			}
		}
	}
	return n
}

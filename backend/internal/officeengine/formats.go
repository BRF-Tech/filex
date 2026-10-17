package officeengine

import (
	"regexp"
	"strings"
)

// ── What the engine reads and makes ────────────────────────────────────
//
// The numbers are ONLYOFFICE's own file types (AVS_OFFICESTUDIO_FILE_*, core
// Common/OfficeFileFormats.h; the editor's Asc.c_oAscFileType): x2t takes the
// target's as m_nFormatTo. By the extension alone x2t writes a plain
// docx/xlsx/pptx for a template or a macro-enabled type, and refuses to read
// a doc, xls, ppt or csv into the editor's format (88, measured), so the type
// is always said.

// Kinds of document, as the Document Server's documentType names them.
const (
	KindWord  = "word"
	KindCell  = "cell"
	KindSlide = "slide"
)

// KindOf is the editor a document of this extension opens in, "" for none:
// the kinds the Document Server lays out (a PDF, a picture), and what x2t
// reads into each editor's format.
func KindOf(ext string) string {
	switch ext {
	case "doc", "docx", "docm", "dot", "dotx", "dotm", "odt", "ott", "fodt", "rtf", "txt",
		"wps", "wpt", "stw", "sxw", "htm", "html", "mht", "mhtml", "xml", "epub", "fb2", "pages":
		return KindWord
	case "xls", "xlsx", "xlsm", "xlsb", "xlt", "xltx", "xltm", "ods", "ots", "fods", "csv",
		"et", "ett", "sxc", "numbers":
		return KindCell
	case "ppt", "pptx", "pptm", "pps", "ppsx", "ppsm", "pot", "potx", "potm", "odp", "otp",
		"fodp", "dps", "dpt", "sxi", "key":
		return KindSlide
	}
	return ""
}

// canvasFormat is each kind's editor format (AVS_OFFICESTUDIO_FILE_CANVAS_*):
// what a document is turned into before it is laid out.
var canvasFormat = map[string]int{KindWord: 0x2001, KindCell: 0x2002, KindSlide: 0x2003}

// Formats with a number of their own.
const (
	formatPDF  = 0x0201
	formatPDFA = 0x0209
	formatJPG  = 0x0401
	formatPNG  = 0x0405
	formatEPUB = 0x0048
	formatMD   = 0x005c
	formatHTML = 0x0046
)

// writeFormat is every format the engine writes, by the conversion API's
// output type.
var writeFormat = map[string]int{
	"docx": 0x0041, "odt": 0x0043, "rtf": 0x0044, "txt": 0x0045, "html": formatHTML,
	"epub": formatEPUB, "docm": 0x004b, "dotx": 0x004c, "dotm": 0x004d, "ott": 0x004f, "md": formatMD,
	"pptx": 0x0081, "odp": 0x0083, "ppsx": 0x0084, "pptm": 0x0085, "ppsm": 0x0086,
	"potx": 0x0087, "potm": 0x0088, "otp": 0x008a,
	"xlsx": 0x0101, "ods": 0x0103, "csv": 0x0104, "xlsm": 0x0105, "xltx": 0x0106,
	"xltm": 0x0107, "xlsb": 0x0108, "ots": 0x010a,
	"pdf": formatPDF, "pdfa": formatPDFA, "jpg": formatJPG, "png": formatPNG,
}

// layoutMode is how a target that needs the pages is made: the layout's
// drawing of them ("pdf"), a picture ("image"), or its HTML ("html", which
// x2t makes epub and md from). A target not here is x2t's alone.
var layoutMode = map[string]string{
	"pdf": "pdf", "pdfa": "pdf", "png": "image", "jpg": "image",
	"html": "html", "epub": "html", "md": "html",
}

// wordOnly are the targets only a text document is made into (the Document
// Server answers -7 for a sheet or a presentation as HTML).
var wordOnly = map[string]bool{"html": true, "epub": true, "md": true}

var extRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// plan is how one conversion is made.
type plan struct {
	kind string
	// mode is the layout's job ("" when x2t makes the target alone).
	mode string
	// formatTo is the target's type (0: x2t goes by the extension).
	formatTo int
	// ext is the result's extension (pdfa is a pdf).
	ext string
}

// planFor is how the engine makes `to` from `from`, or a CodeParams error.
func planFor(from, to string) (plan, error) {
	from, to = strings.ToLower(from), strings.ToLower(to)
	if !extRe.MatchString(from) || !extRe.MatchString(to) {
		return plan{}, &Error{Code: CodeParams, What: "type"}
	}
	p := plan{kind: KindOf(from), mode: layoutMode[to], formatTo: writeFormat[to], ext: to}
	if to == "pdfa" {
		p.ext = "pdf"
	}
	if p.formatTo == 0 {
		return plan{}, &Error{Code: CodeParams, What: "to-" + to}
	}
	if p.kind == "" {
		return plan{}, &Error{Code: CodeParams, What: "from-" + from}
	}
	if from == to {
		return plan{}, &Error{Code: CodeParams, What: "same"}
	}
	if wordOnly[to] && p.kind != KindWord {
		return plan{}, &Error{Code: CodeParams, What: "to-" + to}
	}
	return p, nil
}

// textOptions are what x2t needs to read or write a txt or csv: x2t's code
// page (46 is UTF-8, the one x2t reads and writes whole here: its other
// code pages need ICU's conversion tables, which the WebAssembly build does
// not carry) and a csv's delimiter.
type textOptions struct {
	codepage  int
	delimiter int
}

// x2tUTF8 is UTF-8 by x2t's own numbering.
const x2tUTF8 = 46

// textFor is the text options of a csv or txt on either side of a
// conversion; nil for any other format. A csv with no delimiter said is
// read with the one its first line uses (sniffDelimiter) and written with a
// comma, the Document Server's default.
func textFor(ext string, delimiter int, head []byte) *textOptions {
	switch ext {
	case "csv":
		d := delimiter
		if d < 1 || d > 5 {
			d = 0
		}
		if d == 0 && head != nil {
			d = sniffDelimiter(head)
		}
		if d == 0 {
			d = 4
		}
		return &textOptions{codepage: x2tUTF8, delimiter: d}
	case "txt":
		return &textOptions{codepage: x2tUTF8}
	}
	return nil
}

// sniffDelimiter is the delimiter a csv's first line uses most, as the
// conversion API numbers them (1 tab, 2 semicolon, 3 colon, 4 comma), 0 when
// the line has none. Quoted text is skipped. A tie between a semicolon and a
// comma goes to the semicolon: where the semicolon separates, the comma is
// the decimal mark (`elma;1,5`).
func sniffDelimiter(head []byte) int {
	counts := map[byte]int{}
	quoted := false
	for _, c := range head {
		if c == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		if c == '\n' || c == '\r' {
			break
		}
		counts[c]++
	}
	best, bestN := 0, 0
	for _, d := range []struct {
		c    byte
		code int
	}{{';', 2}, {',', 4}, {'\t', 1}, {':', 3}} {
		if counts[d.c] > bestN {
			best, bestN = d.code, counts[d.c]
		}
	}
	return best
}

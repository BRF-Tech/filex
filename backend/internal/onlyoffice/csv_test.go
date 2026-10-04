package onlyoffice

// How a CSV is read before ONLYOFFICE opens it, and put back the way it was
// written after ONLYOFFICE saved it (csv.go; filex 0.51, GitHub #81).

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSniffCSV(t *testing.T) {
	cases := map[string]struct {
		head string
		want CSVDialect
	}{
		"comma, LF":             {"name,qty,note\nelma,3,\"a, b\"\n", CSVDialect{Comma: ',', UTF8: true}},
		"semicolon, CRLF":       {"ad;adet;not\r\nelma;3;\"a; b\"\r\narmut;5;x, y\r\n", CSVDialect{Comma: ';', CRLF: true, UTF8: true}},
		"tab":                   {"a\tb\tc\n1\t2\t3\n", CSVDialect{Comma: '\t', UTF8: true}},
		"pipe":                  {"a|b\n1|2\n", CSVDialect{Comma: '|', UTF8: true}},
		"UTF-8 byte order mark": {"\xEF\xBB\xBFname,qty\nçilek,7\n", CSVDialect{Comma: ',', BOM: true, UTF8: true}},
		"one column":            {"name\nelma\n", CSVDialect{Comma: ',', UTF8: true}},
		"empty":                 {"", CSVDialect{Comma: ',', UTF8: true}},
		"header alone":          {"a;b;c", CSVDialect{Comma: ';', UTF8: true}},
		// A legacy Turkish code page (windows-1254 "ş" is 0xFE): not UTF-8.
		"windows-1254": {"ad;not\nelma;\xFEeker\n", CSVDialect{Comma: ';', UTF8: false}},
		"UTF-16LE":     {"\xFF\xFEa\x00,\x00b\x00", CSVDialect{Comma: ',', UTF8: false}},
		// Commas inside a field of a semicolon file do not make it a comma
		// file: they are not on every line.
		"commas in a field": {"ad;not\nx;a, b, c\ny;d\n", CSVDialect{Comma: ';', UTF8: true}},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, SniffCSV([]byte(c.head)), name)
	}
}

func TestSniffCSV_ARuneCutWhereTheReadStoppedIsStillUTF8(t *testing.T) {
	full := []byte("ad;not\nçilek;ğüİ\n")
	// Cut in the middle of "İ" (two bytes): what a 64 KiB read does to a
	// longer file.
	cut := full[:len(full)-2]
	assert.True(t, SniffCSV(cut).UTF8)
	assert.False(t, SniffCSV([]byte("ad;not\nx;\xFE\n")).UTF8, "a stray byte in the middle is not forgiven")
}

func TestOpenOptions(t *testing.T) {
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 4}, CSVDialect{Comma: ',', UTF8: true}.OpenOptions())
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 2}, CSVDialect{Comma: ';', UTF8: true}.OpenOptions())
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 1}, CSVDialect{Comma: '\t', UTF8: true}.OpenOptions())
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiterChar": "|"}, CSVDialect{Comma: '|', UTF8: true}.OpenOptions())
	assert.Nil(t, CSVDialect{Comma: ';', UTF8: false}.OpenOptions(), "not UTF-8: ONLYOFFICE asks")
}

// What ONLYOFFICE Docs 9.4 saved for the measured files (csv.go).
const dsSaved = "\xEF\xBB\xBFad,adet,not\nelma,42,a; b\narmut,5,şeker\nçilek,7,\"ı; ş, \"\"q\"\"\"\n"

func TestRewriteCSV_PutsTheFilesOwnWayBack(t *testing.T) {
	assert.Equal(t,
		"ad;adet;not\nelma;42;\"a; b\"\narmut;5;şeker\nçilek;7;\"ı; ş, \"\"q\"\"\"\n",
		string(RewriteCSV([]byte(dsSaved), CSVDialect{Comma: ';', UTF8: true})),
		"a semicolon file stays one; a field holding a semicolon is quoted")
	assert.Equal(t,
		"ad,adet,not\nelma,42,a; b\narmut,5,şeker\nçilek,7,\"ı; ş, \"\"q\"\"\"\n",
		string(RewriteCSV([]byte(dsSaved), CSVDialect{Comma: ',', UTF8: true})),
		"a comma file without a byte order mark gets none")
	assert.Equal(t, dsSaved,
		string(RewriteCSV([]byte(dsSaved), CSVDialect{Comma: ',', BOM: true, UTF8: true})),
		"a file that had one keeps it")
	assert.Equal(t,
		"\xEF\xBB\xBFad\tadet\tnot\r\nelma\t42\ta; b\r\narmut\t5\tşeker\r\nçilek\t7\t\"ı; ş, \"\"q\"\"\"\r\n",
		string(RewriteCSV([]byte(dsSaved), CSVDialect{Comma: '\t', CRLF: true, UTF8: false})),
		"tabs and CRLF; a file that was not UTF-8 is now, and says so")
}

func TestRewriteCSV_KeepsEveryRecordAndField(t *testing.T) {
	in := "a,b,c\n,,\n\n\"line\nbreak\",\"x\"\"y\",\n1,2,3"
	want := "a;b;c\r\n;;\r\n\r\n\"line\nbreak\";\"x\"\"y\";\r\n1;2;3"
	assert.Equal(t, want, string(RewriteCSV([]byte(in), CSVDialect{Comma: ';', CRLF: true, UTF8: true})),
		"empty fields, an empty line, a quoted line break, a doubled quote, a trailing delimiter, no final line end")
	assert.Equal(t, "", string(RewriteCSV([]byte("\xEF\xBB\xBF"), CSVDialect{Comma: ';', UTF8: true})))
	// Round trip: the semicolon file, through ONLYOFFICE's comma, back.
	orig := "ad;not\r\nx;\"a; b\"\r\ny;\"c \"\"d\"\"\"\r\n"
	comma := "\xEF\xBB\xBFad,not\nx,a; b\ny,\"c \"\"d\"\"\"\n"
	assert.Equal(t, orig, string(RewriteCSV([]byte(comma), SniffCSV([]byte(orig)))))
	assert.False(t, strings.HasPrefix(string(RewriteCSV([]byte(comma), SniffCSV([]byte(orig)))), "\xEF\xBB\xBF"))
}

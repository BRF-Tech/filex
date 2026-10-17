package officesave

// SaveCSV (save.go): what an editor saved over a .csv, written in the file's
// own way - whichever way the editor wrote it, and whatever encoding the file
// is in - or refused when its bytes are not what it said.
//
// ⚠ Every vector of TestSaveCSV_TheSavesDialectIsReadNotAssumed with a save
// that is not comma-separated fails on 0.56: KeepCSV and RewriteCSV read every
// save as Docs 9.4 writes it, commas (task #255).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// whole is an Original read whole, as ReadOriginal reads a small file.
func whole(t *testing.T, file string) Original {
	t.Helper()
	o, err := ReadOriginal(strings.NewReader(file), int64(len(file)))
	require.NoError(t, err)
	require.NotNil(t, o.Body)
	return o
}

func TestSaveCSV_TheSavesDialectIsReadNotAssumed(t *testing.T) {
	cases := map[string]struct{ original, saved, want string }{
		"Docs 9.4: commas and a byte order mark over a semicolon file": {
			crlf("kod;ad;not", "007;elma;a"), ds("kod,ad,not", "7,elma,a", "9,nar,b"), crlf("kod;ad;not", "007;elma;a", "9;nar;b"),
		},
		"Docs 10: the file's own semicolon, no byte order mark": {
			crlf("kod;ad;not", "007;elma;a"), lf("kod;ad;not", "7;elma;a", "9;nar;b"), crlf("kod;ad;not", "007;elma;a", "9;nar;b"),
		},
		// The e2e 199 fixture as Docs 10.0 saved it (task #255): 0.56 wrote
		// every row as one quoted field.
		"#255: a cell holding the semicolon": {
			crlf("ad;adet;not;kod;tel;tarih", `elma;3;"a; b";007;05320000001;01.02.2026`, "armut;5;şeker;042;05330000002;15.03.2026"),
			lf("ad;adet;not;kod;tel;tarih", `elma;42;"a; b";7;5320000001;1/2/2026`, "armut;5;şeker;42;5330000002;15.03.2026"),
			crlf("ad;adet;not;kod;tel;tarih", `elma;42;"a; b";007;05320000001;01.02.2026`, "armut;5;şeker;042;05330000002;15.03.2026"),
		},
		"tabs, saved as tabs": {
			lf("kod\tad\tnot", "007\telma\ta"), lf("kod\tad\tnot", "7\telma\tx"), lf("kod\tad\tnot", "007\telma\tx"),
		},
		"pipes, saved with commas": {
			lf("kod|ad|not", "007|elma|a"), ds("kod,ad,not", "7,elma,a", "9,nar,b"), lf("kod|ad|not", "007|elma|a", "9|nar|b"),
		},
		"pipes, saved as pipes": {
			lf("kod|ad|not", "007|elma|a"), lf("kod|ad|not", "7|elma|a", "9|nar|b"), lf("kod|ad|not", "007|elma|a", "9|nar|b"),
		},
		"a byte order mark is kept, whatever the save": {
			"\xEF\xBB\xBF" + lf("kod;ad;not", "007;elma;a"), lf("kod;ad;not", "7;elma;x"), "\xEF\xBB\xBF" + lf("kod;ad;not", "007;elma;x"),
		},
		"no byte order mark stays none": {
			lf("kod;ad;not", "007;elma;a"), "\xEF\xBB\xBF" + lf("kod;ad;not", "7;elma;x"), lf("kod;ad;not", "007;elma;x"),
		},
		"a CRLF save of an LF file": {
			lf("kod;ad;not", "007;elma;a"), crlf("kod;ad;not", "7;elma;a", "9;nar;b"), lf("kod;ad;not", "007;elma;a", "9;nar;b"),
		},
		// As many commas as semicolons on a line: a count of delimiters
		// cannot tell; the cells the save shares with the file can.
		"decimal commas in a semicolon save": {
			lf("ad;tutar", "elma;1,5", "armut;2,5"), lf("ad;tutar", "elma;1,5", "armut;3,5"), lf("ad;tutar", "elma;1,5", "armut;3,5"),
		},
		"semicolons in the cells of a comma save": {
			lf("ad;not", `"x;y";1`, `"z;w";2`), ds("ad,not", "x;y,1", "z;w,3"), lf("ad;not", `"x;y";1`, `"z;w";3`),
		},
		"a one-column file": {
			lf("ad", "elma"), ds("ad", "elma", "nar"), lf("ad", "elma", "nar"),
		},
		"an empty file": {
			"", ds("ad,adet", "elma,3"), lf("ad,adet", "elma,3"),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, res, err := SaveCSV(whole(t, c.original), []byte(c.saved), nil)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(out))
			assert.Empty(t, res.Panic)
		})
	}
}

// A file in a code page is written back in it: its own records byte for
// byte, the new ones encoded in its page.
func TestSaveCSV_ACodePageFileStaysInItsCodePage(t *testing.T) {
	original := "kod;ad;not\r\n007;\xFEeker;a\r\n"
	out, res, err := SaveCSV(whole(t, original), []byte(ds("kod,ad,not", "7,şeker,a", "9,ığdır,b")), nil)
	require.NoError(t, err)
	assert.Equal(t, "kod;ad;not\r\n007;\xFEeker;a\r\n9;\xFD\xF0d\xFDr;b\r\n", string(out))
	assert.True(t, res.Applied)
	assert.Equal(t, "windows-1254", res.File.Encoding)
	assert.Equal(t, EncUTF8, res.Saved.Charset())
}

// The Greek and the Baltic page (filex 0.57): read from the letters, written
// back in them.
func TestSaveCSV_GreekAndBalticFilesStayInTheirCodePage(t *testing.T) {
	cases := map[string]struct{ original, saved, want, enc string }{
		"Greek": {
			"kod;ad;not\r\n007;\xC1\xE8\xDE\xED\xE1;a\r\n",
			ds("kod,ad,not", "7,Αθήνα,a", "9,Πάτρα,b"),
			"kod;ad;not\r\n007;\xC1\xE8\xDE\xED\xE1;a\r\n9;\xD0\xDC\xF4\xF1\xE1;b\r\n",
			"windows-1253",
		},
		"Lithuanian": {
			"kod;ad;not\r\n007;K\xEBdainiai;a\r\n008;\xD0iauliai;b\r\n",
			ds("kod,ad,not", "7,Kėdainiai,a", "8,Šiauliai,b", "9,Panevėžys,c"),
			"kod;ad;not\r\n007;K\xEBdainiai;a\r\n008;\xD0iauliai;b\r\n9;Panev\xEB\xFEys;c\r\n",
			"windows-1257",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, res, err := SaveCSV(whole(t, c.original), []byte(c.saved), nil)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(out))
			assert.Equal(t, c.enc, res.File.Encoding)
		})
	}
}

// A file whose bytes read alike in two code pages (ü, ö: windows-1252 and
// -1254) takes a letter only one of them holds: written in that one. Its own
// bytes do not change.
func TestSaveCSV_ALetterAnAlikePageHoldsIsWrittenInIt(t *testing.T) {
	original := "ad;not;x\nM\xFCller;a;1\n"
	out, res, err := SaveCSV(whole(t, original), []byte(ds("ad,not,x", "Müller,a,1", "Şule,b,2")), nil)
	require.NoError(t, err)
	assert.Equal(t, "ad;not;x\nM\xFCller;a;1\n\xDEule;b;2\n", string(out))
	assert.Equal(t, "windows-1254", res.File.Encoding)
}

// A character the file's encoding cannot hold is a save refused: nothing is
// written as `?`.
func TestSaveCSV_ACharacterTheFileCannotHoldIsRefused(t *testing.T) {
	_, _, err := SaveCSV(whole(t, "kod;ad;not\r\n007;\xFEeker;a\r\n"), []byte(ds("kod,ad,not", "7,şeker,a", "8,日本,b")), nil)
	r, ok := AsRefusal(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, "unencodable", r.Why)
	assert.Equal(t, "windows-1254", r.Encoding)
	assert.Equal(t, '日', r.Char)
}

func TestSaveCSV_UTF16(t *testing.T) {
	original := utf16le("kod\tad\tnot\r\n007\tşeker\ta\r\n", true)
	out, res, err := SaveCSV(whole(t, original), []byte(ds("kod,ad,not", "7,şeker,a", "9,nar,b")), nil)
	require.NoError(t, err)
	assert.Equal(t, utf16le("kod\tad\tnot\r\n007\tşeker\ta\r\n9\tnar\tb\r\n", true), string(out))
	assert.True(t, res.Applied)

	original = utf16be("kod;ad;not\n007;elma;a\n", true)
	out, _, err = SaveCSV(whole(t, original), []byte(utf16be("kod;ad;not\n7;elma;x\n", true)), nil)
	require.NoError(t, err)
	assert.Equal(t, utf16be("kod;ad;not\n007;elma;x\n", true), string(out), "a UTF-16 save of a UTF-16 file")
}

// The editor's declaration is checked against its bytes (an app's `csv` save
// option); one the bytes contradict is refused, never written.
func TestSaveCSV_ADeclarationTheBytesContradictIsRefused(t *testing.T) {
	original := crlf("kod;ad;not", "007;elma;a", "042;armut;b")
	semicolons := lf("kod;ad;not", "7;elma;a", "42;armut;x")
	yes, no := true, false
	cases := map[string]struct {
		saved      string
		decl       Declared
		why, found string
	}{
		"said commas, wrote semicolons":                   {semicolons, Declared{Delimiter: ','}, "delimiter", ";"},
		"said tabs, wrote semicolons":                     {semicolons, Declared{Delimiter: '\t'}, "delimiter", ";"},
		"said UTF-8, wrote a code page":                   {"kod;ad;not\n7;\xFEeker;a\n", Declared{Encoding: EncUTF8}, "encoding", "not utf-8"},
		"said a code page, wrote a UTF-8 byte order mark": {"\xEF\xBB\xBF" + semicolons, Declared{Encoding: "windows-1254"}, "encoding", "a byte order mark"},
		"said UTF-16, wrote UTF-8 with its mark":          {"\xEF\xBB\xBF" + semicolons, Declared{Encoding: EncUTF16LE}, "encoding", EncUTF8},
		"said a byte order mark, wrote none":              {semicolons, Declared{BOM: &yes}, "bom", "no"},
		"said none, wrote one":                            {"\xEF\xBB\xBF" + semicolons, Declared{BOM: &no}, "bom", "yes"},
		"said CRLF, wrote LF":                             {semicolons, Declared{EOL: "crlf"}, "eol", "LF"},
		"said LF, wrote CRLF":                             {crlf("kod;ad;not", "7;elma;a"), Declared{EOL: "lf"}, "eol", "CRLF"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			decl := c.decl
			out, _, err := SaveCSV(whole(t, original), []byte(c.saved), &decl)
			assert.Nil(t, out)
			r, ok := AsRefusal(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, c.why, r.Why)
			assert.Equal(t, c.found, r.Found)
		})
	}
}

// A declaration the bytes agree with is the save's dialect.
func TestSaveCSV_ADeclarationTheBytesAgreeWithIsTaken(t *testing.T) {
	original := crlf("kod;ad;not", "007;elma;a", "042;armut;b")
	no := false
	decl := &Declared{Delimiter: ';', Encoding: EncUTF8, BOM: &no, EOL: "lf"}
	out, res, err := SaveCSV(whole(t, original), []byte(lf("kod;ad;not", "7;elma;a", "42;armut;x")), decl)
	require.NoError(t, err)
	assert.Equal(t, crlf("kod;ad;not", "007;elma;a", "042;armut;x"), string(out))
	assert.Equal(t, byte(';'), res.Saved.Comma)

	// An app that read the file's own code page and saved in it, saying so.
	cp := "kod;ad;not\r\n007;\xFEeker;a\r\n"
	out, _, err = SaveCSV(whole(t, cp), []byte("kod;ad;not\n7;\xFEeker;b\n"), &Declared{Delimiter: ';', Encoding: "windows-1254"})
	require.NoError(t, err)
	assert.Equal(t, "kod;ad;not\r\n007;\xFEeker;b\r\n", string(out))

	// A declared delimiter the save shares nothing with the file under is
	// taken: no evidence against it.
	out, _, err = SaveCSV(whole(t, original), []byte("a|b\n"), &Declared{Delimiter: '|'})
	require.NoError(t, err)
	assert.Equal(t, "a;b\r\n", string(out))
}

// Bytes that are not UTF-8, with no mark and nothing said, over a UTF-8 file:
// nothing to read them in, and nothing is guessed.
func TestSaveCSV_UnreadableBytesAreRefused(t *testing.T) {
	_, _, err := SaveCSV(whole(t, lf("kod;ad", "007;elma")), []byte("kod;ad\n7;\xFEeker\n"), nil)
	r, ok := AsRefusal(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, "unreadable", r.Why)

	_, _, err = SaveCSV(whole(t, lf("kod;ad", "007;elma")), []byte("PK\x03\x04 an XLSX"), nil)
	r, ok = AsRefusal(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, "package", r.Why)
}

// When nothing of the file could be read, the save is written as the editor
// wrote it; when only its head could, in the head's way with the editor's
// values (the cells are not kept, and CSVSave says why).
func TestSaveCSV_WhatCouldNotBeRead(t *testing.T) {
	saved := []byte(ds("kod,tel", "7,5320000001"))
	out, res, err := SaveCSV(Original{Why: "unreadable"}, saved, nil)
	require.NoError(t, err)
	assert.Equal(t, string(saved), string(out))
	assert.Equal(t, "unreadable", res.Why)

	head := crlf("kod;tel", "007;05320000001")
	out, res, err = SaveCSV(Original{Head: []byte(head), Why: "too_large"}, saved, nil)
	require.NoError(t, err)
	assert.Equal(t, crlf("kod;tel", "7;5320000001"), string(out))
	assert.Equal(t, "too_large", res.Why)
	assert.False(t, res.Applied)
}

func TestReadOriginal(t *testing.T) {
	file := crlf("kod;tel", "007;05320000001")
	o, err := ReadOriginal(strings.NewReader(file), int64(len(file)))
	require.NoError(t, err)
	assert.Equal(t, file, string(o.Body))
	assert.Equal(t, file, string(o.Head))
	assert.Empty(t, o.Why)

	o, err = ReadOriginal(strings.NewReader(""), 0)
	require.NoError(t, err)
	assert.NotNil(t, o.Head, "an empty file is read: it has the default way")
	assert.NotNil(t, o.Body)

	defer func(n int64) { KeepMaxBytes = n }(KeepMaxBytes)
	KeepMaxBytes = 8
	o, err = ReadOriginal(strings.NewReader(file), int64(len(file)))
	require.NoError(t, err)
	assert.Equal(t, "too_large", o.Why)
	assert.Nil(t, o.Body)
	assert.Equal(t, file, string(o.Head))
	// The catalogue says less than there is: one byte past the bound tells.
	o, err = ReadOriginal(strings.NewReader(file), 4)
	require.NoError(t, err)
	assert.Equal(t, "too_large", o.Why)
}

func TestParseDeclared(t *testing.T) {
	d, err := ParseDeclared("", "", "", "")
	require.NoError(t, err)
	assert.Nil(t, d, "nothing said")

	d, err = ParseDeclared(";", "UTF-8", "1", "CRLF")
	require.NoError(t, err)
	require.NotNil(t, d.BOM)
	assert.Equal(t, Declared{Delimiter: ';', Encoding: EncUTF8, BOM: d.BOM, EOL: "crlf"}, *d)
	assert.True(t, *d.BOM)

	d, err = ParseDeclared("\t", "cp1254", "false", "")
	require.NoError(t, err)
	assert.Equal(t, byte('\t'), d.Delimiter)
	assert.Equal(t, "windows-1254", d.Encoding)
	assert.False(t, *d.BOM)

	for _, bad := range [][4]string{{";;", "", "", ""}, {":", "", "", ""}, {"", "shift_jis", "", ""}, {"", "", "maybe", ""}, {"", "", "", "cr"}} {
		_, err := ParseDeclared(bad[0], bad[1], bad[2], bad[3])
		assert.ErrorIs(t, err, ErrDeclaration, "%q", bad)
	}
}

func TestTextDialect(t *testing.T) {
	assert.Equal(t, TextDialect{Delimiter: ";", CRLF: true, Encoding: EncUTF8}, CSVDialect{Comma: ';', CRLF: true, UTF8: true}.Text())
	assert.Equal(t, TextDialect{Delimiter: "\t", BOM: true, Encoding: EncUTF16LE},
		SniffCSV([]byte(utf16le("a\tb\n", true))).Text())
	assert.Equal(t, TextDialect{Delimiter: ";", Encoding: "windows-1254"}, SniffCSV([]byte("ad;not\nelma;\xFEeker\n")).Text())
}

// A large file is compared in the bound's time: the delimiter of the save is
// judged on its first records only.
func TestSavedComma_JudgesTheFirstRecords(t *testing.T) {
	var file, save bytes.Buffer
	file.WriteString("kod;ad;not\n")
	save.WriteString("kod;ad;not\n")
	for i := 0; i < 40000; i++ {
		file.WriteString("007;elma;a\n")
		save.WriteString("7;elma;a\n")
	}
	c, err := savedComma(save.Bytes(), CSVDialect{Comma: ';', UTF8: true}, file.Bytes(), 0)
	require.NoError(t, err)
	assert.Equal(t, byte(';'), c)
}

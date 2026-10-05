package onlyoffice

// The cells nobody touched keep their text when ONLYOFFICE saves a CSV
// (csv_keep.go; docs/ONLYOFFICE.md → Cells nobody changed keep their text).
//
// Measured on filex 0.51.0 with ONLYOFFICE Docs 9.4.0: one cell edited in a
// semicolon file, and in cells nobody had touched `05320000001` came back as
// `5320000001`, `007` as `7`, `01.02.2026` as `1/2/2026`, and every data row
// had an empty cell more at its end. RewriteCSV wrote that.
//
// ⚠ The saved bytes in these tests are what the document server hands the
// callback, rebuilt from the file filex 0.51.0 wrote (RewriteCSV undone): comma
// separated, a UTF-8 byte order mark, "\n" line ends, one final "\n".

import (
	"bytes"
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ds is a CSV as the document server saves it: a byte order mark, the rows
// (comma-separated by the caller) with "\n" between them and after the last.
func ds(rows ...string) string { return "\xEF\xBB\xBF" + strings.Join(rows, "\n") + "\n" }

func crlf(rows ...string) string { return strings.Join(rows, "\r\n") + "\r\n" }

func lf(rows ...string) string { return strings.Join(rows, "\n") + "\n" }

func TestCSVSame(t *testing.T) {
	long := strings.Repeat("ş", 40000)
	zeros := strings.Repeat("0", 1000)
	cases := []struct {
		o, s string
		want bool
	}{
		// What ONLYOFFICE writes for a cell nobody touched: a whole number.
		{"007", "7", true},
		{"05320000001", "5320000001", true},
		{"000", "0", true},
		{"+5", "5", true},
		{" 42", "42", true},
		{"42 ", "42", true},
		{"-007", "-7", true},
		{"-0", "0", true},
		{"1e3", "1000", true},
		{"12E5", "1200000", true},
		{"10e-1", "1", true},
		{"1" + zeros + "e-1000", "1", true},
		{"0x10", "16", true},
		{"9007199254740993", "9007199254740992", true},
		{"123456789012345678", "1.2345678901234568e+17", true},
		// A number with a point.
		{"03.50", "3.50", true},
		{"3.50", " 3.50", true},
		{"0.1234567", "0.1234570", true},
		{"+1.5", "1.5", true},
		{"01234567890123.25", "1234567890123.25", true},
		// A date: the same three numbers in the same order.
		{"01.02.2026", "1/2/2026", true},
		{"1.2.2026", "01.02.2026", true},
		{"1/2/2026", "01.02.2026", true},
		{"15-03-2026", "15/3/2026", true},
		{"29.02.2024", "29/2/2024", true},
		// A truth value, text with a line end, a tab, too long a text.
		{"true", "TRUE", true},
		{"false", "FALSE", true},
		{"a\r\nb", "a\nb", true},
		{"a\tb", "ab", true},
		{long, long[:2*32767], true},
		{"same", "same", true},
		{"", "", true},

		// ⚠ One way only: the other direction is a person's typing.
		{"7", "007", false},
		{"7", "70", false},
		{"TRUE", "true", false},
		{"True", "TRUE", false},
		{"a\nb", "a\r\nb", false},
		{"ab", "a\tb", false},
		// A comma is never a decimal mark to ONLYOFFICE: text, kept as typed.
		{"1,50", "1,5", false},
		{"1,5", "1.5", false},
		{"1,000", "1", false},
		// A date in another order, another year, another shape.
		{"01.02.2026", "2/1/2026", false},
		{"01.02.2026", "02.01.2026", false},
		{"01.02.2026", "1/02/2026", false},
		{"2026-02-01", "2/1/2026", false},
		{"01.02.26", "1/2/2026", false},
		{"15.03.2026", "3/15/2026", false},
		{"31.02.2026", "31/2/2026", false},
		{"29.02.2026", "29/2/2026", false},
		{"13.13.2026", "13/13/2026", false},
		{"01.02.1850", "1/2/1850", false},
		// Text is compared as text.
		{" abc ", "abc", false},
		{"abc", "ABC", false},
		{"50%", "50.0%", false},
		{"08:05", "8:05", false},
		{"=1+1", "2", false},
		{"", "0", false},
		{"0", "", false},
		// A number that is not whole, or too long to say what comes back.
		{"1e-3", "0.001", false},
		{"15e-1", "1.5", false},
		// ⚠ An exponent too long to read is not read as a shorter one: these
		// are 0.1 and five with four thousand zeros after the point.
		{"1" + zeros + "e-1001", "1", false},
		{"5" + zeros + "e-5000", "5", false},
		{"1" + zeros + "e-2000", "1", false},
		{"12345678901234567890", "12345678901234567000", false},
		{"12345678901234567890", "1.2345678901234567e+19", false},
		{"9223372036854775808", "9.2233720368547758e+18", false},
		{"3.50", "3.5", false},
		{"3.5", "3.50", false},
		{"5.", "5", false},
		{".5", "0.5", false},
		{"0x", "0", false},
		{"1_000", "1000", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, csvSame([]byte(c.o), []byte(c.s), true), "%.40q written as %.40q", c.o, c.s)
	}
	assert.False(t, csvSame([]byte("a\tb"), []byte("ab"), false),
		"in a tab-separated file a tab is the delimiter, never part of a cell")
}

// csvRows reads every record of in, its values copied.
func csvRows(in []byte, comma byte) [][]string {
	var rows [][]string
	r := csvReader{in: in, comma: comma}
	for r.more() {
		fields, _, _ := r.next()
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = string(f.val)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestCSVReader_KeepsARecordsBytesItsLineEndAndItsFields(t *testing.T) {
	in := []byte("a;\"b;c\";\"d\"\"e\"\r\nx;\"y\"z;\n\n\"l1\r\nl2\";q\r\nlast;")
	type field struct{ raw, val string }
	type record struct {
		raw, term string
		fields    []field
	}
	var got []record
	r := csvReader{in: in, comma: ';'}
	for r.more() {
		start := r.pos
		fields, end, term := r.next()
		rec := record{raw: string(in[start:end]), term: string(in[end : end+term])}
		for _, f := range fields {
			rec.fields = append(rec.fields, field{string(f.raw), string(f.val)})
		}
		got = append(got, rec)
	}
	assert.Equal(t, []record{
		{"a;\"b;c\";\"d\"\"e\"", "\r\n", []field{{"a", "a"}, {"\"b;c\"", "b;c"}, {"\"d\"\"e\"", "d\"e"}}},
		{"x;\"y\"z;", "\n", []field{{"x", "x"}, {"\"y\"z", "yz"}, {"", ""}}},
		{"", "\n", []field{{"", ""}}},
		{"\"l1\r\nl2\";q", "\r\n", []field{{"\"l1\r\nl2\"", "l1\r\nl2"}, {"q", "q"}}},
		{"last;", "", []field{{"last", "last"}, {"", ""}}},
	}, got)
	assert.Empty(t, csvRows(nil, ';'), "an empty file has no record")
	assert.Equal(t, [][]string{{"a"}}, csvRows([]byte("a\n"), ';'), "a final line end starts no record")
}

// assertKept is principle P2, checked from outside: the written file, read in
// the file's own dialect, is the table ONLYOFFICE saved, cell for cell - a
// cell either as saved, or the original text that ONLYOFFICE writes that way.
// A file whose cells were not kept is RewriteCSV's, byte for byte.
func assertKept(t testing.TB, original, saved, out []byte, d CSVDialect, kept CSVKept) {
	t.Helper()
	if !kept.Applied {
		require.NotEmpty(t, kept.Why, "a file not kept says why")
		require.True(t, bytes.Equal(RewriteCSV(saved, d), out), "not kept (%s): the bytes are RewriteCSV's", kept.Why)
		return
	}
	require.Equal(t, d.KeepsBOM(), bytes.HasPrefix(out, utf8BOM), "the byte order mark is the file's")
	comma := d.Comma
	if comma == 0 {
		comma = ','
	}
	got := csvRows(bytes.TrimPrefix(out, utf8BOM), comma)
	want := csvRows(bytes.TrimPrefix(saved, utf8BOM), ',')
	// A `sep=` line ONLYOFFICE consumed is written back in front: the file's
	// first line, and not the save's.
	if sep := csvFirstLine(original); len(sep) == 5 && bytes.HasPrefix(sep, []byte("sep=")) &&
		bytes.Equal(sep, csvFirstLine(out)) && len(got) > len(want) && (len(want) == 0 || !sameRow(got[0], want[0], comma)) {
		got = got[1:]
	}
	require.GreaterOrEqual(t, len(got), len(want), "every saved record is written")
	for i, row := range got {
		if i >= len(want) {
			// Empty lines at the end of the file, which ONLYOFFICE cannot write.
			for _, cell := range row {
				require.Empty(t, cell, "record %d is not in the save", i)
			}
			continue
		}
		require.True(t, sameRow(row, want[i], comma), "record %d: wrote %.200q, ONLYOFFICE saved %.200q", i, row, want[i])
	}
}

// sameRow: every cell written is the saved one, or what ONLYOFFICE writes
// that way; a cell one side does not have is an empty one.
func sameRow(wrote, saved []string, comma byte) bool {
	for j := 0; j < len(wrote) || j < len(saved); j++ {
		var o, s string
		if j < len(wrote) {
			o = wrote[j]
		}
		if j < len(saved) {
			s = saved[j]
		}
		if !csvSame([]byte(o), []byte(s), comma != '\t') {
			return false
		}
	}
	return true
}

// csvFirstLine is the bytes of b up to its first line end, after a byte
// order mark.
func csvFirstLine(b []byte) []byte {
	b = bytes.TrimPrefix(b, utf8BOM)
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	return bytes.TrimSuffix(b, []byte("\r"))
}

// The file of the measurement (production, 0.51.0): semicolons, CRLF, UTF-8,
// no byte order mark, a final line end.
var measuredOriginal = crlf(
	"Ad;Telefon;Kimlik No;Kart No;Tarih;Tutar;Kod;Not",
	"Çağrı Öztürk;05320000001;12345678901;1234567890123456;01.02.2026;3,5;007;ilk satır",
	"Şule Iğdır;05330000002;10987654321;9999888877776666;15.03.2026;1250,75;042;ığüşöç İĞÜŞÖÇ",
	"Deneme Üç;05050000003;11111111111;4000000000000002;31.12.2025;0,5;000;boş değil",
)

// What ONLYOFFICE saved for it after one cell was edited (`ilk satır` to
// `degisti`) and a note typed into a new row two rows below.
var measuredSaved = ds(
	"Ad,Telefon,Kimlik No,Kart No,Tarih,Tutar,Kod,Not",
	"Çağrı Öztürk,5320000001,12345678901,1234567890123456,1/2/2026,\"3,5\",7,degisti,",
	"Şule Iğdır,5330000002,10987654321,9999888877776666,15.03.2026,\"1250,75\",42,ığüşöç İĞÜŞÖÇ,",
	"Deneme Üç,5050000003,11111111111,4000000000000002,31.12.2025,\"0,5\",0,boş değil,",
	"",
	",bunu deneme için masaüstünden girip yazıyorum",
)

var measuredWant = crlf(
	"Ad;Telefon;Kimlik No;Kart No;Tarih;Tutar;Kod;Not",
	"Çağrı Öztürk;05320000001;12345678901;1234567890123456;01.02.2026;3,5;007;degisti",
	"Şule Iğdır;05330000002;10987654321;9999888877776666;15.03.2026;1250,75;042;ığüşöç İĞÜŞÖÇ",
	"Deneme Üç;05050000003;11111111111;4000000000000002;31.12.2025;0,5;000;boş değil",
	"",
	";bunu deneme için masaüstünden girip yazıyorum",
)

var keepCSVCases = map[string]struct{ original, saved, want string }{
	"the measured file": {measuredOriginal, measuredSaved, measuredWant},
	"nothing edited, every cell written another way": {
		crlf("Kod;Tel;Tarih;Oran;Onay;Not", "007;05320000001;01.02.2026;03.50;true;a\tb", "042; 42;1.2.2026;0x10;false;1e3"),
		ds("Kod,Tel,Tarih,Oran,Onay,Not", "7,5320000001,1/2/2026,3.50,TRUE,ab,", "42,42,1/2/2026,16,FALSE,1000"),
		crlf("Kod;Tel;Tarih;Oran;Onay;Not", "007;05320000001;01.02.2026;03.50;true;a\tb", "042; 42;1.2.2026;0x10;false;1e3"),
	},
	"an edited cell that is a number is the person's": {
		crlf("kod;ad;adet", "007;elma;007", "042;armut;5"),
		ds("kod,ad,adet", "7,elma,8", "42,armut,5"),
		crlf("kod;ad;adet", "007;elma;8", "042;armut;5"),
	},
	"a row inserted in the middle": {
		crlf("kod;ad", "007;elma", "042;armut"),
		ds("kod,ad", "7,elma", "9,kiraz", "42,armut"),
		crlf("kod;ad", "007;elma", "9;kiraz", "042;armut"),
	},
	"a row deleted": {
		crlf("kod;ad", "007;elma", "042;armut", "001;kiraz"),
		ds("kod,ad", "7,elma", "1,kiraz"),
		crlf("kod;ad", "007;elma", "001;kiraz"),
	},
	"rows appended": {
		crlf("kod;ad", "007;elma", "042;armut"),
		ds("kod,ad", "7,elma", "42,armut", "5,muz,", "6,nar"),
		crlf("kod;ad", "007;elma", "042;armut", "5;muz", "6;nar"),
	},
	"the whole file sorted descending": {
		crlf("kod;ad", "001;armut", "002;elma", "003;kiraz"),
		ds("kod,ad", "3,kiraz", "2,elma", "1,armut"),
		crlf("kod;ad", "003;kiraz", "002;elma", "001;armut"),
	},
	"the same row twice": {
		crlf("kod;ad", "007;elma", "007;elma", "042;armut"),
		ds("kod,ad", "7,elma", "7,elma", "42,armut"),
		crlf("kod;ad", "007;elma", "007;elma", "042;armut"),
	},
	"the same row twice, one deleted": {
		crlf("kod;ad", "007;elma", "007;elma", "042;armut"),
		ds("kod,ad", "7,elma", "42,armut"),
		crlf("kod;ad", "007;elma", "042;armut"),
	},
	// Which of the two look-alikes was deleted cannot be told: ONLYOFFICE's
	// value is written.
	"look-alike rows, one deleted": {lf("A;007", "A;7"), ds("A,7"), lf("A;7")},
	"look-alike rows, both there":  {lf("A;007", "A;7"), ds("A,7", "A,7"), lf("A;007", "A;7")},
	"look-alike rows, one more":    {lf("A;007", "A;7"), ds("A,7", "A,7", "A,7"), lf("A;7", "A;7", "A;7")},
	// One of several rows that read the same, edited: the saved rows that
	// still read so take the ones that stand where they do, and the one left
	// over is the edited row's.
	"the first of two rows that read the same, edited": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "y,1,7", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "y;1;007", "x;1;007", "z;2;009"),
	},
	"the last of two rows that read the same, edited": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "x,1,7", "y,1,7", "z,2,9"),
		crlf("h1;h2;h3", "x;1;007", "y;1;007", "z;2;009"),
	},
	"the middle one of three rows that read the same, edited": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "x,1,7", "y,1,7", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "x;1;007", "y;1;007", "x;1;007", "z;2;009"),
	},
	"two of three rows that read the same, edited": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "y,1,7", "w,1,7", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "y;1;007", "w;1;007", "x;1;007", "z;2;009"),
	},
	"the first of two rows that read the same, edited, a row added above": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "n,n,n", "y,1,7", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "n;n;n", "y;1;007", "x;1;007", "z;2;009"),
	},
	"the last of two rows that read the same, edited, a row added above": {
		crlf("h1;h2;h3", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "n,n,n", "x,1,7", "y,1,7", "z,2,9"),
		crlf("h1;h2;h3", "n;n;n", "x;1;007", "y;1;007", "z;2;009"),
	},
	"the first of two rows that read the same, edited, a row deleted above": {
		crlf("h1;h2;h3", "q;0;001", "x;1;007", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "y,1,7", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "y;1;007", "x;1;007", "z;2;009"),
	},
	"one of two rows that read the same, deleted": {
		crlf("h1;h2;h3", "x;1;007", "a;1;001", "x;1;007", "z;2;009"),
		ds("h1,h2,h3", "a,1,1", "x,1,7", "z,2,9"),
		crlf("h1;h2;h3", "a;1;001", "x;1;007", "z;2;009"),
	},
	// ⚠ An edit that makes a row read like a row further down: `6` typed over
	// as `5` above a row that says `05`. The typed cell is written as typed,
	// and the row further down keeps its own text.
	"an edit that makes a row read like a row further down": {
		crlf("h1;h2", "B;6", "C;1", "B;05"),
		ds("h1,h2", "B,5", "C,1", "B,5"),
		crlf("h1;h2", "B;5", "C;1", "B;05"),
	},
	"a date typed that reads like a row further down": {
		lf("tarih", "12.01.2026", "x", "13/1/2026"),
		ds("tarih", "13.01.2026", "x", "13/1/2026"),
		lf("tarih", "13.01.2026", "x", "13/1/2026"),
	},
	"a value typed that another row has, in a list of one column": {
		lf("kod", "5", "007", "5"), ds("kod", "7", "7", "5"), lf("kod", "7", "007", "5"),
	},
	"a value typed between look-alike rows": {
		lf("kod", "007", "C", "007", "y", "7"), ds("kod", "7", "7", "7", "y", "7"), lf("kod", "007", "7", "007", "y", "7"),
	},
	"a date typed above look-alike rows": {
		lf("tarih", "6", "01.02.2026", "x", "1/2/2026"),
		ds("tarih", "1/2/2026", "1/2/2026", "x", "1/2/2026"),
		lf("tarih", "1/2/2026", "01.02.2026", "x", "1/2/2026"),
	},
	// The empty lines at the end are no rows of the save: the last rows
	// still stand at their places.
	"a value typed that the last row has, empty lines after it": {
		lf("kod", "05", "007", "13.01.2026", "", ""),
		ds("kod", "5", "13/1/2026", "13.01.2026"),
		lf("kod", "05", "13/1/2026", "13.01.2026", "", ""),
	},
	"a row added that reads like a row of the file": {
		crlf("kod;ad", "007;elma", "042;armut"),
		ds("kod,ad", "7,elma", "42,armut", "7,elma"),
		crlf("kod;ad", "007;elma", "042;armut", "7;elma"),
	},
	"a row added above a row it reads like": {
		crlf("kod;ad", "042;armut", "007;elma"),
		ds("kod,ad", "7,elma", "42,armut", "7,elma"),
		crlf("kod;ad", "7;elma", "042;armut", "007;elma"),
	},
	// The rows were moved as well: which of the two that read the same is the
	// file's cannot be told, and the first one takes its text.
	"a row added that reads like a row of the file, the file sorted": {
		crlf("kod;ad", "001;armut", "002;elma", "003;kiraz"),
		ds("kod,ad", "3,kiraz", "2,elma", "2,elma", "1,armut"),
		crlf("kod;ad", "003;kiraz", "002;elma", "2;elma", "001;armut"),
	},
	"quoted fields, on a row nobody touched and on an edited one": {
		"k;t;n\r\n\"a;b\";\"say \"\"x\"\"\";\"l1\r\nl2\"\r\nz;\"q;r\";5\r\n",
		ds("k,t,n", "a;b,\"say \"\"x\"\"\",\"l1\nl2\"", "z,q;r,6"),
		"k;t;n\r\n\"a;b\";\"say \"\"x\"\"\";\"l1\r\nl2\"\r\nz;\"q;r\";6\r\n",
	},
	"a new value that needs quotes gets them": {
		lf("k;t;n", "007;x;y"),
		ds("k,t,n", "7,x,a;b \"c\""),
		lf("k;t;n", "007;x;\"a;b \"\"c\"\"\""),
	},
	"a cleared last cell stays cleared": {lf("h1;h2;h3", "a;b;c"), ds("h1,h2,h3", "a,b"), lf("h1;h2;h3", "a;b;")},
	"a cleared cell in the middle":      {lf("h1;h2;h3", "007;b;c"), ds("h1,h2,h3", "7,,c"), lf("h1;h2;h3", "007;;c")},
	"a whole row cleared":               {lf("h1;h2", "a;b", "c;d"), ds("h1,h2", "", "c,d"), lf("h1;h2", "", "c;d")},
	"a comma file": {
		lf("kod,ad,not", "007,\"a, b\",x", "042,c,y"),
		ds("kod,ad,not", "7,\"a, b\",x", "42,c,z"),
		lf("kod,ad,not", "007,\"a, b\",x", "042,c,z"),
	},
	"a tab file": {
		lf("kod\tad\tnot", "007\ta, b\tx", "042\tc\ty"),
		ds("kod,ad,not", "7,\"a, b\",x", "42,c,z"),
		lf("kod\tad\tnot", "007\ta, b\tx", "042\tc\tz"),
	},
	"a pipe file": {
		lf("kod|ad|not", "007|a, b|x", "042|c|y"),
		ds("kod,ad,not", "7,\"a, b\",x", "42,c,z"),
		lf("kod|ad|not", "007|a, b|x", "042|c|z"),
	},
	"a byte order mark": {
		"\xEF\xBB\xBF" + lf("kod,ad,not", "007,a,x"),
		ds("kod,ad,not", "7,a,x", "8,b,y"),
		"\xEF\xBB\xBF" + lf("kod,ad,not", "007,a,x", "8,b,y"),
	},
	"no final line end": {"kod;ad\n007;elma", ds("kod,ad", "7,elma"), "kod;ad\n007;elma"},
	"no final line end, a row appended": {
		"kod;ad\n007;elma", ds("kod,ad", "7,elma", "8,nar"), "kod;ad\n007;elma\n8;nar",
	},
	"mixed line ends": {
		"kod;ad\r\n007;elma\n042;armut\r\n",
		ds("kod,ad", "7,elma", "42,armut"),
		"kod;ad\r\n007;elma\n042;armut\r\n",
	},
	"mixed line ends, a row inserted": {
		"kod;ad\r\n007;elma\n042;armut\r\n",
		ds("kod,ad", "7,elma", "8,nar", "42,armut"),
		"kod;ad\r\n007;elma\n8;nar\r\n042;armut\r\n",
	},
	"empty lines at the end are kept": {
		"kod;ad\r\n007;elma\r\n\r\n\r\n", ds("kod,ad", "7,elma"), "kod;ad\r\n007;elma\r\n\r\n\r\n",
	},
	"a line of delimiters at the end is kept": {
		"kod;ad\r\n007;elma\r\n;\r\n", ds("kod,ad", "7,elma"), "kod;ad\r\n007;elma\r\n;\r\n",
	},
	"empty lines at the end, a row appended before them": {
		"kod;ad\n007;elma\n\n", ds("kod,ad", "7,elma", "8,nar"), "kod;ad\n007;elma\n8;nar\n",
	},
	// Empty rows are written one way or another in the file, and ONLYOFFICE
	// never writes the ones at its end: the saved ones take the file's where
	// they stand, and a row of delimiters stays one.
	"a row of delimiters in the middle, empty lines at the end": {
		"a;007;x\r\n;;\r\nb;008;y\r\n\r\n\r\n", ds("a,7,x", ",,", "b,8,y"), "a;007;x\r\n;;\r\nb;008;y\r\n\r\n\r\n",
	},
	"two rows of delimiters, an empty line at the end, an edit elsewhere": {
		crlf("a;007;x", ";;", "b;008;y", ";;", "c;009;z", ""),
		ds("a,7,q", ",,", "b,8,y", ",,", "c,9,z"),
		crlf("a;007;q", ";;", "b;008;y", ";;", "c;009;z", ""),
	},
	"an empty line deleted above a row of delimiters": {
		lf("a;007", "", "b;008", ";", "c;009"), ds("a,7", "b,8", ",", "c,9"), lf("a;007", "b;008", ";", "c;009"),
	},
	"an empty row added above an empty line": {
		lf("a;007", "b;008", "", "c;009"), ds("a,7", "", "b,8", "", "c,9"), lf("a;007", "", "b;008", "", "c;009"),
	},
	"an empty line in the middle": {
		lf("kod;ad", "007;elma", "", "042;armut"), ds("kod,ad", "7,elma", "", "42,armut"), lf("kod;ad", "007;elma", "", "042;armut"),
	},
	"a sep= first line is kept": {
		"sep=;\r\nkod;ad\r\n007;elma\r\n", ds("kod,ad", "7,elma"), "sep=;\r\nkod;ad\r\n007;elma\r\n",
	},
	"a header alone":              {"kod;ad;not", ds("kod,ad,not"), "kod;ad;not"},
	"a header alone, a row added": {"kod;ad;not", ds("kod,ad,not", "1,a,b"), "kod;ad;not\n1;a;b"},
	"one column":                  {lf("kod", "007", "042"), ds("kod", "7", "42", "9"), lf("kod", "007", "042", "9")},
	"rows of different widths": {
		lf("a;b;c", "007;x", "042;y;z;w"), ds("a,b,c", "7,x,,", "42,y,z,w"), lf("a;b;c", "007;x", "042;y;z;w"),
	},
	"everything deleted": {lf("kod;ad", "007;elma"), "\xEF\xBB\xBF", ""},
	// ⚠ The limit, documented: a column added, removed or moved shifts the
	// cells from it on, and they are written as ONLYOFFICE saved them in every
	// row. The cells before it keep their text only while they are more than
	// half of the row's filled cells (the row is then still told by them);
	// here they are one of two, and every row is ONLYOFFICE's.
	"a column inserted": {
		lf("kod;ad", "007;elma", "042;armut"),
		ds("kod,yeni,ad", "7,,elma", "42,,armut"),
		lf("kod;yeni;ad", "7;;elma", "42;;armut"),
	},
	"a column inserted after most of a row's cells": {
		lf("kod;ad;sehir;not", "007;elma;x;y", "042;armut;z;w"),
		ds("kod,ad,sehir,yeni,not", "7,elma,x,,y", "42,armut,z,,w"),
		lf("kod;ad;sehir;yeni;not", "007;elma;x;;y", "042;armut;z;;w"),
	},
	"a column added at the end": {
		lf("kod;ad;not", "007;elma;y", "042;armut;w"),
		ds("kod,ad,not,yeni", "7,elma,y,1", "42,armut,w,2"),
		lf("kod;ad;not;yeni", "007;elma;y;1", "042;armut;w;2"),
	},
	// The removed cell is a cleared one to filex: its delimiter stays.
	"the last column removed": {
		lf("kod;ad;sehir;not", "007;elma;x;y", "042;armut;z;w"),
		ds("kod,ad,sehir", "7,elma,x", "42,armut,z"),
		lf("kod;ad;sehir;", "007;elma;x;", "042;armut;z;"),
	},
	"the first column removed": {
		lf("kod;ad;sehir;not", "007;elma;x;y", "042;armut;z;w"),
		ds("ad,sehir,not", "elma,x,y", "armut,z,w"),
		lf("ad;sehir;not", "elma;x;y", "armut;z;w"),
	},
}

func TestKeepCSV(t *testing.T) {
	for name, c := range keepCSVCases {
		t.Run(name, func(t *testing.T) {
			d := SniffCSV([]byte(c.original))
			out, kept := KeepCSV([]byte(c.original), []byte(c.saved), d)
			assert.Equal(t, c.want, string(out))
			assert.True(t, kept.Applied, "kept: %+v", kept)
			assertKept(t, []byte(c.original), []byte(c.saved), out, d, kept)
		})
	}
}

func TestKeepCSV_SaysWhatItDid(t *testing.T) {
	_, kept := KeepCSV([]byte(measuredOriginal), []byte(measuredSaved), SniffCSV([]byte(measuredOriginal)))
	assert.Equal(t, CSVKept{Applied: true, Records: 6, Unchanged: 3, Restored: 3, New: 2}, kept,
		"the header and two rows as they were; the phone, the date and the code of the edited row; two new rows")

	c := keepCSVCases["a row deleted"]
	_, kept = KeepCSV([]byte(c.original), []byte(c.saved), SniffCSV([]byte(c.original)))
	assert.Equal(t, CSVKept{Applied: true, Records: 3, Unchanged: 3, Deleted: 1}, kept)
}

// A save is never failed over this (P3): whatever stops the cells being kept,
// the bytes are RewriteCSV's, with the reason.
func TestKeepCSV_WhenItCannotKeepItIsRewriteCSV(t *testing.T) {
	saved := []byte(ds("kod,ad", "7,elma"))
	semicolon := CSVDialect{Comma: ';', UTF8: true}
	check := func(t *testing.T, why string, original, saved []byte, d CSVDialect) {
		t.Helper()
		out, kept := KeepCSV(original, saved, d)
		assert.Equal(t, CSVKept{Why: why}, kept)
		assert.Equal(t, string(RewriteCSV(saved, d)), string(out))
		assertKept(t, original, saved, out, d, kept)
	}
	t.Run("an empty original", func(t *testing.T) {
		check(t, "empty", nil, saved, DefaultCSVDialect)
		check(t, "empty", utf8BOM, saved, CSVDialect{Comma: ',', BOM: true, UTF8: true})
	})
	t.Run("not UTF-8", func(t *testing.T) {
		// Windows-1254: its text cannot be compared with what ONLYOFFICE saved.
		legacy := []byte("kod;ad\n007;\xFEeker\n")
		check(t, "not_utf8", legacy, saved, SniffCSV(legacy))
		// UTF-8 as far as the sniff read, not after it.
		check(t, "not_utf8", legacy, saved, semicolon)
	})
	t.Run("too large", func(t *testing.T) {
		defer func(n int64) { csvKeepMaxBytes = n }(csvKeepMaxBytes)
		csvKeepMaxBytes = 16
		check(t, "too_large", []byte("kod;ad\n007;elma\n042;armut\n"), saved, semicolon)
		check(t, "too_large", []byte("kod;ad\n"), []byte(ds("kod,ad", "7,elma", "42,armut")), semicolon)
	})
	t.Run("too many fields in a record", func(t *testing.T) {
		// One line of delimiters is as many fields, 48 bytes each were they
		// held: neither side is split into more than a sheet has columns.
		wide := strings.Repeat(";", 300000)
		check(t, "too_many_fields", []byte(wide), saved, semicolon)
		check(t, "too_many_fields", []byte("kod;ad\n"+wide+"\n"), saved, semicolon)
		out, kept := KeepCSV([]byte("kod;ad\n007;elma\n"), []byte(strings.Repeat(",", 300000)+"\n"), semicolon)
		assert.Equal(t, CSVKept{Why: "too_many_fields"}, kept)
		assert.True(t, wide+"\n" == string(out), "the save in the file's dialect, %d bytes", len(out))

		defer func(n int) { csvKeepMaxFields = n }(csvKeepMaxFields)
		csvKeepMaxFields = 3
		check(t, "too_many_fields", []byte("a;b;c;d\n"), saved, semicolon)
		check(t, "too_many_fields", []byte("kod;ad\n"), []byte(ds("kod,ad", "7,elma,x,")), semicolon)
		out, kept = KeepCSV([]byte("kod;ad;not\n007;elma;\n"), []byte(ds("kod,ad,not", "7,elma,x")), semicolon)
		assert.True(t, kept.Applied, "three fields are not too many: %+v", kept)
		assert.Equal(t, "kod;ad;not\n007;elma;x\n", string(out))
	})
	t.Run("too many records", func(t *testing.T) {
		defer func(n int) { csvKeepMaxRecords = n }(csvKeepMaxRecords)
		csvKeepMaxRecords = 2
		check(t, "too_many_records", []byte("kod;ad\n007;elma\n042;armut\n"), saved, semicolon)
		check(t, "too_many_records", []byte("kod;ad\n"), []byte(ds("kod,ad", "7,elma", "42,armut")), semicolon)
	})
	t.Run("the written file does not read back as the save", func(t *testing.T) {
		// The last record's quote is never closed: written as it was with a
		// record after it, it would swallow that record.
		original := []byte("a;b\n\"c;d")
		check(t, "check_failed", original, []byte(ds("a,b", "c;d", "new,row")), SniffCSV(original))
		// A save with two byte order marks: the second is the first cell's
		// text. At the start of a file without a mark it would be read as one.
		check(t, "check_failed", []byte("kod;ad\n007;elma\n"), []byte("\xEF\xBB\xBF"+ds("kod,ad", "7,elma")), semicolon)
		out, kept := KeepCSV([]byte("\xEF\xBB\xBFkod;ad\n007;elma\n"), []byte("\xEF\xBB\xBF"+ds("kod,ad", "7,elma")),
			CSVDialect{Comma: ';', BOM: true, UTF8: true})
		assert.True(t, kept.Applied, "after the file's own mark it is a cell's text like any other: %+v", kept)
		assert.Equal(t, "\xEF\xBB\xBF\xEF\xBB\xBFkod;ad\n007;elma\n", string(out))
	})
	t.Run("a panic", func(t *testing.T) {
		out, kept := keepOrRewrite([]byte("kod;ad\n007;elma\n"), saved, semicolon,
			func(_, _ []byte, _ CSVDialect) ([]byte, CSVKept) { panic("a bug in lining the records up") })
		assert.Equal(t, "panic", kept.Why)
		assert.False(t, kept.Applied)
		assert.Equal(t, "kod;ad\n7;elma\n", string(out))
		// What panicked and where, for the log: without them a bug in production
		// would leave `why=panic` and nothing to find it by.
		assert.True(t, strings.HasPrefix(kept.Panic, "a bug in lining the records up\n"), "%.80q", kept.Panic)
		assert.Contains(t, kept.Panic, "TestKeepCSV_WhenItCannotKeepItIsRewriteCSV", "the stack of the panic")
	})
}

// RewriteCSV is what a save is written with whenever its cells cannot be kept,
// up to csvSaveMaxBytes of it. A line of a million delimiters is a million
// fields: each is written as it is read. Held, they cost 48 bytes a field and
// the slice's growth on top, 250 times the line (measured: 258 MB for 1 MiB).
func TestRewriteCSV_AWideLineIsNotHeldFieldByField(t *testing.T) {
	const n = 1 << 20
	in := bytes.Repeat([]byte{','}, n)
	d := CSVDialect{Comma: ';', CRLF: true, UTF8: true}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out := RewriteCSV(in, d)
	runtime.ReadMemStats(&after)
	assert.True(t, bytes.Equal(bytes.Repeat([]byte{';'}, n), out), "a field for every delimiter, and the one after the last")
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8*n), "allocated for a line of %d bytes", n)

	out = RewriteCSV(append(in, "\n\"a\"\"b\",\n"...), d)
	assert.True(t, bytes.Equal(append(bytes.Repeat([]byte{';'}, n), "\r\n\"a\"\"b\";\r\n"...), out))
}

// The check every kept file passes before it is returned (P2), fed a pairing
// that is wrong on purpose: the two originals written on each other's row.
func TestCSVKeepCheck_RefusesAWrongPairing(t *testing.T) {
	saved := []byte("kod,ad\n7,elma\n42,armut\n")
	assert.True(t, csvKeepCheck([]byte("kod;ad\n007;elma\n042;armut\n"), saved, ';', 0, 0))
	assert.False(t, csvKeepCheck([]byte("kod;ad\n042;elma\n007;armut\n"), saved, ';', 0, 0),
		"042 is not what ONLYOFFICE writes as 7")
	assert.False(t, csvKeepCheck([]byte("kod;ad\n007;elma\n"), saved, ';', 0, 0), "a record short")
	assert.False(t, csvKeepCheck([]byte("kod;ad\n007;elma\n042;armut\nx;y\n"), saved, ';', 0, 0), "a record too many")
	assert.False(t, csvKeepCheck([]byte("kod;ad\n007;elma;x\n042;armut\n"), saved, ';', 0, 0), "a cell too many")
	assert.False(t, csvKeepCheck([]byte("kod;ad\n007\n042;armut\n"), saved, ';', 0, 0), "a cell short")
	assert.True(t, csvKeepCheck([]byte("kod;ad;\n007;elma\n042;armut;;\n"), saved, ';', 0, 0), "empty cells at a row's end aside")
	assert.True(t, csvKeepCheck([]byte("sep=;\nkod;ad\n007;elma\n042;armut\n\n;\n"), saved, ';', 1, 2),
		"a kept sep= line and kept empty lines at the end are not in the save")
	assert.False(t, csvKeepCheck([]byte("kod;ad\n007;elma\n042;armut\n\nx\n"), saved, ';', 0, 2),
		"a kept line at the end is empty")
}

// Pairing reads the file's records again for every saved record that pairs
// with none of them. A few huge ones in the way are not read thirty thousand
// times: past a bound nothing more is paired, and the rest is written as
// ONLYOFFICE saved it.
func TestKeepCSV_PairingReadsABoundedAmount(t *testing.T) {
	var o, s, want bytes.Buffer
	o.WriteString("kod;ad;not\n")
	s.WriteString("\xEF\xBB\xBFkod,ad,not\n")
	want.WriteString("kod;ad;not\n")
	long := strings.Repeat("a", 100<<10)
	for i := 0; i < csvKeepPairWindow; i++ {
		fmt.Fprintf(&o, "%03d;%s;x%d\n", i, long, i)
	}
	const rows = 30000
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&s, "p%d,q,r\n", i)
		fmt.Fprintf(&want, "p%d;q;r\n", i)
	}
	// The third long row with its note edited: it would pair, were it not
	// behind thirty thousand rows that read the long ones first.
	fmt.Fprintf(&s, "2,%s,edited\n", long)
	fmt.Fprintf(&want, "2;%s;edited\n", long)

	d := SniffCSV(o.Bytes()[:CSVSniffBytes])
	out, kept := KeepCSV(o.Bytes(), s.Bytes(), d)
	assert.True(t, bytes.Equal(want.Bytes(), out), "every row as ONLYOFFICE saved it")
	assert.Equal(t, CSVKept{Applied: true, Records: rows + 2, Unchanged: 1, New: rows + 1, Deleted: csvKeepPairWindow}, kept)
	assertKept(t, o.Bytes(), s.Bytes(), out, d, kept)

	// Alone, it does pair.
	out, kept = KeepCSV(o.Bytes(), []byte(ds("kod,ad,not", "2,"+long+",edited")), d)
	assert.Equal(t, "kod;ad;not\n002;"+long+";edited\n", string(out))
	assert.Equal(t, 1, kept.Restored)
}

// One cell edited, the rows where they stood: whatever the table says, the
// cell is written as typed and no cell is written with a text that is neither
// its own nor what ONLYOFFICE saved for it. ⚠ Lining the records up by what
// they say alone gave a typed cell the text of a row that reads the same
// further down (`6` typed over as `5` above a row that says `05` was written
// `05`, and that row `5`). Tables of few columns and few values, so that rows
// read like each other all the time.
func TestKeepCSV_ACellTypedIsWrittenAsTypedAndNoCellTakesAnothersText(t *testing.T) {
	pool := []string{"007", "7", "05", "5", "6", "x", "y", "01.02.2026", "1/2/2026", "13.01.2026", "13/1/2026", "B", "C", "true", "TRUE", ""}
	// What ONLYOFFICE writes for a cell nobody touched, and for one typed.
	shown := func(v string) string {
		switch v {
		case "007":
			return "7"
		case "05":
			return "5"
		case "01.02.2026":
			return "1/2/2026"
		case "true":
			return "TRUE"
		}
		return v
	}
	d := CSVDialect{Comma: ';', UTF8: true}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 30000; n++ {
		cols, rows := 1+rng.Intn(4), 1+rng.Intn(7)
		orig, saved := make([][]string, rows), make([][]string, rows)
		var o, s strings.Builder
		for r := range orig {
			orig[r], saved[r] = make([]string, cols), make([]string, cols)
			for c := range orig[r] {
				orig[r][c] = pool[rng.Intn(len(pool))]
				saved[r][c] = shown(orig[r][c])
			}
			o.WriteString(strings.Join(orig[r], ";") + "\n")
		}
		er, ec, typed := rng.Intn(rows), rng.Intn(cols), shown(pool[rng.Intn(len(pool))])
		if csvSame([]byte(orig[er][ec]), []byte(typed), true) {
			// Not an edit ONLYOFFICE's save shows.
			continue
		}
		saved[er][ec] = typed
		// ONLYOFFICE stops at the last row that holds something.
		last := rows
		for last > 0 && strings.Join(saved[last-1], "") == "" {
			last--
		}
		for _, row := range saved[:last] {
			s.WriteString(strings.Join(row, ",") + "\n")
		}
		out, kept := KeepCSV([]byte(o.String()), []byte(s.String()), d)
		require.True(t, kept.Applied, "kept: %+v", kept)
		got := csvRows(out, ';')
		for r := range orig {
			for c := range orig[r] {
				cell := ""
				if r < len(got) && c < len(got[r]) {
					cell = got[r][c]
				}
				if r == er && c == ec {
					require.Equal(t, typed, cell, "the cell typed, row %d of\n%q saved as\n%q written\n%q", r, o.String(), s.String(), out)
					continue
				}
				if cell != orig[r][c] && cell != saved[r][c] {
					t.Fatalf("row %d cell %d is %q: neither its own text nor the saved one, of\n%q saved as\n%q written\n%q",
						r, c, cell, o.String(), s.String(), out)
				}
			}
		}
	}
}

// csvGenRow is row i of a generated list, as a person keeps it and as
// ONLYOFFICE saves it untouched (leading zeros gone, a date it reads as one
// written its way, the decimal comma quoted).
func csvGenRow(orig, saved *bytes.Buffer, i int) {
	day, mon := i%28+1, i%12+1
	fmt.Fprintf(orig, "%d;Ad Soyad %d;0532%07d;%02d.%02d.2026;%d,50;%03d;Örnek Mahallesi Deneme Sokak No %d;not %d\r\n",
		i, i, i, day, mon, i%1000, i%1000, i, i)
	date := fmt.Sprintf("%02d.%02d.2026", day, mon)
	if day <= 12 {
		// Month first to ONLYOFFICE (the editor's language was English).
		date = strconv.Itoa(day) + "/" + strconv.Itoa(mon) + "/2026"
	}
	fmt.Fprintf(saved, "%d,Ad Soyad %d,532%07d,%s,\"%d,50\",%d,Örnek Mahallesi Deneme Sokak No %d,not %d\n",
		i, i, i, date, i%1000, i%1000, i, i)
}

// csvGen is a list of n rows under a header, and what ONLYOFFICE saves after:
// one cell edited in row 2, one row inserted after row 5, two rows appended.
// want is the original with exactly those differences.
func csvGen(n int) (original, saved, want []byte) {
	var o, s, w bytes.Buffer
	o.WriteString("No;Ad;Telefon;Tarih;Tutar;Kod;Adres;Not\r\n")
	s.WriteString("\xEF\xBB\xBFNo,Ad,Telefon,Tarih,Tutar,Kod,Adres,Not\n")
	w.WriteString("No;Ad;Telefon;Tarih;Tutar;Kod;Adres;Not\r\n")
	for i := 1; i <= n; i++ {
		at := o.Len()
		csvGenRow(&o, &s, i)
		row := o.Bytes()[at:]
		switch i {
		case 1:
			// Row 2 of the file: its note edited.
			s.Truncate(s.Len() - len("not 1\n"))
			s.WriteString("degisti\n")
			w.Write(bytes.Replace(row, []byte("not 1\r\n"), []byte("degisti\r\n"), 1))
		case 4:
			w.Write(row)
			s.WriteString("900001,Yeni Kayit,905320000001,3/4/2026,\"1,50\",5,\"Yeni; adres\",araya\n")
			w.WriteString("900001;Yeni Kayit;905320000001;3/4/2026;1,50;5;\"Yeni; adres\";araya\r\n")
		default:
			w.Write(row)
		}
	}
	// ONLYOFFICE pads a row to the widest it has written; the last row not.
	s.WriteString("900002,Son,1,,,,,,\n900003,En son,2\n")
	w.WriteString("900002;Son;1;;;;;\r\n900003;En son;2\r\n")
	return o.Bytes(), s.Bytes(), w.Bytes()
}

func TestKeepCSV_ALongListKeepsEveryRowButTheOnesChanged(t *testing.T) {
	const rows = 200000
	original, saved, want := csvGen(rows)
	d := SniffCSV(original[:CSVSniffBytes])
	out, kept := KeepCSV(original, saved, d)
	if !bytes.Equal(want, out) {
		at := 0
		for at < len(want) && at < len(out) && want[at] == out[at] {
			at++
		}
		from := max(at-80, 0)
		t.Fatalf("differs at byte %d of %d (wrote %d):\n want %q\n  got %q", at, len(want), len(out),
			want[from:min(at+80, len(want))], out[from:min(at+80, len(out))])
	}
	// Every row but the edited one as it was; in the edited one the phone and
	// the code put back (its date, 02.02.2026, with them).
	assert.Equal(t, CSVKept{Applied: true, Records: rows + 4, Unchanged: rows, Restored: 3, New: 3}, kept)
	assertKept(t, original, saved, out, d, kept)
}

// Arbitrary bytes on both sides: no panic, and what is written is the save,
// cell for cell (P2), or RewriteCSV's bytes.
func FuzzKeepCSV(f *testing.F) {
	for _, c := range keepCSVCases {
		f.Add([]byte(c.original), []byte(c.saved))
	}
	f.Add([]byte("a;b\n\"c;d"), []byte(ds("a,b", "c;d", "new,row")))
	f.Add([]byte("a;b\rc;d\r"), []byte(ds("a,b", "c,d")))
	f.Add([]byte("sep=;\nç"), []byte(ds("sep=;", "ç")))
	f.Add([]byte("k;v\r\n007;\"x\"y\r\n"), []byte("k,v\n7,xy\n\n"))
	f.Add([]byte("kod;ad;not\n007;elma;y\n"), []byte("\xEF\xBB\xBF"+ds("kod,not,yeni", "7,elma,y,1")))
	f.Add([]byte(strings.Repeat(";", 20000)+"\nkod;ad\n"), []byte(ds("kod,ad")))
	f.Fuzz(func(t *testing.T, original, saved []byte) {
		d := SniffCSV(original[:min(len(original), CSVSniffBytes)])
		out, kept := KeepCSV(original, saved, d)
		assertKept(t, original, saved, out, d, kept)
	})
}

// 500,000 rows, about 55 MB: what a save of a large list costs.
func BenchmarkKeepCSV(b *testing.B) {
	original, saved, want := csvGen(500000)
	d := SniffCSV(original[:CSVSniffBytes])
	b.SetBytes(int64(len(original)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, kept := KeepCSV(original, saved, d)
		if !kept.Applied || len(out) != len(want) {
			b.Fatalf("kept %+v, wrote %d bytes, want %d", kept, len(out), len(want))
		}
	}
}

// The lining up alone: both files indexed and their records matched.
func BenchmarkKeepCSV_LiningUp(b *testing.B) {
	original, saved, _ := csvGen(500000)
	b.SetBytes(int64(len(original)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var o, s csvSide
		if o.index(original, ';', true) != "" || s.index(bytes.TrimPrefix(saved, utf8BOM), ',', true) != "" {
			b.Fatal("too many records")
		}
		from, _ := csvLineUp(&o, &s, 0, true)
		if len(from) != len(s.recs) {
			b.Fatal("not every saved record was looked at")
		}
	}
}

// One line of 16 MiB of delimiters: what RewriteCSV costs on the widest record
// a save may hold.
func BenchmarkRewriteCSV_AWideLine(b *testing.B) {
	in := bytes.Repeat([]byte{','}, 16<<20)
	d := CSVDialect{Comma: ';', UTF8: true}
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := RewriteCSV(in, d); len(out) != len(in) {
			b.Fatalf("wrote %d bytes, want %d", len(out), len(in))
		}
	}
}

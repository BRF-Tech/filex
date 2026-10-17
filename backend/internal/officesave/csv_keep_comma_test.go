package officesave

// A number somebody types into a column whose decimals the file writes with a
// comma keeps the comma (csv_keep.go → csvDecimalCommas;
// docs/ONLYOFFICE.md → A decimal you type keeps the file's comma).
//
// Measured on Docs 9.4.0.1 (onlyoffice/documentserver:9.4.0.1, JWT off, its
// defaults), 2026-10-17: a semicolon CSV opened as filex opens one (UTF-8 and
// its delimiter in document.options), the editor in the language and region
// filex passes (tr / tr-TR, en / en-US, de / de-DE), cells typed through the
// cell name box, the editor closed; the bytes are the file of the status 2
// callback. In the Turkish and the German editor a decimal comma typed is a
// number, and the save writes it with a point and up to 17 significant
// digits: `4,5` as `4.5`, `1,1` as `1.1000000000000001`. In the English one
// it is text and comes back as typed; a point typed there is the number.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/officesave/officesavetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendCSVCommaDecimal(t *testing.T) {
	for in, want := range map[string]string{
		"4.5":       "4,5",
		"-4.5":      "-4,5",
		"0.25":      "0,25",
		"1250.75":   "1250,75",
		"0.0001":    "0,0001",
		"0.1234567": "0,1234567",
		// As many digits as a number format wrote: fifteen significant
		// digits or fewer are the decimal itself.
		"4.50":             "4,50",
		"1.23456789012345": "1,23456789012345",
		"123456789012.5":   "123456789012,5",
		// Seventeen: the writer's %.17g. The shortest decimal that is the
		// same double, which is what was typed (measured, the Turkish editor:
		// `1,1`, `2,675`, `12345678,123456`, `99999999999999,9`).
		"1.1000000000000001":   "1,1",
		"2.6749999999999998":   "2,675",
		"12345678.123455999":   "12345678,123456",
		"99999999999999.906":   "99999999999999,9",
		"-0.10000000000000001": "-0,1",
		"1.000000000000001":    "1,000000000000001",
		// Sixteen or seventeen digits that are not what %.17g prints for
		// their double: a text, its digits kept.
		"1234567890123456.7":  "1234567890123456,7",
		"1.0000000000000000":  "1,0000000000000000",
		"0.10000000000000000": "0,10000000000000000",
	} {
		got, ok := appendCSVCommaDecimal(nil, []byte(in))
		if assert.True(t, ok, in) {
			assert.Equal(t, want, string(got), in)
		}
	}
	// Nothing that is not a number the writer printed with a point and
	// digits only: a whole number has no decimal mark to change, and an
	// exponent, a percent, a date or a text is written as ONLYOFFICE saved it.
	for _, in := range []string{
		"", "4", "45", "-4", "4.", ".5", "-.5", "+4.5", " 4.5", "4.5 ", "4,5",
		"1.50e+20", "9.9999999999999995e-07", "4.50%", "4.May", "Nis.50", "1.02.2026",
		"1.2.3", "0x1.8p1", "1_0.5", "4.5\n", "--4.5", "-", "٤.٥",
		// More than 17 significant digits: no number the writer printed,
		// a text that reads like one, written as it is.
		"0.123456789012345678901", "123456789012345678.5", "1.0000000000000000001",
	} {
		got, ok := appendCSVCommaDecimal(nil, []byte(in))
		assert.False(t, ok, "%q: %q", in, got)
	}
}

func TestCSVCommaNumber(t *testing.T) {
	for _, v := range []string{"3,5", "-4,5", "0,25", "1250,75", "1.250,75", "12.345.678,9", "0,0001", "1250,750", "1,2500", "1.250,750"} {
		assert.True(t, csvCommaNumber([]byte(v)), v)
	}
	// `1,250`: a thousand and more, the English way.
	for _, v := range []string{"", "3", "3,", ",5", "3.5", "1 250,75", "1.25,5", "1250.750,5", "+3,5", " 3,5", "3,5 ", "3,5,5", "a,5", "3,5%", ".250,5",
		"1,250", "-2,500", "999,000"} {
		assert.False(t, csvCommaNumber([]byte(v)), v)
	}
}

func TestCSVThousandsAndPointNumbers(t *testing.T) {
	for _, v := range []string{"1,250,000", "-12,345,678"} {
		assert.True(t, csvThousands([]byte(v)), v)
	}
	for _, v := range []string{"1,250", "1250,000,000", "1,25,000", "1,250,00", ",250,000", "3,5"} {
		assert.False(t, csvThousands([]byte(v)), v)
	}
	for _, v := range []string{"3.5", ".5", "5.", "-1.250", "+1.5", " 3.5", "3.5 ", "1.5e3", "1.5E-7", "1.50e+20", "0.1234567", "1e3", "12E5", "15e-1"} {
		assert.True(t, csvPointNumber([]byte(v)), v)
	}
	for _, v := range []string{"", ".", "-.", "3", "3,5", "01.02.2026", "4.May", "1.2.3", "Mah.", "No.5", "1.5e", "1.5e+", "e.5", "0x1.8p1", "Inf", "1_0.5"} {
		assert.False(t, csvPointNumber([]byte(v)), v)
	}
	assert.Zero(t, testing.AllocsPerRun(100, func() { csvPointNumber([]byte("02.02.2026")) }), "a date is told from a number without a copy")
}

// What the file says, column by column: a number with a decimal comma that a
// save kept as it was says the column writes its decimals so; a number with a
// point anywhere in the column, or a comma delimiter, says nothing can be
// told.
var keepCommaCases = map[string]struct{ original, saved, want string }{
	"an edited decimal gets the column's comma": {
		crlf("ad;tutar", "elma;3,5", "armut;1250,75"),
		ds("ad,tutar", "elma,4.5", "armut,\"1250,75\""),
		crlf("ad;tutar", "elma;4,5", "armut;1250,75"),
	},
	"a decimal typed in a new row gets it too": {
		crlf("ad;tutar", "elma;3,5"),
		ds("ad,tutar", "elma,\"3,5\"", "armut,2.75"),
		crlf("ad;tutar", "elma;3,5", "armut;2,75"),
	},
	"the decimal the person typed back, written as the file had it": {
		crlf("ad;tutar;not", "elma;3,5;a", "armut;0,25;b"),
		ds("ad,tutar,not", "elma,3.5,x", "armut,\"0,25\",b"),
		crlf("ad;tutar;not", "elma;3,5;x", "armut;0,25;b"),
	},
	"seventeen digits come back as the decimal typed": {
		crlf("ad;tutar", "elma;3,5", "armut;0,25"),
		ds("ad,tutar", "elma,1.1000000000000001", "armut,\"0,25\""),
		crlf("ad;tutar", "elma;1,1", "armut;0,25"),
	},
	"a column grouped with points is a comma column": {
		crlf("ad;tutar", "elma;1.250,75", "armut;3,5"),
		ds("ad,tutar", "elma,\"1.250,75\"", "armut,4.5"),
		crlf("ad;tutar", "elma;1.250,75", "armut;4,5"),
	},
	"tab-separated": {
		crlf("ad\ttutar", "elma\t3,5", "armut\t0,25"),
		ds("ad,tutar", "elma,4.5", "armut,\"0,25\""),
		crlf("ad\ttutar", "elma\t4,5", "armut\t0,25"),
	},
	"only the comma column": {
		crlf("ad;tutar;oran;adet", "elma;3,5;1.5;3", "armut;0,25;2.5;5"),
		ds("ad,tutar,oran,adet", "elma,4.5,1.75,3.5", "armut,\"0,25\",2.5,5"),
		crlf("ad;tutar;oran;adet", "elma;4,5;1.75;3.5", "armut;0,25;2.5;5"),
	},
	"what is not a decimal ONLYOFFICE wrote with a point stays as saved": {
		crlf("ad;tutar", "a;3,5", "b;3,5", "c;3,5", "d;3,5", "e;3,5", "f;0,25"),
		ds("ad,tutar", "a,4", "b,1.50e+20", "c,4.50%", "d,4.May", "e,\"4,5\"", "f,\"0,25\""),
		crlf("ad;tutar", "a;4", "b;1.50e+20", "c;4.50%", "d;4.May", "e;4,5", "f;0,25"),
	},
	"a comma-separated file: a comma is its delimiter": {
		lf("ad,tutar", "elma,\"3,5\"", "armut,\"0,25\""),
		ds("ad,tutar", "elma,4.5", "armut,\"0,25\""),
		lf("ad,tutar", "elma,4.5", "armut,\"0,25\""),
	},
	"a column with a decimal point as well: no way to tell": {
		crlf("ad;tutar", "elma;3,5", "armut;0,25", "kiraz;1.5"),
		ds("ad,tutar", "elma,4.5", "armut,\"0,25\"", "kiraz,1.5"),
		crlf("ad;tutar", "elma;4.5", "armut;0,25", "kiraz;1.5"),
	},
	"no decimal comma kept as it was: no evidence the column is still there": {
		crlf("ad;tutar", "elma;3,5", "armut;0,25"),
		ds("ad,tutar", "elma,4.5", "armut,0.75"),
		crlf("ad;tutar", "elma;4.5", "armut;0.75"),
	},
	"a column deleted: the decimals that moved in are not the comma column's": {
		crlf("ad;tutar;oran", "elma;3,5;0.5", "armut;0,25;0.25", "kiraz;1,5;0.125"),
		ds("ad,oran", "elma,0.5", "armut,0.25", "kiraz,0.125"),
		crlf("ad;oran", "elma;0.5", "armut;0.25", "kiraz;0.125"),
	},
	// Found in review: a value the two columns share ("0,00") stands at the
	// deleted column's place in the first row, and the column looks as if it
	// had not moved. The decimals that moved there are the file's elsewhere.
	"a column deleted, a value shared with the next one": {
		crlf("id;ad;a;b", "1;x;0,00;0,00", "2;y;3,5;1.5", "3;z;2,5;7.25"),
		ds("id,ad,b", "1,x,\"0,00\"", "2,y,1.5", "3,z,7.25"),
		crlf("id;ad;b;", "1;x;0,00;", "2;y;1.5;", "3;z;7.25;"),
	},
	"two columns swapped": {
		crlf("ad;tutar;oran", "a;0,5;0.5", "b;0,5;0.25", "c;3,5;0.125"),
		ds("ad,oran,tutar", "a,0.5,\"0,5\"", "b,0.25,\"0,5\"", "c,0.125,\"3,5\""),
		crlf("ad;oran;tutar", "a;0.5;0,5", "b;0.25;0,5", "c;0.125;3,5"),
	},
	"a cell deleted with the cells after it shifted left": {
		crlf("ad;fiyat;enlem", "a;3,5;41.0082", "b;2,5;40.1234", "c;1,5;39.5"),
		ds("ad,fiyat,enlem", "a,\"3,5\",41.0082", "b,40.1234,", "c,\"1,5\",39.5"),
		crlf("ad;fiyat;enlem", "a;3,5;41.0082", "b;40.1234;", "c;1,5;39.5"),
	},
	"a cell inserted with the cells after it shifted right": {
		crlf("ad;enlem;fiyat", "a;41.0082;3,5", "b;40.1234;2,5", "c;39.5;1,5"),
		ds("ad,enlem,fiyat", "a,41.0082,\"3,5\"", "b,,40.1234,\"2,5\"", "c,39.5,\"1,5\""),
		crlf("ad;enlem;fiyat", "a;41.0082;3,5", "b;;40.1234;2,5", "c;39.5;1,5"),
	},
	"a number typed that the file holds with a point keeps the point": {
		crlf("ad;tutar;oran", "a;3,5;4.5", "b;0,25;1.5"),
		ds("ad,tutar,oran", "a,4.5,4.5", "b,\"0,25\",1.5"),
		crlf("ad;tutar;oran", "a;4.5;4.5", "b;0,25;1.5"),
	},
	"a column with thousands between commas the English way": {
		crlf("id;tutar", "1;1,250", "2;2,500", "3;3,5"),
		ds("id,tutar", "1,\"1,250\"", "2,4.5", "3,\"3,5\""),
		crlf("id;tutar", "1;1,250", "2;4,5", "3;3,5"),
	},
	"only thousands the English way: no decimal comma to follow": {
		crlf("id;tutar", "1;1,250", "2;2,500"),
		ds("id,tutar", "1,\"1,250\"", "2,4.5"),
		crlf("id;tutar", "1;1,250", "2;4.5"),
	},
	"millions with commas: the English way": {
		crlf("id;tutar", "1;1,250,000", "2;3,5", "3;0,25"),
		ds("id,tutar", "1,\"1,250,000\"", "2,4.5", "3,\"0,25\""),
		crlf("id;tutar", "1;1,250,000", "2;4.5", "3;0,25"),
	},
	// Found in the second review: moved numbers ONLYOFFICE writes in
	// another spelling than the file's are told by their value.
	"an exponent shifted along a row": {
		crlf("ad;enlem;fiyat", "a;1e3;3,5", "b;2.5e3;2,5", "c;3e3;1,5"),
		ds("ad,enlem,fiyat", "a,1000.00000,\"3,5\"", "b,,2500.00000,\"2,5\"", "c,3000.00000,\"1,5\""),
		crlf("ad;enlem;fiyat", "a;1000.00000;3,5", "b;;2500.00000;2,5", "c;3000.00000;1,5"),
	},
	"an exponent nobody touched in the column": {
		crlf("id;tutar", "1;3,5", "2;1e3", "3;0,25", "4;1,5"),
		ds("id,tutar", "1,\"3,5\"", "2,1000.00000", "3,4.5", "4,\"1,5\""),
		crlf("id;tutar", "1;3,5", "2;1000.00000", "3;4.5", "4;1,5"),
	},
	"a number with a space after it shifted along a row": {
		crlf("id;b;a", "1;1.5 ;3,5", "2;2.5 ;0,5", "3;7 ;1,5"),
		ds("id,b,a", "1,1.50,\"3,5\"", "2,,2.50,\"0,5\"", "3,7,\"1,5\""),
		crlf("id;b;a", "1;1.50;3,5", "2;;2.50;0,5", "3;7 ;1,5"),
	},
	"whole numbers only: no decimal mark to keep": {
		crlf("ad;adet", "elma;3", "armut;5"),
		ds("ad,adet", "elma,4.5", "armut,5"),
		crlf("ad;adet", "elma;4.5", "armut;5"),
	},
}

func TestKeepCSV_AnEditedDecimalKeepsTheColumnsComma(t *testing.T) {
	for name, c := range keepCommaCases {
		t.Run(name, func(t *testing.T) {
			d := SniffCSV([]byte(c.original))
			out, kept := KeepCSV([]byte(c.original), []byte(c.saved), d)
			require.True(t, kept.Applied, "kept: %+v", kept)
			assert.Equal(t, c.want, string(out))
			assertKept(t, []byte(c.original), []byte(c.saved), out, d, kept)
		})
	}
}

func TestKeepCSV_SaysHowManyDecimalsGotTheComma(t *testing.T) {
	c := keepCommaCases["only the comma column"]
	_, kept := KeepCSV([]byte(c.original), []byte(c.saved), SniffCSV([]byte(c.original)))
	assert.Equal(t, CSVKept{Applied: true, Records: 3, Unchanged: 2, New: 1, Deleted: 1, Commas: 1}, kept,
		"three of elma's four cells changed: no row of the file is lined up with it, and it is written as a new one")

	c = keepCommaCases["a decimal typed in a new row gets it too"]
	_, kept = KeepCSV([]byte(c.original), []byte(c.saved), SniffCSV([]byte(c.original)))
	assert.Equal(t, CSVKept{Applied: true, Records: 3, Unchanged: 2, New: 1, Commas: 1}, kept)
}

// P2 with the comma: a cell written with a decimal comma is the saved number
// only in a column whose decimals the file writes so, and only as the one
// spelling appendCSVCommaDecimal gives it.
func TestCSVKeepCheck_ADecimalCommaOnlyWhereTheColumnHasOne(t *testing.T) {
	saved := []byte("ad,tutar\nelma,4.5\narmut,1.1000000000000001\n")
	dec := &csvCommas{cols: []bool{false, true}}
	assert.True(t, csvKeepCheck([]byte("ad;tutar\nelma;4,5\narmut;1,1\n"), saved, ';', ',', 0, 0, dec))
	assert.True(t, csvKeepCheck([]byte("ad;tutar\nelma;4.5\narmut;1.1000000000000001\n"), saved, ';', ',', 0, 0, dec),
		"the point ONLYOFFICE wrote is the save too")
	assert.False(t, csvKeepCheck([]byte("ad;tutar\nelma;4,5\narmut;1,1\n"), saved, ';', ',', 0, 0, nil), "no comma column")
	assert.False(t, csvKeepCheck([]byte("ad;tutar\nelma;4,5\narmut;1,1\n"), saved, ';', ',', 0, 0, &csvCommas{cols: []bool{true, false}}),
		"another column's")
	assert.False(t, csvKeepCheck([]byte("ad;tutar\nelma;4,50\narmut;1,1\n"), saved, ';', ',', 0, 0, dec), "4,50 is not how 4.5 is written")
	assert.False(t, csvKeepCheck([]byte("ad;tutar\nelma;4,5\narmut;1,1000000000000001\n"), saved, ';', ',', 0, 0, dec),
		"seventeen digits are written as the shortest decimal of the same double")
	moved := &csvCommas{cols: []bool{false, true}, moved: map[float64]bool{4.5: true}}
	assert.False(t, csvKeepCheck([]byte("ad;tutar\nelma;4,5\narmut;1,1\n"), saved, ';', ',', 0, 0, moved),
		"a number the file holds with a point keeps it")
}

// ------------------------------------------------------------- as measured

// The file of the report, and what each editor saved for it:
// officesavetest.Docs94CommaSaved.
func TestKeepCSV_TheReportedFileAsDocs94SavedIt(t *testing.T) {
	for lang, c := range officesavetest.Docs94CommaSaved {
		t.Run(lang, func(t *testing.T) {
			docs94Check(t, officesavetest.Docs94CommaOriginal, c.Saved, c.Want)
		})
	}
}

// Column `value` of a file whose every row said `3,5` (two control rows
// `1250,75` and `0,25` untouched), each typed over with the text on its left,
// and a row added below with `2,75`. What each editor saved, and what the
// file gets (as saved, but for the comma).
var docs94CommaTyped = []struct {
	typed      string
	tr, en, de string
	// trWant, enWant, deWant: what the file gets, when not the saved text.
	trWant, enWant, deWant string
}{
	{typed: "4,5", tr: "4.5", en: "4,5", de: "4.5", trWant: "4,5", deWant: "4,5"},
	{typed: "-4,5", tr: "-4.5", en: "-4,5", de: "-4.5", trWant: "-4,5", deWant: "-4,5"},
	{typed: "1250,75", tr: "1250.75", en: "1250,75", de: "1250.75", trWant: "1250,75", deWant: "1250,75"},
	{typed: "0,25", tr: "0.25", en: "0,25", de: "0.25", trWant: "0,25", deWant: "0,25"},
	{typed: "1.250,75", tr: "1250.75", en: "1.250,75", de: "1250.75", trWant: "1250,75", deWant: "1250,75"},
	{typed: "0,1234567", tr: "0.1234567", en: "0,1234567", de: "0.1234567", trWant: "0,1234567", deWant: "0,1234567"},
	// The English editor reads the comma as a thousands separator.
	{typed: "12345678,123456", tr: "12345678.123455999", en: "12345678123456", de: "12345678.123455999", trWant: "12345678,123456", deWant: "12345678,123456"},
	// A point typed: a date in Turkish and German, the number in English.
	{typed: "4.5", tr: "4.May", en: "4.5", de: "04.Mai", enWant: "4,5"},
	{typed: "1,5E+20", tr: "1.50e+20", en: "1,5E+20", de: "1.50e+20"},
	{typed: "4,50", tr: "4.5", en: "4,50", de: "4.5", trWant: "4,5", deWant: "4,5"},
	{typed: "4,", tr: "4", en: "4,", de: "4"},
	{typed: ",5", tr: "0.5", en: ",5", de: "0.5", trWant: "0,5", deWant: "0,5"},
	{typed: "3,5", tr: "3.5", en: "3,5", de: "3.5", trWant: "3,5", deWant: "3,5"},
	{typed: "1 250,75", tr: "1 250,75", en: "1 250,75", de: "1 250,75"},
	{typed: "0,0001", tr: "0.0001", en: "0,0001", de: "0.0001", trWant: "0,0001", deWant: "0,0001"},
	{typed: "123456789012,5", tr: "123456789012.5", en: "123456789012,5", de: "123456789012.5", trWant: "123456789012,5", deWant: "123456789012,5"},
	{typed: "-0,5", tr: "-0.5", en: "-0,5", de: "-0.5", trWant: "-0,5", deWant: "-0,5"},
	{typed: "45", tr: "45", en: "45", de: "45"},
	{typed: "4.50", tr: "Nis.50", en: "4.5", de: "Apr.50", enWant: "4,5"},
	{typed: "-4.5", tr: "-4.5", en: "-4.5", de: "-4.5", trWant: "-4,5", enWant: "-4,5", deWant: "-4,5"},
	{typed: "1250.75", tr: "1250.75", en: "1250.75", de: "1250.75", trWant: "1250,75", enWant: "1250,75", deWant: "1250,75"},
	{typed: "0.1234567", tr: "0.1234567", en: "0.1234567", de: "0.1234567", trWant: "0,1234567", enWant: "0,1234567", deWant: "0,1234567"},
	{typed: "1,23456789012345", tr: "1.23456789012345", en: "123456789012345", de: "1.23456789012345", trWant: "1,23456789012345", deWant: "1,23456789012345"},
	{typed: "4,5%", tr: "4.50%", en: "4,5%", de: "4.50%"},
	{typed: "1/2", tr: "1.Şub", en: "2/jan", de: "01.Feb"},
}

// The second measurement (Turkish and English only): text typed with an
// apostrophe, formulas, exponents and the digits a double cannot hold.
var docs94CommaTyped2 = []struct {
	typed          string
	tr, en         string
	trWant, enWant string
}{
	// ⚠ Text typed as text: the save cannot tell it from the number.
	{typed: "'4.5", tr: "4.5", en: "4.5", trWant: "4,5", enWant: "4,5"},
	{typed: "'4,5", tr: "4,5", en: "4,5"},
	{typed: "=1/4", tr: "0.25", en: "0.25", trWant: "0,25", enWant: "0,25"},
	{typed: "=7/2", tr: "3.5", en: "3.5", trWant: "3,5", enWant: "3,5"},
	{typed: "-4.5", tr: "-4.5", en: "-4.5", trWant: "-4,5", enWant: "-4,5"},
	{typed: "4,5E+2", tr: "4.50e+02", en: "4,5E+2"},
	{typed: "0,000001", tr: "9.9999999999999995e-07", en: "0,000001"},
	{typed: "0,0000001", tr: "9.9999999999999995e-08", en: "0,0000001"},
	{typed: "99999999999999,9", tr: "99999999999999.906", en: "99999999999999,9", trWant: "99999999999999,9"},
	{typed: "1,1", tr: "1.1000000000000001", en: "1,1", trWant: "1,1"},
	{typed: "2,675", tr: "2.6749999999999998", en: "2675", trWant: "2,675"},
}

// docs94CommaFile builds a measured file (rows label;3,5;x and the control
// rows: c1 and c2 in the first measurement, c1 in the second) and, for one
// language, the save and what the file gets.
func docs94CommaFile(saved, want []string, controls int) (original, save, file string) {
	var o, s, f strings.Builder
	o.WriteString("label;value;other\r\n")
	s.WriteString("\xEF\xBB\xBFlabel,value,other\n")
	f.WriteString("label;value;other\r\n")
	for i := range saved {
		fmt.Fprintf(&o, "t%d;3,5;x\r\n", i+1)
		fmt.Fprintf(&s, "t%d,%s,x\n", i+1, csvQuoted(saved[i]))
		w := want[i]
		if w == "" {
			w = saved[i]
		}
		fmt.Fprintf(&f, "t%d;%s;x\r\n", i+1, w)
	}
	o.WriteString("c1;1250,75;y\r\n")
	s.WriteString("c1,\"1250,75\",y\n")
	f.WriteString("c1;1250,75;y\r\n")
	if controls > 1 {
		o.WriteString("c2;0,25;z\r\n")
		s.WriteString("c2,\"0,25\",z\n")
		f.WriteString("c2;0,25;z\r\n")
	}
	return o.String(), s.String(), f.String()
}

// csvQuoted writes v as the document server does: in quotes when it holds a
// comma.
func csvQuoted(v string) string {
	if strings.ContainsAny(v, ",\"") {
		return "\"" + strings.ReplaceAll(v, "\"", "\"\"") + "\""
	}
	return v
}

func TestKeepCSV_WhatEachEditorSavedForATypedDecimal(t *testing.T) {
	for _, lang := range []string{"tr", "en", "de"} {
		t.Run(lang, func(t *testing.T) {
			var saved, want []string
			for _, r := range docs94CommaTyped {
				sw := map[string][2]string{"tr": {r.tr, r.trWant}, "en": {r.en, r.enWant}, "de": {r.de, r.deWant}}[lang]
				saved, want = append(saved, sw[0]), append(want, sw[1])
			}
			original, save, file := docs94CommaFile(saved, want, 2)
			// The row added below: `new` and `2,75` typed, a number in
			// Turkish and German, text in English.
			if lang == "en" {
				save += "new,\"2,75\"\n"
			} else {
				save += "new,2.75\n"
			}
			file += "new;2,75\r\n"
			docs94Check(t, original, save, file)
		})
	}
	for _, lang := range []string{"tr", "en"} {
		t.Run(lang+", the second measurement", func(t *testing.T) {
			var saved, want []string
			for _, r := range docs94CommaTyped2 {
				if lang == "tr" {
					saved, want = append(saved, r.tr), append(want, r.trWant)
				} else {
					saved, want = append(saved, r.en), append(want, r.enWant)
				}
			}
			original, save, file := docs94CommaFile(saved, want, 1)
			docs94Check(t, original, save, file)
		})
	}
}

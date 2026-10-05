package onlyoffice

// KeepCSV against what a real Docs 9.4.0-129 saved, the editor in English,
// Turkish, German and French (the bytes and how they were taken:
// callback_csv_measured_test.go). Each test says, row by row, which cells
// get the file's text back and which are written as ONLYOFFICE saved them;
// every other cell is the file's.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeepCSV_TheFixtureOfTheReleaseGateAsDocs94SavedIt(t *testing.T) {
	for lang, c := range docs94FixtureSaved {
		t.Run(lang, func(t *testing.T) {
			original := []byte(docs94Fixture)
			d := SniffCSV(original)
			out, kept := KeepCSV(original, []byte(c.saved), d)
			assert.Equal(t, c.want, string(out), "kept: %+v", kept)
			assertKept(t, original, []byte(c.saved), out, d, kept)
		})
	}
}

// docs94Want is the file KeepCSV must write for a measurement whose rows are
// labelled in column label: a row whose label is in theirs is written with
// the saved values (in the file's dialect, none of them needs quotes); every
// other row is the file's own line. The first row is replaced by head when
// head is not empty (the cell that was edited).
func docs94Want(t *testing.T, original, saved string, label int, theirs []string, head string) string {
	t.Helper()
	savedRows := map[string][]string{}
	for _, row := range csvRows(bytes.TrimPrefix([]byte(saved), utf8BOM), ',') {
		if len(row) > label {
			savedRows[row[label]] = row
		}
	}
	take := map[string]bool{}
	for _, l := range theirs {
		require.Contains(t, savedRows, l, "a saved row labelled %s", l)
		take[l] = true
	}
	var want strings.Builder
	for i, line := range strings.SplitAfter(original, "\r\n") {
		if line == "" {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(line, "\r\n"), ";")
		switch {
		case i == 0 && head != "":
			want.WriteString(head + "\r\n")
		case len(cells) > label && take[cells[label]]:
			row := savedRows[cells[label]]
			for _, v := range row {
				require.NotContains(t, v, ";")
			}
			want.WriteString(strings.Join(row, ";") + "\r\n")
		default:
			want.WriteString(line)
		}
	}
	return want.String()
}

func docs94Check(t *testing.T, original, saved, want string) {
	t.Helper()
	d := SniffCSV([]byte(original))
	out, kept := KeepCSV([]byte(original), []byte(saved), d)
	require.True(t, kept.Applied, "kept: %+v", kept)
	assertKept(t, []byte(original), []byte(saved), out, d, kept)
	assert.Equal(t, want, string(out))
}

// Every cell of column A typed over (the harness's TYPED list: the text of
// each row typed in place of the file's), one control row untouched. The
// file keeps its text where what was saved is the value it held, written
// ONLYOFFICE's way (keeps); every other typed cell is written as saved.
var docs94TypedOriginal = "orig;note\r\n" +
	"15.03.2026;t1\r\n" +
	"1-2-2026;t2\r\n" +
	" 42;t3\r\n" +
	"+5;t4\r\n" +
	"true;t5\r\n" +
	"7;t6\r\n" +
	"01.02.2026;t7\r\n" +
	"a\tb;t8\r\n" +
	"42 ;t9\r\n" +
	"0x10;t10\r\n" +
	"1e3;t11\r\n" +
	"3.50;t12\r\n" +
	"1/2/2026;t13\r\n" +
	"13.01.2026;t14\r\n" +
	"1.2.2026;t15\r\n" +
	"15-03-2026;t16\r\n" +
	"01.02.2026;t17\r\n" +
	"007;control\r\n"

var docs94Typed = map[string]struct {
	saved string
	keeps []string
}{
	// ⚠ The English editor keeps a date it does not read as one, and a date
	// typed with dots, as typed (t1, t13 to t17): each of those is the
	// person's text, and the file gets it.
	"en": {
		saved: "\xEF\xBB\xBForig,note\n" +
			"15/3/2026,t1\n" +
			"1/2/2026,t2\n" +
			"42,t3\n" +
			"5,t4\n" +
			"1,t5\n" +
			"7,t6\n" +
			"2/1/2026,t7\n" +
			"ab,t8\n" +
			"42,t9\n" +
			"16,t10\n" +
			"1000.00000,t11\n" +
			"3.50,t12\n" +
			"01.02.2026,t13\n" +
			"13/1/2026,t14\n" +
			"01.02.2026,t15\n" +
			"15/03/2026,t16\n" +
			"1.2.2026,t17\n" +
			"7,control\n",
		keeps: []string{"t2", "t3", "t4", "t8", "t9", "t10", "control"},
	},
	// The Turkish editor reads what was typed as a date and writes it its
	// own way: where that is the date the cell held, nothing was changed.
	"tr": {
		saved: "\xEF\xBB\xBForig,note\n" +
			"15.03.2026,t1\n" +
			"1.02.2026,t2\n" +
			"42,t3\n" +
			"5,t4\n" +
			"1,t5\n" +
			"7,t6\n" +
			"2.01.2026,t7\n" +
			"ab,t8\n" +
			"42,t9\n" +
			"16,t10\n" +
			"1000.00000,t11\n" +
			"46145.00,t12\n" +
			"1.02.2026,t13\n" +
			"13.01.2026,t14\n" +
			"1.02.2026,t15\n" +
			"15.03.2026,t16\n" +
			"1.02.2026,t17\n" +
			"7,control\n",
		keeps: []string{"t2", "t3", "t4", "t8", "t9", "t10", "t13", "t15", "t16", "t17", "control"},
	},
	// German and French write dates in forms no rule reads (01.02.2026,
	// 01/02/2026): those come back as they saved them, a date the cell
	// already held included. Nothing typed is undone.
	"de": {
		saved: "\xEF\xBB\xBForig,note\n" +
			"15.03.2026,t1\n" +
			"01.02.2026,t2\n" +
			"42,t3\n" +
			"5,t4\n" +
			"TRUE,t5\n" +
			"7,t6\n" +
			"02.01.2026,t7\n" +
			"ab,t8\n" +
			"42,t9\n" +
			"16,t10\n" +
			"1000.00000,t11\n" +
			"46145.00,t12\n" +
			"01.02.2026,t13\n" +
			"13.01.2026,t14\n" +
			"01.02.2026,t15\n" +
			"15.03.2026,t16\n" +
			"01.02.2026,t17\n" +
			"7,control\n",
		keeps: []string{"t3", "t4", "t5", "t8", "t9", "t10", "t16", "control"},
	},
	"fr": {
		saved: "\xEF\xBB\xBForig,note\n" +
			"15/03/2026,t1\n" +
			"01/02/2026,t2\n" +
			"42,t3\n" +
			"5,t4\n" +
			"TRUE,t5\n" +
			"7,t6\n" +
			"02/01/2026,t7\n" +
			"ab,t8\n" +
			"42,t9\n" +
			"16,t10\n" +
			"1000.00000,t11\n" +
			"3.5,t12\n" +
			"01.02.2026,t13\n" +
			"13/01/2026,t14\n" +
			"01.02.2026,t15\n" +
			"15/03/2026,t16\n" +
			"1.2.2026,t17\n" +
			"7,control\n",
		keeps: []string{"t3", "t4", "t5", "t8", "t9", "t10", "control"},
	},
}

func TestKeepCSV_ACellTypedOverInDocs94IsWrittenAsSaved(t *testing.T) {
	for lang, c := range docs94Typed {
		t.Run(lang, func(t *testing.T) {
			keep := map[string]bool{}
			for _, k := range c.keeps {
				keep[k] = true
			}
			var theirs []string
			for _, row := range csvRows([]byte(docs94TypedOriginal), ';')[1:] {
				if !keep[row[1]] {
					theirs = append(theirs, row[1])
				}
			}
			docs94Check(t, docs94TypedOriginal, c.saved, docs94Want(t, docs94TypedOriginal, c.saved, 1, theirs, ""))
		})
	}
}

// The probe: one value a row, labelled in column A, the header cell edited.
// theirs: the rows ONLYOFFICE writes another way that no rule puts back.
var docs94ProbeOriginal = "label;value\r\n" +
	"p1;007\r\n" +
	"p2;000\r\n" +
	"p3;05320000001\r\n" +
	"p4;-007\r\n" +
	"p5;-0\r\n" +
	"p6;+5\r\n" +
	"p7;+905320000001\r\n" +
	"p8; 42\r\n" +
	"p9;42 \r\n" +
	"p10;  42\r\n" +
	"p11;1e3\r\n" +
	"p12;12E5\r\n" +
	"p13;10e-1\r\n" +
	"p14;1e-3\r\n" +
	"p15;15e-1\r\n" +
	"p16;0x10\r\n" +
	"p17;0x1F\r\n" +
	"p18;9007199254740993\r\n" +
	"p19;123456789012345678\r\n" +
	"p20;1234567890123456789\r\n" +
	"p21;12345678901234567890\r\n" +
	"p22;1234567890123456\r\n" +
	"p23;3.50\r\n" +
	"p24;03.50\r\n" +
	"p25;0.1234567\r\n" +
	"p26;+1.5\r\n" +
	"p27;-1.5\r\n" +
	"p28;01234567890123.25\r\n" +
	"p29;0.1\r\n" +
	"p30;.5\r\n" +
	"p31;5.\r\n" +
	"p32;1.5 \r\n" +
	"p33; 3.50\r\n" +
	"p34;1_000\r\n" +
	"p35;3,5\r\n" +
	"p36;1,000\r\n" +
	"p37;1.000\r\n" +
	"p38;1.234,56\r\n" +
	"p39;01.02.2026\r\n" +
	"p40;1.2.2026\r\n" +
	"p41;15.03.2026\r\n" +
	"p42;13.01.2026\r\n" +
	"p43;29.02.2024\r\n" +
	"p44;31.02.2026\r\n" +
	"p45;01.02.1850\r\n" +
	"p46;01.02.26\r\n" +
	"p47;01/02/2026\r\n" +
	"p48;1/2/2026\r\n" +
	"p49;15/3/2026\r\n" +
	"p50;1-2-2026\r\n" +
	"p51;01-02-2026\r\n" +
	"p52;15-03-2026\r\n" +
	"p53;2026-02-01\r\n" +
	"p54;2026-02-01T10:30:45Z\r\n" +
	"p55;10/12\r\n" +
	"p56;08:05\r\n" +
	"p57;00:13\r\n" +
	"p58;08:05:30\r\n" +
	"p59;50%\r\n" +
	"p60;12.34%\r\n" +
	"p61;true\r\n" +
	"p62;True\r\n" +
	"p63;TRUE\r\n" +
	"p64;false\r\n" +
	"p65;FALSE\r\n" +
	"p66;a\tb\r\n" +
	"p67;=1+1\r\n" +
	"p68; abc \r\n" +
	"crlf;\"a\r\n" +
	"b\"\r\n" +
	"lf;\"a\n" +
	"b\"\r\n"

// Common to every language: an exponent (`1e3` as `1000.00000`), `5.` as `5`,
// `1.5 ` as `1.50`, a year before 1900 (`01.02.1850` as a year 3750), a
// two-digit year, a date with a time, a day and a month alone, times
// (`00:13` as `0:12`), percents (`12.34%` as `12.3%`), a formula.
var docs94ProbeTheirs = []string{
	"p11", "p12", "p13", "p14", "p15", "p31", "p32", "p45", "p46", "p54", "p55",
	"p56", "p57", "p58", "p59", "p60", "p67",
}

var docs94Probe = map[string]struct {
	saved  string
	theirs []string
}{
	"en": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"p1,7\n" +
		"p2,0\n" +
		"p3,5320000001\n" +
		"p4,-7\n" +
		"p5,0\n" +
		"p6,5\n" +
		"p7,905320000001\n" +
		"p8,42\n" +
		"p9,42\n" +
		"p10,42\n" +
		"p11,1000.00000\n" +
		"p12,1200000.00000\n" +
		"p13,1.00000\n" +
		"p14,0.00100\n" +
		"p15,1.50000\n" +
		"p16,16\n" +
		"p17,31\n" +
		"p18,9007199254740992\n" +
		"p19,1.2345678901234568e+17\n" +
		"p20,1.2345678901234568e+18\n" +
		"p21,-9.2233720368547758e+18\n" +
		"p22,1234567890123456\n" +
		"p23,3.50\n" +
		"p24,3.50\n" +
		"p25,0.1234570\n" +
		"p26,1.5\n" +
		"p27,-1.5\n" +
		"p28,1234567890123.25\n" +
		"p29,0.1\n" +
		"p30,.0\n" +
		"p31,5\n" +
		"p32,1.50\n" +
		"p33,3.50\n" +
		"p34,1_000\n" +
		"p35,\"3,5\"\n" +
		"p36,\"1,000\"\n" +
		"p37,1.000\n" +
		"p38,\"1.234,56\"\n" +
		"p39,1/2/2026\n" +
		"p40,1/2/2026\n" +
		"p41,15.03.2026\n" +
		"p42,13.01.2026\n" +
		"p43,29.02.2024\n" +
		"p44,31.02.2026\n" +
		"p45,1/2/3750\n" +
		"p46,1/2/2026\n" +
		"p47,1/2/2026\n" +
		"p48,1/2/2026\n" +
		"p49,15/3/2026\n" +
		"p50,1/2/2026\n" +
		"p51,1/2/2026\n" +
		"p52,15-03-2026\n" +
		"p53,2/1/2026\n" +
		"p54,2/1/2026 10:30\n" +
		"p55,10/12/2000\n" +
		"p56,8:05\n" +
		"p57,0:12\n" +
		"p58,8:05\n" +
		"p59,50.0%\n" +
		"p60,12.3%\n" +
		"p61,TRUE\n" +
		"p62,True\n" +
		"p63,TRUE\n" +
		"p64,FALSE\n" +
		"p65,FALSE\n" +
		"p66,ab\n" +
		"p67,2\n" +
		"p68, abc \n" +
		"crlf,\"a\r\n" +
		"b\"\n" +
		"lf,\"a\n" +
		"b\"\n"},
	// `31.02.2026` rolled over to `3.03.2026`.
	"tr": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"p1,7\n" +
		"p2,0\n" +
		"p3,5320000001\n" +
		"p4,-7\n" +
		"p5,0\n" +
		"p6,5\n" +
		"p7,905320000001\n" +
		"p8,42\n" +
		"p9,42\n" +
		"p10,42\n" +
		"p11,1000.00000\n" +
		"p12,1200000.00000\n" +
		"p13,1.00000\n" +
		"p14,0.00100\n" +
		"p15,1.50000\n" +
		"p16,16\n" +
		"p17,31\n" +
		"p18,9007199254740992\n" +
		"p19,1.2345678901234568e+17\n" +
		"p20,1.2345678901234568e+18\n" +
		"p21,-9.2233720368547758e+18\n" +
		"p22,1234567890123456\n" +
		"p23,3.50\n" +
		"p24,3.50\n" +
		"p25,0.1234570\n" +
		"p26,1.5\n" +
		"p27,-1.5\n" +
		"p28,1234567890123.25\n" +
		"p29,0.1\n" +
		"p30,.0\n" +
		"p31,5\n" +
		"p32,1.50\n" +
		"p33,3.50\n" +
		"p34,1_000\n" +
		"p35,\"3,5\"\n" +
		"p36,\"1,000\"\n" +
		"p37,1.000\n" +
		"p38,\"1.234,56\"\n" +
		"p39,1.02.2026\n" +
		"p40,1.02.2026\n" +
		"p41,15.03.2026\n" +
		"p42,13.01.2026\n" +
		"p43,29.02.2024\n" +
		"p44,3.03.2026\n" +
		"p45,1.02.3750\n" +
		"p46,1.02.2026\n" +
		"p47,1.02.2026\n" +
		"p48,1.02.2026\n" +
		"p49,15.03.2026\n" +
		"p50,1.02.2026\n" +
		"p51,1.02.2026\n" +
		"p52,15.03.2026\n" +
		"p53,1.02.2026\n" +
		"p54,1.02.2026 10:30\n" +
		"p55,10.12.2000\n" +
		"p56,8:05\n" +
		"p57,0:12\n" +
		"p58,8:05\n" +
		"p59,50.0%\n" +
		"p60,12.3%\n" +
		"p61,TRUE\n" +
		"p62,True\n" +
		"p63,TRUE\n" +
		"p64,FALSE\n" +
		"p65,FALSE\n" +
		"p66,ab\n" +
		"p67,2\n" +
		"p68, abc \n" +
		"crlf,\"a\r\n" +
		"b\"\n" +
		"lf,\"a\n" +
		"b\"\n", theirs: []string{"p44"}},
	// Dates written 01.02.2026, the form no rule reads.
	"de": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"p1,7\n" +
		"p2,0\n" +
		"p3,5320000001\n" +
		"p4,-7\n" +
		"p5,0\n" +
		"p6,5\n" +
		"p7,905320000001\n" +
		"p8,42\n" +
		"p9,42\n" +
		"p10,42\n" +
		"p11,1000.00000\n" +
		"p12,1200000.00000\n" +
		"p13,1.00000\n" +
		"p14,0.00100\n" +
		"p15,1.50000\n" +
		"p16,16\n" +
		"p17,31\n" +
		"p18,9007199254740992\n" +
		"p19,1.2345678901234568e+17\n" +
		"p20,1.2345678901234568e+18\n" +
		"p21,-9.2233720368547758e+18\n" +
		"p22,1234567890123456\n" +
		"p23,3.50\n" +
		"p24,3.50\n" +
		"p25,0.1234570\n" +
		"p26,1.5\n" +
		"p27,-1.5\n" +
		"p28,1234567890123.25\n" +
		"p29,0.1\n" +
		"p30,.0\n" +
		"p31,5\n" +
		"p32,1.50\n" +
		"p33,3.50\n" +
		"p34,1_000\n" +
		"p35,\"3,5\"\n" +
		"p36,\"1,000\"\n" +
		"p37,1.000\n" +
		"p38,\"1.234,56\"\n" +
		"p39,01.02.2026\n" +
		"p40,01.02.2026\n" +
		"p41,15.03.2026\n" +
		"p42,13.01.2026\n" +
		"p43,29.02.2024\n" +
		"p44,03.03.2026\n" +
		"p45,01.02.3750\n" +
		"p46,01.02.2026\n" +
		"p47,01.02.2026\n" +
		"p48,01.02.2026\n" +
		"p49,15.03.2026\n" +
		"p50,01.02.2026\n" +
		"p51,01.02.2026\n" +
		"p52,15.03.2026\n" +
		"p53,01.02.2026\n" +
		"p54,01.02.2026 10:30\n" +
		"p55,10.12.2000\n" +
		"p56,8:05\n" +
		"p57,0:12\n" +
		"p58,8:05\n" +
		"p59,50.0%\n" +
		"p60,12.3%\n" +
		"p61,TRUE\n" +
		"p62,True\n" +
		"p63,TRUE\n" +
		"p64,FALSE\n" +
		"p65,FALSE\n" +
		"p66,ab\n" +
		"p67,2\n" +
		"p68, abc \n" +
		"crlf,\"a\r\n" +
		"b\"\n" +
		"lf,\"a\n" +
		"b\"\n", theirs: []string{"p40", "p44", "p47", "p48", "p50", "p51", "p53"}},
	// Dates written 01/02/2026.
	"fr": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"p1,7\n" +
		"p2,0\n" +
		"p3,5320000001\n" +
		"p4,-7\n" +
		"p5,0\n" +
		"p6,5\n" +
		"p7,905320000001\n" +
		"p8,42\n" +
		"p9,42\n" +
		"p10,42\n" +
		"p11,1000.00000\n" +
		"p12,1200000.00000\n" +
		"p13,1.00000\n" +
		"p14,0.00100\n" +
		"p15,1.50000\n" +
		"p16,16\n" +
		"p17,31\n" +
		"p18,9007199254740992\n" +
		"p19,1.2345678901234568e+17\n" +
		"p20,1.2345678901234568e+18\n" +
		"p21,-9.2233720368547758e+18\n" +
		"p22,1234567890123456\n" +
		"p23,3.50\n" +
		"p24,3.50\n" +
		"p25,0.1234570\n" +
		"p26,1.5\n" +
		"p27,-1.5\n" +
		"p28,1234567890123.25\n" +
		"p29,0.1\n" +
		"p30,.0\n" +
		"p31,5\n" +
		"p32,1.50\n" +
		"p33,3.50\n" +
		"p34,1_000\n" +
		"p35,\"3,5\"\n" +
		"p36,\"1,000\"\n" +
		"p37,1.000\n" +
		"p38,\"1.234,56\"\n" +
		"p39,01/02/2026\n" +
		"p40,01/02/2026\n" +
		"p41,15/03/2026\n" +
		"p42,13/01/2026\n" +
		"p43,29/02/2024\n" +
		"p44,03/03/2026\n" +
		"p45,01/02/3750\n" +
		"p46,01/02/2026\n" +
		"p47,01/02/2026\n" +
		"p48,01/02/2026\n" +
		"p49,15/03/2026\n" +
		"p50,01/02/2026\n" +
		"p51,01/02/2026\n" +
		"p52,15/03/2026\n" +
		"p53,01/02/2026\n" +
		"p54,01/02/2026 10:30\n" +
		"p55,10/12/2000\n" +
		"p56,8:05\n" +
		"p57,0:12\n" +
		"p58,8:05\n" +
		"p59,50.0%\n" +
		"p60,12.3%\n" +
		"p61,TRUE\n" +
		"p62,True\n" +
		"p63,TRUE\n" +
		"p64,FALSE\n" +
		"p65,FALSE\n" +
		"p66,ab\n" +
		"p67,2\n" +
		"p68, abc \n" +
		"crlf,\"a\r\n" +
		"b\"\n" +
		"lf,\"a\n" +
		"b\"\n", theirs: []string{"p39", "p40", "p41", "p42", "p43", "p44", "p48", "p49", "p50", "p51", "p52", "p53"}},
}

func TestKeepCSV_WhatDocs94WritesAnotherWayIsPutBack(t *testing.T) {
	for lang, c := range docs94Probe {
		t.Run(lang, func(t *testing.T) {
			theirs := append(append([]string{}, docs94ProbeTheirs...), c.theirs...)
			docs94Check(t, docs94ProbeOriginal, c.saved, docs94Want(t, docs94ProbeOriginal, c.saved, 0, theirs, "label2;value"))
		})
	}
}

// The second probe: ISO dates, whole numbers from 2^63 up (all written
// `-9.2233720368547758e+18`), a point with nothing before it (`.5` as `.0`),
// dates the two languages read in one order only, 40000 characters (cut to
// 32767).
var docs94Probe2Original = "label;value\r\n" +
	"q1;2026-02-01\r\n" +
	"q2;2026-2-1\r\n" +
	"q3;2026-12-31\r\n" +
	"q4;2024-02-29\r\n" +
	"q5;2026-02-30\r\n" +
	"q6;1850-02-01\r\n" +
	"q7;2026/02/01\r\n" +
	"q8;2026.02.01\r\n" +
	"q9;12345678901234567890\r\n" +
	"q10;99999999999999999999\r\n" +
	"q11;123456789012345678901234\r\n" +
	"q12;-12345678901234567890\r\n" +
	"q13;+12345678901234567890\r\n" +
	"q14;9223372036854775807\r\n" +
	"q15;9223372036854775808\r\n" +
	"q16;18446744073709551616\r\n" +
	"q17;000012345678901234567890\r\n" +
	"q18;.5\r\n" +
	"q19;.25\r\n" +
	"q20;-.5\r\n" +
	"q21;0.5\r\n" +
	"q22;5.\r\n" +
	"q23;1/13/2026\r\n" +
	"q24;13/1/2026\r\n" +
	"q25;1.13.2026\r\n" +
	"q26;13.1.2026\r\n" +
	"q27;02/29/2024\r\n" +
	"q28;29/02/2024\r\n" +
	"q29;12/31/2026\r\n" +
	"q30;31/12/2026\r\n" +
	"q31;1-13-2026\r\n" +
	"q32;13-1-2026\r\n" +
	"q33;1.02.2026\r\n" +
	"q34;10.11.2026\r\n" +
	"q35;11/10/2026\r\n" +
	"q36;" + strings.Repeat("x", 40000) + "\r\n" +
	"q37;" + strings.Repeat("ş", 40000) + "\r\n"

// Common to every language: `2026-02-30` rolled over, a year before 1900,
// `5.` as `5`.
var docs94Probe2Theirs = []string{"q5", "q6", "q22"}

var docs94Probe2 = map[string]struct {
	saved  string
	theirs []string
}{
	"en": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"q1,2/1/2026\n" +
		"q2,2026-2-1\n" +
		"q3,12/31/2026\n" +
		"q4,2/29/2024\n" +
		"q5,3/2/2026\n" +
		"q6,2/1/3750\n" +
		"q7,2026/02/01\n" +
		"q8,2026.02.01\n" +
		"q9,-9.2233720368547758e+18\n" +
		"q10,-9.2233720368547758e+18\n" +
		"q11,-9.2233720368547758e+18\n" +
		"q12,-9.2233720368547758e+18\n" +
		"q13,-9.2233720368547758e+18\n" +
		"q14,-9.2233720368547758e+18\n" +
		"q15,-9.2233720368547758e+18\n" +
		"q16,-9.2233720368547758e+18\n" +
		"q17,-9.2233720368547758e+18\n" +
		"q18,.0\n" +
		"q19,.00\n" +
		"q20,-0.5\n" +
		"q21,0.5\n" +
		"q22,5\n" +
		"q23,1/13/2026\n" +
		"q24,13/1/2026\n" +
		"q25,1/13/2026\n" +
		"q26,13.1.2026\n" +
		"q27,2/29/2024\n" +
		"q28,29/02/2024\n" +
		"q29,12/31/2026\n" +
		"q30,31/12/2026\n" +
		"q31,1/13/2026\n" +
		"q32,13-1-2026\n" +
		"q33,1/2/2026\n" +
		"q34,10/11/2026\n" +
		"q35,11/10/2026\n" +
		"q36," + strings.Repeat("x", 32767) + "\n" +
		"q37," + strings.Repeat("ş", 32767) + "\n"},
	"tr": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"q1,1.02.2026\n" +
		"q2,2026-2-1\n" +
		"q3,31.12.2026\n" +
		"q4,29.02.2024\n" +
		"q5,2.03.2026\n" +
		"q6,1.02.3750\n" +
		"q7,2026/02/01\n" +
		"q8,2026.02.01\n" +
		"q9,-9.2233720368547758e+18\n" +
		"q10,-9.2233720368547758e+18\n" +
		"q11,-9.2233720368547758e+18\n" +
		"q12,-9.2233720368547758e+18\n" +
		"q13,-9.2233720368547758e+18\n" +
		"q14,-9.2233720368547758e+18\n" +
		"q15,-9.2233720368547758e+18\n" +
		"q16,-9.2233720368547758e+18\n" +
		"q17,-9.2233720368547758e+18\n" +
		"q18,.0\n" +
		"q19,.00\n" +
		"q20,-0.5\n" +
		"q21,0.5\n" +
		"q22,5\n" +
		"q23,1/13/2026\n" +
		"q24,13.01.2026\n" +
		"q25,1.13.2026\n" +
		"q26,13.01.2026\n" +
		"q27,02/29/2024\n" +
		"q28,29.02.2024\n" +
		"q29,12/31/2026\n" +
		"q30,31.12.2026\n" +
		"q31,1-13-2026\n" +
		"q32,13.01.2026\n" +
		"q33,1.02.2026\n" +
		"q34,10.11.2026\n" +
		"q35,11.10.2026\n" +
		"q36," + strings.Repeat("x", 32767) + "\n" +
		"q37," + strings.Repeat("ş", 32767) + "\n"},
	"de": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"q1,01.02.2026\n" +
		"q2,2026-2-1\n" +
		"q3,31.12.2026\n" +
		"q4,29.02.2024\n" +
		"q5,02.03.2026\n" +
		"q6,01.02.3750\n" +
		"q7,2026/02/01\n" +
		"q8,2026.02.01\n" +
		"q9,-9.2233720368547758e+18\n" +
		"q10,-9.2233720368547758e+18\n" +
		"q11,-9.2233720368547758e+18\n" +
		"q12,-9.2233720368547758e+18\n" +
		"q13,-9.2233720368547758e+18\n" +
		"q14,-9.2233720368547758e+18\n" +
		"q15,-9.2233720368547758e+18\n" +
		"q16,-9.2233720368547758e+18\n" +
		"q17,-9.2233720368547758e+18\n" +
		"q18,.0\n" +
		"q19,.00\n" +
		"q20,-0.5\n" +
		"q21,0.5\n" +
		"q22,5\n" +
		"q23,1/13/2026\n" +
		"q24,13.01.2026\n" +
		"q25,1.13.2026\n" +
		"q26,13.01.2026\n" +
		"q27,02/29/2024\n" +
		"q28,29.02.2024\n" +
		"q29,12/31/2026\n" +
		"q30,31.12.2026\n" +
		"q31,1-13-2026\n" +
		"q32,13.01.2026\n" +
		"q33,01.02.2026\n" +
		"q34,10.11.2026\n" +
		"q35,11.10.2026\n" +
		"q36," + strings.Repeat("x", 32767) + "\n" +
		"q37," + strings.Repeat("ş", 32767) + "\n", theirs: []string{"q1", "q33"}},
	"fr": {saved: "\xEF\xBB\xBFlabel2,value\n" +
		"q1,01/02/2026\n" +
		"q2,2026-2-1\n" +
		"q3,31/12/2026\n" +
		"q4,29/02/2024\n" +
		"q5,02/03/2026\n" +
		"q6,01/02/3750\n" +
		"q7,2026/02/01\n" +
		"q8,2026.02.01\n" +
		"q9,-9.2233720368547758e+18\n" +
		"q10,-9.2233720368547758e+18\n" +
		"q11,-9.2233720368547758e+18\n" +
		"q12,-9.2233720368547758e+18\n" +
		"q13,-9.2233720368547758e+18\n" +
		"q14,-9.2233720368547758e+18\n" +
		"q15,-9.2233720368547758e+18\n" +
		"q16,-9.2233720368547758e+18\n" +
		"q17,-9.2233720368547758e+18\n" +
		"q18,.0\n" +
		"q19,.00\n" +
		"q20,-0.5\n" +
		"q21,0.5\n" +
		"q22,5\n" +
		"q23,1/13/2026\n" +
		"q24,13/01/2026\n" +
		"q25,1.13.2026\n" +
		"q26,13/01/2026\n" +
		"q27,02/29/2024\n" +
		"q28,29/02/2024\n" +
		"q29,12/31/2026\n" +
		"q30,31/12/2026\n" +
		"q31,1-13-2026\n" +
		"q32,13/01/2026\n" +
		"q33,01/02/2026\n" +
		"q34,10/11/2026\n" +
		"q35,11/10/2026\n" +
		"q36," + strings.Repeat("x", 32767) + "\n" +
		"q37," + strings.Repeat("ş", 32767) + "\n", theirs: []string{"q1", "q3", "q4", "q24", "q26", "q32", "q33"}},
}

func TestKeepCSV_WhatDocs94WritesAnotherWayIsPutBack2(t *testing.T) {
	for lang, c := range docs94Probe2 {
		t.Run(lang, func(t *testing.T) {
			theirs := append(append([]string{}, docs94Probe2Theirs...), c.theirs...)
			docs94Check(t, docs94Probe2Original, c.saved, docs94Want(t, docs94Probe2Original, c.saved, 0, theirs, "label2;value"))
		})
	}
}

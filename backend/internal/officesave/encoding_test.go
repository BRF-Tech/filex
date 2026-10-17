package officesave

// The encoding of a CSV that is not UTF-8 (encoding.go): which one a file is
// in is read from its bytes, and a save is written back in it.

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// utf16le is s as UTF-16LE bytes (characters of the Basic Multilingual Plane).
func utf16le(s string, bom bool) string {
	b := []byte{}
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return string(b)
}

func utf16be(s string, bom bool) string {
	b := []byte{}
	if bom {
		b = append(b, 0xFE, 0xFF)
	}
	for _, r := range s {
		b = append(b, byte(r>>8), byte(r))
	}
	return string(b)
}

// What each code page is judged from: a file of its language, in it.
func TestSniffCSV_TheCodePageIsReadFromTheLetters(t *testing.T) {
	cases := map[string]struct{ head, want string }{
		// ş ç İ ı ğ: Turkish's letters, ş/ı/ğ/İ read as þ/ý/ð/Ý in windows-1252.
		"Turkish": {"ad;\xFEehir\n\xE7ilek;\xDDstanbul\n\xFD\xF0d\xFDr;Mu\xFE\n", "windows-1254"},
		// ü ö é ß read alike in windows-1252, -1254 and -1250: the first.
		"German and French": {"name;city\nM\xFCller;K\xF6ln\nCaf\xE9;Stra\xDFe\n", "windows-1252"},
		// ł ż Ł are symbols in windows-1252 (³ ¿ £), letters in windows-1250.
		"Polish": {"imi\xEA;miasto\n\xA3ukasz;\xA3\xF3d\x9F\n\xBF\xF3\xB3w;Gda\xF1sk\n", "windows-1250"},
		// Words of letters none of which is ASCII: no Latin word reads so.
		"Russian": {"\xE8\xEC\xFF;\xE3\xEE\xF0\xEE\xE4\n\xC8\xE2\xE0\xED;\xCC\xEE\xF1\xEA\xE2\xE0\n", "windows-1251"},
		// όνομα;πόλη / Αθήνα;Θεσσαλονίκη / γλώσσα;Κρήτη: Cyrillic letters in
		// windows-1251 as well; the accented vowels are Greek's.
		"Greek": {"\xFC\xED\xEF\xEC\xE1;\xF0\xFC\xEB\xE7\n\xC1\xE8\xDE\xED\xE1;\xC8\xE5\xF3\xF3\xE1\xEB\xEF\xED\xDF\xEA\xE7\n\xE3\xEB\xFE\xF3\xF3\xE1;\xCA\xF1\xDE\xF4\xE7\n", "windows-1253"},
		// Panevėžys;Kėdainiai / Šiauliai / ąžuolas;žąsis: Western letters in
		// windows-1252 (ë þ Ð à) and Turkish ones in -1254 (ş Ğ), but no one
		// language of theirs writes them all.
		"Lithuanian": {"vardas;miestas\nPanev\xEB\xFEys;K\xEBdainiai\n\xD0iauliai;Vilnius\n\xE0\xFEuolas;\xFE\xE0sis\n", "windows-1257"},
		// vārds;pilsēta / Rīga;Jūrmala / ķēķis;ļoti / ņemt;ģimene.
		"Latvian": {"v\xE2rds;pils\xE7ta\nR\xEEga;J\xFBrmala\n\xED\xE7\xEDis;\xEFoti\n\xF2emt;\xECimene\n", "windows-1257"},
		// What the two new pages must not take: ë is ė in windows-1257 (Dutch
		// België), ð þ are š ž there (Icelandic), and a Turkish line with one
		// foreign name reads as Icelandic letters in windows-1252.
		"Dutch":                       {"land;stad\nBelgi\xEB;Gent\nidee\xEBn;x\n", "windows-1252"},
		"Icelandic":                   {"nafn;borg\n\xDE\xF3r\xF0ur;Reykjav\xEDk\nGu\xF0r\xFAn;Akureyri\n", "windows-1252"},
		"Turkish with a foreign name": {"ad;not\n\xFE\xFD Ren\xE9;x\n", "windows-1254"},
	}
	for name, c := range cases {
		d := SniffCSV([]byte(c.head))
		assert.False(t, d.UTF8, name)
		assert.Equal(t, c.want, d.Encoding, name)
		assert.Equal(t, byte(';'), d.Comma, name)
	}
}

func TestSniffCSV_UTF16(t *testing.T) {
	d := SniffCSV([]byte(utf16le("ad;not\r\nelma;şeker\r\n", true)))
	assert.Equal(t, CSVDialect{Comma: ';', BOM: true, CRLF: true, Encoding: EncUTF16LE}, d)
	d = SniffCSV([]byte(utf16be("ad\tnot\nelma\tşeker\n", true)))
	assert.Equal(t, CSVDialect{Comma: '\t', BOM: true, Encoding: EncUTF16BE}, d)
	// No mark: every other byte of its ASCII is zero.
	d = SniffCSV([]byte(utf16le("ad,not\nelma,7\n", false)))
	assert.Equal(t, CSVDialect{Comma: ',', Encoding: EncUTF16LE}, d)
}

func TestEncodingLabel(t *testing.T) {
	for in, want := range map[string]string{
		"utf-8": EncUTF8, "UTF8": EncUTF8, "utf-16": EncUTF16LE, "UTF-16BE": EncUTF16BE,
		"windows-1254": "windows-1254", "cp1254": "windows-1254", "ISO-8859-9": "windows-1254",
		"latin1": "windows-1252", "windows-1251": "windows-1251",
		"windows-1253": "windows-1253", "cp1253": "windows-1253", "x-cp1253": "windows-1253",
		"windows-1257": "windows-1257", "CP1257": "windows-1257", "x-cp1257": "windows-1257",
	} {
		got, ok := EncodingLabel(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"shift_jis", "ebcdic", ""} {
		_, ok := EncodingLabel(in)
		assert.False(t, ok, in)
	}
}

func TestCodePagesReadAndWriteTheirBytes(t *testing.T) {
	p := pageOf("windows-1254")
	require.NotNil(t, p)
	text := decodePage([]byte("\xFEeker;\xFD\xF0d\xFDr"), p)
	assert.Equal(t, "şeker;ığdır", string(text))
	back, _, ok := encodePage(text, p)
	assert.True(t, ok)
	assert.Equal(t, "\xFEeker;\xFD\xF0d\xFDr", string(back))
	_, bad, ok := encodePage([]byte("a€b日"), p)
	assert.False(t, ok)
	assert.Equal(t, '日', bad, "€ is in windows-1254 (0x80), 日 is not")
	_, bad, ok = encodePage([]byte("ok\xFF"), p)
	assert.False(t, ok, "text that is not UTF-8 is no text to encode")
	assert.Equal(t, utf8.RuneError, bad)

	for label, c := range map[string]struct{ bytes, text string }{
		"windows-1253": {"\xD0\xDC\xF4\xF1\xE1", "Πάτρα"},
		"windows-1257": {"\xD0iauli\xF8;R\xEEga", "Šiaulių;Rīga"},
	} {
		p := pageOf(label)
		require.NotNil(t, p, label)
		assert.Equal(t, c.text, string(decodePage([]byte(c.bytes), p)), label)
		back, _, ok := encodePage([]byte(c.text), p)
		assert.True(t, ok, label)
		assert.Equal(t, c.bytes, string(back), label)
	}
	_, bad, ok = encodePage([]byte("ş"), pageOf("windows-1257"))
	assert.False(t, ok)
	assert.Equal(t, 'ş', bad, "a Turkish letter has no place in the Baltic page")
}

func TestAlikePages(t *testing.T) {
	western := []byte("M\xFCller;K\xF6ln\n")
	var labels []string
	for _, p := range alikePages(western, pageOf("windows-1252")) {
		labels = append(labels, p.label)
	}
	assert.Equal(t, []string{"windows-1252", "windows-1254", "windows-1250", "windows-1257"}, labels,
		"ü and ö are the same bytes in all four: a save may be written in any of them")
	labels = nil
	for _, p := range alikePages([]byte("\xFEeker\n"), pageOf("windows-1254")) {
		labels = append(labels, p.label)
	}
	assert.Equal(t, []string{"windows-1254"}, labels, "0xFE is ş in one of them only")
}

func TestUTF16RoundTrip(t *testing.T) {
	s := "ad;not\r\nçilek;𝄞\r\n"
	le := []byte(utf16le(s, false))
	// utf16le above writes one unit a rune: the clef is two units, written here.
	le = append(le[:len(le)-6], 0x34, 0xD8, 0x1E, 0xDD, '\r', 0, '\n', 0)
	text := decodeUTF16(le, false)
	assert.Equal(t, s, string(text))
	assert.Equal(t, le, encodeUTF16(text, false))
}

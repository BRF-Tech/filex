package officesave

// The encoding of a CSV that is not UTF-8.
//
// An editor hands its save back as text it chose the encoding of: UTF-8 in
// practice (the Document Server always, the Office editor app as its
// interface hands it over). A file in another encoding is written back in
// its own (SaveCSV): its untouched bytes stay what they were, and a
// character it cannot hold is a save refused, never one written as `?`.
//
// Which encoding a file is in is read from its bytes:
//
//   - UTF-16, by its byte order mark, or, without one, by the zero bytes of
//     its ASCII (every other byte of a CSV's digits and delimiters);
//   - UTF-8, when the bytes are;
//   - otherwise a single-byte code page, the one of codePages its letters
//     read best in (detectPage). A code page cannot be known from bytes
//     alone, only judged: the judge is how its high bytes read as letters
//     inside words, and how well they read as the letters of one language
//     written in that page (pageFit). A wrong judgement between two pages
//     that read every byte of the file alike changes nothing (alikePages:
//     the save may be written in either); between two that do not, the
//     person sees other letters and a save with a letter the page cannot
//     hold is refused.
//
// The names are the WHATWG Encoding Standard's labels, the names a browser's
// TextDecoder takes: an app's interface decodes the file with the name it is
// handed (`text_dialect.encoding`).

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Encoding names.
const (
	EncUTF8    = "utf-8"
	EncUTF16LE = "utf-16le"
	EncUTF16BE = "utf-16be"
)

// codePage is a single-byte code page filex recognises in a CSV.
type codePage struct {
	label string
	cm    *charmap.Charmap
	// latin: its letters are Latin script (a Latin word holds few letters
	// outside ASCII).
	latin bool
	// own are letters frequent in its languages that another page reads as
	// other letters: they count for it.
	own string
	// alphabets are the letters beyond ASCII of the languages written in the
	// page, lower case, one string a language (pageFit). A page of one script
	// that is not Latin is one alphabet: its languages share their bytes.
	alphabets []string
}

// codePages are the pages a CSV that is not UTF-8 is judged for, in the
// order a tie is broken.
var codePages = []codePage{
	{label: "windows-1252", cm: charmap.Windows1252, latin: true, alphabets: []string{
		"àâæçéèêëîïôœùûüÿ", // French
		"äöüß",             // German
		"áéíñóúü",          // Spanish, Galician, Basque
		"àáâãçéêíóôõú",     // Portuguese
		"àèéìíîòóùú",       // Italian
		"àçéèíïòóúü",       // Catalan
		"áéèëïóöüú",        // Dutch, Afrikaans
		"æøåéóòô",          // Danish, Norwegian
		"åäöé",             // Swedish
		"åäöšž",            // Finnish
		"áðéíóúýþæö",       // Icelandic, Faroese
		"áéíóú",            // Irish
		"çë",               // Albanian
		"äõöüšž",           // Estonian
	}},
	// ğ ı ş İ Ğ Ş are ð ý þ Ý Ð Þ in windows-1252: letters there too, so
	// only their being Turkish's tells the two apart.
	{label: "windows-1254", cm: charmap.Windows1254, latin: true, own: "ğışĞİŞ", alphabets: []string{
		"çğıöşüâîû", // Turkish
	}},
	{label: "windows-1250", cm: charmap.Windows1250, latin: true, alphabets: []string{
		"ąćęłńóśźż",         // Polish
		"áčďéěíňóřšťúůýž",   // Czech
		"áäčďéíĺľňóôŕšťúýž", // Slovak
		"áéíóöőúüű",         // Hungarian
		"čćđšž",             // Slovenian, Croatian, Bosnian, Serbian in Latin
		"ăâîşţ",             // Romanian
		"äöüß",              // German, as Central Europe writes it
	}},
	{label: "windows-1251", cm: charmap.Windows1251, alphabets: []string{
		"абвгдеёжзийклмнопрстуфхцчшщъыьэюяђѓєѕіїјљњћќўџґ", // Russian, Ukrainian, Belarusian, Bulgarian, Serbian, Macedonian
	}},
	// Greek and Cyrillic read each other's bytes as letters: Greek's accented
	// vowels (one in nearly every word) are its own, and a Russian я or Т is
	// a byte windows-1253 leaves undefined.
	{label: "windows-1253", cm: charmap.Windows1253, own: "άέήίόύώ", alphabets: []string{
		"αβγδεζηθικλμνξοπρσςτυφχψωάέήίόύώϊϋΐΰ", // Greek
	}},
	// Every Baltic letter is a Western letter in windows-1252 (ą à, č è, ė ë,
	// ų ø, š ð, ž þ) and š ž are ğ ş in windows-1254: the languages' whole
	// alphabets tell them apart (pageFit), and š ž are its own as much as
	// windows-1254's, so a long file is not won by how often they come.
	{label: "windows-1257", cm: charmap.Windows1257, latin: true, own: "šžŠŽ", alphabets: []string{
		"ąčęėįšųūž",   // Lithuanian
		"āčēģīķļņšūž", // Latvian
		"äõöüšž",      // Estonian
	}},
}

// pageNeutral are letters that belong to no one language: an ordinal
// indicator (1ª, 2º) and the micro sign.
const pageNeutral = "ªºµ"

// pageJudgedBytes bounds what of a file its code page is judged on.
const pageJudgedBytes = 1 << 20

// pageOf is the code page by its label, nil for UTF-8, UTF-16 and a label
// filex does not know.
func pageOf(label string) *codePage {
	for i := range codePages {
		if codePages[i].label == label {
			return &codePages[i]
		}
	}
	return nil
}

// encodingAliases are the other names a producer may declare an encoding by.
var encodingAliases = map[string]string{
	"utf8": EncUTF8, "unicode-1-1-utf-8": EncUTF8,
	"utf-16": EncUTF16LE, "ucs-2": EncUTF16LE, "unicode": EncUTF16LE,
	"cp1250": "windows-1250", "x-cp1250": "windows-1250",
	"cp1251": "windows-1251", "x-cp1251": "windows-1251",
	"cp1252": "windows-1252", "x-cp1252": "windows-1252", "iso-8859-1": "windows-1252", "latin1": "windows-1252", "us-ascii": "windows-1252", "ascii": "windows-1252",
	"cp1254": "windows-1254", "x-cp1254": "windows-1254", "iso-8859-9": "windows-1254", "latin5": "windows-1254",
	"cp1253": "windows-1253", "x-cp1253": "windows-1253",
	"cp1257": "windows-1257", "x-cp1257": "windows-1257",
}

// EncodingLabel is a declared encoding by the name filex uses for it, and
// whether filex knows it.
func EncodingLabel(name string) (string, bool) {
	l := strings.ToLower(strings.TrimSpace(name))
	if a, ok := encodingAliases[l]; ok {
		l = a
	}
	switch {
	case l == EncUTF8, l == EncUTF16LE, l == EncUTF16BE:
		return l, true
	case pageOf(l) != nil:
		return l, true
	}
	return "", false
}

// utf16Of reads b's start as UTF-16: its byte order (EncUTF16LE or
// EncUTF16BE, "" when it is not) and whether it starts with a byte order
// mark. Without one it is UTF-16 when the zero bytes say so: CSV text is
// mostly ASCII, whose every other byte is zero in UTF-16 and none in any
// other encoding.
func utf16Of(b []byte) (enc string, bom bool) {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return EncUTF16LE, true
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return EncUTF16BE, true
	}
	n := min(len(b), 4096) &^ 1
	if n < 4 {
		return "", false
	}
	even, odd := 0, 0
	for i := 0; i < n; i += 2 {
		if b[i] == 0 {
			even++
		}
		if b[i+1] == 0 {
			odd++
		}
	}
	pairs := n / 2
	switch {
	case 10*odd >= 3*pairs && 20*even <= pairs:
		return EncUTF16LE, false
	case 10*even >= 3*pairs && 20*odd <= pairs:
		return EncUTF16BE, false
	}
	return "", false
}

// decodeUTF16 is UTF-16 bytes (an even number of them, no byte order mark) as
// UTF-8. An unpaired surrogate reads as U+FFFD.
func decodeUTF16(b []byte, bigEndian bool) []byte {
	units := make([]uint16, len(b)/2)
	for i := range units {
		if bigEndian {
			units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			units[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	out := make([]byte, 0, len(b))
	for _, r := range utf16.Decode(units) {
		out = utf8.AppendRune(out, r)
	}
	return out
}

// encodeUTF16 is UTF-8 text as UTF-16 bytes, no byte order mark. Text that is
// not UTF-8 holds U+FFFD where it is not (never asked: the text was checked).
func encodeUTF16(text []byte, bigEndian bool) []byte {
	out := make([]byte, 0, 2*len(text))
	for _, u := range utf16.Encode([]rune(string(text))) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

// decodePage is a code page's bytes as UTF-8. A byte the page leaves undefined
// reads as U+FFFD.
func decodePage(b []byte, p *codePage) []byte {
	out := make([]byte, 0, len(b)+len(b)/4)
	for _, c := range b {
		if c < utf8.RuneSelf {
			out = append(out, c)
			continue
		}
		out = utf8.AppendRune(out, p.cm.DecodeByte(c))
	}
	return out
}

// encodePage is UTF-8 text in a code page, or the first character the page
// cannot hold (ok false).
func encodePage(text []byte, p *codePage) (out []byte, bad rune, ok bool) {
	out = make([]byte, 0, len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRune(text[i:])
		i += size
		if r < utf8.RuneSelf {
			out = append(out, byte(r))
			continue
		}
		c, held := p.cm.EncodeRune(r)
		if !held || r == utf8.RuneError {
			return nil, r, false
		}
		out = append(out, c)
	}
	return out, 0, true
}

// detectPage is the code page b's letters read best in (pageJudgement), its
// label; the first of codePages on a tie.
func detectPage(b []byte) string {
	b = b[:min(len(b), pageJudgedBytes)]
	var counts [256]int
	for _, c := range b {
		counts[c]++
	}
	best, bestScore := 0, 0
	for i := range codePages {
		if s := pageJudgement(b, &counts, &codePages[i]); i == 0 || s > bestScore {
			best, bestScore = i, s
		}
	}
	return codePages[best].label
}

// pageJudgement weighs how b reads in page p: its letters inside words
// (pageScore) and as the letters of one of its languages (pageFit), the
// latter three quarters as much. Measured on the languages of the six pages:
// at the full weight a Turkish line with one foreign name ("şı René") read as
// Icelandic in windows-1252; at half, a long Icelandic file read as Baltic.
func pageJudgement(b []byte, counts *[256]int, p *codePage) int {
	return 4*pageScore(b, p) + 3*pageFit(counts, p)
}

// pageFit is how well the letters of a file (counts: how often each byte
// comes) read as the alphabet of ONE language of page p: every time a letter
// of it comes, +1, every time another letter does, -1; the best language's
// sum. A file of several languages fits none well, and loses nothing it did
// not lose in every page.
func pageFit(counts *[256]int, p *codePage) int {
	best := 0
	for i, alphabet := range p.alphabets {
		fit := 0
		for c := utf8.RuneSelf; c < 256; c++ {
			n := counts[c]
			if n == 0 {
				continue
			}
			r := unicode.ToLower(p.cm.DecodeByte(byte(c)))
			if !unicode.IsLetter(r) || r < utf8.RuneSelf || strings.ContainsRune(pageNeutral, r) {
				continue
			}
			if strings.ContainsRune(alphabet, r) {
				fit += n
			} else {
				fit -= n
			}
		}
		if i == 0 || fit > best {
			best = fit
		}
	}
	return best
}

// alikePages are the code pages that read every byte of b as page p does:
// for b, any of them is as much its encoding as p (p first).
func alikePages(b []byte, p *codePage) []*codePage {
	var seen [256]bool
	for _, c := range b {
		seen[c] = true
	}
	out := []*codePage{p}
	for i := range codePages {
		q := &codePages[i]
		if q == p {
			continue
		}
		same := true
		for c := utf8.RuneSelf; c < 256 && same; c++ {
			if seen[c] && q.cm.DecodeByte(byte(c)) != p.cm.DecodeByte(byte(c)) {
				same = false
			}
		}
		if same {
			out = append(out, q)
		}
	}
	return out
}

func asciiLetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }

// pageScore judges how well b reads in page p: every high byte (0x80 and up)
// is a character of p, and the characters of a real text in p are letters
// inside words, in the script of the words around them.
//
//   - a letter: +1, and +1 more when it is one of p.own; -2 instead when p is
//     not Latin and an ASCII letter stands beside it (a Cyrillic letter in
//     the middle of a Latin word is a Latin letter of another page);
//   - in a Latin page, a word with no ASCII letter and three or more others:
//     -2 for each of them (Latin words are mostly ASCII; that is a word of
//     another script read in the wrong page);
//   - a symbol between two letters: -2 (a letter of another page);
//   - a byte p leaves undefined, or a control character: -5.
func pageScore(b []byte, p *codePage) int {
	b = b[:min(len(b), pageJudgedBytes)]
	score := 0
	letter := func(i int) bool {
		if i < 0 || i >= len(b) {
			return false
		}
		c := b[i]
		if c < utf8.RuneSelf {
			return asciiLetter(c)
		}
		return unicode.IsLetter(p.cm.DecodeByte(c))
	}
	ascii := func(i int) bool { return i >= 0 && i < len(b) && asciiLetter(b[i]) }
	wordHigh, wordASCII := 0, 0
	endWord := func() {
		if p.latin && wordASCII == 0 && wordHigh >= 3 {
			score -= 2 * wordHigh
		}
		wordHigh, wordASCII = 0, 0
	}
	for i, c := range b {
		if c < utf8.RuneSelf {
			if asciiLetter(c) {
				wordASCII++
			} else {
				endWord()
			}
			continue
		}
		r := p.cm.DecodeByte(c)
		switch {
		case r == utf8.RuneError || unicode.IsControl(r):
			score -= 5
			endWord()
		case unicode.IsLetter(r):
			if !p.latin && (ascii(i-1) || ascii(i+1)) {
				score -= 2
			} else {
				score++
			}
			if strings.ContainsRune(p.own, r) {
				score++
			}
			wordHigh++
		default:
			if letter(i-1) && letter(i+1) {
				score -= 2
			}
			endWord()
		}
	}
	endWord()
	return score
}

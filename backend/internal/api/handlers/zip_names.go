package handlers

import (
	"archive/zip"
	"context"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// zipNames reads the name of a ZIP member the way the person who made the
// archive wrote it (task #238).
//
// ⚠ Why. A member with the UTF-8 flag (general purpose bit 11) is UTF-8, and
// so is - in practice - a name without the flag whose bytes are valid UTF-8
// (macOS's Archive Utility writes those): both are read as they are. A name
// WITHOUT the flag whose bytes are not UTF-8 is in a DOS code page - the ZIP
// specification says IBM 437, and Windows' own "Send to > Compressed folder"
// writes the OEM code page of the machine: 857 on a Turkish Windows, 850 in
// most of Western Europe, 852 in Central Europe, 866 for Cyrillic, 862 for
// Hebrew. Read as UTF-8 such a name became U+FFFD replacement characters:
// listed wrongly, never matched by a selective extraction, and on a full
// extraction written as an invalid name the next request could not open.
//
// Which code page: the one Windows uses for the language of the account that
// asks (or of the instance, for nobody's request) - that is who most likely
// zipped it - and IBM 437, the specification's, for any other language.
// There is no way to tell the code page from the bytes; this is a choice,
// written down in docs/FILE-NAMES.md.
type zipNames struct{ high *[128]rune }

// zipNamesFor is the decoder for ctx's account language.
func zipNamesFor(ctx context.Context) zipNames {
	lang := ""
	if u := auth.UserFrom(ctx); u != nil {
		lang = u.Locale
	}
	if lang == "" {
		lang = srvtext.Pick()
	}
	return zipNames{high: oemTableFor(lang)}
}

// name is f's name, decoded when it is in a code page.
func (z zipNames) name(f *zip.File) string {
	if f.Flags&0x800 != 0 || utf8.ValidString(f.Name) || z.high == nil {
		return f.Name
	}
	return z.decode(f.Name)
}

func (z zipNames) decode(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) * 2)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 0x80 {
			b.WriteByte(c)
			continue
		}
		b.WriteRune(z.high[c-0x80])
	}
	return b.String()
}

// oemTableFor is the upper half (0x80-0xFF) of the DOS code page Windows uses
// for lang's primary subtag.
func oemTableFor(lang string) *[128]rune {
	primary := strings.ToLower(lang)
	if i := strings.IndexAny(primary, "-_"); i >= 0 {
		primary = primary[:i]
	}
	switch primary {
	case "tr", "az":
		return &cp857High
	case "de", "fr", "es", "it", "pt", "nl", "ca", "da", "sv", "nb", "nn", "no", "fi", "is", "ga", "eu", "gl", "af":
		return highOf(charmap.CodePage850)
	case "cs", "pl", "hu", "sk", "sl", "hr", "ro", "bs", "sq":
		return highOf(charmap.CodePage852)
	case "ru", "uk", "be", "bg", "mk", "kk":
		return highOf(charmap.CodePage866)
	case "he":
		return highOf(charmap.CodePage862)
	}
	return highOf(charmap.CodePage437)
}

func highOf(cm *charmap.Charmap) *[128]rune {
	var t [128]rune
	for i := range t {
		t[i] = cm.DecodeByte(byte(0x80 + i))
	}
	return &t
}

// cp857High is IBM 857 (DOS Turkish), which golang.org/x/text does not
// carry: IBM 850 with the Turkish letters in their places. Written as the
// differences from 850, each a code point, so a reviewer can check them
// against the IBM table one by one. 0xE7 and 0xF2 are unassigned (U+FFFD);
// 0xD5 is the euro sign Windows' table gives it.
var cp857High = func() [128]rune {
	t := *highOf(charmap.CodePage850)
	for b, r := range map[byte]rune{
		0x8D: 0x0131, // dotless i
		0x98: 0x0130, // capital I with dot above
		0x9E: 0x015E, // capital S with cedilla
		0x9F: 0x015F, // small s with cedilla
		0xA6: 0x011E, // capital G with breve
		0xA7: 0x011F, // small g with breve
		0xD0: 0x00BA, // masculine ordinal indicator
		0xD1: 0x00AA, // feminine ordinal indicator
		0xD5: 0x20AC, // euro sign
		0xE7: 0xFFFD, // unassigned
		0xE8: 0x00D7, // multiplication sign
		0xEC: 0x00EC, // small i with grave
		0xED: 0x00FF, // small y with diaeresis
		0xF2: 0xFFFD, // unassigned
	} {
		t[b-0x80] = r
	}
	return t
}()

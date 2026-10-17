// Package localname is the one rule for what a name becomes on the disk of
// the computer a file is written to: whether that operating system can hold
// it at all (Problem), what it is called there when it cannot (Safe, Alias,
// Numbered), and when two different names are ONE file there (Key).
//
// filex keeps the name it is given (internal/leafname says which names a door
// accepts), so a server holds names a person's own computer cannot:
// `report.` (Windows drops the trailing dot), `CON` and `aux.txt` (device
// names), `a:b.txt` (a forbidden character), a 300-byte Chinese name (ext4
// and APFS hold 255 bytes), and `Rapor.docx` beside `rapor.docx` (NTFS and
// APFS compare names without case, APFS also without the normalisation form).
// Every program that writes server names onto a local disk asks this package:
// the sync engine (internal/filesync, which writes such an item under its
// Alias and remembers the pairing), `filex decrypt` (internal/e2edecrypt,
// which renames and warns). The desktop
// app's drag-out applies the same rules in TypeScript (desktop/src/local-name.ts,
// tested against the same hostile names).
//
// The operating system is a parameter, never runtime.GOOS read here, so the
// rules of all three are tested on the one the suite runs on.
//
// Leaf package: it imports nothing from filex.
package localname

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Why a name cannot exist on a computer, as a stable code.
const (
	// Reserved: a Windows device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9),
	// with or without an extension - `aux.txt` included, which Windows 10
	// still opens as the device.
	Reserved = "reserved"
	// Forbidden: a character Windows refuses in a name - < > : " | ? * \ /
	// or a control character.
	Forbidden = "forbidden"
	// Trailing: the name ends in a dot or a space, which Windows removes, so
	// the file it writes has another name.
	Trailing = "trailing"
	// TooLong: over MaxLen - bytes of UTF-8 on Linux and macOS, UTF-16
	// units on Windows.
	TooLong = "too_long"
)

// MaxLen is the longest one name may be: 255 bytes of UTF-8 on ext4, XFS and
// APFS, 255 UTF-16 units on NTFS.
const MaxLen = 255

// Problem is why name cannot be a file or folder name on goos ("windows",
// "darwin", "linux", ...), or "" when it can.
func Problem(name, goos string) string {
	if tooLong(name, goos) {
		return TooLong
	}
	if goos == "windows" {
		switch {
		case strings.IndexFunc(name, forbiddenOnWindows) >= 0:
			return Forbidden
		case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
			return Trailing
		case reservedOnWindows(name):
			return Reserved
		}
	}
	return ""
}

// tooLong: over MaxLen in goos's unit.
func tooLong(name, goos string) bool {
	if goos == "windows" {
		return len(utf16.Encode([]rune(name))) > MaxLen
	}
	return len(name) > MaxLen
}

// Fit shortens a name to MaxLen for goos, taking whole characters off the end
// of its stem and keeping its extension (when the extension is a short one).
// A name that fits is returned as it is.
func Fit(name, goos string) string {
	if !tooLong(name, goos) {
		return name
	}
	ext := path.Ext(name)
	if ext == name || len(ext) > 32 {
		ext = ""
	}
	return fitStem(strings.TrimSuffix(name, ext), ext, goos)
}

// fitStem is stem+tail with the stem shortened until it fits.
func fitStem(stem, tail, goos string) string {
	for stem != "" && tooLong(stem+tail, goos) {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return stem + tail
}

// Alias is the name a copy of a server item is written under on goos when
// its own name cannot be (Problem != ""): Safe, shortened to fit (Fit), and
// made safe again - a cut can leave a trailing space. A name goos holds comes
// back as it is.
func Alias(name, goos string) string {
	return Safe(Fit(Safe(name, goos), goos), goos)
}

// Numbered is name with " (n)" before its extension, shortened to fit:
// the next candidate when an Alias is already one name here with another
// item's.
func Numbered(name string, n int, goos string) string {
	ext := path.Ext(name)
	if ext == name || len(ext) > 32 {
		ext = ""
	}
	return fitStem(strings.TrimSuffix(name, ext), fmt.Sprintf(" (%d)%s", n, ext), goos)
}

// Safe adapts a name to goos's rules: on Windows every forbidden character and
// every trailing dot or space becomes `_`, and a device name gets a `_` in
// front; elsewhere the name is returned as it is. It does not shorten a name
// (Problem says TooLong).
func Safe(name, goos string) string {
	if goos != "windows" {
		return name
	}
	b := []rune(name)
	for i, r := range b {
		if forbiddenOnWindows(r) {
			b[i] = '_'
		}
	}
	for i := len(b) - 1; i >= 0 && (b[i] == '.' || b[i] == ' '); i-- {
		b[i] = '_'
	}
	s := string(b)
	if reservedOnWindows(s) {
		s = "_" + s
	}
	return s
}

// Folds reports whether goos's usual file system calls two different names
// one: case-insensitive NTFS, case- and normalisation-insensitive APFS.
func Folds(goos string) bool { return goos == "windows" || goos == "darwin" }

// Key is the name as goos's file system compares it: two names with one Key
// are one file there. NTFS ignores case and keeps both normalisation forms
// apart; APFS ignores both; everything else compares the bytes.
func Key(name, goos string) string {
	switch goos {
	case "windows":
		return strings.ToLower(name)
	case "darwin":
		return Fold(name)
	}
	return name
}

// Fold compares names the way the strictest target does (case-insensitive and
// Unicode-normalised): a key for output that may land on any computer.
func Fold(name string) string {
	return strings.ToLower(norm.NFC.String(name))
}

// Image is the name goos actually writes when a program asks for name and
// does not check: Windows drops trailing dots and spaces (`report.` is
// written as `report`) and reads a backslash as a separator (`a\b.txt` is a
// folder `a`). ok is false when the name is written as it is, or when nothing
// recognisable is written at all.
func Image(name, goos string) (image string, ok bool) {
	if goos != "windows" {
		return "", false
	}
	if i := strings.IndexByte(name, '\\'); i > 0 {
		return name[:i], true
	}
	if t := strings.TrimRight(name, ". "); t != "" && t != name {
		return t, true
	}
	return "", false
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func forbiddenOnWindows(r rune) bool {
	return r < 0x20 || strings.ContainsRune(`<>:"|?*\/`, r)
}

// reservedOnWindows: the part before the first dot, trailing spaces dropped,
// is a device name.
func reservedOnWindows(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	return windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))]
}

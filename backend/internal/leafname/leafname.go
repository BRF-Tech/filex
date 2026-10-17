// Package leafname is the one rule for the names filex lets a file or a
// folder be GIVEN: what every door that creates an entry or renames one asks
// of the new name - the explorer and the API (upload, new folder, new file,
// rename, the file request, the agent API), WebDAV, S3, SFTP, FTPS and NFS.
//
// ⚠⚠ Why one package. Until 0.57 each door had its own idea (task #238): the
// explorer's upload refused a backslash and `..`, its new-folder box refused
// only a slash, WebDAV, SFTP, FTPS and NFS refused nothing at all. A name one
// door lets through and another refuses is a file that exists on one surface
// and cannot be opened on the next: `...` uploaded fine and then answered
// 400 "bad path" to every preview, download, rename and delete (the read
// side's guard, handlers.pathHasDotDot, refuses it, and must: Windows trims
// trailing dots, so on a Windows host `...` IS the folder it sits in).
//
// What is refused, and nothing else:
//
//   - the empty name, and a name of nothing but spaces: Windows drops
//     trailing spaces, so it is the empty name there (and the new-folder and
//     rename boxes trim it to nothing everywhere);
//   - a separator, `/` or `\`: a name is one path segment, and to a Windows
//     host, a Windows client and an unzip on Windows a backslash separates
//     (the desktop app would write a folder called `back` for `back\slash`);
//   - NUL: no file system stores it, and every C string ends there;
//   - any other control character (U+0001-U+001F, U+007F): a tab, a line
//     break, an escape. Windows refuses them, an FTP listing and an XML
//     listing (S3, WebDAV) cannot carry them, and a person cannot see them
//     (the owner's decision for 0.57, task #238). A file that already has
//     one - written to the disk by something else - stays readable, can be
//     overwritten and deleted: only a NEW name is asked;
//   - any name made of nothing but dots and spaces with at least one dot
//     (`.`, `..`, `...`, `. .`, ` . . . `): the folder itself, its parent,
//     or - once Windows has trimmed the trailing dots and spaces - the
//     folder it is in (the owner's decision for 0.57, task #238: every name
//     that falls to the folder itself on Windows; until then only those
//     holding `..` were refused).
//
// Everything else is a name, however hostile it looks: Unicode in any script
// and either normalisation form, emoji, spaces at either end, URL and shell
// metacharacters, Windows device names and forbidden characters, bidi
// controls. The surfaces that cannot carry one of those
// (Windows, an FTP listing) deal with it where they are; docs/FILE-NAMES.md
// says how. A name too long for the storage is the storage's answer, given
// back to the client as "name too long" (storage.IsNameTooLong), not a rule
// here: an object store holds names a local disk refuses.
//
// The stored name is never changed: a name passes or is refused. Leaf package:
// it imports nothing from filex, so every handler and protocol server can use
// it.
package leafname

import (
	"errors"
	"strings"
	"unicode"
)

// ErrRefused is what every refusal wraps; errors.Is(err, ErrRefused) is how a
// door recognises one and answers it as the client's mistake (400, 553,
// NFS3ERR_INVAL), never as a server fault.
var ErrRefused = errors.New("this name is not allowed")

// Why a name is refused, as a stable code a client and a test can read.
const (
	Empty     = "empty"
	Separator = "separator"
	NUL       = "nul"
	Control   = "control"
	Dots      = "dots"
)

// Error is a refusal: the name and why.
type Error struct {
	Name   string
	Reason string
}

func (e *Error) Error() string {
	switch e.Reason {
	case Empty:
		return "a name cannot be empty"
	case Separator:
		return "a name cannot contain / or \\"
	case NUL:
		return "a name cannot contain a NUL character"
	case Control:
		return "a name cannot contain a control character (a tab, a line break)"
	default:
		return "a name cannot be . or .. or only dots and spaces"
	}
}

func (e *Error) Unwrap() error { return ErrRefused }

// Check reports whether name may be given to a file or a folder; nil when it
// may, an *Error (wrapping ErrRefused) when not.
func Check(name string) error {
	switch {
	case name == "" || OnlySpaces(name):
		return &Error{Name: name, Reason: Empty}
	case strings.ContainsAny(name, `/\`):
		return &Error{Name: name, Reason: Separator}
	case strings.IndexByte(name, 0) >= 0:
		return &Error{Name: name, Reason: NUL}
	case strings.ContainsFunc(name, IsControl):
		return &Error{Name: name, Reason: Control}
	case OnlyDots(name):
		return &Error{Name: name, Reason: Dots}
	}
	return nil
}

// OnlyDots reports a name of nothing but dots and spaces with at least one
// dot - `.`, `..`, `...`, `. .`, ` . . . ` - the names that are, or that
// Windows turns into, the folder itself or its parent. The rule for a name
// that is GIVEN (Check); the web's twin is core lib/nameRule onlyDots.
func OnlyDots(name string) bool {
	return strings.ContainsRune(name, '.') && strings.Trim(name, ". ") == ""
}

// OnlySpaces reports a name of one or more spaces and nothing else.
func OnlySpaces(name string) bool {
	return name != "" && strings.Trim(name, " ") == ""
}

// DotSegment reports a path segment that reads as the folder itself or its
// parent: `.`, `..`, or dots and spaces holding `..`. ⚠ The READ side's rule
// (a key that only reads or deletes, s3api), not the one for a new name: a
// file already on the disk named `. .` stays readable and deletable there
// (only a NEW name is held to OnlyDots, the owner's rule for 0.57).
func DotSegment(seg string) bool {
	if seg == "." || seg == ".." {
		return true
	}
	return strings.Contains(seg, "..") && strings.Trim(seg, ". ") == ""
}

// CheckPath is Check on every segment of a slash-separated relative path, for
// a door where one request makes the folders on the way too (an S3 key, a
// path a client hands over whole). An empty segment - a leading, trailing or
// doubled slash - is refused like an empty name: `a//b` and `a/b` must not be
// two spellings of one file.
func CheckPath(rel string) error {
	for _, seg := range strings.Split(rel, "/") {
		if err := Check(seg); err != nil {
			return err
		}
	}
	return nil
}

// IsControl reports a C0 control character or DEL (U+0001-U+001F, U+007F;
// NUL is refused under its own reason before this is asked).
func IsControl(r rune) bool { return (r >= 0x01 && r < 0x20) || r == 0x7F }

// SavedRune is what one rune of a name becomes in a copy saved OUTSIDE filex -
// a download's file name (httpx.ContentDisposition), a ZIP member's name
// (zipstream): itself, or `_` for a control character (C0, DEL, C1) or a
// bidi control. The stored name is never changed; the copy on someone's disk
// must not carry a RIGHT-TO-LEFT OVERRIDE that shows `invoice` + U+202E +
// `fdp.exe` as `invoiceexe.pdf`, nor a control character no file manager
// shows (the owner's decision for 0.57, task #238).
func SavedRune(r rune) rune {
	if unicode.IsControl(r) || IsBidiControl(r) {
		return '_'
	}
	return r
}

// IsBidiControl reports a bidirectional control character (Unicode's
// Bidi_Control: U+061C, U+200E, U+200F, U+202A-U+202E, U+2066-U+2069).
func IsBidiControl(r rune) bool { return unicode.Is(unicode.Bidi_Control, r) }

// Visible is name as a page, a mail or a notification SHOWS it: every bidi
// control replaced by U+FFFD REPLACEMENT CHARACTER, everything else as it is.
//
// ⚠⚠ Why. `invoice` + U+202E RIGHT-TO-LEFT OVERRIDE + `fdp.exe` is an .exe
// that any text renderer draws as `invoiceexe.pdf`, and isolating the name
// (FSI ... PDI, <bdi>) does not help: the override acts inside the isolate.
// A name may hold one - it is a legal name - but no surface may let it hide
// the true extension (task #238). The marker shows that something invisible
// is there; the stored name is never changed, and a search, a link or a
// download uses the real one. The explorer does the same on the client
// (packages/core/src/lib/nameRule.ts).
func Visible(name string) string {
	if !strings.ContainsFunc(name, IsBidiControl) {
		return name
	}
	return strings.Map(func(r rune) rune {
		if IsBidiControl(r) {
			return unicode.ReplacementChar
		}
		return r
	}, name)
}

// Reason is the refusal's code when err is one (errors.As), else "".
func Reason(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

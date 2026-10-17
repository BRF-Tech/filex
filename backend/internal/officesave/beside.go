package officesave

// An office save that is not written over its file, and the names it takes
// (docs/ONLYOFFICE.md → Old formats are saved beside them; → When the file
// changed while it was edited).
//
// The rules are an office save's, whichever editor made it: which formats an
// edit saved in another format than the file's is kept in (beside the file,
// never over it: an edited .doc comes back as DOCX), what that copy is
// called, and what a save of a session the file moved on from is called. The
// Document Server's callback writes both today (internal/onlyoffice
// callback_format.go, session_base.go); the desktop app finds them by the
// same rules (desktop/src/openwith.ts BESIDE_FORMATS, conflictPathFor).

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"time"
)

// BesideFormats are the formats a save may come back in that are kept beside
// the file: the OOXML and ODF documents an office editor writes. The desktop
// app's list is held to this one (web/tests/lib/serverRuleVectors.test.ts,
// TestRuleVectors_BesideFormats).
var BesideFormats = map[string]bool{
	"docx": true, "xlsx": true, "pptx": true,
	"docm": true, "xlsm": true, "pptm": true,
	"odt": true, "ods": true, "odp": true,
}

// BesideBytesOK reports whether head - the first bytes of a save in one of
// BesideFormats - starts as that format does: every one of them is a zip
// package (OOXML, ODF). Anything else is never written beside a document,
// whichever editor sent it.
func BesideBytesOK(head []byte) bool { return bytes.HasPrefix(head, []byte("PK\x03\x04")) }

// DocExt is a document's type as its name says it: the extension, lower
// case, no dot.
func DocExt(name string) string {
	return strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
}

// FormatName is a type as a person reads it: "DOCX".
func FormatName(ext string) string { return strings.ToUpper(ext) }

// IsPackage reports whether b starts like a spreadsheet package and not like
// text: a zip (XLSX, ODS) or an OLE compound file (XLS). Such bytes are never
// written under a .csv name.
func IsPackage(b []byte) bool {
	return bytes.HasPrefix(b, []byte("PK\x03\x04")) ||
		bytes.HasPrefix(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

// BesideName is where an edit saved as ext goes: the file's own name with
// that extension, in its folder (`rapor.doc` → `rapor.docx`). When that name
// is taken the caller numbers it (`rapor (2).docx`, ops.UniqueDestNumbered).
func BesideName(filePath, ext string) string {
	name := path.Base(filePath)
	base := strings.TrimSuffix(name, path.Ext(name))
	return path.Join(path.Dir(filePath), base+"."+ext)
}

// ConflictName is `<stem>.filex-conflict-<UTC time>.<ext>`: what a save of a
// session the file moved on from is called, beside the file - the desktop
// app's conflict copy name too (desktop/src/openwith.ts conflictPathFor).
func ConflictName(name string, now time.Time) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	return stem + ".filex-conflict-" + now.UTC().Format("20060102T150405") + ext
}

// conflictNameRe is ConflictName's mark: `.filex-conflict-` and the UTC time,
// then the extension, a ` (2)` the numbering added, or nothing.
var conflictNameRe = regexp.MustCompile(`\.filex-conflict-\d{8}T\d{6}( \(\d+\))?(\.[^.]*)?$`)

// IsConflictName reports whether name is a conflict copy's (ConflictName,
// numbered or not).
func IsConflictName(name string) bool { return conflictNameRe.MatchString(name) }

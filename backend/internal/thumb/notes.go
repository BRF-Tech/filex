package thumb

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Why a file has no thumbnail, said on the file (filex 0.50, docs/thumbnails.md
// → Why a file has no thumbnail). A listing carries `thumb_note` on a file
// whose thumbnail will not come because of the FILE - it is damaged, it is
// encrypted or protected by a password, it is too large - and the explorer
// draws a small marker with a sentence on the file's type icon, in the list,
// the grid and the gallery alike (packages/core ThumbTile).
//
// ⚠ Only reasons that are the file's, and that hold until the file changes.
// A failure that may pass (the network, a busy or misconfigured document
// server: oo_retry) gets no marker: it is asked again. Nor does a missing
// program (no_tool) or an administrator's choice (no_handler): nothing is
// wrong with the file, and the Thumbnail repair tab says what is.

// The notes a listing may carry.
const (
	NoteCorrupt   = "corrupt"
	NoteEncrypted = "encrypted"
	NoteTooLarge  = "too_large"
)

// noteTable is the ONE table of which reason gets which note: the reason's
// code (the part before its first ":"), or a prefix of the reason (prefix).
var noteTable = []struct {
	code   string
	prefix bool
	note   string
}{
	// damaged: the document server could not read it (-3, -7, -9).
	{code: SkipOfficeCorrupt, note: NoteCorrupt},
	// encrypted: a password on an office document (-5), a zip with
	// encrypted entries, and filex's own end-to-end encryption (a file of an
	// encrypted folder, an .fxe, or its content).
	{code: SkipOfficePassword, note: NoteEncrypted},
	{code: SkipArchiveEncrypted, note: NoteEncrypted},
	{code: "e2e-encrypted", prefix: true, note: NoteEncrypted},
	// too large: over a size limit - filex's for an office document, the
	// document server's own (-10), an SVG's, an archive's, an app's.
	{code: SkipOfficeTooLarge, note: NoteTooLarge},
	{code: SkipSVGTooLarge, note: NoteTooLarge},
	{code: SkipArchiveTooLarge, note: NoteTooLarge},
	{code: SkipAppTooLarge, note: NoteTooLarge},
}

// NoteOf is the note a file's thumbnail row gives it: "" when it has a
// picture, is being drawn, or has none for a reason that is not the file's.
func NoteOf(t *model.Thumbnail) string {
	if t == nil || (t.State != "failed" && t.State != "skipped") {
		return ""
	}
	return NoteForReason(t.Error)
}

// NoteForReason is the note of one recorded reason (noteTable).
func NoteForReason(reason string) string {
	code, _, _ := strings.Cut(reason, ":")
	for _, row := range noteTable {
		if code == row.code || (row.prefix && strings.HasPrefix(reason, row.code)) {
			return row.note
		}
	}
	return ""
}

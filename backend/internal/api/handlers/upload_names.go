package handlers

import (
	"mime/multipart"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/leafname"
)

// keptPath is a path or a name a client sent, kept exactly as sent unless it
// is nothing but whitespace (then ""): ` notes.txt` and `report.txt ` are
// names of their own, and trimming them read, or saved over, the sibling
// without the space (task #238).
func keptPath(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// uploadNameField is the optional form field that carries each file's name
// beside its `file[]` part, in the same order.
//
// ⚠ Why a second field. A browser does not write a multipart filename as it
// is: the WHATWG form-data rules turn `"` into %22 and a line break into %0A
// or %0D, so `say "hi".txt` arrived as `say %22hi%22.txt` and was stored
// under that name (task #238). Decoding the filename back is not possible -
// a file really named `100%22.txt` would turn into `100".txt` - so the
// explorer and the file-request page send the name again as a plain form
// value, which a browser writes byte for byte. A client that does not send
// it (curl, an older explorer, ShareX) is read as before, from the filename.
const uploadNameField = "name[]"

// uploadNames is the name of every file of a multipart upload, in order, each
// through the one name rule (checkUploadName): `name[]` when the form carries
// exactly one per file, else each part's filename. The first refused name is
// returned as the error (a *leafname.Error), and nothing is written by a
// caller that asks this before its first write - a batch is refused whole,
// never half-written. exists (nil: never) answers whether a file of that name
// is already where it would land (keepsExistingName).
func uploadNames(form *multipart.Form, files []*multipart.FileHeader, exists func(name string) bool) ([]string, error) {
	var given []string
	if form != nil {
		given = form.Value[uploadNameField]
	}
	useGiven := len(given) == len(files)
	out := make([]string, len(files))
	for i, fh := range files {
		raw := fh.Filename
		if useGiven {
			raw = given[i]
		}
		name, err := checkUploadName(raw)
		if err != nil {
			base := path.Base(raw)
			if !keepsExistingName(err, func() bool { return exists != nil && exists(base) }) {
				return nil, err
			}
			name = base
		}
		out[i] = name
	}
	return out, nil
}

// keepsExistingName reports a refusal that does not hold because the file is
// already there: a name refused only for a control character (a tab, a line
// break - leafname.Control) belongs to a file that exists, so the write is to
// THAT file. The owner's rule for 0.57 (task #238): a file that already has
// such a name stays readable, writable, renamable away and deletable; only a
// NEW name is refused. Every other refusal (a separator, `..`, NUL) holds
// whatever is on the disk: those name a different place, not a file.
func keepsExistingName(err error, exists func() bool) bool {
	return leafname.Reason(err) == leafname.Control && exists()
}

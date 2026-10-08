// Package nodefilter is the server's rule for "which of these rows does a
// search's narrowing keep": the kind of file, its mime type, when it was
// modified, how big it is, which folder it is in, who owns it and whether it
// is a hidden (dot) name.
//
// ⚠ Why it exists (filex 0.54, audit D6). The explorer's advanced search used
// to send the text and the tags to the server and narrow the hits that came
// back in the browser: "files over 100 MB modified this week" was answered from
// the first 250 name hits, and an MCP agent or the CLI could not ask it at all.
// The narrowing now travels with the query (Parse reads the parameters every
// search entrance takes) and the search applies Accept to each candidate BEFORE
// the limit counts it, so `limit` counts rows that satisfy the whole question.
//
// Kind is also the rule the explorer's Type chip and the advanced dialog's type
// choice name: the listing rows carry it as `kind` (KindOf), so the browser has
// no second extension table to drift from this one.
package nodefilter

import (
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The kinds, as the wire spells them (`kind` on a row, `type=` on a search).
const (
	KindFolder       = "folder"
	KindDocument     = "document"
	KindSpreadsheet  = "spreadsheet"
	KindPresentation = "presentation"
	KindPDF          = "pdf"
	KindImage        = "image"
	KindVideo        = "video"
	KindAudio        = "audio"
	KindArchive      = "archive"
	KindCode         = "code"
	KindText         = "text"
	KindOther        = "other"
)

var extKinds = map[string]string{}

func reg(kind string, exts ...string) {
	for _, e := range exts {
		extKinds[e] = kind
	}
}

func init() {
	reg(KindImage, "jpg", "jpeg", "png", "webp", "gif", "bmp", "avif", "heic", "heif", "svg", "ico", "tiff", "tif")
	reg(KindVideo, "mp4", "webm", "mov", "mkv", "avi", "ogv", "m4v")
	reg(KindAudio, "mp3", "wav", "flac", "ogg", "m4a", "aac", "opus")
	reg(KindPDF, "pdf")
	reg(KindDocument, "doc", "docx", "docm", "odt", "rtf")
	reg(KindSpreadsheet, "xls", "xlsx", "xlsm", "xlsb", "ods", "csv", "tsv")
	reg(KindPresentation, "ppt", "pptx", "pptm", "ppsx", "odp")
	reg(KindArchive, "zip", "tar", "gz", "bz2", "7z", "rar", "xz", "zst",
		"jar", "war", "ear", "aar", "apk", "aab", "apks", "xapk", "ipa", "whl", "egg", "vsix",
		"nupkg", "snupkg", "xpi", "crx", "appx", "appxbundle", "msix", "msixbundle", "deb", "rpm",
		"snap", "gem", "crate")
	reg(KindCode, "js", "ts", "jsx", "tsx", "mjs", "cjs", "vue", "py", "go", "rs", "php", "rb",
		"java", "kt", "swift", "c", "cpp", "h", "hpp", "cs", "css", "scss", "less",
		"html", "htm", "json", "yml", "yaml", "xml", "sh", "bash", "ps1", "sql", "toml")
	reg(KindText, "txt", "md", "markdown", "log", "ini", "conf", "cfg", "env")
}

// mimeKinds answers a file whose extension says nothing (a `LICENSE`, an
// `IMG_0042` with no suffix) from the mime type the server sniffed.
var mimeKinds = []struct{ prefix, kind string }{
	{"image/", KindImage},
	{"video/", KindVideo},
	{"audio/", KindAudio},
	{"application/pdf", KindPDF},
	{"application/msword", KindDocument},
	{"application/vnd.openxmlformats-officedocument.wordprocessing", KindDocument},
	{"text/csv", KindSpreadsheet},
	{"application/vnd.ms-excel", KindSpreadsheet},
	{"application/vnd.openxmlformats-officedocument.spreadsheet", KindSpreadsheet},
	{"application/vnd.ms-powerpoint", KindPresentation},
	{"application/vnd.openxmlformats-officedocument.presentation", KindPresentation},
	{"application/zip", KindArchive},
	{"application/x-tar", KindArchive},
	{"application/gzip", KindArchive},
	{"application/x-7z", KindArchive},
	{"text/", KindText},
}

// KindOf is what a row is: KindFolder for a directory, else the kind its
// extension names, else the kind its mime type names, else KindOther.
func KindOf(name, mime string, dir bool) string {
	if dir {
		return KindFolder
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if k, ok := extKinds[ext]; ok && ext != "" {
		return k
	}
	m := strings.ToLower(strings.TrimSpace(mime))
	if m != "" {
		for _, mk := range mimeKinds {
			if strings.HasPrefix(m, mk.prefix) {
				return mk.kind
			}
		}
	}
	return KindOther
}

// kindAccepts is the type choice's rule: "document" takes plain text too (the
// explorer's Documents chip always did), "file" is any file, "dir" a folder.
func kindAccepts(want, kind string) bool {
	switch want {
	case "", "any":
		return true
	case "file":
		return kind != KindFolder
	case "dir":
		return kind == KindFolder
	case KindDocument:
		return kind == KindDocument || kind == KindText
	}
	return want == kind
}

// validKind reports whether want is a type= the rule knows.
func validKind(want string) bool {
	switch want {
	case "", "any", "file", "dir", KindFolder, KindDocument, KindSpreadsheet, KindPresentation, KindPDF,
		KindImage, KindVideo, KindAudio, KindArchive, KindCode, KindText, KindOther:
		return true
	}
	return false
}

// Criteria is one search's narrowing. The zero value narrows nothing.
type Criteria struct {
	// Type is a kind (KindOf's words), "file" or "dir".
	Type string
	// Mime keeps rows whose mime type starts with it ("image/",
	// "application/pdf").
	Mime string
	// ModifiedAfter / ModifiedBefore bound the modification time, inclusive.
	ModifiedAfter, ModifiedBefore *time.Time
	// MinSize / MaxSize bound the size in bytes, inclusive. A folder never
	// satisfies a size bound: its size is not a measurement of anything a
	// person asked about.
	MinSize, MaxSize *int64
	// Under keeps rows inside this storage-relative folder (the folder row
	// itself counts as inside it); NotUnder keeps rows outside it. A value
	// given adapter-qualified (`docs://Reports`) also names the storage
	// (UnderStorage / NotUnderStorage), which AcceptIn compares with the
	// row's storage.
	Under, NotUnder               string
	UnderStorage, NotUnderStorage string
	// Owner is "me", "system" (nobody put it here through filex) or an
	// account id.
	Owner string
	// Hidden: nil keeps every name; false drops dot names (a name starting
	// with "."), true keeps them. The explorer sends its own "show hidden
	// files" choice.
	Hidden *bool
}

// ErrBad is wrapped by every refusal Parse returns.
var ErrBad = errors.New("bad filter")

// Active reports whether c narrows anything.
func (c Criteria) Active() bool {
	return (c.Type != "" && c.Type != "any") || c.Mime != "" || c.ModifiedAfter != nil || c.ModifiedBefore != nil ||
		c.MinSize != nil || c.MaxSize != nil || c.Under != "" || c.NotUnder != "" ||
		c.UnderStorage != "" || c.NotUnderStorage != "" || c.Owner != "" ||
		(c.Hidden != nil && !*c.Hidden)
}

// Values is where Parse reads from: url.Values, or the JSON body's fields
// turned into the same names (FromMap).
type Values interface {
	Get(key string) string
}

// FromMap reads a decoded JSON body's fields as Values: strings as they are,
// numbers and booleans in their usual text.
func FromMap(m map[string]any) Values {
	v := url.Values{}
	for k, x := range m {
		switch t := x.(type) {
		case string:
			v.Set(k, t)
		case float64:
			v.Set(k, strconv.FormatFloat(t, 'f', -1, 64))
		case bool:
			v.Set(k, strconv.FormatBool(t))
		}
	}
	return v
}

// Parse reads the narrowing parameters: type, mime, modified_after,
// modified_before (RFC 3339, a YYYY-MM-DD date, or milliseconds since the
// epoch), min_size, max_size (bytes), under, not_under (storage-relative
// folders), owner (me | system | an account id) and hidden (true | false).
// A value it cannot read is refused rather than ignored: a filter that is
// quietly dropped answers a different question than the one that was asked.
func Parse(v Values) (Criteria, error) {
	var c Criteria
	c.Type = strings.ToLower(strings.TrimSpace(v.Get("type")))
	if c.Type == KindFolder {
		c.Type = "dir"
	}
	if !validKind(c.Type) {
		return c, badf("type")
	}
	c.Mime = strings.ToLower(strings.TrimSpace(v.Get("mime")))
	var err error
	if c.ModifiedAfter, err = parseTime(v.Get("modified_after")); err != nil {
		return c, badf("modified_after")
	}
	if c.ModifiedBefore, err = parseTime(v.Get("modified_before")); err != nil {
		return c, badf("modified_before")
	}
	if c.MinSize, err = parseSize(v.Get("min_size")); err != nil {
		return c, badf("min_size")
	}
	if c.MaxSize, err = parseSize(v.Get("max_size")); err != nil {
		return c, badf("max_size")
	}
	c.UnderStorage, c.Under = splitFolder(v.Get("under"))
	c.NotUnderStorage, c.NotUnder = splitFolder(v.Get("not_under"))
	c.Owner = strings.ToLower(strings.TrimSpace(v.Get("owner")))
	if c.Owner != "" && c.Owner != "me" && c.Owner != "system" {
		if id, perr := strconv.ParseInt(c.Owner, 10, 64); perr != nil || id <= 0 {
			return c, badf("owner")
		}
	}
	switch strings.ToLower(strings.TrimSpace(v.Get("hidden"))) {
	case "":
	case "1", "true", "yes":
		t := true
		c.Hidden = &t
	case "0", "false", "no":
		f := false
		c.Hidden = &f
	default:
		return c, badf("hidden")
	}
	return c, nil
}

func badf(field string) error { return &badField{field} }

type badField struct{ field string }

func (e *badField) Error() string { return "bad filter: " + e.field }
func (e *badField) Unwrap() error { return ErrBad }

// Field names the parameter a refusal is about.
func Field(err error) string {
	var b *badField
	if errors.As(err, &b) {
		return b.field
	}
	return ""
}

func parseTime(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		t := time.UnixMilli(ms).UTC()
		return &t, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t, nil
		}
	}
	return nil, ErrBad
}

func parseSize(s string) (*int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return nil, ErrBad
	}
	return &n, nil
}

// splitFolder reads a folder parameter: the storage named by an adapter
// prefix (`docs://Reports` -> "docs"; "" when there is none) and the
// storage-relative folder with no leading or trailing slash.
func splitFolder(s string) (storage, rel string) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		storage, s = s[:i], s[i+3:]
	}
	return storage, strings.Trim(strings.ReplaceAll(s, "\\", "/"), "/")
}

// within reports whether rel is folder or inside it.
func within(rel, folder string) bool {
	rel = strings.Trim(rel, "/")
	return folder == "" || rel == folder || strings.HasPrefix(rel, folder+"/")
}

// Accept reports whether n satisfies every part of c, for the account
// viewer (0 when nobody is signed in: "me" then matches nothing). The
// folders are compared by path alone; see AcceptIn.
func (c Criteria) Accept(n *model.Node, viewer int64) bool {
	return c.AcceptIn(n, viewer, "")
}

// AcceptIn is Accept for a row of the storage named storage ("" when the
// caller does not know it: the folders are then compared by path alone).
func (c Criteria) AcceptIn(n *model.Node, viewer int64, storage string) bool {
	if n == nil {
		return false
	}
	dir := n.Type == model.NodeTypeDirectory
	if !kindAccepts(c.Type, KindOf(n.Name, n.Mime, dir)) {
		return false
	}
	if c.Mime != "" && (dir || !strings.HasPrefix(strings.ToLower(n.Mime), c.Mime)) {
		return false
	}
	if c.ModifiedAfter != nil || c.ModifiedBefore != nil {
		mt := modifiedOf(n)
		if mt.IsZero() {
			return false
		}
		if c.ModifiedAfter != nil && mt.Before(*c.ModifiedAfter) {
			return false
		}
		if c.ModifiedBefore != nil && mt.After(*c.ModifiedBefore) {
			return false
		}
	}
	if c.MinSize != nil || c.MaxSize != nil {
		if dir {
			return false
		}
		if c.MinSize != nil && n.Size < *c.MinSize {
			return false
		}
		if c.MaxSize != nil && n.Size > *c.MaxSize {
			return false
		}
	}
	if c.Under != "" || c.UnderStorage != "" {
		if c.UnderStorage != "" && storage != "" && storage != c.UnderStorage {
			return false
		}
		if !within(n.Path, c.Under) {
			return false
		}
	}
	if c.NotUnder != "" || c.NotUnderStorage != "" {
		sameStorage := c.NotUnderStorage == "" || storage == "" || storage == c.NotUnderStorage
		if sameStorage && within(n.Path, c.NotUnder) {
			return false
		}
	}
	switch c.Owner {
	case "":
	case "me":
		if viewer <= 0 || n.OwnerID == nil || *n.OwnerID != viewer {
			return false
		}
	case "system":
		if n.OwnerID != nil && *n.OwnerID > 0 {
			return false
		}
	default:
		id, _ := strconv.ParseInt(c.Owner, 10, 64)
		if n.OwnerID == nil || *n.OwnerID != id {
			return false
		}
	}
	if c.Hidden != nil && !*c.Hidden && strings.HasPrefix(n.Name, ".") {
		return false
	}
	return true
}

// modifiedOf is the date the listing shows (handlers listingMtimeMillis): the
// storage's modification time, else when filex first saw the row.
func modifiedOf(n *model.Node) time.Time {
	if n.BackendMtime != nil && !n.BackendMtime.IsZero() {
		return *n.BackendMtime
	}
	return n.CreatedAt
}

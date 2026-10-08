// Package listorder is the ONE order the server puts rows in: a folder's
// contents (from the catalogue, from the storage driver or merged), the MCP
// `file_list` tool, and the per-person views (Recent, Starred, Shared with me,
// a tag).
//
// ⚠ Why it exists (filex 0.54, audit Y3). Before it, the same folder came back
// in three orders depending on which code path answered: the catalogue path
// said `ORDER BY type DESC, name` (files first, because "file" > "dir"), the
// merged catalogue-and-driver path put folders first, and `file_list` returned
// whatever order the storage driver listed in. A CLI user, an MCP agent and the
// explorer saw one folder three ways, and the explorer's own comment claimed
// the server "already" answered by name. Every path now sorts through Sort.
//
// The rule, in order:
//
//  1. Folders before files, in every key and both directions (the explorer's
//     rule, packages/core lib/sortOrder byFoldersFirst). The direction reverses
//     the order INSIDE each group, never the groups. KeyWhen is the exception:
//     "when did I open / star / get this" is one timeline, not two groups.
//  2. The key: name (numbers as numbers, case and the four i's folded by
//     namefold), modified, size, type (the extension), or when (the person's
//     own time on the row).
//  3. Ties: the name, then the path's bytes, so the same rows always come back
//     in the same order.
package listorder

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
)

// Key is what a listing is ordered by.
type Key string

// The keys. The wire spelling is the constant's value; a leading "-" asks for
// the descending order (`sort=-modified` is newest first).
const (
	KeyName     Key = "name"
	KeyModified Key = "modified"
	KeySize     Key = "size"
	KeyType     Key = "type"
	// KeyWhen is the person's own time on the row: when they opened it
	// (Recent), starred it (Starred), were given it (Shared with me).
	KeyWhen Key = "when"
)

// Order is a key and a direction.
type Order struct {
	Key  Key
	Desc bool
}

// Default is a folder's order when nobody asked for one: by name, A to Z.
var Default = Order{Key: KeyName}

// Newest is a per-person view's order when nobody asked for one: the
// person's own time, newest first.
var Newest = Order{Key: KeyWhen, Desc: true}

// String renders the order the way Parse reads it.
func (o Order) String() string {
	if o.Desc {
		return "-" + string(o.Key)
	}
	return string(o.Key)
}

// Parse reads a `sort` parameter: a key, optionally prefixed with "-" for the
// descending order. "opened", "starred", "shared" and "tagged" are accepted
// as the name of a view's own time (KeyWhen). An empty value answers fallback;
// an unknown key answers fallback and false, so a handler can refuse it.
func Parse(s string, fallback Order) (Order, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return fallback, true
	}
	desc := false
	if strings.HasPrefix(s, "-") {
		desc, s = true, s[1:]
	}
	switch Key(s) {
	case KeyName, KeyModified, KeySize, KeyType, KeyWhen:
		return Order{Key: Key(s), Desc: desc}, true
	}
	switch s {
	case "opened", "starred", "shared", "tagged":
		return Order{Key: KeyWhen, Desc: desc}, true
	}
	return fallback, false
}

// Fields is what the rule reads off one row.
type Fields struct {
	Name string
	Path string
	Dir  bool
	Size int64
	// Modified and When are milliseconds since the epoch; 0 is unknown.
	Modified int64
	When     int64
}

// NodeFields reads a catalogue row. When is left for the caller.
func NodeFields(n *model.Node) Fields {
	f := Fields{Name: n.Name, Path: n.Path, Dir: n.Type == model.NodeTypeDirectory, Size: n.Size}
	// The listing's own date (handlers listingMtimeMillis): the storage's
	// modification time, else when filex first saw the row.
	if n.BackendMtime != nil && !n.BackendMtime.IsZero() {
		f.Modified = n.BackendMtime.UnixMilli()
	} else if !n.CreatedAt.IsZero() {
		f.Modified = n.CreatedAt.UnixMilli()
	}
	return f
}

// EntryFields reads a listing row as the manager projects it (`basename`,
// `path`, `type`, `size`, `last_modified`), with `whenKey` (e.g. "shared_at",
// "" for none) as the person's own time.
func EntryFields(e map[string]any, whenKey string) Fields {
	f := Fields{}
	f.Name, _ = e["basename"].(string)
	f.Path, _ = e["path"].(string)
	f.Dir = e["type"] == "dir"
	f.Size = int64Of(e["size"])
	f.Modified = int64Of(e["last_modified"])
	if whenKey != "" {
		f.When = int64Of(e[whenKey])
	}
	return f
}

func int64Of(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case int32:
		return int64(x)
	}
	return 0
}

// Less reports whether a comes before b in order o.
func Less(a, b Fields, o Order) bool {
	if o.Key != KeyWhen && a.Dir != b.Dir {
		return a.Dir // folders first, whatever the direction
	}
	if c := compareKey(a, b, o.Key); c != 0 {
		if o.Desc {
			return c > 0
		}
		return c < 0
	}
	if c := CompareNames(a.Name, b.Name); c != 0 {
		return c < 0
	}
	return a.Path < b.Path
}

func compareKey(a, b Fields, k Key) int {
	switch k {
	case KeyModified:
		return cmpInt(a.Modified, b.Modified)
	case KeySize:
		return cmpInt(a.Size, b.Size)
	case KeyWhen:
		return cmpInt(a.When, b.When)
	case KeyType:
		if c := strings.Compare(extOf(a), extOf(b)); c != 0 {
			return c
		}
		return 0
	}
	return CompareNames(a.Name, b.Name)
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func extOf(f Fields) string {
	if f.Dir {
		return ""
	}
	i := strings.LastIndexByte(f.Name, '.')
	if i <= 0 || i == len(f.Name)-1 {
		return ""
	}
	return namefold.String(f.Name[i+1:])
}

// CompareNames orders two names the way the Name column does: case and the
// four Latin i's folded (namefold.String), and a run of digits compared as a
// number, so "Disk 2" comes before "Disk 10". -1, 0 or 1.
func CompareNames(a, b string) int {
	a, b = namefold.String(a), namefold.String(b)
	for a != "" && b != "" {
		ra, wa := utf8.DecodeRuneInString(a)
		rb, wb := utf8.DecodeRuneInString(b)
		if isDigit(ra) && isDigit(rb) {
			da, db := digitRun(a), digitRun(b)
			if c := compareDigits(da, db); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		a, b = a[wa:], b[wb:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// digitRun is the leading run of ASCII digits of s.
func digitRun(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

// compareDigits compares two ASCII digit runs as numbers: leading zeros
// ignored, then the longer run is the larger number, then digit by digit.
func compareDigits(a, b string) int {
	ta, tb := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(ta) != len(tb) {
		if len(ta) < len(tb) {
			return -1
		}
		return 1
	}
	if c := strings.Compare(ta, tb); c != 0 {
		return c
	}
	return cmpInt(int64(len(a)), int64(len(b)))
}

// SortNodes orders catalogue rows in place.
func SortNodes(nodes []*model.Node, o Order) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i] == nil || nodes[j] == nil {
			return nodes[j] == nil && nodes[i] != nil
		}
		return Less(NodeFields(nodes[i]), NodeFields(nodes[j]), o)
	})
}

// SortEntries orders projected listing rows in place; whenKey names the
// row's own-time field ("" when the listing has none).
func SortEntries(entries []map[string]any, o Order, whenKey string) {
	sort.SliceStable(entries, func(i, j int) bool {
		return Less(EntryFields(entries[i], whenKey), EntryFields(entries[j], whenKey), o)
	})
}

// Package hostilenames is the one list of hostile file names every surface
// of filex is tested with: Turkish and CJK letters, right-to-left scripts,
// emoji, the same name composed and decomposed, spaces and dots at the ends,
// URL and shell metacharacters, Windows device names and forbidden
// characters, names at and over the 255-byte limit, invisible and bidi
// control characters, case twins (task #238).
//
// The list is names.json beside this file. The e2e specs, the web unit tests
// and the desktop scripts read the same file through ONE small JavaScript
// reader (e2e/helpers/hostile-names.mjs), so a name added there reaches every
// surface. ⚠ Never copy a name into a second list: add it to names.json.
//
// Like archivetest it imports nothing from filex, so any package's internal
// tests can use it without an import cycle.
package hostilenames

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

//go:embed names.json
var raw []byte

// TwinKind says why two names are one name to some file system.
type TwinKind string

const (
	// TwinCase: the names differ only in letter case (Windows, macOS).
	TwinCase TwinKind = "case"
	// TwinNFC: the same text composed and decomposed (macOS).
	TwinNFC TwinKind = "nfc-nfd"
)

// Name is one hostile name.
type Name struct {
	// ID is stable and safe in a test name or a log line ("rlo-extension").
	ID string
	// Name is the name itself, invisible characters and all.
	Name string
	// Why says in a few words what the name breaks.
	Why string
	// Tags group the names: unicode, cjk, rtl, emoji, nfd, space, dots, url,
	// html, quote, separator, win-reserved, win-forbidden, long,
	// over-255-bytes, invisible, bidi-control, control, case.
	Tags []string
	// Refused: no filex surface may create a file with this name.
	Refused bool
	// WindowsInvalid: the name cannot exist as a file name on Windows (a
	// device name, a forbidden character, a trailing space or dot).
	WindowsInvalid bool
	// Twin is the ID of the name that is the same name to a case- or
	// normalisation-insensitive file system, TwinKind why.
	Twin     string
	TwinKind TwinKind
}

// Has reports whether the name carries tag.
func (n Name) Has(tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Bytes is the length of the name in bytes of UTF-8.
func (n Name) Bytes() int { return len(n.Name) }

// Path is a hostile relative path (slash-separated, no leading slash).
type Path struct {
	ID   string
	Path string
	Why  string
	// Depth is how many folders the path goes through before its leaf.
	Depth int
	// Leaf is the file name at the end of the path.
	Leaf string
}

// Dirs is every folder of the path, shortest first ("a", "a/b", ...): the
// order a test creates them in.
func (p Path) Dirs() []string {
	segs := strings.Split(p.Path, "/")
	out := make([]string, 0, len(segs)-1)
	for i := 1; i < len(segs); i++ {
		out = append(out, strings.Join(segs[:i], "/"))
	}
	return out
}

type fileJSON struct {
	Doc   []string `json:"_doc"`
	Names []struct {
		ID       string            `json:"id"`
		Parts    []json.RawMessage `json:"parts"`
		Why      string            `json:"why"`
		Tags     []string          `json:"tags"`
		Refused  bool              `json:"refused"`
		Windows  string            `json:"windows"`
		Twin     string            `json:"twin"`
		TwinKind string            `json:"twin_kind"`
	} `json:"names"`
	Paths []struct {
		ID      string            `json:"id"`
		Segment []json.RawMessage `json:"segment"`
		Depth   int               `json:"depth"`
		Leaf    []json.RawMessage `json:"leaf"`
		Why     string            `json:"why"`
	} `json:"paths"`
}

type list struct {
	names []Name
	paths []Path
}

// loaded is names.json, read once. A malformed file is a defect of the test
// fixture: it panics, and hostilenames_test.go reads it so it never gets as
// far as a surface's test.
var loaded = func() list {
	l, err := parse(raw)
	if err != nil {
		panic("hostilenames: names.json: " + err.Error())
	}
	return l
}()

func parse(b []byte) (list, error) {
	var f fileJSON
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return list{}, err
	}
	var l list
	for _, n := range f.Names {
		name, err := join(n.Parts)
		if err != nil {
			return list{}, fmt.Errorf("%s: %w", n.ID, err)
		}
		if n.Windows != "" && n.Windows != "invalid" {
			return list{}, fmt.Errorf("%s: windows is %q, want \"invalid\" or nothing", n.ID, n.Windows)
		}
		l.names = append(l.names, Name{
			ID: n.ID, Name: name, Why: n.Why, Tags: n.Tags, Refused: n.Refused,
			WindowsInvalid: n.Windows == "invalid",
			Twin:           n.Twin, TwinKind: TwinKind(n.TwinKind),
		})
	}
	for _, p := range f.Paths {
		seg, err := join(p.Segment)
		if err != nil {
			return list{}, fmt.Errorf("%s: %w", p.ID, err)
		}
		leaf, err := join(p.Leaf)
		if err != nil {
			return list{}, fmt.Errorf("%s: %w", p.ID, err)
		}
		if p.Depth < 1 {
			return list{}, fmt.Errorf("%s: depth %d", p.ID, p.Depth)
		}
		segs := make([]string, 0, p.Depth+1)
		for i := 0; i < p.Depth; i++ {
			segs = append(segs, seg)
		}
		segs = append(segs, leaf)
		l.paths = append(l.paths, Path{ID: p.ID, Path: strings.Join(segs, "/"), Why: p.Why, Depth: p.Depth, Leaf: leaf})
	}
	return l, nil
}

// join builds a name from its parts: a JSON string is itself, a number one
// code point, {"repeat": <string or code point>, "times": n} n copies.
func join(parts []json.RawMessage) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		s, err := part(p)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

func part(p json.RawMessage) (string, error) {
	p = bytes.TrimSpace(p)
	if len(p) == 0 {
		return "", fmt.Errorf("empty part")
	}
	switch p[0] {
	case '"':
		var s string
		if err := json.Unmarshal(p, &s); err != nil {
			return "", err
		}
		return s, nil
	case '{':
		var rep struct {
			Repeat json.RawMessage `json:"repeat"`
			Times  int             `json:"times"`
		}
		if err := json.Unmarshal(p, &rep); err != nil {
			return "", err
		}
		if rep.Times < 1 {
			return "", fmt.Errorf("repeat times %d", rep.Times)
		}
		one, err := part(rep.Repeat)
		if err != nil {
			return "", err
		}
		return strings.Repeat(one, rep.Times), nil
	default:
		var cp int64
		if err := json.Unmarshal(p, &cp); err != nil {
			return "", fmt.Errorf("part %s: %w", p, err)
		}
		if cp < 0 || cp > utf8.MaxRune || (cp >= 0xD800 && cp <= 0xDFFF) {
			return "", fmt.Errorf("code point %d is not a Unicode scalar value", cp)
		}
		return string(rune(cp)), nil
	}
}

// All is every hostile name, in the file's order. The slice is the caller's.
func All() []Name {
	return append([]Name(nil), loaded.names...)
}

// Filter is every name keep accepts, in the file's order.
func Filter(keep func(Name) bool) []Name {
	var out []Name
	for _, n := range loaded.names {
		if keep(n) {
			out = append(out, n)
		}
	}
	return out
}

// Creatable is every name a filex surface must accept (not Refused).
func Creatable() []Name { return Filter(func(n Name) bool { return !n.Refused }) }

// Refused is every name no filex surface may create.
func Refused() []Name { return Filter(func(n Name) bool { return n.Refused }) }

// Without is every name carrying none of tags — the names a surface can carry
// at all (an FTP command line cannot carry a line break; a URL path cannot
// carry a NUL).
func Without(names []Name, tags ...string) []Name {
	var out []Name
	for _, n := range names {
		skip := false
		for _, t := range tags {
			if n.Has(t) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, n)
		}
	}
	return out
}

// ForLocalDisk is every Creatable name the test machine's own file system can
// hold side by side in ONE folder - what a test writes through a local-disk
// storage, which keeps the name it is given:
//
//   - no name over 255 bytes, except on Windows, whose limit is 255 UTF-16
//     units (TooLongForLocalDisk has them, for the refusal);
//   - on Windows, no name Windows refuses (WindowsInvalid);
//   - on Windows and macOS, whose file systems are case-insensitive (and on
//     macOS normalisation-insensitive), only the first name of each twin
//     pair: the second would land on the first.
//
// The suite runs on Linux, where every name but those over 255 bytes is kept;
// the filter is what lets the same test pass on a developer's laptop.
func ForLocalDisk() []Name {
	foldsTwins := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	kept := map[string]bool{}
	return Filter(func(n Name) bool {
		switch {
		case n.Refused:
			return false
		case runtime.GOOS == "windows" && (n.WindowsInvalid || len(utf16.Encode([]rune(n.Name))) > 255):
			return false
		case runtime.GOOS != "windows" && len(n.Name) > 255:
			return false
		case foldsTwins && n.Twin != "" && kept[n.Twin]:
			return false
		}
		kept[n.ID] = true
		return true
	})
}

// TooLongForLocalDisk is every Creatable name the test machine's file system
// refuses for its length: over 255 bytes on Linux and macOS (ENAMETOOLONG).
// Empty on Windows, which counts UTF-16 units and holds them all.
func TooLongForLocalDisk() []Name {
	if runtime.GOOS == "windows" {
		return nil
	}
	return Filter(func(n Name) bool { return !n.Refused && len(n.Name) > 255 })
}

// Get is the name with id; it panics on an unknown id (a typo in a test).
func Get(id string) Name {
	for _, n := range loaded.names {
		if n.ID == id {
			return n
		}
	}
	panic("hostilenames: no name " + id)
}

// Paths is every hostile path.
func Paths() []Path { return append([]Path(nil), loaded.paths...) }

// Deep is the deep path (64 folders, about 900 bytes).
func Deep() Path {
	for _, p := range loaded.paths {
		if p.ID == "deep" {
			return p
		}
	}
	panic("hostilenames: no deep path")
}

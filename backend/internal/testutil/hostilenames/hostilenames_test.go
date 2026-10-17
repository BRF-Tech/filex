package hostilenames

import (
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// knownTags is the vocabulary of names.json; a tag outside it is a typo that
// would silently drop a name from a surface's filter.
var knownTags = map[string]bool{
	"unicode": true, "cjk": true, "rtl": true, "emoji": true, "nfd": true,
	"space": true, "dots": true, "url": true, "html": true, "quote": true,
	"separator": true, "win-reserved": true, "win-forbidden": true,
	"long": true, "over-255-bytes": true, "invisible": true,
	"bidi-control": true, "control": true, "case": true,
}

var idRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestNames_TheListIsWellFormed(t *testing.T) {
	all := All()
	if len(all) < 40 {
		t.Fatalf("only %d names: names.json lost its entries", len(all))
	}
	seenID := map[string]bool{}
	seenName := map[string]string{}
	for _, n := range all {
		if !idRE.MatchString(n.ID) {
			t.Errorf("%q: an id is lower-case words joined by dashes", n.ID)
		}
		if seenID[n.ID] {
			t.Errorf("%s: id used twice", n.ID)
		}
		seenID[n.ID] = true
		if other, dup := seenName[n.Name]; dup {
			t.Errorf("%s: the same name as %s", n.ID, other)
		}
		seenName[n.Name] = n.ID
		if n.Name == "" || !utf8.ValidString(n.Name) {
			t.Errorf("%s: empty or not UTF-8", n.ID)
		}
		if strings.Contains(n.Name, "/") {
			t.Errorf("%s: a name holds no slash; a slash makes it a path (use paths)", n.ID)
		}
		if strings.TrimSpace(n.Why) == "" {
			t.Errorf("%s: say why the name is hostile", n.ID)
		}
		for _, tag := range n.Tags {
			if !knownTags[tag] {
				t.Errorf("%s: unknown tag %q", n.ID, tag)
			}
		}
	}
}

// The windows flag is checked against the rule Windows itself applies, so a
// name added without it cannot slip past the desktop's tests.
func TestNames_WindowsFlagFollowsTheWindowsRule(t *testing.T) {
	for _, n := range All() {
		if got := windowsRefuses(n.Name); got != n.WindowsInvalid {
			t.Errorf("%s (%q): Windows refuses it = %v, names.json says windows invalid = %v", n.ID, n.Name, got, n.WindowsInvalid)
		}
	}
}

func windowsRefuses(name string) bool {
	if strings.ContainsAny(name, `<>:"/\|?*`) {
		return true
	}
	for _, r := range name {
		if r < 0x20 {
			return true
		}
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return true
	}
	stem := strings.ToUpper(name)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}

func TestNames_TwinsAreTwins(t *testing.T) {
	for _, n := range All() {
		if n.Twin == "" {
			continue
		}
		o := Get(n.Twin)
		if o.Twin != n.ID || o.TwinKind != n.TwinKind {
			t.Errorf("%s: twin %s does not point back with the same kind", n.ID, o.ID)
		}
		if n.Name == o.Name {
			t.Errorf("%s: a twin has other bytes", n.ID)
		}
		switch n.TwinKind {
		case TwinNFC:
			if norm.NFC.String(n.Name) != norm.NFC.String(o.Name) {
				t.Errorf("%s and %s: not the same text once composed", n.ID, o.ID)
			}
		case TwinCase:
			if !strings.EqualFold(n.Name, o.Name) {
				t.Errorf("%s and %s: do not differ only in case", n.ID, o.ID)
			}
		default:
			t.Errorf("%s: unknown twin kind %q", n.ID, n.TwinKind)
		}
	}
	if d := Get("nfd-butce").Name; !norm.NFD.IsNormalString(d) || norm.NFC.IsNormalString(d) {
		t.Errorf("nfd-butce is not decomposed: % x", d)
	}
}

func TestNames_LengthsAreWhatTheyClaim(t *testing.T) {
	if got := Get("bytes-255").Bytes(); got != 255 {
		t.Errorf("bytes-255 is %d bytes", got)
	}
	if got := Get("bytes-256").Bytes(); got != 256 {
		t.Errorf("bytes-256 is %d bytes", got)
	}
	c := Get("chars-under-bytes-over")
	if utf8.RuneCountInString(c.Name) >= 255 || c.Bytes() <= 255 {
		t.Errorf("chars-under-bytes-over: %d characters, %d bytes", utf8.RuneCountInString(c.Name), c.Bytes())
	}
	for _, n := range All() {
		if over := n.Bytes() > 255; over != n.Has("over-255-bytes") {
			t.Errorf("%s: %d bytes but tagged over-255-bytes = %v", n.ID, n.Bytes(), n.Has("over-255-bytes"))
		}
	}
	d := Deep()
	if d.Depth != 64 || len(d.Path) >= 1024 || utf8.RuneCountInString(d.Path) <= 260 {
		t.Errorf("deep: depth %d, %d bytes, %d characters", d.Depth, len(d.Path), utf8.RuneCountInString(d.Path))
	}
	if dirs := d.Dirs(); len(dirs) != d.Depth || !strings.HasSuffix(d.Path, "/"+d.Leaf) || dirs[len(dirs)-1]+"/"+d.Leaf != d.Path {
		t.Errorf("deep: %d folders, leaf %q", len(dirs), d.Leaf)
	}
}

func TestNames_InvisibleAndControlCarryTheirCharacter(t *testing.T) {
	for _, n := range All() {
		hasFormat, hasControl := false, false
		for _, r := range n.Name {
			if unicode.Is(unicode.Cf, r) && r != 0x200D {
				hasFormat = true
			}
			if unicode.IsControl(r) {
				hasControl = true
			}
		}
		if hasFormat != n.Has("invisible") {
			t.Errorf("%s: holds an invisible format character = %v, tagged invisible = %v", n.ID, hasFormat, n.Has("invisible"))
		}
		if hasControl != n.Has("control") {
			t.Errorf("%s: holds a control character = %v, tagged control = %v", n.ID, hasControl, n.Has("control"))
		}
	}
	if !strings.ContainsRune(Get("rlo-extension").Name, 0x202E) {
		t.Error("rlo-extension lost its RIGHT-TO-LEFT OVERRIDE")
	}
}

func TestNames_RefusedIsTheShortList(t *testing.T) {
	// internal/leafname is the rule; its own test walks this list.
	want := map[string]bool{"dot-dot": true, "three-dots": true, "dots-spaced": true, "dot-space-dot": true, "nul": true, "backslash": true, "control-tab": true, "control-newline": true}
	for _, n := range Refused() {
		if !want[n.ID] {
			t.Errorf("%s is refused: every surface's test then expects a refusal; say why in the docs first", n.ID)
		}
		delete(want, n.ID)
	}
	for id := range want {
		t.Errorf("%s is no longer refused", id)
	}
	if len(Creatable())+len(Refused()) != len(All()) {
		t.Error("Creatable and Refused do not split the list")
	}
}

func TestForLocalDisk_OnLinuxOnlyTheTooLongAreLeftOut(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("measured on the suite's own OS")
	}
	disk, long := ForLocalDisk(), TooLongForLocalDisk()
	if len(disk)+len(long) != len(Creatable()) {
		t.Fatalf("%d on disk + %d too long != %d creatable", len(disk), len(long), len(Creatable()))
	}
	for _, n := range long {
		if !n.Has("over-255-bytes") {
			t.Errorf("%s is left out as too long at %d bytes", n.ID, n.Bytes())
		}
	}
	if len(long) == 0 {
		t.Error("no name tests the 255-byte refusal")
	}
}

func TestParse_RefusesWhatWouldHideAName(t *testing.T) {
	for _, bad := range []string{
		`{"names":[{"id":"x","parts":[55296],"why":"w"}],"paths":[]}`,
		`{"names":[{"id":"x","parts":[{"repeat":"a","times":0}],"why":"w"}],"paths":[]}`,
		`{"names":[{"id":"x","parts":[true],"why":"w"}],"paths":[]}`,
		`{"names":[{"id":"x","parts":["a"],"why":"w","windows":"ok"}],"paths":[]}`,
		`{"names":[{"id":"x","parts":["a"],"why":"w","typo":1}],"paths":[]}`,
	} {
		if _, err := parse([]byte(bad)); err == nil {
			t.Errorf("parsed %s", bad)
		}
	}
}

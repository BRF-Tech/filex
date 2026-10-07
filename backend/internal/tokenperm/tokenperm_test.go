package tokenperm

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// shippedDefaults is the default of every permission a release has shipped.
//
// ⚠ Frozen history: a line is added when a permission is added, and never
// edited afterwards. A key that does not name a permission holds its default
// (LevelIn), so changing a shipped default changes, without a word, every key
// that was minted without choosing - the keys from before the permission
// existed among them. A permission whose default must change is a new
// permission.
var shippedDefaults = map[string]Level{
	Comments: Read,
}

// reservedKeys are the names a permission can never take: the verbs and the
// confinement prefix share the list with it (auth/drivers/apitoken).
var reservedKeys = map[string]bool{"root": true, "read": true, "write": true, "delete": true, "mcp": true, "admin": true}

var keyShape = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// catalogueProblems is the guard: everything wrong with defs as a catalogue,
// one line each. It is a function of its input so the test below can show it
// catches each mistake on a catalogue made to have it.
func catalogueProblems(defs []Def, shipped map[string]Level) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range defs {
		name := d.Key
		if name == "" {
			name = "(no key)"
		}
		if !keyShape.MatchString(d.Key) || reservedKeys[d.Key] {
			out = append(out, fmt.Sprintf("%s: a key is lower-case letters, digits and _ and is not a verb or `root`", name))
		}
		if seen[d.Key] {
			out = append(out, name+": listed twice")
		}
		seen[d.Key] = true
		if len(d.Levels) == 0 {
			out = append(out, name+": no levels")
		}
		for i := 1; i < len(d.Levels); i++ {
			if d.Levels[i].rank() <= d.Levels[i-1].rank() {
				out = append(out, name+": levels are not listed lowest first")
			}
		}
		switch {
		case d.DefaultLevel == "":
			out = append(out, name+": no DefaultLevel - every permission states the level every key that does not name it holds "+
				"(Read, or None for a Superadmin one; docs/CONTRIBUTING.md → Adding a permission)")
		case d.Superadmin && d.DefaultLevel != None:
			out = append(out, fmt.Sprintf("%s: a Superadmin permission defaults to %q, not %q", name, None, d.DefaultLevel))
		case !d.Superadmin && d.DefaultLevel != Read:
			out = append(out, fmt.Sprintf("%s: a permission defaults to %q (only a Superadmin one to %q), not %q", name, Read, None, d.DefaultLevel))
		case d.DefaultLevel != None && !d.has(d.DefaultLevel):
			out = append(out, fmt.Sprintf("%s: the default %q is not one of its levels", name, d.DefaultLevel))
		}
		if was, ok := shipped[d.Key]; !ok {
			out = append(out, name+": not in shippedDefaults - add its line there; that line is the decision, and it never changes")
		} else if d.DefaultLevel != "" && was != d.DefaultLevel {
			out = append(out, fmt.Sprintf("%s: its shipped default was %q and is now %q - every key that never named it would change without a word", name, was, d.DefaultLevel))
		}
	}
	for k := range shipped {
		if !seen[k] {
			out = append(out, k+": shipped, and gone from the catalogue - a key that names it would keep naming a permission nothing reads")
		}
	}
	return out
}

// TestCatalogue_EveryPermissionStatesItsDefault - the guard on the real
// catalogue. A permission added without a DefaultLevel, with a default that
// breaks the rule, or with a shipped default changed, is red here.
func TestCatalogue_EveryPermissionStatesItsDefault(t *testing.T) {
	if p := catalogueProblems(catalogue, shippedDefaults); len(p) > 0 {
		t.Fatalf("the token permission catalogue:\n  %s", strings.Join(p, "\n  "))
	}
}

// TestCatalogue_TheGuardCatchesEachMistake - the guard is not vacuous: each
// mistake it is there for, made on purpose, is reported.
func TestCatalogue_TheGuardCatchesEachMistake(t *testing.T) {
	shipped := map[string]Level{"notes": Read, "tenants": None}
	cases := []struct {
		name string
		def  Def
		want string
	}{
		{"no default", Def{Key: "notes", Levels: []Level{Read, ReadWrite}}, "no DefaultLevel"},
		{"defaults to write", Def{Key: "notes", Levels: []Level{Read, ReadWrite}, DefaultLevel: ReadWrite}, "defaults to"},
		{"superadmin defaults to read", Def{Key: "tenants", Levels: []Level{None, Read, ReadWrite}, DefaultLevel: Read, Superadmin: true}, "Superadmin"},
		{"not shipped", Def{Key: "tags", Levels: []Level{Read, ReadWrite}, DefaultLevel: Read}, "shippedDefaults"},
		{"shipped default changed", Def{Key: "tenants", Levels: []Level{None, Read}, DefaultLevel: Read}, "shipped default was"},
		{"a verb's name", Def{Key: "write", Levels: []Level{Read}, DefaultLevel: Read}, "not a verb"},
		{"levels out of order", Def{Key: "notes", Levels: []Level{ReadWrite, Read}, DefaultLevel: Read}, "lowest first"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := strings.Join(catalogueProblems([]Def{c.def}, shipped), "\n")
			if !strings.Contains(p, c.want) {
				t.Fatalf("want a problem saying %q, got:\n%s", c.want, p)
			}
		})
	}
	// …and a correct one, superadmin included, is not reported (beyond the
	// shipped line the other one leaves unused).
	good := []Def{
		{Key: "notes", Levels: []Level{Read, ReadWrite}, DefaultLevel: Read},
		{Key: "tenants", Levels: []Level{None, Read, ReadWrite}, DefaultLevel: None, Superadmin: true},
	}
	if p := catalogueProblems(good, shipped); len(p) > 0 {
		t.Fatalf("a correct catalogue was reported:\n%s", strings.Join(p, "\n"))
	}
}

// TestLevelIn_AKeyThatNamesNothingHoldsTheDefault - a key from before the
// permission existed (its list names only verbs) holds the default; the level
// a list names wins; an empty list holds nothing.
func TestLevelIn_AKeyThatNamesNothingHoldsTheDefault(t *testing.T) {
	cases := []struct {
		scopes string
		want   Level
	}{
		{"read,write,delete,mcp", Read},
		{"read,write,delete,mcp,admin", Read},
		{"read,root:main://docs", Read},
		{"read,comments:rw", ReadWrite},
		{"read,comments:write", ReadWrite},
		{"comments:rw,read,root:main://docs", ReadWrite},
		{"read,comments:read", Read},
		// Named twice: the lower level.
		{"read,comments:rw,comments:read", Read},
		// An entry it cannot read, or a level it does not have: the default.
		{"read,comments:bogus", Read},
		{"read,comments:none", Read},
		// An empty list grants nothing.
		{"", None},
		{"   ", None},
	}
	for _, c := range cases {
		if got := LevelIn(c.scopes, Comments); got != c.want {
			t.Errorf("LevelIn(%q) = %q, want %q", c.scopes, got, c.want)
		}
	}
	if got := LevelIn("read,write", "nosuch"); got != None {
		t.Errorf("an unknown permission = %q, want none", got)
	}
}

func TestParseEntry(t *testing.T) {
	if _, _, isPerm, _ := ParseEntry("read"); isPerm {
		t.Error("a verb is not a permission entry")
	}
	if _, _, isPerm, _ := ParseEntry("root:main://docs"); isPerm {
		t.Error("a root is not a permission entry")
	}
	if _, _, isPerm, _ := ParseEntry("bogus:rw"); isPerm {
		t.Error("an unknown key is not a permission entry (the caller refuses it as an unknown scope)")
	}
	key, l, isPerm, err := ParseEntry(" comments:write ")
	if !isPerm || err != nil || key != Comments || l != ReadWrite {
		t.Errorf("comments:write = %q %q %v %v, want comments rw", key, l, isPerm, err)
	}
	if _, _, isPerm, err := ParseEntry("comments:admin"); !isPerm || err == nil {
		t.Errorf("a level the permission does not have is an error, got %v %v", isPerm, err)
	}
}

func TestCanonicalAndReplace(t *testing.T) {
	if got := Canonical(map[string]Level{Comments: Read}); len(got) != 0 {
		t.Errorf("the default is not written, got %v", got)
	}
	if got := strings.Join(Canonical(map[string]Level{Comments: ReadWrite}), ","); got != "comments:rw" {
		t.Errorf("Canonical(rw) = %q", got)
	}
	cases := []struct{ scopes, want string }{
		{"read,write,mcp", "read,write,mcp,comments:rw"},
		{"read,comments:rw,root:main://docs", "read,root:main://docs,comments:rw"},
	}
	for _, c := range cases {
		if got := Replace(c.scopes, map[string]Level{Comments: ReadWrite}); got != c.want {
			t.Errorf("Replace(%q, rw) = %q, want %q", c.scopes, got, c.want)
		}
	}
	if got := Replace("read,comments:rw,root:main://docs", map[string]Level{Comments: Read}); got != "read,root:main://docs" {
		t.Errorf("back to the default drops the entry, got %q", got)
	}
	if got := Replace("read,write", nil); got != "read,write" {
		t.Errorf("no change keeps the list, got %q", got)
	}
}

func TestNeedNamesItself(t *testing.T) {
	if got := (Need{Key: Comments, Level: ReadWrite}).String(); got != "comments:write" {
		t.Errorf("got %q", got)
	}
	if got := (Need{Key: Comments, Level: Read}).String(); got != "comments:read" {
		t.Errorf("got %q", got)
	}
	if !ReadWrite.Covers(Read) || Read.Covers(ReadWrite) || !Read.Covers(Read) || None.Covers(Read) {
		t.Error("Covers is not the level order")
	}
}

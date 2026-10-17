package leafname

import (
	"errors"
	"testing"
	"unicode"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// The rule and the shared list agree: every name the list calls refused is
// refused here, and every other one passes (task #238). A door that asks
// Check therefore refuses exactly the list's refused names.
func TestCheck_FollowsTheHostileNamesList(t *testing.T) {
	for _, n := range hostilenames.All() {
		err := Check(n.Name)
		if n.Refused {
			if err == nil {
				t.Errorf("%s (%q): passed, the list says no door may create it", n.ID, n.Name)
			} else if !errors.Is(err, ErrRefused) || Reason(err) == "" {
				t.Errorf("%s: %v is not a refusal with a reason", n.ID, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s (%q): refused (%v), the list says every door must accept it", n.ID, n.Name, err)
		}
	}
}

func TestCheck_Reasons(t *testing.T) {
	cases := map[string]string{
		"":         Empty,
		"a/b":      Separator,
		`a\b`:      Separator,
		"a\x00b":   NUL,
		"a\tb":     Control,
		"a\nb":     Control,
		"a\x1bb":   Control,
		"a\x7fb":   Control,
		".":        Dots,
		"..":       Dots,
		"...":      Dots,
		".. ":      Dots,
		" . . . ":  Dots,
		". .":      Dots,
		". ":       Dots,
		" .":       Dots,
		"a..b":     "",
		"..hidden": "",
		".env":     "",
		"report.":  "",
		"   ":      Empty,
		" ":        Empty,
		" a ":      "",
	}
	for name, want := range cases {
		if got := Reason(Check(name)); got != want {
			t.Errorf("Check(%q): reason %q, want %q", name, got, want)
		}
	}
}

// What a page shows of the RLO name keeps the true extension at the end, and
// every other hostile name is shown exactly as it is.
func TestVisible_NoBidiControlReachesAPage(t *testing.T) {
	for _, n := range hostilenames.All() {
		got := Visible(n.Name)
		for _, r := range got {
			if IsBidiControl(r) {
				t.Errorf("%s: %q still holds U+%04X", n.ID, got, r)
			}
		}
		if !n.Has("bidi-control") && got != n.Name {
			t.Errorf("%s: shown as %q, the name is %q", n.ID, got, n.Name)
		}
	}
	got := Visible(hostilenames.Get("rlo-extension").Name)
	if want := "invoice" + string(unicode.ReplacementChar) + "fdp.exe"; got != want {
		t.Errorf("rlo-extension shown as %q, want %q", got, want)
	}
}

// A copy saved outside filex (a download's name, a ZIP member) carries `_` for
// every control and bidi control character and nothing else changes.
func TestSavedRune_ControlsAndBidiControlsOnly(t *testing.T) {
	for _, r := range []rune{0x01, 0x09, 0x0A, 0x1F, 0x7F, 0x85, 0x202E, 0x2066, 0x200E} {
		if got := SavedRune(r); got != '_' {
			t.Errorf("U+%04X saved as U+%04X", r, got)
		}
	}
	for _, r := range []rune{'a', ' ', '.', 'ğ', 0x200B, 0x200D, 0x0308, '%', '#'} {
		if got := SavedRune(r); got != r {
			t.Errorf("U+%04X saved as U+%04X", r, got)
		}
	}
}

func TestCheckPath_EverySegment(t *testing.T) {
	for p, ok := range map[string]bool{
		"a/b/c.txt":     true,
		"a/../b":        false,
		"a/./b":         false,
		"a//b":          false,
		"/a":            false,
		"a/":            false,
		"a/b\\c/d":      false,
		"Bütçe/報告書.pdf": true,
	} {
		if got := CheckPath(p) == nil; got != ok {
			t.Errorf("CheckPath(%q) passes = %v, want %v", p, got, ok)
		}
	}
	if d := hostilenames.Deep(); CheckPath(d.Path) != nil {
		t.Errorf("the deep path is refused: %v", CheckPath(d.Path))
	}
}

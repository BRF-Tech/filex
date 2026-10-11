// Package compat is filex's compatibility window for what storage plugins
// and apps send it.
//
// ⚠⚠ The rule (docs/CONTRIBUTING.md → "Compatibility window for plugins and
// apps"): when a newer form replaces an old one - a signature format, a
// manifest field, a bridge call, a host function - the old form keeps
// working, with a warning, for at least Rule.Minors minor releases and
// Rule.Months months, whichever is longer, counted from the release that
// shipped the replacement. A plugin or an app published (built, signed)
// for that release therefore runs unchanged through the window. Only after
// it may a release refuse the old form, and only when the CHANGELOG of an
// earlier, published release announced that removal.
//
// windows.json is the one place a window is written. The code that still
// accepts an old form reads its end from here (Get, Window.Until), the
// sentences that name the end take it as {version} and {date}, and
// web/tests/docs/compatWindow.test.ts holds the docs and the CHANGELOG to
// the same numbers. A window is never deleted: the release that finally
// refuses the old form sets `removed` (and `announced`, the earlier release
// whose CHANGELOG said so), which the test checks against the rule.
package compat

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

//go:embed windows.json
var raw []byte

// DateLayout is how a window's dates are written (ISO 8601, a day).
const DateLayout = "2006-01-02"

// Rule is how long an old form stays accepted: Minors minor releases or
// Months months after the release that replaced it, whichever is longer.
type Rule struct {
	Minors int `json:"minors"`
	Months int `json:"months"`
}

// Window is one old form that is still accepted (or, once Removed is set,
// the record of when it stopped being).
type Window struct {
	// ID names the form for code, docs and the CHANGELOG
	// ("storage-build-signature-sha256").
	ID string `json:"id"`
	// Form says what the old form is, in one English sentence.
	Form string `json:"form"`
	// Since is the minor release that shipped the replacement ("0.55") and
	// SinceDate that release's date ("2026-10-09"); the window counts from
	// them.
	Since     string `json:"since"`
	SinceDate string `json:"since_date"`
	// Docs is the page and anchor that describe the form, from the
	// repository root.
	Docs string `json:"docs"`
	// Announced is the release whose CHANGELOG announced the removal, and
	// Removed the release that refuses the old form; both empty while it is
	// accepted.
	Announced string `json:"announced,omitempty"`
	Removed   string `json:"removed,omitempty"`

	rule Rule
}

type registry struct {
	Rule    Rule     `json:"rule"`
	Windows []Window `json:"windows"`
}

// loaded is windows.json, read once. A malformed file is a build defect,
// not a runtime condition: it panics at start-up, and compat_test.go reads
// it so the defect never reaches a binary.
var loaded = func() registry {
	r, err := parse(raw)
	if err != nil {
		panic("compat: windows.json: " + err.Error())
	}
	return r
}()

func parse(b []byte) (registry, error) {
	var r registry
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return registry{}, err
	}
	if r.Rule.Minors <= 0 || r.Rule.Months <= 0 {
		return registry{}, fmt.Errorf("rule: minors and months must both be positive, got %d and %d", r.Rule.Minors, r.Rule.Months)
	}
	seen := map[string]bool{}
	for i := range r.Windows {
		w := &r.Windows[i]
		if w.ID == "" || seen[w.ID] {
			return registry{}, fmt.Errorf("window %d: empty or repeated id %q", i, w.ID)
		}
		seen[w.ID] = true
		if strings.TrimSpace(w.Form) == "" || strings.TrimSpace(w.Docs) == "" {
			return registry{}, fmt.Errorf("%s: form and docs are required", w.ID)
		}
		for name, v := range map[string]string{"since": w.Since, "announced": w.Announced, "removed": w.Removed} {
			if v == "" && name != "since" {
				continue
			}
			if _, _, err := minorOf(v); err != nil {
				return registry{}, fmt.Errorf("%s: %s: %w", w.ID, name, err)
			}
		}
		if _, err := time.Parse(DateLayout, w.SinceDate); err != nil {
			return registry{}, fmt.Errorf("%s: since_date: %w", w.ID, err)
		}
		if (w.Announced == "") != (w.Removed == "") {
			return registry{}, fmt.Errorf("%s: announced and removed are set together", w.ID)
		}
		w.rule = r.Rule
	}
	return r, nil
}

// minorOf reads "MAJOR.MINOR" (a leading "v" and a ".PATCH" are tolerated).
func minorOf(v string) (major, minor int, err error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, fmt.Errorf("%q is not MAJOR.MINOR", v)
	}
	if major, err = strconv.Atoi(parts[0]); err != nil || major < 0 {
		return 0, 0, fmt.Errorf("%q is not MAJOR.MINOR", v)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil || minor < 0 {
		return 0, 0, fmt.Errorf("%q is not MAJOR.MINOR", v)
	}
	return major, minor, nil
}

// CurrentRule is the rule every window is measured with.
func CurrentRule() Rule { return loaded.Rule }

// All is every window, in the file's order.
func All() []Window {
	out := make([]Window, len(loaded.Windows))
	copy(out, loaded.Windows)
	return out
}

// Get is the window of one old form. An unknown id is a defect in the code
// that asks (it names a form windows.json does not hold): Get panics, and
// the asking package's tests reach it.
func Get(id string) Window {
	for _, w := range loaded.Windows {
		if w.ID == id {
			return w
		}
	}
	panic("compat: no window " + strconv.Quote(id) + " in windows.json")
}

// Until is the end of the window: version is the earliest minor release
// that may refuse the old form ("0.61"), date the day before which no
// release refuses it ("2027-10-09"). The later of the two holds.
func (w Window) Until() (version, date string) {
	major, minor, _ := minorOf(w.Since)
	since, _ := time.Parse(DateLayout, w.SinceDate)
	return fmt.Sprintf("%d.%d", major, minor+w.rule.Minors), since.AddDate(0, w.rule.Months, 0).Format(DateLayout)
}

// Open reports whether a release (its version and date) must still accept
// the old form: it is older than Until's version, or dated before Until's
// date.
func (w Window) Open(release string, on time.Time) bool {
	version, date := w.Until()
	endMajor, endMinor, _ := minorOf(version)
	major, minor, err := minorOf(release)
	if err != nil {
		return true
	}
	end, _ := time.Parse(DateLayout, date)
	older := major < endMajor || (major == endMajor && minor < endMinor)
	return older || on.Before(end)
}

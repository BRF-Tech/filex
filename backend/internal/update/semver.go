// Package update implements filex's release awareness: it learns which
// versions exist, decides what may be applied automatically, and (on installs
// that own their binary) applies them.
//
// The policy is expressed in terms of which part of x.y.z moves:
//
//	z (patch) → applied automatically when the policy allows it
//	y (minor) → announced; the operator confirms with one click
//	x (major) → announced only, with upgrade instructions
//
// The asymmetry is deliberate. A patch is a bugfix on a known-good shape; a
// minor may add a migration or change an embedded API; a major is a migration
// the operator should read about first. See docs/UPDATES.md.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Pre-release and build metadata are
// kept for display but ignored when ranking (a pre-release never wins an
// automatic upgrade — see Newer).
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "rc.1" in 1.2.3-rc.1 (without the dash)
	Raw                 string // exactly as parsed, e.g. "v0.7.6"
}

// ParseVersion accepts "v0.7.6", "0.7.6", "0.7.6-rc.1" and "0.1.0-dev".
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	v := Version{Raw: raw}
	body := strings.TrimPrefix(raw, "v")
	if i := strings.IndexByte(body, '+'); i >= 0 { // build metadata: display only
		body = body[:i]
	}
	if i := strings.IndexByte(body, '-'); i >= 0 {
		v.Pre = body[i+1:]
		body = body[:i]
	}
	parts := strings.Split(body, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("not a semantic version: %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("not a semantic version: %q", s)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// IsPre reports whether this is a pre-release (rc, dev, beta…).
func (v Version) IsPre() bool { return v.Pre != "" }

// String renders the canonical "v0.7.6" / "v0.7.6-rc.1" form.
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or +1 ordering v against o. A pre-release sorts BELOW
// the same x.y.z release (0.7.6-rc.1 < 0.7.6), per semver §11.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sign(v.Major - o.Major)
	case v.Minor != o.Minor:
		return sign(v.Minor - o.Minor)
	case v.Patch != o.Patch:
		return sign(v.Patch - o.Patch)
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "": // release beats pre-release
		return 1
	case o.Pre == "":
		return -1
	}
	return strings.Compare(v.Pre, o.Pre)
}

// Newer reports whether o is a release strictly newer than v. Pre-releases are
// never "newer" for update purposes: an install must not drift onto an rc
// because someone tagged one.
func (v Version) Newer(o Version) bool {
	if o.IsPre() {
		return false
	}
	return v.Compare(o) < 0
}

// Step names the largest version component that moved between v and o.
type Step string

const (
	StepNone  Step = "none"  // same version, or o is older
	StepPatch Step = "patch" // z moved
	StepMinor Step = "minor" // y moved
	StepMajor Step = "major" // x moved
)

// StepTo classifies the jump from v to o.
func (v Version) StepTo(o Version) Step {
	if !v.Newer(o) {
		return StepNone
	}
	switch {
	case o.Major != v.Major:
		return StepMajor
	case o.Minor != v.Minor:
		return StepMinor
	default:
		return StepPatch
	}
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	if n > 0 {
		return 1
	}
	return 0
}

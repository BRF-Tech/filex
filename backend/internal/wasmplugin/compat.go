package wasmplugin

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/update"
)

// ── Which filex an app works with ──────────────────────────────────────
//
// An app says which filex versions it works with in `filex`
// (wire.Manifest.Filex), a range written as a small, standard subset of the
// npm/Cargo syntax:
//
//	range       := alternative ( "||" alternative )*
//	alternative := comparator ( " " comparator )*      every one must hold
//	comparator  := [op] version                        op: >= > <= < =
//	version     := MAJOR.MINOR.PATCH                    a leading "v" is fine
//
//	">=0.47.0"                   0.47.0 and everything after it
//	">=0.47.0 <0.60.0"           0.47.0 up to, not including, 0.60.0
//	">=0.47.0 <0.50.0 || >=0.52.0"
//
// No `^`, `~`, `x` wildcards or pre-release versions: `^0.47.0` means
// ">=0.47.0 <0.48.0" in npm, which is not what an author writing it for
// filex 0.47 usually means, and a grammar nobody has to look up is worth more
// than a shorter string. The older `min_filex` ("0.43.0" = ">=0.43.0") is
// still honoured, together with `filex` (both must hold).
//
// ⚠⚠ Which filex is "running" (hostRelease):
//
//   - A release (`0.47.0`) and a named pre-release of one (`0.47.0-rc.1`,
//     `-beta.2`, `-alpha`) count as that release's x.y.z. A release candidate
//     is tested with the apps written for the release it becomes; semver
//     would rank 0.47.0-rc.1 BELOW 0.47.0 and lock every ">=0.47.0" app out
//     of exactly the build that has to prove them.
//   - Anything else — `0.1.0-dev` (an unstamped build), a `git describe`
//     suffix, a version that does not parse — is a DEVELOPMENT build, and
//     ranges are not enforced there at all: a developer running filex from
//     source must be able to install the app they are writing, which declares
//     the release it will ship for. The Apps screen says so
//     (runtime.compat_enforced = false) rather than pretending to check.

// filexComparator is one `op version` of a range.
type filexComparator struct {
	op string // ">=", ">", "<=", "<", "="
	v  update.Version
}

func (c filexComparator) holds(host update.Version) bool {
	d := host.Compare(c.v)
	switch c.op {
	case ">=":
		return d >= 0
	case ">":
		return d > 0
	case "<=":
		return d <= 0
	case "<":
		return d < 0
	default:
		return d == 0
	}
}

// filexRange is a parsed range: any alternative whose comparators all hold.
// The zero value (no alternatives) admits every filex.
type filexRange struct {
	alts [][]filexComparator
	// text is the range as a person reads it on the Apps screen.
	text string
}

// admits reports whether the range lets host in.
func (r filexRange) admits(host update.Version) bool {
	if len(r.alts) == 0 {
		return true
	}
	for _, alt := range r.alts {
		ok := true
		for _, c := range alt {
			if !c.holds(host) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

var filexOps = []string{">=", "<=", ">", "<", "="}

// parseFilexVersion reads one x.y.z of a range (no pre-release).
func parseFilexVersion(s string) (update.Version, error) {
	v, err := update.ParseVersion(s)
	if err != nil {
		return update.Version{}, fmt.Errorf("%q is not a version (write it as MAJOR.MINOR.PATCH, e.g. 0.47.0)", s)
	}
	if v.Pre != "" {
		return update.Version{}, fmt.Errorf("%q: a range names releases, not pre-releases", s)
	}
	return v, nil
}

// parseFilexRange reads the `filex` field. Empty = no constraint.
func parseFilexRange(s string) (filexRange, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return filexRange{}, nil
	}
	out := filexRange{text: strings.Join(strings.Fields(s), " ")}
	for _, altText := range strings.Split(s, "||") {
		fields := strings.Fields(altText)
		if len(fields) == 0 {
			return filexRange{}, fmt.Errorf("%q has an empty alternative around ||", s)
		}
		var alt []filexComparator
		for i := 0; i < len(fields); i++ {
			tok := fields[i]
			op := ""
			for _, o := range filexOps {
				if strings.HasPrefix(tok, o) {
					op = o
					break
				}
			}
			ver := strings.TrimPrefix(tok, op)
			if ver == "" {
				// ">= 0.47.0": the operator and its version written apart.
				if i+1 >= len(fields) {
					return filexRange{}, fmt.Errorf("%q ends with an operator and no version", s)
				}
				i++
				ver = fields[i]
			}
			if op == "" {
				op = "="
			}
			v, err := parseFilexVersion(ver)
			if err != nil {
				return filexRange{}, err
			}
			alt = append(alt, filexComparator{op: op, v: v})
		}
		out.alts = append(out.alts, alt)
	}
	return out, nil
}

// compatRange is everything a manifest says about which filex it needs:
// `filex` and the older `min_filex`, both of which must hold.
func compatRange(m *Manifest) (filexRange, error) {
	rng, err := parseFilexRange(m.Filex)
	if err != nil {
		return filexRange{}, fmt.Errorf("manifest: filex: %w", err)
	}
	floorText := strings.TrimPrefix(strings.TrimSpace(m.MinFilex), "v")
	if floorText == "" {
		return rng, nil
	}
	v, err := parseFilexVersion(floorText)
	if err != nil {
		return filexRange{}, fmt.Errorf("manifest: min_filex: %w", err)
	}
	floor := filexComparator{op: ">=", v: v}
	if len(rng.alts) == 0 {
		return filexRange{alts: [][]filexComparator{{floor}}, text: ">=" + floorText}, nil
	}
	for i := range rng.alts {
		rng.alts[i] = append(rng.alts[i], floor)
	}
	rng.text += ", >=" + floorText
	return rng, nil
}

// hostRelease is the running filex as x.y.z, and whether ranges are enforced
// on it at all (false on a development build — see the note at the top).
func hostRelease() (update.Version, bool) {
	f := strings.Fields(HostVersion)
	if len(f) == 0 {
		return update.Version{}, false
	}
	v, err := update.ParseVersion(f[0])
	if err != nil {
		return update.Version{}, false
	}
	if v.Pre != "" {
		named := false
		for _, p := range []string{"rc", "beta", "alpha"} {
			if v.Pre == p || strings.HasPrefix(v.Pre, p+".") {
				named = true
				break
			}
		}
		if !named {
			return update.Version{}, false
		}
		v.Pre = ""
	}
	v.Raw = fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	return v, true
}

// CompatEnforced reports whether this filex checks app ranges at all (false on
// a development build).
func CompatEnforced() bool {
	_, ok := hostRelease()
	return ok
}

// FilexVersion is the running filex as the Apps screen names it: x.y.z of a
// release, or the build's own version string on a development build.
func FilexVersion() string {
	if v, ok := hostRelease(); ok {
		return v.Raw
	}
	if f := strings.Fields(HostVersion); len(f) > 0 {
		return strings.TrimPrefix(f[0], "v")
	}
	return ""
}

// Compat is what the Apps screens say about an app's range: the range as
// written, whether the running filex is in it, and which filex that is.
// Absent when the manifest names none.
type Compat struct {
	Requires string `json:"requires"`
	OK       bool   `json:"ok"`
	Filex    string `json:"filex"`
	// Message is the install review's sentence for a range that leaves this
	// filex out, in the reader's language (Said); empty on a list row and
	// on a range this filex is in. The wizard prints it as it came.
	Message string `json:"message,omitempty"`
}

// Said fills Message for the review of name at version, in lang, when the
// range leaves this filex out (server.install.review_incompatible), and
// answers c. ⚠ The sentence is the server's: the wizard used to build it
// from requires and filex with a copy of its own (0.55).
func (c *Compat) Said(lang, name, version string) *Compat {
	if c == nil || c.OK {
		return c
	}
	c.Message = srvtext.Text(srvtext.Pick(lang), "server.install.review_incompatible",
		srvtext.Vars{"name": name, "version": version, "requires": c.Requires, "filex": c.Filex})
	return c
}

// compatOf is the manifest's range judged against the running filex. Nil when
// it names none. A range that does not parse (only possible for a row
// installed before ranges were read — a new install refuses it) is shown
// as written and not held against the app.
func compatOf(m *Manifest) *Compat {
	if strings.TrimSpace(m.Filex) == "" && strings.TrimSpace(m.MinFilex) == "" {
		return nil
	}
	rng, err := compatRange(m)
	if err != nil {
		return &Compat{Requires: strings.TrimSpace(m.Filex + " " + m.MinFilex), OK: true, Filex: FilexVersion()}
	}
	host, enforced := hostRelease()
	return &Compat{Requires: rng.text, OK: !enforced || rng.admits(host), Filex: FilexVersion()}
}

// JudgeFilexRange reads a bare `filex` range the way an app's is read and
// judges it against the running filex — for a storage plugin's feed
// (plugin/updates.go), so there is one range grammar in filex, not two. ok is
// true on a development build (ranges are not enforced there) and for an
// empty range; err when the range does not parse.
func JudgeFilexRange(rng string) (ok bool, requires, filex string, err error) {
	r, err := parseFilexRange(rng)
	if err != nil {
		return false, strings.TrimSpace(rng), FilexVersion(), err
	}
	host, enforced := hostRelease()
	return !enforced || r.admits(host), r.text, FilexVersion(), nil
}

// refuseIncompatible is the install and upgrade gate: an app whose range
// leaves the running filex out is not installed.
func refuseIncompatible(m *Manifest) error {
	c := compatOf(m)
	if c == nil || c.OK {
		return nil
	}
	return &InstallError{
		Code:     ErrCodeIncompatible,
		Message:  m.Name + " " + m.Version + " works with filex " + c.Requires + "; this is filex " + FilexVersion(),
		Requires: c.Requires, Filex: FilexVersion(),
	}
}

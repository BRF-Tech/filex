// Package scanrule is the ONE answer to "does the scan walk this path of this
// storage?" (issue #44).
//
// Two things say no, and they are composed here rather than beside each other:
//
//   - filex's own trees — the trash, the version history, the thumbnail cache
//     (syspath.Sealed). Always, on every storage; nobody configures them.
//   - the storage's own exclusions: glob patterns an operator writes into the
//     storage's `scan_exclude` setting (storage.ScanExcludeKey) for the parts
//     of a big existing tree filex has no business cataloguing — `.git`,
//     `.snapshots`, a download client's `incomplete/`, `*.tmp`.
//
// The third thing a walk turns away — named pipes, sockets and device nodes —
// never reaches this far: the local driver drops them from its listing
// (internal/regfile), so a walk cannot even see one to ask about it.
//
// ⚠⚠ Scan-only, and deliberately so. A path this package excludes is not
// walked, catalogued, indexed, thumbnailed or virus-scanned, and the fsnotify
// watcher does not watch it. It is NOT hidden: it is still on the storage, and
// everything that reads the storage directly — the file protocols (WebDAV,
// SFTP, FTP, NFS, S3), the AI/MCP tools, a public folder share, an archive
// download, a copy — still sees it. Nor are the byte-moving walks (copy, move,
// delete, archive) taught to skip it, and they must never be: a folder move
// that copied everything but the excluded part and then deleted the source
// would delete the excluded part. So this is a cost control, not an access
// control, and the storage form's help text says exactly that.
//
// ⚠ filex's own names are outside the operator's reach: a pattern never
// matches anything syspath.Hidden (`.filex-open` working copies, `.keepdir`)
// nor an encrypted folder's marker (e2e.MarkerName). `.*` is the pattern
// people write first, and it would otherwise take both away from the catalogue
// — the desktop's "open with" round trip and every encrypted folder's lock
// screen read their rows there.
package scanrule

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// Limits on what one storage may configure — a textarea, not a database.
const (
	MaxPatterns      = 200
	MaxPatternLength = 512
)

// Rule is one storage's scan rule. The zero value and a nil *Rule are both
// valid and exclude nothing of the operator's: only filex's own trees.
type Rule struct {
	patterns []pattern
}

type pattern struct {
	raw string
	// segs are the pattern's path segments; a segment that is exactly "**"
	// matches zero or more whole segments.
	segs []string
	// floating: the pattern had no "/" (other than a trailing one), so it
	// names an entry at any depth — `.git`, `*.tmp` — the way .gitignore
	// reads it.
	floating bool
}

// ErrInvalid wraps every reason a pattern is refused.
var ErrInvalid = errors.New("invalid scan exclusion")

// Problem says which rule a refused pattern broke. The admin API turns it
// into a sentence in the reader's language (`server.storage.scan_exclude_*`),
// so the three are the whole list a translator sees.
type Problem string

const (
	// ProblemSyntax: not a pattern filex can use — `!`, `..`, a broken
	// character class, a NUL byte, a value that is not text.
	ProblemSyntax Problem = "syntax"
	// ProblemEverything: it matches every name there is, so it would leave
	// the storage empty.
	ProblemEverything Problem = "everything"
	// ProblemLimit: more than MaxPatterns patterns, or one longer than
	// MaxPatternLength.
	ProblemLimit Problem = "limit"
)

// InvalidError is a refused setting: which pattern, which rule it broke, and
// an English detail for logs and API callers. errors.Is(err, ErrInvalid)
// finds it.
type InvalidError struct {
	// Pattern is the line as written; empty when the setting as a whole was
	// refused (too many patterns).
	Pattern string
	Problem Problem
	detail  string
}

func (e *InvalidError) Error() string {
	if e.Pattern == "" {
		return ErrInvalid.Error() + ": " + e.detail
	}
	return fmt.Sprintf("%s %q: %s", ErrInvalid, e.Pattern, e.detail)
}

func (e *InvalidError) Unwrap() error { return ErrInvalid }

// FromConfig reads a storage config map's scan exclusions. A missing or empty
// setting is a rule that excludes nothing.
func FromConfig(cfg map[string]any) (*Rule, error) {
	v, ok := storage.ConfigLookup(cfg, storage.ScanExcludeKey)
	if !ok {
		return &Rule{}, nil
	}
	return Parse(v)
}

// Parse reads the setting's value: a string, one pattern per line (what the
// storage form sends), or a list of strings (what an API caller may send).
// Blank lines and lines starting with `#` are skipped.
func Parse(v any) (*Rule, error) {
	var lines []string
	switch x := v.(type) {
	case nil:
	case string:
		lines = strings.Split(strings.ReplaceAll(x, "\r\n", "\n"), "\n")
	case []string:
		lines = x
	case []any:
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, &InvalidError{Pattern: fmt.Sprint(e), Problem: ProblemSyntax,
					detail: "every entry of " + storage.ScanExcludeKey + " must be a string"}
			}
			lines = append(lines, s)
		}
	default:
		return nil, &InvalidError{Pattern: fmt.Sprint(v), Problem: ProblemSyntax,
			detail: storage.ScanExcludeKey + " must be text, one pattern per line"}
	}
	r := &Rule{}
	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		p, err := compile(raw)
		if err != nil {
			return nil, err
		}
		r.patterns = append(r.patterns, p)
		if len(r.patterns) > MaxPatterns {
			return nil, &InvalidError{Problem: ProblemLimit, detail: fmt.Sprintf("at most %d patterns", MaxPatterns)}
		}
	}
	return r, nil
}

// probes are three root entries with nothing in common — no shared first
// character, one with an extension, one without, one a digit. A pattern that
// matches all three matches every name there is, and is refused.
var probes = []string{"Zq7", "a.b", "0"}

func compile(raw string) (pattern, error) {
	bad := func(why string) (pattern, error) {
		return pattern{}, &InvalidError{Pattern: raw, Problem: ProblemSyntax, detail: why}
	}
	if len(raw) > MaxPatternLength {
		return pattern{}, &InvalidError{Pattern: raw, Problem: ProblemLimit,
			detail: fmt.Sprintf("longer than %d characters", MaxPatternLength)}
	}
	if strings.ContainsRune(raw, 0) {
		return bad("contains a NUL byte")
	}
	if strings.HasPrefix(raw, "!") {
		return bad(`"!" (re-including a path) is not supported`)
	}
	// A leading "/" or "./" anchors the pattern at the storage root.
	p := raw
	anchored := strings.HasPrefix(p, "/") || strings.HasPrefix(p, "./")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	p = strings.Trim(p, "/")
	var segs []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return bad(`".." cannot be part of a pattern`)
		}
		// `[!…]` negates a class in a shell and in .gitignore; Go's path.Match
		// spells it `[^…]` and would read `!` as a member.
		seg = strings.ReplaceAll(seg, "[!", "[^")
		if _, err := path.Match(seg, ""); err != nil {
			return bad("not a valid glob (" + err.Error() + ")")
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return bad("names no path")
	}
	out := pattern{raw: raw, segs: segs, floating: !anchored && len(segs) == 1 && segs[0] != "**"}
	all := true
	for _, name := range probes {
		if !out.matches([]string{name}) {
			all = false
			break
		}
	}
	if all {
		return pattern{}, &InvalidError{Pattern: raw, Problem: ProblemEverything,
			detail: "it would exclude everything on the storage"}
	}
	return out, nil
}

// matches reports whether the pattern names exactly segs — one path, not the
// paths below it (Excluded walks the prefixes).
func (p pattern) matches(segs []string) bool {
	if p.floating {
		if len(segs) == 0 {
			return false
		}
		ok, _ := path.Match(p.segs[0], segs[len(segs)-1])
		return ok
	}
	return matchSegs(p.segs, segs)
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for len(rest) > 0 && rest[0] == "**" {
				rest = rest[1:]
			}
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// Patterns returns the patterns as written, in order. A copy.
func (r *Rule) Patterns() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.patterns))
	for i, p := range r.patterns {
		out[i] = p.raw
	}
	return out
}

// Empty reports whether the rule has no patterns of the operator's.
func (r *Rule) Empty() bool { return r == nil || len(r.patterns) == 0 }

// Skips reports whether the scan must not walk rel: it is inside filex's own
// trees, or the storage's exclusions match it or a folder above it. Every walk
// that catalogues asks this one question — the full scan, the one-pass
// prefetch, a folder rescan, the copy mirror, the fsnotify watcher — and so
// does the delete pass, which must never judge a row the walk cannot see.
func (r *Rule) Skips(rel string) bool {
	return syspath.Sealed(rel) || r.Excluded(rel)
}

// Excluded reports whether the storage's own patterns match rel or a folder
// above it. filex's own names never are (see the package comment).
func (r *Rule) Excluded(rel string) bool {
	if r.Empty() {
		return false
	}
	segs := segments(rel)
	if len(segs) == 0 || syspath.Hidden(rel) {
		return false
	}
	for i := range segs {
		if segs[i] == e2e.MarkerName {
			continue
		}
		prefix := segs[:i+1]
		for _, p := range r.patterns {
			if p.matches(prefix) {
				return true
			}
		}
	}
	return false
}

// segments splits a storage path the way syspath does: `a/b`, `/a/b/` and
// `a\b` are the same two segments.
func segments(rel string) []string {
	p := strings.ReplaceAll(strings.TrimSpace(rel), `\`, "/")
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

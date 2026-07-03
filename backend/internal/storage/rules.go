package storage

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// RuleSpec is the declarative shape of one rule. It mirrors
// model.ReplicaRule but lives here so the storage package doesn't
// import model.
type RuleSpec struct {
	ID       int64
	Pattern  string
	Mode     ReplicaMode
	Priority int
	Enabled  bool
}

// rulesEngine implements RuleEngine over an in-memory slice. Rules
// are loaded via SetRules (typically called from Reload) so cache
// invalidation is explicit rather than time-based — admin endpoint
// invokes Reload after each CRUD op.
type rulesEngine struct {
	cache       atomic.Value // []RuleSpec sorted ascending by Priority
	defaultMode ReplicaMode

	// reloadFn is invoked whenever the engine wants a fresh slice.
	// Provided by callers that wire to db.Store; tests can swap it.
	reloadFn func() ([]RuleSpec, ReplicaMode)
	reloadMu sync.Mutex
}

// NewRulesEngine returns an engine that calls reloadFn lazily. If
// reloadFn is nil the engine returns ModeMirror for every path.
func NewRulesEngine(reloadFn func() ([]RuleSpec, ReplicaMode)) RuleEngine {
	e := &rulesEngine{
		defaultMode: ModeMirror,
		reloadFn:    reloadFn,
	}
	e.cache.Store([]RuleSpec(nil))
	if reloadFn != nil {
		_ = e.Reload()
	}
	return e
}

// Match returns the mode for a path. Priority asc, first enabled
// match wins. Empty rule set falls back to defaultMode (mirror by
// default — Burak E2 / SPEC §4.4).
func (e *rulesEngine) Match(path string) ReplicaMode {
	rules, _ := e.cache.Load().([]RuleSpec)
	if len(rules) == 0 {
		return e.defaultMode
	}
	// Normalize to forward slashes so windows-style backslashes don't
	// trip up the matcher.
	p := strings.ReplaceAll(path, "\\", "/")
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		ok := matchPattern(r.Pattern, p)
		if ok {
			return r.Mode
		}
	}
	return e.defaultMode
}

// SetRules replaces the engine's cache. Rules are sorted by Priority
// asc; ties broken by ID asc for determinism.
func (e *rulesEngine) SetRules(rules []RuleSpec, defaultMode ReplicaMode) {
	if defaultMode == "" {
		defaultMode = ModeMirror
	}
	cp := make([]RuleSpec, len(rules))
	copy(cp, rules)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Priority != cp[j].Priority {
			return cp[i].Priority < cp[j].Priority
		}
		return cp[i].ID < cp[j].ID
	})
	e.cache.Store(cp)
	e.defaultMode = defaultMode
}

// Reload fetches the latest rules via reloadFn.
func (e *rulesEngine) Reload() error {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	if e.reloadFn == nil {
		return nil
	}
	rules, def := e.reloadFn()
	e.SetRules(rules, def)
	return nil
}

// Reloader is the optional Reload method exposed by rulesEngine.
type Reloader interface {
	Reload() error
}

// matchPattern is a thin wrapper over filepath.Match that supports
// "**" as a multi-segment wildcard via prefix expansion. (filepath.
// Match alone does single-segment * + character classes only.)
//
// Examples:
//
//	pattern="fileman/sensitive/*"   path="fileman/sensitive/foo.pdf"   → true
//	pattern="fileman/temp/**"       path="fileman/temp/a/b.txt"        → true
//	pattern="*.tmp"                 path="foo.tmp"                     → true
//	pattern="fileman/**/cache/*"    path="fileman/x/y/cache/c.bin"     → true
func matchPattern(pattern, path string) bool {
	// Fast path: literal or simple glob without **.
	if !strings.Contains(pattern, "**") {
		ok, _ := filepath.Match(pattern, path)
		if ok {
			return true
		}
		// filepath.Match on linux/mac is forward-slash-friendly but
		// on windows it may escape "/" — normalize the pattern too.
		ok, _ = filepath.Match(strings.ReplaceAll(pattern, "/", string(filepath.Separator)), strings.ReplaceAll(path, "/", string(filepath.Separator)))
		return ok
	}
	// "**" path: split on "**" and require each chunk to appear in
	// the path in order.
	chunks := strings.Split(pattern, "**")
	pos := 0
	first := true
	for _, c := range chunks {
		c = strings.Trim(c, "/")
		if c == "" {
			first = false
			continue
		}
		// Find c (or a glob-match for c) starting at pos.
		idx := matchAt(c, path, pos, first)
		if idx < 0 {
			return false
		}
		pos = idx + len(c)
		first = false
	}
	return true
}

// matchAt returns the position in path at which subpattern matches,
// honoring '*' as a single-segment wildcard. Anchor at start when
// firstChunk == true; otherwise scan forward.
func matchAt(subpattern, path string, start int, anchor bool) int {
	// Translate subpattern's '*' → regex-ish single-segment match.
	// For simplicity we only support '*' (no character classes here).
	parts := strings.Split(subpattern, "*")
	pos := start
	for i, p := range parts {
		if p == "" {
			if i == 0 {
				continue
			}
			continue
		}
		idx := strings.Index(path[pos:], p)
		if idx < 0 {
			return -1
		}
		if anchor && i == 0 && idx != 0 {
			return -1
		}
		anchor = false
		pos += idx + len(p)
	}
	// We don't validate the trailing remainder belongs to a single
	// segment — good enough for the v0.1 surface.
	return pos - len(parts[len(parts)-1])
}

// DefaultRules returns a no-op engine (everything mirrors). Useful
// when callers haven't wired a real engine yet.
func DefaultRules() RuleEngine { return NewRulesEngine(nil) }

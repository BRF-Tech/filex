package storage

// MaxWalkDepth bounds every recursive walk in filex.
//
// It is the backstop, not the guard: it catches a cyclic tree no driver
// reported a link for — a plugin backend, or a future driver that resolves
// links without saying so — and it catches it at a depth no real storage
// reaches. Deepest directory measured on the storages filex runs against is
// well under 20; a symlink cycle reached depth 81 on Linux and 127 on Windows
// before the OS's own link limit stopped it, so 64 sits clear of the first and
// well inside the second.
const MaxWalkDepth = 64

// CycleGuard stops a recursive walk from following a symlinked directory back
// into a tree it is already inside.
//
// ⚠⚠ Why this had to exist BEFORE symlinked directories became walkable.
// Until v0.43.0 the catalogue walk descended only on Kind == KindDirectory
// and the local driver called every link KindSymlink, so a linked directory
// was catalogued as a childless leaf. That accident — not any guard — was the
// only thing keeping the walk finite. Measured with a wrapper that relabelled
// links as directories, on a root containing `real/cycle -> root`:
//
//	Linux    depth 81,  123 visits
//	Windows  depth 127, 192 visits
//
// before the operating system's symlink limit ended it. Every visit
// re-catalogued the same subtree under a different path, so each pass minted a
// fresh set of rows with distinct path hashes: the walk does not merely spin,
// it fills the catalogue with duplicates of real files.
//
// ⚠ A `seen` set keyed on the LOGICAL path does not help and looks like it
// does. The paths in a cycle are all different — /real, /real/cycle/real,
// /real/cycle/real/cycle/real — which is exactly why kindscan's existing
// seen[dir] map never stopped one.
//
// The key is the driver's own resolved identity for the link (Object.Metadata
// under MetaLinkTarget), so two different paths naming one directory collapse
// to one entry. A driver that cannot resolve targets reports nothing, and such
// a walk is bounded by MaxWalkDepth alone.
//
// ⚠ A link is recorded when the walk DESCENDS into it, not when the root is
// opened, so a cycle is traversed once before it is recognised. That is
// deliberate and it is the cheap half of the trade: a link genuinely is a
// second view of that data, one extra pass is bounded work, and seeding the
// set would need a real identity for the root that only some drivers can give.
type CycleGuard struct {
	seen map[string]bool
}

// NewCycleGuard returns a guard for one walk. It is not safe for concurrent
// use; each walk owns its own.
func NewCycleGuard() *CycleGuard {
	return &CycleGuard{seen: map[string]bool{}}
}

// Enter reports whether a walk at the given depth may descend into o, and
// records o as visited when it may.
//
// depth is the recursion depth about to be entered (the storage root is 0).
func (g *CycleGuard) Enter(o Object, depth int) bool {
	if depth >= MaxWalkDepth {
		return false
	}
	if g == nil {
		return true
	}
	target := o.Metadata[MetaLinkTarget]
	if target == "" {
		// Not a followed link — an ordinary directory cannot lead back into
		// itself, so there is nothing to remember and nothing to refuse.
		return true
	}
	if g.seen[target] {
		return false
	}
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	g.seen[target] = true
	return true
}

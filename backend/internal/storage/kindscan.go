package storage

import (
	"context"
	"path"
	"sort"
)

// Collision is one path that exists as BOTH a file and a folder.
type Collision struct {
	Path string `json:"path"`
	// Dir is the listing the pair was found in ("" = storage root).
	Dir string `json:"dir"`
}

// ScanKindCollisions walks root and reports every name that a listing returns
// twice — once as a file, once as a folder.
//
// This finds damage that predates the write guards (EnsureFileTarget /
// EnsureDirTarget): an object store accepted `X` and `X/…` side by side, and
// nothing surfaced it. The consequences were only visible downstream — a DR
// mirror that re-copied the prefix on every run and, worse, a prefix whose
// contents no longer listed at all, so its objects were silently unbacked.
//
// Detection is cheap because the collision is visible in a single listing: S3
// returns `X` in Contents and `X/` in CommonPrefixes, so the driver yields two
// entries with the same name and different kinds. No full key enumeration is
// needed.
//
// A directory that fails to list is skipped rather than aborting the scan —
// a colliding prefix is exactly the thing that may refuse to list, and giving
// up there would hide the rest of the storage.
func ScanKindCollisions(ctx context.Context, d Driver, root string) ([]Collision, error) {
	if d == nil {
		return nil, nil
	}
	var out []Collision
	seen := map[string]bool{}
	// ⚠ seen above is keyed on the LOGICAL path and cannot stop a symlink
	// cycle: `real/cycle -> root` yields /real, /real/cycle/real,
	// /real/cycle/real/cycle/real … all different strings. The guard keys on
	// the driver's resolved target instead. Both are kept — the logical map
	// still collapses a directory reached twice by the same name.
	guard := NewCycleGuard()

	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if seen[dir] {
			return nil
		}
		seen[dir] = true

		entries, err := d.List(ctx, dir)
		if err != nil {
			// Unlistable directory: report nothing, keep walking elsewhere.
			return nil //nolint:nilerr // deliberate — see doc comment
		}

		kinds := map[string]map[ObjectKind]bool{}
		var subdirs []Object
		for _, e := range entries {
			name := e.Name
			if name == "" {
				name = path.Base(e.Path)
			}
			if name == "" || name == "." || name == "/" {
				continue
			}
			if kinds[name] == nil {
				kinds[name] = map[ObjectKind]bool{}
			}
			kinds[name][e.Kind] = true
			if e.Kind == KindDirectory {
				e.Path = path.Join(dir, name)
				subdirs = append(subdirs, e)
			}
		}
		for name, ks := range kinds {
			if ks[KindDirectory] && (ks[KindFile] || ks[KindSymlink]) {
				out = append(out, Collision{Path: path.Join(dir, name), Dir: dir})
			}
		}
		for _, sd := range subdirs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !guard.Enter(sd, depth+1) {
				continue
			}
			if err := walk(sd.Path, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(root, 0); err != nil {
		return out, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

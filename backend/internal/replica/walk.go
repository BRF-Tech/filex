package replica

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// errSliceOver stops a walk at the end of a slice of work; the caller keeps
// the cursor and comes back.
var errSliceOver = errors.New("replica: slice over")

// walkFiles visits every file on d below dir in ONE fixed order - the names in
// a folder sorted byte by byte, a folder's contents at the folder's own place
// - and skips every file at or before `after` in that order. That is what lets
// a copy stop anywhere and resume from the last file it finished, across a
// restart, with only the folders on the way to `after` listed again.
//
// filex's own folders (the trash, version history, ... storage.InternalPath)
// are not entered at all: replication leaves them out.
//
// Memory is one folder listing per level of depth, never the storage: a walk
// over a million files holds the folders on the current path and nothing else.
// A followed directory link is entered once (storage.CycleGuard), and the
// depth is bounded (storage.MaxWalkDepth).
//
// Paths passed to visit are absolute ("/a/b.txt"), as the catalogue writes
// them. visit returning an error stops the walk with that error.
func walkFiles(ctx context.Context, d storage.Driver, after string, visit func(storage.Object) error) error {
	return walkDir(ctx, d, "/", after, storage.NewCycleGuard(), 0, visit)
}

func walkDir(ctx context.Context, d storage.Driver, dir, after string, guard *storage.CycleGuard, depth int, visit func(storage.Object) error) error {
	objs, err := d.List(ctx, dir)
	if err != nil {
		// A folder that went away between its parent's listing and its own
		// has nothing left to copy.
		if errors.Is(err, storage.ErrNotFound) && dir != "/" {
			return nil
		}
		return err
	}
	type entry struct {
		name string
		obj  storage.Object
	}
	entries := make([]entry, 0, len(objs))
	for _, o := range objs {
		name := o.Name
		if name == "" {
			name = path.Base(strings.TrimRight(o.Path, "/"))
		}
		if name == "" || name == "." || name == "/" {
			continue
		}
		entries = append(entries, entry{name: name, obj: o})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := path.Join(dir, e.name)
		if storage.InternalPath(p) {
			continue // filex's own folders are never replicated, nor counted
		}
		switch e.obj.Kind {
		case storage.KindDirectory:
			if after != "" && walkBefore(p, after) && !isAncestor(p, after) {
				continue // everything in it was finished before
			}
			if !guard.Enter(e.obj, depth+1) {
				continue
			}
			if err := walkDir(ctx, d, p, after, guard, depth+1, visit); err != nil {
				return err
			}
		case storage.KindFile:
			if after != "" && !walkBefore(after, p) {
				continue // at or before the cursor: done in an earlier slice
			}
			o := e.obj
			o.Path = p
			o.Name = e.name
			if err := visit(o); err != nil {
				return err
			}
		default:
			// A link the driver could not follow: nothing it could copy.
		}
	}
	return nil
}

// walkBefore reports whether a comes strictly before b in walkFiles' order:
// segment by segment, byte order within a segment, and a folder before what
// is inside it.
func walkBefore(a, b string) bool {
	as, bs := segs(a), segs(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// isAncestor reports whether dir is a folder above p.
func isAncestor(dir, p string) bool {
	d := strings.TrimRight(dir, "/")
	return strings.HasPrefix(p, d+"/") && len(p) > len(d)+1
}

func segs(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

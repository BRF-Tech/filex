package filesync

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// RemoteLister is the slice of the API client this package needs. Declaring it
// here rather than importing cliclient.Client keeps the planner and the walkers
// testable with a fake, and stops this package from growing a dependency on
// every REST call the CLI happens to support.
type RemoteLister interface {
	List(ctx context.Context, remote string) (*Listing, error)
}

// Listing is one remote directory. It mirrors the server's index response,
// reduced to what sync actually reads.
type Listing struct {
	Files []ListedFile
}

// ListedFile is one row of a remote listing.
type ListedFile struct {
	Basename     string
	IsDir        bool
	Size         int64
	LastModified int64 // Unix millis; 0 = the storage does not report one
}

// skipNames are never synced in either direction.
//
// ⚠ The state directory has to be excluded or the engine syncs its own
// bookkeeping, which then changes, which schedules another sync — a loop that
// grows a file on the server forever.
var skipNames = map[string]bool{
	".filex-sync":               true, // this engine's own state + trash
	".DS_Store":                 true,
	"Thumbs.db":                 true,
	"desktop.ini":               true,
	".Trash-1000":               true,
	"$RECYCLE.BIN":              true,
	"System Volume Information": true,
}

// WalkLocal snapshots a directory tree. Unreadable entries are skipped rather
// than failing the run: one locked file must not stop the other thousand from
// syncing. The names of skipped entries are returned so the caller can report
// them instead of silently doing less than it claimed.
func WalkLocal(root string) (Snapshot, []string, error) {
	out := Snapshot{}
	var skipped []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read: note it and carry on.
			skipped = append(skipped, relOf(root, p))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if skipNames[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Symlinks are not followed. A link pointing outside the pair would
		// upload files the user never put in the folder, and a link pointing
		// inside it makes the walk infinite.
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, relOf(root, p))
			return nil
		}
		rel := relOf(root, p)
		if rel == "" {
			return nil
		}
		if d.IsDir() {
			out[rel] = Node{Rel: rel, IsDir: true}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			skipped = append(skipped, rel)
			return nil
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, rel)
			return nil
		}
		out[rel] = Node{
			Rel:       rel,
			Size:      info.Size(),
			ModMillis: info.ModTime().UnixMilli(),
		}
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	sort.Strings(skipped)
	return out, skipped, nil
}

func relOf(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// WalkRemote snapshots a server folder by listing it depth-first.
//
// remoteRoot is an `adapter://path` prefix; every returned Node.Rel is relative
// to it, so local and remote snapshots share one key space and the planner can
// compare them directly.
//
// progress, when non-nil, is called after each listed directory with the
// running totals. One network round-trip per folder makes this walk the slow
// phase of a big first sync, and the only one with nothing else to show.
func WalkRemote(ctx context.Context, api RemoteLister, remoteRoot string, progress func(dirsListed, itemsSeen int)) (Snapshot, error) {
	out := Snapshot{}
	dirsListed := 0
	// Iterative rather than recursive: a deep tree on the server should not be
	// able to exhaust this process's stack.
	queue := []string{""}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]

		res, err := api.List(ctx, joinRemote(remoteRoot, rel))
		if err != nil {
			if rel == "" {
				return nil, fmt.Errorf("list %s: %w", remoteRoot, err)
			}
			// A folder that vanished mid-walk is not fatal; the next run sees it.
			continue
		}
		dirsListed++
		for _, f := range res.Files {
			if skipNames[f.Basename] {
				continue
			}
			childRel := f.Basename
			if rel != "" {
				childRel = rel + "/" + f.Basename
			}
			if f.IsDir {
				out[childRel] = Node{Rel: childRel, IsDir: true}
				queue = append(queue, childRel)
				continue
			}
			out[childRel] = Node{
				Rel:       childRel,
				Size:      f.Size,
				ModMillis: f.LastModified,
			}
		}
		if progress != nil {
			progress(dirsListed, len(out))
		}
	}
	return out, nil
}

// joinRemote appends a pair-relative path to an `adapter://root` prefix.
func joinRemote(root, rel string) string {
	if rel == "" {
		return root
	}
	if strings.HasSuffix(root, "/") {
		return root + rel
	}
	return root + "/" + rel
}

// localPathOf resolves a pair-relative path against the local root and refuses
// anything that would land outside it.
//
// ⚠ Names come from the SERVER. A listing carrying `..` or an absolute path
// would otherwise let a compromised or buggy server write anywhere on the disk
// of everyone syncing with it.
func localPathOf(root, rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("refusing suspicious path %q", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("refusing suspicious path %q", rel)
		}
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	// Belt and braces: even with the segment check above, confirm the result is
	// still under the root after Join has cleaned it.
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing path outside the sync folder: %q", rel)
	}
	return full, nil
}

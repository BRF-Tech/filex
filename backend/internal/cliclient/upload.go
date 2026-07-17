// upload.go — recursive (tree) upload on top of the single-file Upload
// core in api.go. Only existing server verbs are used: `newfolder` to
// materialize directories (empty ones included) and the streaming
// multipart `upload` for files. Symlinks are never followed — they are
// reported and skipped, so a cyclic link can't wedge the walk.
package cliclient

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Tree event kinds delivered to a TreeProgress callback.
const (
	TreeDir     = "dir"     // remote directory ensured (created or existing)
	TreeFile    = "file"    // file uploaded
	TreeSymlink = "symlink" // local symlink skipped
	TreeErr     = "error"   // one item failed (walk continues)
)

// TreeEvent is one progress notification from UploadTree.
type TreeEvent struct {
	Kind   string     // TreeDir | TreeFile | TreeSymlink | TreeErr
	Local  string     // local path of the item
	Remote RemotePath // resolved remote target (zero for symlink/local errors)
	Err    error      // set when Kind == TreeErr
}

// TreeProgress receives one call per processed item during UploadTree —
// the CLI streams these as they happen. May be nil.
type TreeProgress func(ev TreeEvent)

// TreeError is one failed item of a recursive upload.
type TreeError struct {
	Local string
	Err   error
}

// TreeReport summarizes a finished UploadTree run. Errors are per-item:
// the walk keeps going past a failed file, and a failed mkdir skips just
// that subtree.
type TreeReport struct {
	Files    int         // files uploaded successfully
	Dirs     int         // remote directories ensured (created or pre-existing)
	Symlinks []string    // local symlinks skipped (never followed)
	Errors   []TreeError // items that failed (upload, mkdir, walk)
}

// relRemote maps the local path p (inside walk root `root`) onto the
// remote destination rooted at destRoot. Pure path computation — kept
// separate so it unit-tests without a server.
func relRemote(destRoot RemotePath, root, p string) (RemotePath, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return RemotePath{}, err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return destRoot, nil
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return RemotePath{}, fmt.Errorf("%s is outside the upload root", p)
	}
	return destRoot.Join(rel), nil
}

// treeDestRoot resolves where the uploaded tree lands, mirroring the
// single-file Upload semantics: an existing remote folder target (or a
// storage root / trailing slash) receives the local folder BY NAME;
// otherwise the remote path itself becomes the new folder (rename form).
func (c *Client) treeDestRoot(ctx context.Context, localDir, remote string, rp RemotePath) (RemotePath, error) {
	if !rp.IsRoot() && !strings.HasSuffix(remote, "/") && !c.remoteIsDir(ctx, rp) {
		return rp, nil
	}
	abs, err := filepath.Abs(localDir)
	if err != nil {
		return RemotePath{}, err
	}
	base := filepath.Base(abs)
	if base == "." || base == string(filepath.Separator) || base == "/" {
		return RemotePath{}, fmt.Errorf("cannot derive a folder name from %s — give a full remote target instead", localDir)
	}
	return rp.Join(base), nil
}

// ensureRemoteDir makes target exist as a directory. The probe-first
// order keeps mkdir off directories that already exist (the server
// rejects duplicate folders).
func (c *Client) ensureRemoteDir(ctx context.Context, target RemotePath) error {
	if target.IsRoot() || c.remoteIsDir(ctx, target) {
		return nil
	}
	_, err := c.Mkdir(ctx, target.String())
	return err
}

// UploadTree uploads localDir recursively into remote. Empty local
// folders are created remotely too. Per-item failures accumulate in the
// report (a failed mkdir skips its subtree); only pre-flight problems
// (bad remote path, unreadable root) and context cancellation return a
// non-nil error. A plain file with -r degrades to a single upload,
// matching `cp -r` behavior.
func (c *Client) UploadTree(ctx context.Context, localDir, remote string, progress TreeProgress) (*TreeReport, error) {
	if progress == nil {
		progress = func(TreeEvent) {}
	}
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(localDir)
	if err != nil {
		return nil, err
	}
	rep := &TreeReport{}

	if !fi.IsDir() {
		dest, _, err := c.Upload(ctx, localDir, remote)
		if err != nil {
			rep.Errors = append(rep.Errors, TreeError{Local: localDir, Err: err})
			progress(TreeEvent{Kind: TreeErr, Local: localDir, Err: err})
			return rep, nil
		}
		rep.Files++
		progress(TreeEvent{Kind: TreeFile, Local: localDir, Remote: dest})
		return rep, nil
	}

	destRoot, err := c.treeDestRoot(ctx, localDir, remote, rp)
	if err != nil {
		return nil, err
	}

	fail := func(local string, target RemotePath, err error) {
		rep.Errors = append(rep.Errors, TreeError{Local: local, Err: err})
		progress(TreeEvent{Kind: TreeErr, Local: local, Remote: target, Err: err})
	}

	walkErr := filepath.WalkDir(localDir, func(p string, d fs.DirEntry, werr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if werr != nil {
			fail(p, RemotePath{}, werr)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			rep.Symlinks = append(rep.Symlinks, p)
			progress(TreeEvent{Kind: TreeSymlink, Local: p})
			return nil
		}
		target, err := relRemote(destRoot, localDir, p)
		if err != nil {
			fail(p, RemotePath{}, err)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if err := c.ensureRemoteDir(ctx, target); err != nil {
				fail(p, target, err)
				return fs.SkipDir // uploads into a missing folder would only cascade
			}
			rep.Dirs++
			progress(TreeEvent{Kind: TreeDir, Local: p, Remote: target})
			return nil
		}
		if _, err := c.uploadFile(ctx, target.Dir(), target.Base(), p); err != nil {
			fail(p, target, err)
			return nil
		}
		rep.Files++
		progress(TreeEvent{Kind: TreeFile, Local: p, Remote: target})
		return nil
	})
	if walkErr != nil {
		return rep, walkErr
	}
	return rep, nil
}

package thumb

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ───────────────────── the file a thumbnail program reads ─────────────────────
//
// ffmpeg and ghostscript read a path, not a Go reader. Until 0.56.2 every
// generator that runs one copied the WHOLE source into the system temp
// directory first, whatever the source was. Issue #114 measured what that
// costs on a folder of films on a CIFS mount: a 7 GB copy within 17 s of
// opening the folder and 75% iowait, to draw one frame ffmpeg finds in the
// first few megabytes. The copy also outlived a restart, because a deferred
// remove does not run when the process is killed (14 GB of them on one
// install, on 0.53).
//
// Two rules now:
//
//   - a local storage's file is handed over IN PLACE. It is opened here,
//     through the driver's own root, symlink and regular-file checks, and the
//     program inherits it as a descriptor, so it reads that file and only the
//     bytes it asks for. Not the path: a program looks a path up again, and
//     what it finds then is not what was checked.
//   - anything else (an object store, a remote share, a staged upload) is
//     still copied, into the pipeline's own work directory, which ClearWork
//     empties at startup.

// workDirName is the pipeline's scratch directory, inside its cache
// directory: one server per data directory, so emptying it at startup never
// takes a file from under another process.
const workDirName = ".work"

// inheritedPath is how a program on a unix names the descriptor it inherits
// as ExtraFiles[0]. Opening it reopens the same file (Linux) or duplicates
// the descriptor (macOS); either way it is the file that was checked, and it
// seeks.
const inheritedPath = "/dev/fd/3"

// input is a source file as a program reads it. Close it when the program is
// done.
type input struct {
	path string   // what the program is told to open
	file *os.File // the local file handed over in place; nil for a copy
	tmp  string   // the copy to remove afterwards; "" when there is none
}

// pathInput is a file that is already on disk under a path (the office
// converter's PDF, a test fixture).
func pathInput(path string) input { return input{path: path} }

// command builds a program's command with the source attached.
//
// ⚠ The offset goes back to 0 before every run. On macOS the program's
// /dev/fd/3 is a duplicate of this descriptor and shares its offset, and
// ffmpeg starts probing wherever the descriptor stands - so the second pass
// of extractFirstFrame would open a file positioned where the first one
// stopped reading.
func (in input) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if in.file != nil && in.path == inheritedPath {
		_, _ = in.file.Seek(0, io.SeekStart)
		cmd.ExtraFiles = []*os.File{in.file}
	}
	return cmd
}

// close releases the source: the local file, or the copy.
func (in input) close() {
	if in.file != nil {
		_ = in.file.Close()
	}
	if in.tmp != "" {
		_ = os.Remove(in.tmp)
	}
}

// sourceInput gives node's bytes as a file a program can open: the local
// file itself when there is one, otherwise a copy named after `pattern`
// (os.CreateTemp's) in the work directory. End-to-end encrypted content is
// refused either way (errEncryptedContent), as openSource refuses it.
func (p *Pipeline) sourceInput(ctx context.Context, drv storage.Driver, node *model.Node, pattern string) (input, error) {
	src, err := p.body.Resolve(ctx, drv, node.StorageID, node.Path, node)
	if err != nil {
		return input{}, err
	}
	f, err := src.LocalFile(ctx)
	if err != nil {
		return input{}, err
	}
	if f == nil {
		return p.copyInput(ctx, drv, node, pattern)
	}
	head := make([]byte, len(e2e.MagicPrefix))
	if n, _ := f.ReadAt(head, 0); n == len(head) && e2e.HasEncryptedPrefix(head) {
		_ = f.Close()
		return input{}, errEncryptedContent
	}
	if runtime.GOOS == "windows" {
		// os/exec cannot pass a descriptor on Windows: the path the driver
		// checked, with the file held open for the run.
		return input{path: f.Name(), file: f}, nil
	}
	return input{path: inheritedPath, file: f}, nil
}

// copyInput copies node's bytes into the work directory.
func (p *Pipeline) copyInput(ctx context.Context, drv storage.Driver, node *model.Node, pattern string) (input, error) {
	tmp, err := p.workTemp(pattern)
	if err != nil {
		return input{}, err
	}
	in := input{path: tmp.Name(), tmp: tmp.Name()}
	rc, err := p.openSource(ctx, drv, node)
	if err == nil {
		_, err = io.Copy(tmp, rc)
		_ = rc.Close()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		in.close()
		return input{}, err
	}
	return in, nil
}

// workTemp creates a temporary file in the work directory (the system temp
// directory for a pipeline without a cache directory).
func (p *Pipeline) workTemp(pattern string) (*os.File, error) {
	dir := p.workDir()
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	return os.CreateTemp(dir, pattern)
}

// workDir is where copies go; "" means the system temp directory.
func (p *Pipeline) workDir() string {
	if p.cacheDir == "" {
		return ""
	}
	return filepath.Join(p.cacheDir, workDirName)
}

// legacyTempPatterns are the names filex before 0.56.2 gave its copies in the
// system temp directory.
var legacyTempPatterns = []string{"filex-vid-*.bin", "filex-aud-*.bin", "filex-pdf-*.pdf", "filex-zip-*"}

// legacyTempAge: a legacy copy older than this is the leftover of a killed
// process. A younger one is left alone, because another filex on the same
// machine (an older version, a test) may still be writing it.
const legacyTempAge = time.Hour

// ClearWork empties the work directory and removes the copies an older filex
// left in the system temp directory. Called once at startup, before the first
// thumbnail: a copy in the work directory at that moment belongs to a process
// that is gone.
func (p *Pipeline) ClearWork() {
	if dir := p.workDir(); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("thumb: could not empty the work directory", "dir", dir, "err", err)
		}
	}
	cutoff := time.Now().Add(-legacyTempAge)
	for _, pat := range legacyTempPatterns {
		matches, _ := filepath.Glob(filepath.Join(os.TempDir(), pat))
		for _, m := range matches {
			st, err := os.Lstat(m)
			if err != nil || !st.Mode().IsRegular() || st.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(m); err == nil {
				slog.Info("thumb: removed a copy an older version left behind", "file", m, "bytes", st.Size())
			}
		}
	}
}

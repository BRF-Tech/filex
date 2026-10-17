package officeengine

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// ── The engine's files, read from the app's zips in place ───────────────
//
// filex never unpacks an app's zip onto its disk (wasmplugin/uibundle.go: no
// request path is ever joined onto a directory, so there is no "zip slip").
// The engine keeps that rule: the layout reads sdkjs and the fonts from the
// installed interface bundle and its own scripts from engine.zip through
// zipTree, a read-only file system over parts of both, mounted at /e. A file
// is read into memory when it is opened - a seekable copy, which QuickJS's
// loader needs (it seeks to the end for the size) and a deflated zip entry is
// not.

// maxTreeFile bounds one file the layout opens.
const maxTreeFile = 256 << 20

// zipTree is a read-only fs.FS over grafted parts of zip archives.
type zipTree struct {
	files map[string]*zip.File
	dirs  map[string]map[string]bool
}

func newZipTree() *zipTree {
	return &zipTree{files: map[string]*zip.File{}, dirs: map[string]map[string]bool{".": {}}}
}

// graft makes every file of zr under the directory `from` ("" for the
// archive's root) appear under `to` ("" for the tree's root). An entry whose
// name is not a clean relative path is refused: the archive is the app's, and
// what it names must not reach outside the tree.
func (t *zipTree) graft(zr *zip.Reader, from, to string) (int, error) {
	from = strings.Trim(from, "/")
	to = strings.Trim(to, "/")
	n := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") || f.Mode().IsDir() {
			continue
		}
		rel := f.Name
		if from != "" {
			var ok bool
			if rel, ok = strings.CutPrefix(f.Name, from+"/"); !ok {
				continue
			}
		}
		if !fs.ValidPath(rel) || rel == "." || strings.Contains(rel, "\\") {
			return n, fmt.Errorf("office engine: the package holds %q, which is not a clean relative path", f.Name)
		}
		if !f.Mode().IsRegular() {
			return n, fmt.Errorf("office engine: the package holds %q, which is not a file", f.Name)
		}
		name := rel
		if to != "" {
			name = to + "/" + rel
		}
		t.files[name] = f
		for dir, child := path.Dir(name), path.Base(name); ; dir, child = path.Dir(dir), path.Base(dir) {
			if t.dirs[dir] == nil {
				t.dirs[dir] = map[string]bool{}
			}
			t.dirs[dir][child] = true
			if dir == "." {
				break
			}
		}
		n++
	}
	return n, nil
}

// Open implements fs.FS.
func (t *zipTree) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if f, ok := t.files[name]; ok {
		if f.UncompressedSize64 > maxTreeFile {
			return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("file too large")}
		}
		rc, err := f.Open()
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		b, err := io.ReadAll(io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		if err != nil || uint64(len(b)) != f.UncompressedSize64 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("the entry does not read back as its header says")}
		}
		return &memFile{Reader: bytes.NewReader(b), info: treeInfo{name: path.Base(name), size: int64(len(b)), mod: f.Modified}}, nil
	}
	if kids, ok := t.dirs[name]; ok {
		names := make([]string, 0, len(kids))
		for k := range kids {
			names = append(names, k)
		}
		sort.Strings(names)
		ents := make([]fs.DirEntry, 0, len(names))
		for _, k := range names {
			full := k
			if name != "." {
				full = name + "/" + k
			}
			if f, ok := t.files[full]; ok {
				ents = append(ents, fs.FileInfoToDirEntry(treeInfo{name: k, size: int64(f.UncompressedSize64), mod: f.Modified}))
			} else {
				ents = append(ents, fs.FileInfoToDirEntry(treeInfo{name: k, dir: true}))
			}
		}
		return &dirFile{info: treeInfo{name: path.Base(name), dir: true}, entries: ents}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// memFile is an opened file: its bytes in memory, seekable.
type memFile struct {
	*bytes.Reader
	info treeInfo
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }

// dirFile is an opened directory.
type dirFile struct {
	info    treeInfo
	entries []fs.DirEntry
	pos     int
}

func (d *dirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dirFile) Close() error               { return nil }
func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: errors.New("is a directory")}
}

// ReadDir implements fs.ReadDirFile.
func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.pos:]
	if n <= 0 {
		d.pos = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.pos += n
	return rest[:n], nil
}

type treeInfo struct {
	name string
	size int64
	mod  time.Time
	dir  bool
}

func (i treeInfo) Name() string { return i.name }
func (i treeInfo) Size() int64  { return i.size }
func (i treeInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i treeInfo) ModTime() time.Time { return i.mod }
func (i treeInfo) IsDir() bool        { return i.dir }
func (i treeInfo) Sys() any           { return nil }

// openPinned opens a zip archive that must hash to sum (lower hex): the at-rest
// check an app's module and interface get every time they are loaded.
func openPinned(file, sum string) (*zip.ReadCloser, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		return nil, fmt.Errorf("office engine: %s hashes to %s…, not the installed %s…", path.Base(strings.ReplaceAll(file, "\\", "/")), got[:12], clip(sum, 12))
	}
	return zip.OpenReader(file)
}

// readEntry reads one file of a zip archive whole.
func readEntry(zr *zip.Reader, name string, max int64) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if int64(f.UncompressedSize64) > max {
			return nil, fmt.Errorf("office engine: %s is larger than %d MiB", name, max>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, max+1))
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, fmt.Errorf("office engine: the engine package has no %s", name)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

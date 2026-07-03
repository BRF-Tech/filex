// Package local is a Storage Driver fronting an on-disk directory.
//
// Path safety: every operation joins paths via filepath.Clean and rejects
// any result that escapes the configured root with `..`.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/internal/storage"
)

func init() {
	storage.Register("local", func() storage.Driver { return &Driver{} })
}

// Driver implements local FS access.
type Driver struct {
	root string
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "local" }

// Init validates and stores the root directory from config["path"]
// (preferred) or config["root"] (legacy). Matches storage.ValidateNonRootPath's
// own preference order — without this fallback an operator could create a
// storage with {path: "/data"} via the admin API, pass validate.go cleanly,
// and then watch the driver init silently with an empty root because it
// only looked at config["root"].
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	root, _ := cfg["path"].(string)
	if root == "" {
		root, _ = cfg["root"].(string)
	}
	if root == "" {
		return errors.New("local: config.path (or config.root) is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("local: abs root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("local: mkdir root: %w", err)
	}
	d.root = abs
	return nil
}

// Capabilities — local FS supports everything except Presign.
func (d *Driver) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Read:   true,
		Write:  true,
		Move:   true,
		Copy:   true,
		Delete: true,
		Mkdir:  true,
		Watch:  true,
	}
}

// resolve joins p onto the root, returning a clean absolute path or an
// error if the result escapes root.
func (d *Driver) resolve(p string) (string, error) {
	p = path.Clean("/" + strings.TrimLeft(p, "/"))
	abs := filepath.Join(d.root, filepath.FromSlash(p))
	if !strings.HasPrefix(abs, d.root) {
		return "", errors.New("local: path escapes root")
	}
	return abs, nil
}

// List implements storage.Driver.
func (d *Driver) List(_ context.Context, p string) ([]storage.Object, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		obj := storage.Object{
			Path:  path.Join(p, e.Name()),
			Name:  e.Name(),
			Size:  info.Size(),
			Mtime: info.ModTime(),
		}
		switch {
		case e.IsDir():
			obj.Kind = storage.KindDirectory
		case info.Mode()&os.ModeSymlink != 0:
			obj.Kind = storage.KindSymlink
		default:
			obj.Kind = storage.KindFile
			obj.Mime = sniffMime(filepath.Join(abs, e.Name()))
		}
		out = append(out, obj)
	}
	return out, nil
}

// Stat implements storage.Driver.
func (d *Driver) Stat(_ context.Context, p string) (storage.Object, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return storage.Object{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return storage.Object{}, storage.ErrNotFound
		}
		return storage.Object{}, err
	}
	obj := storage.Object{
		Path:  p,
		Name:  filepath.Base(p),
		Size:  info.Size(),
		Mtime: info.ModTime(),
	}
	if info.IsDir() {
		obj.Kind = storage.KindDirectory
	} else {
		obj.Kind = storage.KindFile
		obj.Mime = sniffMime(abs)
	}
	return obj, nil
}

// Read implements storage.Driver.
func (d *Driver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	abs, err := d.resolve(p)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Write implements storage.Writer.
func (d *Driver) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	abs, err := d.resolve(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	f, err := os.Create(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// Move implements storage.Mover.
func (d *Driver) Move(_ context.Context, src, dst string) error {
	a, err := d.resolve(src)
	if err != nil {
		return err
	}
	b, err := d.resolve(dst)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		return err
	}
	return os.Rename(a, b)
}

// Copy implements storage.Copier.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	a, err := d.resolve(src)
	if err != nil {
		return err
	}
	b, err := d.resolve(dst)
	if err != nil {
		return err
	}
	info, err := os.Stat(a)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyTree(a, b)
	}
	return copyFile(a, b)
}

// Delete implements storage.Deleter.
func (d *Driver) Delete(_ context.Context, p string) error {
	abs, err := d.resolve(p)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(_ context.Context, p string) error {
	abs, err := d.resolve(p)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Root returns the absolute disk path — exposed so the sync worker's
// fsnotify watcher can attach to it.
func (d *Driver) Root() string { return d.root }

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(p, target)
	})
}

// sniffMime peeks the first 512 bytes for magic-byte detection, then
// refines ZIP-based formats via storage.RefineOfficeMime.
//
// http.DetectContentType returns "application/zip" for every ZIP
// container, including the OOXML/ODF office formats which are just
// ZIPs with a manifest. OnlyOffice Document Server fetches the source
// bytes from filex and inspects Content-Type for sanity: when fileType
// in the JWT-signed config says "pptx" but the fetch response says
// "application/zip" the converter aborts with "Download failed."
// xlsx works only because OnlyOffice's xlsx pipeline accepts ZIP MIME
// — pptx/docx/odt do not. Setting the correct office MIME at sniff
// time keeps the downstream contract consistent for ALL office types.
func sniffMime(abs string) string {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()
	var buf [512]byte
	n, _ := f.Read(buf[:])
	return storage.RefineOfficeMime(http.DetectContentType(buf[:n]), abs)
}

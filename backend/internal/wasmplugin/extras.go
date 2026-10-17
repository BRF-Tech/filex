package wasmplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Files an administrator adds to an app: extra/ ───────────────────────
//
// An app's interface reads its own package and nothing else (`ui:package-
// fetch`, uipolicy.go: connect-src is this version's path). Some of what an
// interface can use is too large to ship in every package: the office editor
// carries the spell-check and hyphenation dictionaries of filex's interface
// languages, and reads any other language from
// `<package>/extra/dictionaries/<lang>/<file>.txt` (the Document Server's 50
// dictionaries are 227 MB; a bundle's ceiling is 128 MiB). An administrator
// adds those files here, for ONE app (docs/APP-PLUGINS-API.md → Files an
// administrator adds to an app):
//
//   - kept beside the apps, outside the signed package
//     (`<Dir>/_extras/<app>/<path>`; an app's name cannot start with `_`,
//     nameRe), so an upgrade and a rollback keep them and removing the app
//     takes them away (Registry.Remove);
//   - served by the interface route under the package's own address
//     (`/_appui/<app>/<sha16>/extra/<path>`, uiserve.go), so the interface
//     reads them with the grant it already has: no new permission, nothing
//     outside its package path in its policy. Only to an app granted
//     `ui:package-fetch` - nothing else could read them;
//   - only where the package itself has no file of that path: a package's
//     own (reviewed, pinned) file is never shadowed by an added one;
//   - only of the kinds below (extraKinds): a fixed pattern and a fixed type
//     each, never a page, a script, a stylesheet or an image. An added file is
//     data the interface reads, never code it runs (the package's address is
//     in its script-src; text/plain under nosniff is never a script);
//   - like the package, on this filex's own disk (the registry's Dir, under
//     FILEX_DATA_DIR): a replica serves them exactly when it holds the app's
//     package, which is when the data directory is shared (docs/
//     DEPLOYMENT.md → Scaling).
//
// ⚠ Served like the package: without a sign-in, to anybody who has the
// address. What an administrator adds here is public material (a Hunspell
// dictionary), never a file of somebody's.

// ExtraPrefix is the folder of the package's address the added files are
// served under.
const ExtraPrefix = "extra/"

const (
	// extrasDirName is the folder beside the apps that holds every app's
	// added files. An app's name cannot start with `_` (nameRe), so it is
	// never an app's folder.
	extrasDirName = "_extras"
	// MaxExtraFileBytes is one added file's ceiling. The largest of the
	// Document Server's dictionaries is Turkish (14.5 MB zipped, several
	// times that as the .dic text the spell checker reads).
	MaxExtraFileBytes int64 = 128 << 20
	// MaxExtrasBytes is one app's added files together: every one of the
	// Document Server's 50 dictionaries (227 MB) and room beside them.
	MaxExtrasBytes int64 = 1 << 30
	// maxExtraFiles bounds how many files one app is given.
	maxExtraFiles = 2000
	// extraSniff is how much of an added text file is read for a NUL byte: a
	// zip or a binary picked by mistake is refused, not served as text.
	extraSniff = 8 << 10
	// extraTempPrefix names a file still being written; never listed, never
	// served.
	extraTempPrefix = ".partial-"
)

// extraCaps are the ceilings a write is held to: the constants above, and
// smaller in a test that measures them without writing a gigabyte.
var extraCaps = struct {
	file, total int64
	files       int
}{MaxExtraFileBytes, MaxExtrasBytes, maxExtraFiles}

// extraKind is one kind of file an administrator may add, and the only place
// its rules live: its folder under extra/, the whole path it must match and
// the type it is served as (fixed: never guessed from the name).
type extraKind struct {
	Name string
	re   *regexp.Regexp
	typ  string
}

// extraKinds is the closed list. Dictionaries: a Hunspell `.aff` / `.dic`
// (and a hyphenation `hyph_<lang>.dic`) of one language, as the office
// editor's spell checker asks for them - the bundle's copies are served as
// `.txt` the same way.
//
// ⚠ The office engine's fonts (`extra/fonts/`) are the next row: their own
// pattern and a font type (uiTypes serves .ttf/.otf/.woff2). Not before an
// app reads them.
var extraKinds = []extraKind{
	{
		Name: "dictionaries",
		re:   regexp.MustCompile(`^dictionaries/[A-Za-z_]{2,16}/[A-Za-z_]{2,32}\.(?:aff|dic)\.txt$`),
		typ:  "text/plain; charset=utf-8",
	},
}

// ExtraKinds are the kinds' names, for the administrator's screen.
func ExtraKinds() []string {
	out := make([]string, 0, len(extraKinds))
	for _, k := range extraKinds {
		out = append(out, k.Name)
	}
	return out
}

// extraKindOf is the kind of rel, a path under extra/ (no `extra/` before
// it); ok is false for every path no kind takes.
func extraKindOf(rel string) (extraKind, bool) {
	if !uiPathOK(rel) {
		return extraKind{}, false
	}
	for _, k := range extraKinds {
		if k.re.MatchString(rel) {
			return k, true
		}
	}
	return extraKind{}, false
}

// ExtraFile is one file an administrator added to an app.
type ExtraFile struct {
	// Path is where it is under extra/ (`dictionaries/pl_PL/pl_PL.dic.txt`).
	Path     string    `json:"path"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// SHA256 is said by the write that stored it (and its audit row), not
	// by the list: hashing every dictionary at every look would be slow.
	SHA256 string `json:"sha256,omitempty"`
}

// ExtrasAnswer is an app's added files, its ceilings and whether its
// interface reads them at all.
type ExtrasAnswer struct {
	Files        []ExtraFile `json:"files"`
	Bytes        int64       `json:"bytes"`
	MaxFileBytes int64       `json:"max_file_bytes"`
	MaxBytes     int64       `json:"max_bytes"`
	// Kinds are the kinds the app names in its manifest (`ui.extras`,
	// extras_catalog.go): the only ones it is given. Catalogs are those its
	// release offers a catalog of (one-click install).
	Kinds    []string `json:"kinds"`
	Catalogs []string `json:"catalogs"`
	// Read: the app was granted `ui:package-fetch` and names a kind, so its
	// interface can read what is added here. False: nothing would, and a new
	// file is refused (ErrExtrasUnused).
	Read bool `json:"read"`
}

// Why an added file was refused or not found. The handler says each in the
// reader's words (server.error.extra_*).
var (
	// ErrExtraPath: the path is not one of a kind filex takes (extraKinds).
	ErrExtraPath = errors.New("extras: not a path an added file may have")
	// ErrExtrasUnused: the app's interface cannot read added files (no
	// `ui:package-fetch`).
	ErrExtrasUnused = errors.New("extras: this app's interface reads no added files")
	// ErrExtraTooLarge: one file over MaxExtraFileBytes.
	ErrExtraTooLarge = errors.New("extras: the file is larger than one added file may be")
	// ErrExtrasFull: the app's added files would pass MaxExtrasBytes or
	// maxExtraFiles.
	ErrExtrasFull = errors.New("extras: the app's added files are at their ceiling")
	// ErrExtraNotText: a text kind whose bytes are not text (a zip, a
	// binary picked by mistake).
	ErrExtraNotText = errors.New("extras: the file is not text")
	// ErrExtraMissing: no such added file.
	ErrExtraMissing = errors.New("extras: no such added file")
)

// extrasMu makes a write's ceiling check and its rename one step: two
// administrators adding at once cannot both pass a ceiling that holds only
// one of them.
var extrasMu sync.Mutex

// extrasDir is where one app's added files are kept.
func (r *Registry) extrasDir(name string) string {
	return filepath.Join(r.opts.Dir, extrasDirName, name)
}

// extraFile is where rel (a path under extra/ that a kind takes) is kept.
// ok is false when the joined path would leave the app's folder - never
// for a path extraKindOf took, checked anyway.
func (r *Registry) extraFile(name, rel string) (string, bool) {
	dir := r.extrasDir(name)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	back, err := filepath.Rel(dir, full)
	if err != nil || back == "." || strings.HasPrefix(back, "..") || filepath.IsAbs(back) {
		return "", false
	}
	return full, true
}

// listExtras walks one app's folder: every file a kind takes, by path, and
// what they take together. A folder that is not there is no files.
func (r *Registry) listExtras(name string) ([]ExtraFile, int64, error) {
	dir := r.extrasDir(name)
	files := []ExtraFile{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasPrefix(d.Name(), extraTempPrefix) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		k, ok := extraKindOf(rel)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		files = append(files, ExtraFile{Path: rel, Kind: k.Name, Size: info.Size(), Modified: info.ModTime().UTC()})
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, total, nil
}

// Extras answers an app's added files (GET …/extras).
func (r *Registry) Extras(id int64) (*ExtrasAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	files, total, err := r.listExtras(p.Row.Name)
	if err != nil {
		return nil, err
	}
	kinds := declaredKinds(p.Manifest)
	catalogs := []string{}
	for _, k := range kinds {
		if _, err := extraCatalogOf(p, k); err == nil {
			catalogs = append(catalogs, k)
		}
	}
	return &ExtrasAnswer{
		Files: files, Bytes: total, MaxFileBytes: extraCaps.file, MaxBytes: extraCaps.total,
		Kinds: kinds, Catalogs: catalogs, Read: p.Grants.Has(PermUIPackageFetch) && len(kinds) > 0,
	}, nil
}

// PutExtra adds (or replaces) one file of app id at rel, a path under
// extra/, from src. The file is written beside its place and renamed into it
// once whole, so a reader never sees half of it and a refused write leaves
// the old file as it was.
func (r *Registry) PutExtra(id int64, rel string, src io.Reader) (*ExtraFile, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	if !p.Grants.Has(PermUIPackageFetch) {
		return nil, ErrExtrasUnused
	}
	k, ok := extraKindOf(rel)
	if !ok {
		return nil, ErrExtraPath
	}
	// Only a kind the app names (`ui.extras`): an app that does not read
	// dictionaries is never handed one.
	if !declaredExtras(p.Manifest)[k.Name] {
		return nil, ErrExtrasUnused
	}
	target, ok := r.extraFile(p.Row.Name, rel)
	if !ok {
		return nil, ErrExtraPath
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), extraTempPrefix+"*")
	if err != nil {
		return nil, err
	}
	written := false
	defer func() {
		if !written {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			pruneEmptyDirs(r.extrasDir(p.Row.Name), filepath.Dir(target))
		}
	}()
	h := sha256.New()
	sniff := &sniffWriter{remaining: extraSniff}
	n, err := io.Copy(io.MultiWriter(tmp, h, sniff), io.LimitReader(src, extraCaps.file+1))
	if err != nil {
		return nil, err
	}
	if n > extraCaps.file {
		return nil, ErrExtraTooLarge
	}
	if strings.HasPrefix(k.typ, "text/") && sniff.binary {
		return nil, ErrExtraNotText
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	extrasMu.Lock()
	defer extrasMu.Unlock()
	files, total, err := r.listExtras(p.Row.Name)
	if err != nil {
		return nil, err
	}
	count := len(files)
	for _, f := range files {
		if f.Path == rel {
			total -= f.Size
			count--
		}
	}
	if total+n > extraCaps.total || count+1 > extraCaps.files {
		return nil, ErrExtrasFull
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return nil, err
	}
	written = true
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	return &ExtraFile{Path: rel, Kind: k.Name, Size: info.Size(), Modified: info.ModTime().UTC(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// DeleteExtra removes one added file of app id, and the folders it leaves
// empty.
func (r *Registry) DeleteExtra(id int64, rel string) error {
	p, ok := r.ByID(id)
	if !ok {
		return installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	if _, ok := extraKindOf(rel); !ok {
		return ErrExtraPath
	}
	target, ok := r.extraFile(p.Row.Name, rel)
	if !ok {
		return ErrExtraPath
	}
	extrasMu.Lock()
	defer extrasMu.Unlock()
	if err := os.Remove(target); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrExtraMissing
		}
		return err
	}
	pruneEmptyDirs(r.extrasDir(p.Row.Name), filepath.Dir(target))
	return nil
}

// pruneEmptyDirs removes dir and its parents up to (not including) root while
// they are empty: a language folder goes with its last file, and a refused
// first write leaves no empty folder behind.
func pruneEmptyDirs(root, dir string) {
	for ; dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

// serveExtra answers a request of the interface route for a file the
// package does not have: an added file of the bundle's app, when rel (the
// path under the package) is one, of a kind that version names
// (`ui.extras`), and the app was granted what reads it. false: not an added
// file, nothing written (the caller answers 404).
//
// The common headers (CORS for the sandboxed frame, nosniff, no referrer, no
// cookie) are serveUIFile's; the type is the kind's, the policy the one every
// file that is not a page gets. ⚠ Not immutable, unlike the package's own
// files: an administrator can replace an added file under the same address,
// so it is revalidated (no-cache, an ETag of its size and time).
func (r *Registry) serveExtra(w http.ResponseWriter, req *http.Request, grants Grants, b *uiBundle, rel string) bool {
	sub, ok := strings.CutPrefix(rel, ExtraPrefix)
	if !ok || !grants.Has(PermUIPackageFetch) {
		return false
	}
	k, ok := extraKindOf(sub)
	if !ok || !b.extras[k.Name] {
		return false
	}
	full, ok := r.extraFile(b.app, sub)
	if !ok {
		return false
	}
	f, err := os.Open(full)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	h := w.Header()
	etag := `"x` + strconv.FormatInt(info.Size(), 16) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 16) + `"`
	h.Set("Content-Type", k.typ)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if match := req.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	if req.Method != http.MethodHead {
		_, _ = io.Copy(w, f)
	}
	return true
}

// sniffWriter looks for a NUL byte in the first `remaining` bytes written.
type sniffWriter struct {
	remaining int
	binary    bool
}

func (s *sniffWriter) Write(b []byte) (int, error) {
	if s.remaining > 0 {
		head := b
		if len(head) > s.remaining {
			head = head[:s.remaining]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			s.binary = true
		}
		s.remaining -= len(head)
	}
	return len(b), nil
}

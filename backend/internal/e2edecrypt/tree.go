package e2edecrypt

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Decrypting a downloaded encrypted folder (or a .zip of one, or a single
// encrypted file) into a plain folder with plaintext names.
//
// The contract, in the order a user meets it:
//   - Open resolves the input, finds and parses the marker, and refuses early
//     — before anyone types a password — when the output already exists, the
//     marker is missing, or the folder needs a feature this build lacks.
//   - Unlock takes the password or the recovery key. Never from an argument:
//     that is the CLI's job, and it reads a terminal or stdin.
//   - Run writes everything under "<out>.partial-*" next to the target and
//     renames it to <out> only when every name and every file decrypted. Any
//     error removes the partial directory: a wrong key or one damaged file
//     leaves NOTHING behind, so a half-decrypted tree can never be mistaken
//     for a whole one.

// ErrOutputExists: the output directory is already there. Decrypting over it
// would mix old and new plaintext; pick another -o.
var ErrOutputExists = errors.New("the output directory already exists")

// ErrNoMarker: the input carries no .filex-e2e.json and none was given.
var ErrNoMarker = errors.New("no " + MarkerName + " found in the input - pass the encrypted folder's root, or its marker with --marker")

// CorruptError names the entry that stopped a run.
type CorruptError struct {
	Path string // plaintext path where known, with the stored name
	Err  error
}

func (e *CorruptError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *CorruptError) Unwrap() error { return e.Err }

// OpenOptions configure Open.
type OpenOptions struct {
	// Out is the output directory. Empty: "<input>-decrypted" next to it
	// (".zip" dropped).
	Out string
	// MarkerPath is an explicit .filex-e2e.json, for an input that does not
	// carry its own (a subfolder, a single file, a zip made below the root).
	MarkerPath string
	// GOOS overrides runtime.GOOS for the output-name rules (tests).
	GOOS string
	// Generation, for a vault, decrypts that generation (one still kept)
	// instead of the latest. 0: the latest that verifies.
	Generation uint64
}

// Result summarises a finished run.
type Result struct {
	Out      string
	Files    int
	Dirs     int
	Warnings []string
}

// Job is one decryption, between Open and Close.
type Job struct {
	Marker *Marker

	root      string // directory to walk; "" in single-file mode
	rootIsE2  bool   // root is the encrypted root itself (holds the marker)
	single    string // the one file, in single-file mode
	markerDir string // the directory the marker was found in (or given)
	// ids is every folder id under the walked root (names-encrypted folders
	// only): what a name moved in from outside filex is tried against.
	ids      [][]byte
	out      string
	goos     string
	keys     *Keys
	cleanups []func()
	// fxe is set when the input is a single encrypted file (.fxe): it
	// carries its own key slots and needs no marker.
	fxe *fxeInput
	// gen is the vault generation asked for (OpenOptions.Generation).
	gen uint64

	res *Result
}

// Out is where Run will write. For a .fxe without -o it is only known after
// Unlock (the original name comes out of the header); until then it is "".
func (j *Job) Out() string {
	if j.fxe != nil {
		if j.fxe.explicit != "" || j.fxe.keys == nil {
			return j.fxe.explicit
		}
		t, _ := j.fxe.target(j.goos)
		return t
	}
	return j.out
}

// IsFxe reports whether the input is a single encrypted file (.fxe).
func (j *Job) IsFxe() bool { return j.fxe != nil }

// Close removes anything Open staged (an extracted zip).
func (j *Job) Close() {
	for i := len(j.cleanups) - 1; i >= 0; i-- {
		j.cleanups[i]()
	}
	j.cleanups = nil
}

// Open resolves the input and parses its marker. The returned Job must be
// closed. An *UnsupportedError comes back with a nil Job.
func Open(in string, opts OpenOptions) (*Job, error) {
	abs, err := filepath.Abs(in)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	j := &Job{goos: opts.GOOS}
	if j.goos == "" {
		j.goos = runtime.GOOS
	}
	ok := false
	defer func() {
		if !ok {
			j.Close()
		}
	}()

	/* A single encrypted file (.fxe) holds its own key slots: no marker, and
	   the output is the file's ORIGINAL name next to it (or -o). */
	if fi.Mode().IsRegular() {
		head, err := readHead(abs, 8)
		if err != nil {
			return nil, err
		}
		if HasFxeMagic(head) {
			in, err := openFxe(abs, opts.Out)
			if err != nil {
				return nil, err
			}
			j.fxe = in
			ok = true
			return j, nil
		}
	}

	out := opts.Out
	if out == "" {
		base := strings.TrimRight(abs, `/\`)
		if !fi.IsDir() && strings.EqualFold(filepath.Ext(base), ".zip") {
			base = strings.TrimSuffix(base, filepath.Ext(base))
		}
		out = base + "-decrypted"
	}
	if j.out, err = filepath.Abs(out); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(j.out); err == nil {
		return nil, fmt.Errorf("%s: %w", j.out, ErrOutputExists)
	}

	var markerPath string
	switch {
	case fi.IsDir():
		j.root = abs
		if isFile(filepath.Join(abs, MarkerName)) {
			markerPath = filepath.Join(abs, MarkerName)
			j.rootIsE2 = true
		} else if opts.MarkerPath == "" && dirHasFxe(abs) {
			// wiring:e2 fxe — not an encrypted folder: a folder of single
			// encrypted files, each with its own password.
			return nil, errors.New("this folder has no " + MarkerName + ", but it holds single encrypted files (.fxe) - each has its own password: decrypt them one by one (filex decrypt <file>.fxe)")
		}
	case fi.Mode().IsRegular():
		head, err := readHead(abs, 8)
		if err != nil {
			return nil, err
		}
		switch {
		case HasMagic(head):
			j.single = abs
			if sib := filepath.Join(filepath.Dir(abs), MarkerName); isFile(sib) {
				markerPath = sib
			}
		case strings.HasPrefix(string(head), "PK\x03\x04") || strings.HasPrefix(string(head), "PK\x05\x06"):
			tmp, err := os.MkdirTemp("", "filex-decrypt-zip-")
			if err != nil {
				return nil, err
			}
			j.cleanups = append(j.cleanups, func() { _ = os.RemoveAll(tmp) })
			if err := extractZip(abs, tmp, j.goos); err != nil {
				return nil, err
			}
			j.root, j.rootIsE2 = zipRoot(tmp)
			if j.rootIsE2 {
				markerPath = filepath.Join(j.root, MarkerName)
			}
		default:
			return nil, errors.New("the input is neither an encrypted folder, a .zip of one, nor an encrypted file")
		}
	default:
		return nil, errors.New("the input is not a regular file or directory")
	}
	if opts.MarkerPath != "" {
		markerPath = opts.MarkerPath
	}
	if markerPath == "" {
		return nil, ErrNoMarker
	}
	if absMarker, err := filepath.Abs(markerPath); err == nil {
		j.markerDir = filepath.Dir(absMarker)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return nil, fmt.Errorf("reading the marker: %w", err)
	}
	m, err := ParseMarker(data)
	if err != nil {
		return nil, err
	}
	if m.IsVault() {
		// wiring:e2 vault - a vault's tree is in its index: it opens whole,
		// from the folder itself (or a zip of it), never a part of it.
		if !j.rootIsE2 || (j.markerDir != "" && !samePath(j.markerDir, j.root)) {
			return nil, errors.New("this is a vault (encryption level 3): it is decrypted whole - pass the vault folder itself, or a .zip of it")
		}
	} else if opts.Generation != 0 {
		return nil, errors.New("--generation is for a vault (encryption level 3); this folder is not one")
	}
	j.gen = opts.Generation
	j.Marker = m
	ok = true
	return j, nil
}

// Unlock opens the marker with the password, or with the recovery key when
// recovery is true.
func (j *Job) Unlock(secret string, recovery bool) error {
	if j.fxe != nil {
		k, err := j.fxe.header.Unlock(secret, recovery)
		if err != nil {
			return err
		}
		j.fxe.keys = k
		return nil
	}
	var k *Keys
	var err error
	if recovery {
		k, err = j.Marker.UnlockRecoveryKey(secret)
	} else {
		k, err = j.Marker.UnlockPassword(secret)
	}
	if err != nil {
		return err
	}
	j.keys = k
	return nil
}

// Run decrypts into the output directory, all or nothing.
func (j *Job) Run() (*Result, error) {
	if j.fxe != nil {
		if j.fxe.keys == nil {
			return nil, errors.New("e2edecrypt: Run before Unlock")
		}
		return j.fxe.run(j.goos)
	}
	if j.keys == nil {
		return nil, errors.New("e2edecrypt: Run before Unlock")
	}
	if j.Marker.IsVault() {
		return j.runVault()
	}
	return j.intoPartial(func(tmp string) error {
		if j.single != "" {
			return j.decryptSingle(tmp)
		}
		id, err := j.idOf(j.root)
		if err != nil {
			return err
		}
		if id != nil {
			j.ids = nil
			j.collectIDs(j.root, id)
		}
		return j.decryptDir(j.root, tmp, "", "", j.rootIsE2, id)
	})
}

// intoPartial writes the job's output all or nothing - a folder and a vault
// alike: fill gets a new "<out>.partial-*" directory next to the output, which
// is renamed into place only when fill succeeded and is removed otherwise. The
// output must not exist, before or after.
func (j *Job) intoPartial(fill func(tmp string) error) (*Result, error) {
	if _, err := os.Lstat(j.out); err == nil {
		return nil, fmt.Errorf("%s: %w", j.out, ErrOutputExists)
	}
	parent := filepath.Dir(j.out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(parent, filepath.Base(j.out)+".partial-")
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(tmp)
		}
	}()
	j.res = &Result{Out: j.out}
	if err := fill(tmp); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(j.out); err == nil {
		return nil, fmt.Errorf("%s: %w", j.out, ErrOutputExists)
	}
	if err := os.Rename(tmp, j.out); err != nil {
		return nil, err
	}
	done = true
	return j.res, nil
}

// DecryptTree is Open + Unlock + Run + Close.
func DecryptTree(in string, opts OpenOptions, secret string, recovery bool) (*Result, error) {
	j, err := Open(in, opts)
	if err != nil {
		return nil, err
	}
	defer j.Close()
	if err := j.Unlock(secret, recovery); err != nil {
		return nil, err
	}
	return j.Run()
}

func (j *Job) warn(format string, a ...any) {
	j.res.Warnings = append(j.res.Warnings, fmt.Sprintf(format, a...))
}

func joinRel(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func isBelow(p, dir string) bool {
	return strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)+string(filepath.Separator))
}

// idOf is the folder id names directly inside dir are sealed under
// (docs/E2E-ENCRYPTION.md → "Folder ids"): the root's from the marker, a
// folder's from its own stored name, or — a folder whose name was never
// encrypted — derived from its parent's. nil for a folder without encrypted
// names.
func (j *Job) idOf(dir string) ([]byte, error) {
	nk := j.keys.Names
	if nk == nil {
		return nil, nil
	}
	if (j.rootIsE2 && samePath(dir, j.root)) || (j.markerDir != "" && samePath(dir, j.markerDir)) {
		return nk.RootID, nil
	}
	base := filepath.Base(dir)
	if id := DirIDOf(base); id != nil {
		return id, nil
	}
	if j.markerDir != "" && isBelow(dir, j.markerDir) {
		parent, err := j.idOf(filepath.Dir(dir))
		if err != nil {
			return nil, err
		}
		return nk.DeriveDirID(parent, base), nil
	}
	return nil, fmt.Errorf("%s: its name carries no folder id and it is not inside the folder of the marker, so the names in it cannot be read - pass the encrypted folder's root (or a folder inside it with --marker pointing at the root's %s)", base, MarkerName)
}

// collectIDs records every folder id below dir (id is dir's own).
func (j *Job) collectIDs(dir string, id []byte) {
	j.ids = append(j.ids, id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		j.collectIDs(filepath.Join(dir, e.Name()), j.keys.Names.EffectiveDirID(id, e.Name()))
	}
}

// resolveName turns one stored entry name, found in the folder dir whose id
// is parentID, into the name to write, or returns ok=false for an entry that
// is bookkeeping (a sidecar).
func (j *Job) resolveName(dir, stored, plainRel string, parentID []byte) (string, bool, error) {
	nk := j.keys.Names
	if nk == nil {
		return stored, true, nil
	}
	read := func(name string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return b, err == nil
	}
	where := joinRel(plainRel, stored)
	plain, state := nk.DecryptStoredName(stored, parentID, read)
	if state == NameUnreadable || (state == NamePlain && ClassifyStoredName(stored).Kind == KindFile) {
		// Sealed for another folder of this encrypted folder: moved outside
		// filex (over WebDAV, sync) without being re-sealed. Every folder id
		// is known, so the name is recovered rather than lost.
		if p, ok := nk.RecoverMoved(stored, parentID, j.ids, read); ok {
			j.warn("%s: this name was sealed for another folder - the item was moved outside filex; its name was recovered (open the folder in the filex web UI and fix its names)", where)
			plain, state = p, NameDecrypted
		}
	}
	switch state {
	case NameSidecar:
		return "", false, nil
	case NameUnreadable:
		c := ClassifyStoredName(stored)
		if c.Kind == KindLong || c.Kind == KindLongDir {
			if _, ok := EncodedOf(stored, read); !ok {
				side, _ := SidecarNameFor(stored)
				return "", false, &CorruptError{
					Path: where,
					Err:  fmt.Errorf("its long name cannot be read: the sidecar %s is missing or does not match", side),
				}
			}
		}
		// Ours by its spelling, and it opens under no folder id here: moved
		// in from a folder that no longer exists, or damaged. What is inside
		// it (a folder keeps its id) and its content still decrypt, so it is
		// written under its stored name rather than stopping the run.
		j.warn("%s: this name does not open in any folder of this encrypted folder (moved outside filex from a folder that no longer exists, or damaged); kept as stored", where)
		return stored, true, nil
	case NamePlain:
		j.warn("%s: this name was never encrypted (written outside the filex web UI, or not reached by a level change); kept as it is", where)
		return plain, true, nil
	}
	if p := NameProblem(plain); p != "" {
		return "", false, &CorruptError{
			Path: joinRel(plainRel, stored),
			Err:  fmt.Errorf("the decrypted name is not safe to write (%s)", p),
		}
	}
	return plain, true, nil
}

func (j *Job) decryptDir(src, dst, plainRel, storedRel string, isRoot bool, dirID []byte) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	// Long names present here, by hash: a sidecar with none is an orphan.
	longHere := map[string]bool{}
	for _, e := range entries {
		if c := ClassifyStoredName(e.Name()); c.Kind == KindLong || c.Kind == KindLongDir {
			longHere[c.Hash] = true
		}
	}
	taken := map[string]bool{}
	for _, e := range entries {
		stored := e.Name()
		if isRoot && stored == MarkerName && !e.IsDir() {
			continue
		}
		storedPath := joinRel(storedRel, stored)
		if e.Type()&os.ModeSymlink != 0 {
			j.warn("%s: a symbolic link - skipped", storedPath)
			continue
		}
		plain, keep, err := j.resolveName(src, stored, plainRel, dirID)
		if err != nil {
			return err
		}
		if !keep {
			if c := ClassifyStoredName(stored); j.keys.Names != nil && c.Kind == KindSidecar && !longHere[c.Hash] {
				j.warn("%s: a long-name sidecar with no item next to it - ignored", storedPath)
			}
			continue
		}
		name := j.outputName(plain, taken, joinRel(plainRel, plain))
		target := filepath.Join(dst, name)
		rel := joinRel(plainRel, name)
		switch {
		case e.IsDir():
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			j.res.Dirs++
			var inner []byte
			if dirID != nil {
				inner = j.keys.Names.EffectiveDirID(dirID, stored)
			}
			if err := j.decryptDir(filepath.Join(src, stored), target, rel, storedPath, false, inner); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if err := j.decryptFileTo(filepath.Join(src, stored), target, rel, storedPath); err != nil {
				return err
			}
		default:
			j.warn("%s: not a regular file or folder - skipped", storedPath)
		}
	}
	return nil
}

func (j *Job) decryptSingle(dst string) error {
	dir, stored := filepath.Dir(j.single), filepath.Base(j.single)
	parentID, err := j.idOf(dir)
	if nk := j.keys.Names; nk != nil {
		if err != nil {
			// A file copied out of its folder: where it came from is not
			// known. Start from the root's id, and let the name be tried
			// against every folder id of the folder the marker is in.
			parentID, err = nk.RootID, nil
		}
		if j.markerDir != "" {
			j.ids = nil
			j.collectIDs(j.markerDir, nk.RootID)
		}
	}
	if err != nil {
		return err
	}
	plain, keep, err := j.resolveName(dir, stored, "", parentID)
	if err != nil {
		return err
	}
	if !keep {
		return errors.New("the input is a long-name sidecar, not a file - pass the item next to it")
	}
	name := j.outputName(plain, map[string]bool{}, plain)
	return j.decryptFileTo(j.single, filepath.Join(dst, name), name, stored)
}

func (j *Job) decryptFileTo(src, target, rel, storedPath string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 256*1024)
	head, _ := br.Peek(8)
	w, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 256*1024)
	var derr error
	switch {
	case HasMagic(head):
		// 0x01 is read whole (at most 200 MiB); 0x02 streams. Mid re-key, a
		// file not re-wrapped yet is under the previous key.
		derr = DecryptFileStream(bw, br, j.keys.FMK, j.keys.Previous)
		var ue *UnsupportedError
		if derr != nil && !errors.As(derr, &ue) {
			where := rel
			if storedPath != rel {
				where = rel + " (stored as " + storedPath + ")"
			}
			derr = &CorruptError{Path: where, Err: derr}
		}
	case HasFxeMagic(head):
		j.warn("%s: a single encrypted file (.fxe) with its own password; copied as it is - decrypt it on its own with filex decrypt", rel)
		_, derr = io.Copy(bw, br)
	default:
		if j.Marker != nil && j.Marker.ConvPending {
			j.warn("%s: not encrypted yet - the folder is being encrypted in place and this file was not reached; copied as it is", rel)
		} else {
			j.warn("%s: this file was never encrypted (no filexe2e header); copied as it is", rel)
		}
		_, derr = io.Copy(bw, br)
	}
	if derr == nil {
		derr = bw.Flush()
	}
	if cerr := w.Close(); derr == nil {
		derr = cerr
	}
	if derr != nil {
		return derr
	}
	j.res.Files++
	return nil
}

// outputName makes a plaintext name safe for the output disk and unique in
// its folder, warning about every change.
func (j *Job) outputName(name string, taken map[string]bool, rel string) string {
	safe := safeOutputName(name, j.goos)
	if safe != name {
		j.warn("%s: written as %q (not a valid file name on %s)", rel, safe, j.goos)
	}
	unique := safe
	for n := 2; taken[collisionKey(unique)]; n++ {
		unique = numbered(safe, n)
	}
	if unique != safe {
		j.warn("%s: another entry in this folder has the same name (ignoring case); written as %q", rel, unique)
	}
	taken[collisionKey(unique)] = true
	return unique
}

// collisionKey compares names the way NTFS and APFS do (case-insensitive,
// Unicode-normalised), because that is where the output may land.
func collisionKey(name string) string {
	return strings.ToLower(norm.NFC.String(name))
}

func numbered(name string, n int) string {
	ext := path.Ext(name)
	if ext == name || strings.HasPrefix(name, ".") && strings.Count(name, ".") == 1 {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// safeOutputName adapts a name to the rules of the output OS. Only Windows
// has rules a legitimate name breaks (`a:b.txt` is a fine name on Linux and
// in filex).
func safeOutputName(name, goos string) string {
	if goos != "windows" {
		return name
	}
	b := []rune(name)
	for i, r := range b {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*\/`, r) {
			b[i] = '_'
		}
	}
	for i := len(b) - 1; i >= 0 && (b[i] == '.' || b[i] == ' '); i-- {
		b[i] = '_'
	}
	s := string(b)
	stem := s
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	if windowsReserved[strings.ToUpper(strings.TrimRight(stem, " "))] {
		s = "_" + s
	}
	return s
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func readHead(p string, n int) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:k], nil
}

// zipRoot finds the encrypted root inside an extracted zip: the extraction
// directory itself, or its single top-level folder when that holds the
// marker (a zip of the folder, as a browser download makes one). Otherwise
// the extraction directory, and the caller needs --marker.
func zipRoot(dir string) (string, bool) {
	if isFile(filepath.Join(dir, MarkerName)) {
		return dir, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dir, false
	}
	var dirs []string
	for _, e := range entries {
		if e.Name() == "__MACOSX" {
			continue
		}
		if !e.IsDir() {
			return dir, false
		}
		dirs = append(dirs, e.Name())
	}
	if len(dirs) == 1 && isFile(filepath.Join(dir, dirs[0], MarkerName)) {
		return filepath.Join(dir, dirs[0]), true
	}
	return dir, false
}

// extractZip unpacks a zip under dst, refusing any entry that would land
// outside it (zip-slip) and skipping links.
func extractZip(zipPath, dst, goos string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("reading the zip: %w", err)
	}
	defer r.Close()
	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		unsafe := strings.HasPrefix(name, "/") || (goos == "windows" && strings.Contains(name, ":"))
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				unsafe = true
			}
		}
		clean := path.Clean("/" + name)[1:]
		if unsafe || filepath.VolumeName(filepath.FromSlash(clean)) != "" {
			return fmt.Errorf("the zip holds an unsafe entry %q - refusing to extract it", f.Name)
		}
		if clean == "" {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(clean))
		if rel, err := filepath.Rel(dst, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the zip holds an unsafe entry %q - refusing to extract it", f.Name)
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			continue
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

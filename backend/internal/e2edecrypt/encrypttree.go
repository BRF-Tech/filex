package e2edecrypt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/regfile"
)

// Encrypting a folder on this machine into an encrypted folder filex opens:
// `filex encrypt <folder>`. The input is read and never changed; the output
// is a new folder, "<input>-encrypted" by default, holding the key file and
// every file encrypted under it - byte for byte what the browser writes
// (CreateFolder, EncryptFile; names at level 2 with EncryptName). Upload it
// into filex (the web, or `filex client upload -r`) and it opens there with
// the password; `filex decrypt` opens it offline.
//
// Resumable. The output is assembled in "<out>.partial" and renamed into
// place at the end. The key file is written there FIRST, so a run that was
// stopped (Ctrl-C, a full disk, a reboot) continues when it is started again
// with the same input and output: it unlocks that key file with the password
// (or the recovery key) and writes only what is missing. Each file is written
// as "<name>.filex-part" and renamed when whole, so a file that is there is a
// file that is done. At level 2 the stored names are the same on every run (a
// folder's id is the one derived from its name, as a converted folder's is),
// so a resumed run finds what it wrote before.

// ErrAlreadyEncrypted: the input is an encrypted folder already.
var ErrAlreadyEncrypted = errors.New("this folder is already an encrypted folder (it holds " + MarkerName + ")")

// ErrNestedEncrypted: the input holds an encrypted folder. Encrypted folders
// do not nest - the browser refuses the same.
var ErrNestedEncrypted = errors.New("this folder holds an encrypted folder, and encrypted folders cannot be nested")

// partSuffix marks a file being written; it is renamed when whole.
const partSuffix = ".filex-part"

// EncryptTreeOptions configure OpenEncrypt.
type EncryptTreeOptions struct {
	// Out is the output folder. Empty: "<input>-encrypted" next to the input.
	Out string
	// Names encrypts file and folder names too (level 2).
	Names bool
	// EscrowPublicKey is an installation's escrow key (base64 SPKI) to seal
	// the folder key to, or "" for none.
	EscrowPublicKey string
	// Iterations and Rand are for tests; writers use MinIterations and
	// crypto/rand.
	Iterations int
	Rand       io.Reader
}

// EncryptTreeResult summarises a finished run.
type EncryptTreeResult struct {
	Out   string
	Files int
	Dirs  int
	// Kept is the files a resumed run found already written.
	Kept int
	// Large is the files written as STREAM (over 200 MB).
	Large    int
	Warnings []string
}

// EncryptTreeJob is one encryption of a local folder, between OpenEncrypt and
// Run.
type EncryptTreeJob struct {
	in, out, partial string
	opts             EncryptTreeOptions
	resume           bool
	marker           *Marker
	keys             *Keys
	res              *EncryptTreeResult
	ctx              context.Context
}

// ctxReader stops a read the moment its context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, ErrStopped
	}
	return c.r.Read(p)
}

// OpenEncrypt checks the input and the output before anyone types a
// password: the input is a folder, not encrypted already and holding no
// encrypted folder; the output does not exist and is not inside the input.
// When "<out>.partial" holds a key file, the job resumes it (Resuming).
func OpenEncrypt(in string, opts EncryptTreeOptions) (*EncryptTreeJob, error) {
	abs, err := filepath.Abs(in)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("the input is not a folder (a single file is encrypted in the web app, as a .fxe)")
	}
	if isFile(filepath.Join(abs, MarkerName)) {
		return nil, ErrAlreadyEncrypted
	}
	out := opts.Out
	if out == "" {
		out = strings.TrimRight(abs, `/\`) + "-encrypted"
	}
	if out, err = filepath.Abs(out); err != nil {
		return nil, err
	}
	if samePath(out, abs) || isBelow(out, abs) || isBelow(abs, out) {
		return nil, errors.New("the output must be outside the input folder")
	}
	if _, err := os.Lstat(out); err == nil {
		return nil, fmt.Errorf("%s: %w", out, ErrOutputExists)
	}
	nested := false
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == MarkerName {
			nested = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if nested {
		return nil, ErrNestedEncrypted
	}
	j := &EncryptTreeJob{in: abs, out: out, partial: out + ".partial", opts: opts}
	if fi, err := os.Lstat(j.partial); err == nil {
		if !fi.IsDir() || !isFile(filepath.Join(j.partial, MarkerName)) {
			return nil, fmt.Errorf("%s is there but holds no key file - remove it to start over", j.partial)
		}
		data, err := os.ReadFile(filepath.Join(j.partial, MarkerName))
		if err != nil {
			return nil, err
		}
		m, err := ParseMarker(data)
		if err != nil {
			return nil, err
		}
		j.resume, j.marker = true, m
	}
	return j, nil
}

// Out is where Run writes.
func (j *EncryptTreeJob) Out() string { return j.out }

// Resuming reports whether a stopped run is continued: its key file is
// unlocked (Unlock) rather than a new one created (Create).
func (j *EncryptTreeJob) Resuming() bool { return j.resume }

// Names reports whether names are encrypted: what was asked for a new run,
// what the key file says for a resumed one.
func (j *EncryptTreeJob) Names() bool {
	if j.resume {
		return j.marker.HasNames()
	}
	return j.opts.Names
}

// Create mints the key file of a new run and writes it into the partial
// output, before any file. It returns the recovery key, to be shown once.
func (j *EncryptTreeJob) Create(password string) (string, error) {
	if j.resume {
		return "", errors.New("e2edecrypt: Create on a resumed run")
	}
	made, err := CreateFolder(password, CreateOptions{
		Iterations:      j.opts.Iterations,
		EscrowPublicKey: j.opts.EscrowPublicKey,
		EncryptNames:    j.opts.Names,
		Rand:            j.opts.Rand,
	})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(j.partial, 0o700); err != nil {
		return "", err
	}
	if err := writeWhole(filepath.Join(j.partial, MarkerName), made.Marker); err != nil {
		return "", err
	}
	if j.marker, err = ParseMarker(made.Marker); err != nil {
		return "", err
	}
	j.keys = made.Keys
	return made.RecoveryKey, nil
}

// Unlock opens the key file of a resumed run with its password, or with its
// recovery key when recovery is true.
func (j *EncryptTreeJob) Unlock(secret string, recovery bool) error {
	if !j.resume {
		return errors.New("e2edecrypt: Unlock on a new run")
	}
	var err error
	if recovery {
		j.keys, err = j.marker.UnlockRecoveryKey(secret)
	} else {
		j.keys, err = j.marker.UnlockPassword(secret)
	}
	return err
}

// Run encrypts every file of the input into the partial output and renames
// it into place. onFile, when set, is told each file as its turn comes. A
// cancelled ctx stops it at once (ErrStopped), the file in flight discarded.
// On an error the partial output stays, for the next run to continue.
func (j *EncryptTreeJob) Run(ctx context.Context, onFile func(rel string, size int64)) (*EncryptTreeResult, error) {
	if j.keys == nil {
		return nil, errors.New("e2edecrypt: Run before Create or Unlock")
	}
	j.ctx = ctx
	if j.Names() && j.keys.Names == nil {
		return nil, errors.New("e2edecrypt: the key file has no name key")
	}
	j.res = &EncryptTreeResult{Out: j.out}
	var rootID []byte
	if j.keys.Names != nil {
		rootID = j.keys.Names.RootID
	}
	if err := j.encryptDir(j.in, j.partial, "", rootID, onFile); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(j.out); err == nil {
		return nil, fmt.Errorf("%s: %w", j.out, ErrOutputExists)
	}
	if err := os.Rename(j.partial, j.out); err != nil {
		return nil, err
	}
	return j.res, nil
}

// storedName is the name an entry gets in the output, and the sidecar a long
// encrypted name needs. dirID is the entry's own id when it is a folder.
func (j *EncryptTreeJob) storedName(plain string, parentID, dirID []byte) (EncryptedName, error) {
	if j.keys.Names == nil {
		return EncryptedName{Stored: plain}, nil
	}
	return j.keys.Names.EncryptName(plain, parentID, dirID)
}

func (j *EncryptTreeJob) encryptDir(src, dst, rel string, id []byte, onFile func(string, int64)) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	// Two names that are one name once normalised (NFC) would be one stored
	// name: the second is kept under a numbered name, as an upload conflict is.
	taken := map[string]bool{}
	for _, e := range entries {
		if j.ctx.Err() != nil {
			return ErrStopped
		}
		name := e.Name()
		childRel := joinRel(rel, name)
		srcPath := filepath.Join(src, name)
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			j.res.Warnings = append(j.res.Warnings, childRel+": a symbolic link, skipped (links are not followed)")
			continue
		case regfile.Special(info.Mode()):
			j.res.Warnings = append(j.res.Warnings, childRel+": a "+regfile.KindOf(info.Mode())+", skipped")
			continue
		}
		if j.keys.Names != nil {
			if p := NameProblem(name); p != "" {
				return &CorruptError{Path: childRel, Err: fmt.Errorf("a name filex cannot encrypt (%s)", p)}
			}
		}
		var dirID []byte
		if info.IsDir() && j.keys.Names != nil {
			dirID = j.keys.Names.DeriveDirID(id, name)
		}
		enc, err := j.storedName(name, id, dirID)
		for n := 2; err == nil && taken[enc.Stored] && n < 100; n++ {
			enc, err = j.storedName(NumberedName(name, n), id, dirID)
		}
		if err != nil {
			return &CorruptError{Path: childRel, Err: err}
		}
		if taken[enc.Stored] {
			return &CorruptError{Path: childRel, Err: errors.New("no free name")}
		}
		taken[enc.Stored] = true
		if enc.SidecarName != "" {
			if err := writeIfMissing(filepath.Join(dst, enc.SidecarName), []byte(enc.SidecarContent)); err != nil {
				return err
			}
		}
		target := filepath.Join(dst, enc.Stored)
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			j.res.Dirs++
			if err := j.encryptDir(srcPath, target, childRel, dirID, onFile); err != nil {
				return err
			}
			continue
		}
		if isFile(target) {
			j.res.Kept++
			j.res.Files++
			continue
		}
		if onFile != nil {
			onFile(childRel, info.Size())
		}
		if err := j.encryptOne(srcPath, target, info.Size()); err != nil {
			if errors.Is(err, ErrStopped) {
				return ErrStopped
			}
			return &CorruptError{Path: childRel, Err: err}
		}
		j.res.Files++
		if info.Size() > MaxOneShot {
			j.res.Large++
		}
	}
	return nil
}

func (j *EncryptTreeJob) encryptOne(src, target string, size int64) error {
	in, err := regfile.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := target + partSuffix
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := EncryptFile(out, ctxReader{ctx: j.ctx, r: in}, size, j.keys.FMK, j.opts.Rand); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// writeWhole writes data to p through a temporary name, so p is never half
// written.
func writeWhole(p string, data []byte) error {
	tmp := p + partSuffix
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func writeIfMissing(p string, data []byte) error {
	if isFile(p) {
		return nil
	}
	return writeWhole(p, data)
}

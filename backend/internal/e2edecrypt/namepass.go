package e2edecrypt

import (
	"strconv"
	"strings"
)

// The name pass: give every entry of an encrypted-names folder the name it
// should have. The Go twin of packages/core/src/lib/e2enamepass.ts
// (docs/E2E-ENCRYPTION.md → "Changing the level"), for `filex encrypt` at
// level 2.
//
// One pass does three jobs, because they are one job seen from the server:
// raising a folder from level 1 to level 2 (every entry still has its
// plaintext name), finishing that after an interruption (some do, some do
// not), and repairing what arrived from outside the browser (a name written
// over WebDAV, or an entry moved into another folder of the same encrypted
// folder - its name is sealed for the folder it came from).
//
// Renames only: no content is read or written. Idempotent: an entry whose
// name opens where it is is left alone, and a plaintext-named folder's id is
// derived (the same every time), so running the pass again after a stop
// continues it. Folders go top-down to learn ids and bottom-up to rename, so
// a folder's contents are sealed under its id before the folder itself is
// renamed.

// NamePassIO is what the name pass needs from wherever the folder is.
type NamePassIO interface {
	// List is the raw listing of a folder (stored names).
	List(dir string) ([]ConvertRow, error)
	// ReadSidecar is a sibling sidecar's content; ok is false when there is
	// none.
	ReadSidecar(dir, name string) ([]byte, bool)
	WriteSidecar(dir, name, content string) error
	// Rename gives row (in dir) the stored name `to`.
	Rename(dir string, row ConvertRow, to string) error
	// Stopped is asked between entries.
	Stopped() bool
}

// NamePassProgress counts a pass.
type NamePassProgress struct {
	// Seen is the entries found needing a name; Renamed of them were given
	// one, Repaired of those had been moved without being re-sealed.
	Seen, Renamed, Repaired int
	// Failed is the entries that could not be renamed (no permission, a name
	// a disk cannot hold, a name that cannot be read at all).
	Failed   int
	Failures []ConvertFailure
}

func (p *NamePassProgress) fail(path string, err string) {
	p.Failed++
	p.Failures = append(p.Failures, ConvertFailure{Path: path, Err: errString(err)})
}

type errString string

func (e errString) Error() string { return string(e) }

// NumberedName is "name (2).ext": a free plaintext name when the one needed
// is taken (numberedName).
func NumberedName(name string, n int) string {
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return name + " (" + strconv.Itoa(n) + ")"
	}
	return name[:dot] + " (" + strconv.Itoa(n) + ")" + name[dot:]
}

// RunNamePass renames every entry under root whose name does not open where
// it is. It returns ErrStopped when Stopped said so.
func RunNamePass(nk *NameKey, root string, io NamePassIO, prog *NamePassProgress) error {
	type folder struct {
		path string
		id   []byte
		rows []ConvertRow
	}
	// 1. Every folder once, top-down, with the id its contents are sealed under.
	var dirs []folder
	type queued struct {
		path string
		id   []byte
	}
	queue := []queued{{path: root, id: nk.RootID}}
	for len(queue) > 0 {
		if io.Stopped() {
			return ErrStopped
		}
		d := queue[0]
		queue = queue[1:]
		rows, err := io.List(d.path)
		if err != nil {
			prog.fail(d.path, err.Error())
			continue
		}
		dirs = append(dirs, folder{path: d.path, id: d.id, rows: rows})
		for _, r := range rows {
			if r.Dir {
				queue = append(queue, queued{path: r.Path, id: nk.EffectiveDirID(d.id, r.Name)})
			}
		}
	}
	ids := make([][]byte, 0, len(dirs))
	for _, d := range dirs {
		ids = append(ids, d.id)
	}

	// 2. Deepest folders first (a breadth-first list, reversed).
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		taken := make(map[string]bool, len(d.rows))
		for _, r := range d.rows {
			taken[r.Name] = true
		}
		sidecar := func(name string) ([]byte, bool) { return io.ReadSidecar(d.path, name) }
		for _, row := range d.rows {
			if io.Stopped() {
				return ErrStopped
			}
			stored := row.Name
			if stored == MarkerName {
				continue
			}
			_, state := nk.DecryptStoredName(stored, d.id, sidecar)
			if state == NameDecrypted || state == NameSidecar {
				continue
			}
			prog.Seen++
			plain, moved := nk.RecoverMoved(stored, d.id, ids, sidecar)
			if moved && NameProblem(plain) != "" {
				moved = false
				plain = ""
			}
			if !moved {
				if state != NamePlain {
					prog.fail(row.Path, "the name cannot be read here (moved in from another folder, or damaged)")
					continue
				}
				plain = stored
			}
			if p := NameProblem(plain); p != "" {
				prog.fail(row.Path, "a name a disk cannot hold ("+p+")")
				continue
			}
			// A folder keeps the id its contents are already sealed under.
			var dirID []byte
			if row.Dir {
				dirID = nk.EffectiveDirID(d.id, stored)
			}
			enc, err := nk.EncryptName(plain, d.id, dirID)
			for n := 2; err == nil && taken[enc.Stored] && n < 100; n++ {
				// Taken: something was written under this very name after the
				// change began. Keep both, as an upload conflict is kept.
				enc, err = nk.EncryptName(NumberedName(plain, n), d.id, dirID)
			}
			if err != nil {
				prog.fail(row.Path, err.Error())
				continue
			}
			if taken[enc.Stored] {
				prog.fail(row.Path, "no free name")
				continue
			}
			if enc.SidecarName != "" {
				if err := io.WriteSidecar(d.path, enc.SidecarName, enc.SidecarContent); err != nil {
					prog.fail(row.Path, err.Error())
					continue
				}
			}
			if err := io.Rename(d.path, row, enc.Stored); err != nil {
				prog.fail(row.Path, err.Error())
				continue
			}
			delete(taken, stored)
			taken[enc.Stored] = true
			prog.Renamed++
			if moved {
				prog.Repaired++
			}
		}
	}
	return nil
}

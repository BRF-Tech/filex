package filesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// ── discard: "these held files are not wanted on the server" ─────────────
//
// The other answer to a hold (hold.go): the files are not wanted on the
// server - typically the server cleaned them up, and this machine's copy is
// stale. They are deleted from this computer, not moved anywhere (task #259:
// no local sync trash).
//
// ⚠⚠ But only a file the server still holds an IDENTICAL copy of - the same
// bytes (sha256), in the person's trash or elsewhere on the server - is
// deleted (the owner, 2026-10-15). "Not on the server" is what made the file
// held in the first place, and the person deciding from a list cannot tell a
// stale copy of a cleaned-up file from the only copy of something written
// here: deleting the second loses it for good. A file the server has no copy
// of stays where it is and stays HELD - released, the next run would upload it
// again, the very thing the hold stops - and the person is told why, file by
// file (DiscardResult, `filex sync discard --json`, the desktop's dialog).

// CopyCheck is one held file the server is asked about.
type CopyCheck struct {
	Rel    string // pair-relative path
	Name   string // its name
	Size   int64
	SHA256 string // lower-case hex
}

// CopyFinder looks for an identical copy of a local file that the person can
// reach on the server (cmd/filex serverCopies: their trash, then the pair's
// storage). where names the copy for the person; "" with a nil error is "the
// server holds none". An error is "could not say", and the file stays.
type CopyFinder interface {
	FindCopy(ctx context.Context, c CopyCheck) (where string, err error)
}

// Why a held item stayed on this computer after a discard.
const (
	// KeptNoCopy: the server holds no identical copy. It stays held.
	KeptNoCopy = "no_copy"
	// KeptChanged: it changed after it was held - not what the person decided
	// about. It stays held.
	KeptChanged = "changed"
	// KeptUnchecked: the server could not be asked (Err says why). It stays held.
	KeptUnchecked = "unchecked"
	// KeptFailed: it had a copy, and could not be deleted (Err says why). It
	// stays held.
	KeptFailed = "failed"
	// KeptSynced: the engine has it in step on both sides by now (an identical
	// copy turned up on the server and was adopted, say). Deleting it here
	// would make the next run carry the deletion to the server; it is not held
	// any more.
	KeptSynced = "synced"
)

// DiscardItem is one held file a discard deleted or left in place.
type DiscardItem struct {
	Rel string
	// Where is, for a deleted file, where the server's copy is.
	Where string
	// Reason is, for a file left in place, why (Kept*).
	Reason string
	// Err is what went wrong, for KeptUnchecked and KeptFailed.
	Err string
}

// DiscardResult is what a discard did.
type DiscardResult struct {
	Deleted []DiscardItem
	Kept    []DiscardItem
	// StillHeld is how many items the pair still holds afterwards (files and
	// the folders holding them); 0 is "the hold is over".
	StillHeld int
}

// DiscardHeld answers a hold with "not wanted": every held file the server
// holds an identical copy of (find) is deleted from this computer, and a
// folder that held only such files is removed once empty. Every other held
// item stays in place and stays held, and the result says why. A file that
// is gone already is nothing to decide about.
//
// find nil (no server to ask) deletes nothing: every file stays, unchecked.
func (s *Store) DiscardHeld(ctx context.Context, p Pair, find CopyFinder) (DiscardResult, error) {
	var res DiscardResult
	h, err := s.loadHold(p.ID)
	if err != nil {
		return res, err
	}
	base, _, err := s.LoadBaseline(p.ID)
	if err != nil {
		return res, err
	}
	var files, dirs []string
	for rel, sig := range h.Items {
		if sig == "dir" {
			dirs = append(dirs, rel)
		} else {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	still := map[string]string{}
	keep := func(rel, reason string, err error) {
		it := DiscardItem{Rel: rel, Reason: reason}
		if err != nil {
			it.Err = err.Error()
		}
		res.Kept = append(res.Kept, it)
		if reason != KeptSynced {
			still[rel] = h.Items[rel]
		}
	}
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			// Stopped: what was not looked at stays held.
			keep(rel, KeptUnchecked, err)
			continue
		}
		lp, err := localPathOf(p.Local, rel)
		if err != nil {
			keep(rel, KeptUnchecked, err)
			continue
		}
		info, err := os.Lstat(lp)
		if errors.Is(err, os.ErrNotExist) {
			continue // already gone: nothing to decide
		}
		if err != nil {
			keep(rel, KeptUnchecked, err)
			continue
		}
		// ⚠⚠ A baseline row means the engine has since recorded it in step on
		// BOTH sides; deleting it here would delete it on the server too.
		if _, agreed := base[rel]; agreed {
			keep(rel, KeptSynced, nil)
			continue
		}
		if !info.Mode().IsRegular() || heldSig(rel, info) != h.Items[rel] {
			keep(rel, KeptChanged, nil)
			continue
		}
		if find == nil {
			keep(rel, KeptUnchecked, errors.New("no server to ask"))
			continue
		}
		sum, err := fileSHA256(lp)
		if err != nil {
			keep(rel, KeptUnchecked, err)
			continue
		}
		where, err := find.FindCopy(ctx, CopyCheck{Rel: rel, Name: path.Base(rel), Size: info.Size(), SHA256: sum})
		if err != nil {
			keep(rel, KeptUnchecked, err)
			continue
		}
		if where == "" {
			keep(rel, KeptNoCopy, nil)
			continue
		}
		// ⚠ Looked at again: the file may have been written while it was hashed
		// and the server asked. A different file is not the one compared.
		again, err := os.Lstat(lp)
		if err != nil || !again.Mode().IsRegular() || heldSig(rel, again) != h.Items[rel] {
			keep(rel, KeptChanged, nil)
			continue
		}
		if err := RemoveLocal(lp); err != nil {
			keep(rel, KeptFailed, err)
			continue
		}
		res.Deleted = append(res.Deleted, DiscardItem{Rel: rel, Where: where})
	}
	// Deepest first, and only when empty: a folder that still holds anything -
	// a file kept above - keeps it, and stays held with it.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/")
	})
	for _, rel := range dirs {
		if _, agreed := base[rel]; agreed {
			continue
		}
		lp, err := localPathOf(p.Local, rel)
		if err != nil {
			still[rel] = "dir"
			continue
		}
		if err := os.Remove(lp); err != nil && !errors.Is(err, os.ErrNotExist) {
			still[rel] = "dir"
		}
	}
	res.StillHeld = len(still)
	if len(still) == 0 {
		return res, s.saveHold(p.ID, holdFile{})
	}
	return res, s.saveHold(p.ID, holdFile{Holding: true, Items: still})
}

// heldSig is a local file's signature as the hold records it.
func heldSig(rel string, info os.FileInfo) string {
	return Node{Rel: rel, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}.Signature()
}

// fileSHA256 is the sha256 of a file's bytes, in lower-case hex.
func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

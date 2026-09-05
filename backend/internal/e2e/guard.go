package e2e

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ── The encryption boundary, and why a move may not cross it ─────────
//
// Uploads into an encrypted folder are encrypted in the browser before a
// byte leaves it. Copy, move and paste are not: they are server-side byte
// operations, and the server has no key. Left alone, filex's own UI would
// happily drop a PLAINTEXT file into an encrypted folder, where it looks
// like it belongs and is readable by anyone with disk access — and it would
// just as happily move an ENCRYPTED file out of one, to a place where the
// marker is gone, no password prompt will ever appear, and the bytes are
// scrap.
//
// The server cannot fix either case by encrypting or decrypting. So it
// refuses, here, in one function that every transfer surface calls — the
// web UI's paste and drag, the async ops queue, the sync move, the AI/MCP
// tools. A rule that lived in the explorer would be a rule WebDAV never
// heard of.
//
// The rule is one sentence: a copy or move is allowed only when its source
// and its destination sit in the same encryption context. "Same" means the
// same encrypted folder, or both outside any encrypted folder. Moving an
// encrypted folder ITSELF is fine, because its marker travels with it —
// unless the destination is inside another encrypted folder, since nesting
// them makes "which folder am I in" unanswerable.

// ErrPlaintextIntoEncrypted / ErrEncryptedOutOfFolder / ErrAcrossEncrypted
// are the three ways to cross the boundary. They are separate errors
// because the operator-facing advice differs for each.
var (
	ErrPlaintextIntoEncrypted = errors.New("cannot move or copy a file into an encrypted folder: the server has no key, so the file would be stored unencrypted inside it. Upload it through the web UI with the folder unlocked instead")
	ErrEncryptedOutOfFolder   = errors.New("cannot move or copy an encrypted file out of its folder: it stays encrypted, and outside the folder nothing knows which password opens it")
	ErrAcrossEncrypted        = errors.New("cannot move or copy between two different encrypted folders: each folder has its own key, and the destination's key cannot open the source's files")
	ErrNestedEncrypted        = errors.New("cannot move an encrypted folder into another encrypted folder: encrypted folders cannot be nested")
)

// TransferGuardError names the item that was refused alongside the reason,
// so the message a user sees says which file, not just "no".
type TransferGuardError struct {
	Source string
	Dest   string
	Reason error
}

func (e *TransferGuardError) Error() string {
	return fmt.Sprintf("%s: %s", e.Source, e.Reason.Error())
}

func (e *TransferGuardError) Unwrap() error { return e.Reason }

// GuardTransfer reports whether a copy/move of srcRels (relative paths on
// srcStorageID) into dstRel (a directory on dstStorageID) crosses an
// encryption boundary.
//
// A nil lookup, or a deployment with no encrypted folders at all, costs one
// cheap ancestor walk per item and always returns nil.
func GuardTransfer(ctx context.Context, lk NodeByPathLookup, srcStorageID int64, srcRels []string, dstStorageID int64, dstRel string) error {
	if lk == nil {
		return nil
	}
	dstRoot, dstEncrypted := FindRoot(ctx, lk, dstStorageID, dstRel)

	for _, src := range srcRels {
		src = strings.Trim(path.Clean("/"+strings.Trim(src, "/")), "/")
		if src == "" {
			continue
		}
		// Is the item ITSELF an encrypted folder root? FindRoot answers
		// "nearest marked ancestor-OR-SELF", so the test is whether it came
		// back with the path we asked about. Such a folder carries its
		// marker with it and may go anywhere not inside another one.
		if root, enc := FindRoot(ctx, lk, srcStorageID, src); enc && root == src {
			if dstEncrypted {
				return &TransferGuardError{Source: src, Dest: dstRel, Reason: ErrNestedEncrypted}
			}
			continue
		}

		srcRoot, srcEncrypted := FindRoot(ctx, lk, srcStorageID, parentOf(src))
		switch {
		case !srcEncrypted && !dstEncrypted:
			// Ordinary files going to an ordinary place.
		case !srcEncrypted && dstEncrypted:
			return &TransferGuardError{Source: src, Dest: dstRel, Reason: ErrPlaintextIntoEncrypted}
		case srcEncrypted && !dstEncrypted:
			return &TransferGuardError{Source: src, Dest: dstRel, Reason: ErrEncryptedOutOfFolder}
		case srcStorageID != dstStorageID:
			// Two encrypted folders on two storages are two folders, even if
			// the paths happen to match.
			return &TransferGuardError{Source: src, Dest: dstRel, Reason: ErrAcrossEncrypted}
		case srcRoot != dstRoot:
			return &TransferGuardError{Source: src, Dest: dstRel, Reason: ErrAcrossEncrypted}
		}
	}
	return nil
}

func parentOf(rel string) string {
	if idx := strings.LastIndex(rel, "/"); idx != -1 {
		return rel[:idx]
	}
	return ""
}

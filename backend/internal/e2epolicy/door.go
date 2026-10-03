package e2epolicy

import (
	"context"
	"errors"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// What a door asks around the rule: does its write create the name, does a
// rename, a move or a copy encrypt, and what it answers when the rule could
// not be decided. The HTTP doors (internal/api/handlers) and the protocol ones
// (internal/protoperm) both ask it here, so the two cannot judge one write two
// ways.

// ErrUndecided is what a door answers when the rule could not be decided: a
// lookup behind it failed (the tenant, the policy, the permissions, the
// approvals), or one the door made to ask it (the storage's row, the person a
// write is judged for). A server failure, never a refusal: a broken store must
// not read as the policy doing its job. Its words are the whole of it; what
// failed is in the log (DoorError).
var ErrUndecided = errors.New("could not check the encryption policy")

// DoorError is what a door answers for err, the rule's verdict on u writing
// on st: nil and a refusal (*RefusedError) as they are, ErrUndecided for any
// other error. That error is logged here, once, with who and where (the
// storage and the person) and what failed, and never the path: a file's name
// can say as much as its contents. ErrUndecided itself passes unchanged, so a
// door may hand on what an earlier one decided.
func DoorError(u *model.User, st *model.Storage, err error) error {
	if err == nil || errors.Is(err, ErrRefused) || errors.Is(err, ErrUndecided) {
		return err
	}
	var userID, storageID int64
	if u != nil {
		userID = u.ID
	}
	if st != nil {
		storageID = st.ID
	}
	slog.Error("e2e policy: could not decide a create",
		slog.Int64("storage_id", storageID), slog.Int64("user_id", userID), slog.String("err", err.Error()))
	return ErrUndecided
}

// FileThere reports whether a FILE is at rel on drv: Stat answers, and not
// with a folder. Only then is writing a key file or a `.fxe` at rel a rewrite
// (a password change, a key slot), never a new encryption.
//
// A folder with the name is not the file: an object store keeps the file
// beside it (a prefix and an object of one name), and a copy onto it replaces
// it. A Stat that fails for any reason, or no driver to ask, is not "it is
// there" either: a backend that could not answer must not switch the rule off.
// storage.Exists answers the other way round (for the snapshot guard) and is
// the wrong question here.
func FileThere(ctx context.Context, drv storage.Driver, rel string) bool {
	if drv == nil {
		return false
	}
	obj, err := drv.Stat(ctx, acl.CleanRel(rel))
	return err == nil && obj.Kind != storage.KindDirectory
}

// RelocationEncrypts reports whether giving the item at src the path dst (a
// rename, a move, or a copy under a name of the caller's choosing) is a new
// encryption, to be asked as a create at dst. Landing on a key file's
// (`.filex-e2e.json`) or a `.fxe`'s name is one, except in exactly three cases
// (operator decisions 2026-09-30 and 2026-10-03):
//
//   - src is a folder, under any name: a folder is no key file, and what it
//     holds is not asked, as a folder copy's contents are not;
//   - a `.fxe` that stays a `.fxe`: it was encrypted already;
//   - a key file that stays the key file of its own folder (sameStorage, one
//     folder: a change of case at most).
//
// Everything else is asked: a plain file given either name, a `.fxe` given
// the key file's, a key file given a `.fxe`'s, and a key file moved into
// another folder, on this storage or another. Each makes an encryption nobody
// was asked about: upload a `.fxe` holding a key file's bytes, rename it
// `.filex-e2e.json`, and its folder is encrypted.
//
// sameStorage: src and dst are on one storage. srcDrv is src's storage's
// driver. The names are read first, and src is looked at only when they do not
// decide. Only a clean "a folder" exempts it: a Stat that fails, or no driver
// to ask, counts as a file.
func RelocationEncrypts(ctx context.Context, srcDrv storage.Driver, src, dst string, sameStorage bool) bool {
	dstDir, dstKind := TargetOf(dst)
	if dstKind == "" {
		return false
	}
	// TargetOf's kind is what the name encrypts: a key file's is its folder's
	// (E2ERequestFolder), a `.fxe`'s its own (E2ERequestFile).
	switch srcDir, srcKind := TargetOf(src); {
	case srcKind == model.E2ERequestFile && dstKind == model.E2ERequestFile:
		return false // a .fxe stays a .fxe
	case srcKind == model.E2ERequestFolder && dstKind == model.E2ERequestFolder && sameStorage && srcDir == dstDir:
		return false // the key file stays its folder's
	}
	if srcDrv == nil {
		return true
	}
	obj, err := srcDrv.Stat(ctx, acl.CleanRel(src))
	return err != nil || obj.Kind != storage.KindDirectory
}

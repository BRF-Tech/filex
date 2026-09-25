package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ── conditional uploads ─────────────────────────────────────────────────
//
// `expect` is an optional precondition on an upload that REPLACES a file:
// "only write if the file is still what I last saw". The desktop sync engine
// sends it; nothing else has to.
//
// Why it exists: the engine decides "this changed on my disk, the server copy
// is untouched — upload it" from a listing it took a moment earlier. If
// somebody saves the same file in the browser inside that moment, a plain
// upload replaces their save with no trace in the sync folder — the server's
// version history still has it, but the file everyone sees is the other one.
// The engine's own rule for that case is "changed in both places: keep both",
// and it can only honour the rule if the write that would break it is REFUSED
// instead of performed. Once changes are pushed the instant they happen (the
// live change stream, v0.43.0), both saves landing in the same second is the
// ordinary shape of a person editing the same document on the web and on the
// desktop, not a corner case.
//
// Forms (a form field on the multipart upload, a query parameter on a staged
// commit):
//
//	expect=none            the target must not exist yet
//	expect=<size>:<mtime>  the target must be a file whose LISTING signature
//	                       (size, last_modified in Unix ms) is exactly this
//
// A mismatch answers 412 with code PRECONDITION_FAILED and writes nothing.
// ⚠ The signature is read from the catalogue row — the same row the listing
// that the client planned from was built from (projectFileNodes) — so the two
// can never disagree about the same unchanged file. Comparing against a driver
// Stat instead would refuse every upload on a storage whose driver reports a
// different mtime than the catalogue carries.
//
// An older server ignores the unknown field and writes as it always did; the
// client is no worse off than before, and the next round still reconciles.

const uploadExpectNone = "none"

// errPreconditionBody is the fixed 412 body.
var errPreconditionBody = map[string]string{
	"error": "the file changed on the server since it was listed",
	"code":  "PRECONDITION_FAILED",
}

// listingMtimeMillis is the last_modified a listing reports for a node — the
// ONE definition both the listing and the upload precondition use.
func listingMtimeMillis(n *model.Node) (int64, bool) {
	if n.BackendMtime != nil {
		return n.BackendMtime.UnixMilli(), true
	}
	if !n.CreatedAt.IsZero() {
		return n.CreatedAt.UnixMilli(), true
	}
	return 0, false
}

// uploadExpectHolds reports whether the precondition `expect` holds for rel on
// storageID. An empty expect always holds. A malformed one never does — a
// client that asked for a check must not get an unchecked write because it
// spelled the check wrong.
//
// ⚠ The catalogue is not the only thing a listing is built from any more. A
// folder the catalogue cannot vouch for — a storage whose first scan is still
// running, a lazily catalogued folder nobody has had catalogued yet
// (docs/LAZY-CATALOGUE.md) — is listed from the storage itself, so the client
// may have planned from the DRIVER's size and date. resolve (optional) lets
// the check ask the storage too:
//
//   - `none` also requires that nothing is at rel on the storage: a file that
//     is there but has no row would otherwise be replaced by a write that asked
//     for an empty spot. A storage that cannot answer does not block it.
//   - `<size>:<mtime>` that does not match the row (or finds no row) holds when
//     it matches the file on the storage exactly — which is what the client
//     saw, and what is there now. A row that matches is still enough on its
//     own, so a driver whose dates differ from the catalogue's changes nothing.
func uploadExpectHolds(ctx context.Context, store db.Store, resolve func(int64) (storage.Driver, error), storageID int64, rel, expect string) bool {
	expect = strings.TrimSpace(expect)
	if expect == "" {
		return true
	}
	clean := normalizeDBPath(rel)
	n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, clean))
	if err != nil || n == nil {
		n = nil
	}
	if expect == uploadExpectNone {
		if n != nil {
			return false
		}
		obj, known := statOnStorage(ctx, resolve, storageID, clean)
		return !known || obj == nil
	}
	sizeStr, modStr, ok := strings.Cut(expect, ":")
	if !ok {
		return false
	}
	size, err1 := strconv.ParseInt(sizeStr, 10, 64)
	mod, err2 := strconv.ParseInt(modStr, 10, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	if n != nil {
		if n.Type == model.NodeTypeDirectory {
			return false
		}
		if got, _ := listingMtimeMillis(n); n.Size == size && got == mod {
			return true
		}
	}
	obj, known := statOnStorage(ctx, resolve, storageID, clean)
	return known && obj != nil && obj.Kind == storage.KindFile && obj.Size == size && obj.Mtime.UnixMilli() == mod
}

// statOnStorage asks the storage what is at rel. known is false when it could
// not say (no resolver, no driver, an error other than "not found"); obj is nil
// when it said "nothing".
func statOnStorage(ctx context.Context, resolve func(int64) (storage.Driver, error), storageID int64, rel string) (obj *storage.Object, known bool) {
	if resolve == nil {
		return nil, false
	}
	drv, err := resolve(storageID)
	if err != nil || drv == nil {
		return nil, false
	}
	o, err := drv.Stat(ctx, strings.TrimPrefix(rel, "/"))
	switch {
	case err == nil:
		return &o, true
	case errors.Is(err, storage.ErrNotFound):
		return nil, true
	default:
		return nil, false
	}
}

// writePreconditionFailed answers a refused conditional upload.
func writePreconditionFailed(w http.ResponseWriter) {
	writeJSON(w, http.StatusPreconditionFailed, errPreconditionBody)
}

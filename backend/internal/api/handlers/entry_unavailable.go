package handlers

// An entry the storage could not answer for (issue #104, migration 00078).
//
// The storage sync marks a catalogue row whose object its Stat could neither
// find nor rule out (internal/sync unavailable.go). Such a row stays listed -
// with a warning, so the person can see it is there and why nothing works on
// it - and every surface refuses to act on it, or on anything below it, with
// 409 ENTRY_UNAVAILABLE until the storage answers for it again:
//
//	{"error": "...", "code": "ENTRY_UNAVAILABLE", "path": "s3://Proje", "reason": "<the storage's answer>"}
//
// Opening, previewing, downloading, listing into it, moving, copying,
// renaming, deleting, sharing, editing, archiving: the manager (require,
// streamBody, the listing verbs), the id-addressed reads, sharing, the text
// editor, archives, versions and the AI/MCP surface (resolveStorage) all ask
// here. ⚠ Refused on the server, not only hidden in the explorer: an API
// client, an agent or an old explorer never sees the warning.
//
// ⚠ WebDAV, SFTP, FTPS, NFS and the S3 gateway do NOT ask: they serve the
// storage driver directly and never read the catalogue's view of a path, so a
// client of theirs meets whatever the storage itself answers (docs/PROTOCOLS.md).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// CodeEntryUnavailable is the refusal's code.
const CodeEntryUnavailable = "ENTRY_UNAVAILABLE"

// errEntryUnavailable matches every entryUnavailableError (errors.Is).
var errEntryUnavailable = errors.New("entry unavailable")

// entryUnavailableError names the entry that makes a path unavailable - the
// path itself, or the folder above it - and what the storage answered.
type entryUnavailableError struct {
	Path   string // adapter://rel
	Reason string
}

func (e *entryUnavailableError) Error() string {
	return fmt.Sprintf("%s is unavailable: the storage could not say whether it still exists (%s); "+
		"nothing can be done with it, or with anything inside it, until the storage answers for it again (%s)",
		e.Path, e.Reason, CodeEntryUnavailable)
}

func (e *entryUnavailableError) Is(target error) bool { return target == errEntryUnavailable }

// body is the 409's JSON: the code and the sentence in lang (apierr), the
// entry and the storage's own answer beside them. The English Error() stays
// the log's.
func (e *entryUnavailableError) body(lang string) map[string]any {
	return apierr.Map(lang, "entry_unavailable", nil, map[string]any{"code": CodeEntryUnavailable, "path": e.Path, "reason": e.Reason})
}

// unavailableIn returns the refusal for rel on s, or nil when rel - and every
// folder above it - is an ordinary entry. A lookup that fails lets the call
// through: the storage is asked anyway, and it answers for itself.
func unavailableIn(ctx context.Context, store db.Store, s *model.Storage, rel string) error {
	if store == nil || s == nil {
		return nil
	}
	e, err := store.UnavailableAt(ctx, s.ID, rel)
	if err != nil || e == nil {
		return nil
	}
	return &entryUnavailableError{Path: joinAdapterPath(s.Name, strings.Trim(e.Path, "/")), Reason: e.Reason}
}

// refuseUnavailable answers 409 ENTRY_UNAVAILABLE and returns true when rel on
// s is unavailable (unavailableIn).
func refuseUnavailable(w http.ResponseWriter, r *http.Request, store db.Store, s *model.Storage, rel string) bool {
	err := unavailableIn(r.Context(), store, s, rel)
	if err == nil {
		return false
	}
	var ue *entryUnavailableError
	if errors.As(err, &ue) {
		writeJSON(w, http.StatusConflict, ue.body(langOf(r)))
	}
	return true
}

// refuseUnavailableID is refuseUnavailable for a caller that holds a storage
// id (the archive endpoints).
func refuseUnavailableID(w http.ResponseWriter, r *http.Request, store db.Store, storageID int64, rel string) bool {
	if store == nil {
		return false
	}
	st, err := store.GetStorage(r.Context(), storageID)
	if err != nil || st == nil {
		return false
	}
	return refuseUnavailable(w, r, store, st, rel)
}

// refuseUnavailableNode is refuseUnavailable for a row already in hand: the
// row's own mark is enough, and only a folder above it is looked up.
func refuseUnavailableNode(w http.ResponseWriter, r *http.Request, store db.Store, s *model.Storage, n *model.Node) bool {
	if n == nil || s == nil {
		return false
	}
	if n.Unavailable {
		writeJSON(w, http.StatusConflict, (&entryUnavailableError{
			Path: joinAdapterPath(s.Name, strings.Trim(n.Path, "/")), Reason: n.UnavailableReason,
		}).body(langOf(r)))
		return true
	}
	return refuseUnavailable(w, r, store, s, n.Path)
}

// linkTargetUnavailable reports whether what a public link serves - full, a
// path on storageID: the shared row's own, or one below a shared folder - is
// an entry the storage could not answer for, or inside one. The share page,
// its no-JS pages and its downloads ask it, so a visitor is told so in their
// language instead of meeting the storage driver's own error; minting a NEW
// link on such an entry is refused already (Share.HandleCreate). A lookup
// that fails lets the request through, as unavailableIn does.
func linkTargetUnavailable(ctx context.Context, store db.Store, storageID int64, full string) bool {
	if store == nil {
		return false
	}
	e, err := store.UnavailableAt(ctx, storageID, full)
	return err == nil && e != nil
}

// unavailableFields adds the listing's two keys for a marked row; an ordinary
// row gets none, so a client that does not know them sees the row it always
// did.
func unavailableFields(entry map[string]any, n *model.Node) {
	if n == nil || !n.Unavailable {
		return
	}
	entry["unavailable"] = true
	entry["unavailable_reason"] = n.UnavailableReason
}

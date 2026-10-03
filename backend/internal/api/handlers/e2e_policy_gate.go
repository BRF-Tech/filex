package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Who may encrypt (internal/e2epolicy), at the HTTP doors.
//
// Creating an encrypted folder's key file (`.filex-e2e.json`) or a single
// encrypted file (`*.fxe`) is a NEW encryption: the service provider's ceiling
// for the tenant, the tenant's policy, files.encrypt on that path and — under
// the approval policy — an approval must all allow it. Replacing a key file
// or a `.fxe` that is there (a password change, a recovery key, a level
// change) is not a new encryption and never asks.
//
// Every door that can create a file asks one of two ways:
//   - a door whose write may also replace a file asks checkE2EWrite
//     (refuseE2EWrite): the write creates unless a FILE is at the path. The
//     door's own "is it there" — files.create or files.modify, a catalogue
//     row, an archive it could read — is the permission's question, and it
//     reads a folder with the name as something to replace;
//   - a door whose write only ever creates asks for every write
//     (checkE2ECreate, refuseE2ECreate): a new document, a draft's save and a
//     new archive refuse a name that is taken, a drop writes into a fresh
//     submission folder, an app's output takes a free name.
//
// ⚠ Asked ONCE per create. Under the approval policy CheckCreate spends the
// approval, so a door that asked twice — at the start of an upload and again
// at its commit — would refuse its own second question. The resumable and the
// presigned uploads ask at begin/init, never at commit/finalize.
//
// A rename, a move or a copy that lands an item on a key file's or a `.fxe`'s
// name is a new encryption as well (operator decisions 2026-09-30 and
// 2026-10-03): upload rapor.bin, rename it rapor.bin.fxe, and a `.fxe` exists
// that nobody was asked about. Every HTTP door that gives an item a name of
// the caller's choosing asks checkE2ERename (refuseE2ERenameAt): the
// explorer's rename (and the queue's rename it hands a folder to), the queue's
// copy and move under a name — POST /api/files/copy and /move with `name`,
// /api/files/ops with a literal destination — and the agent's move
// (/api/ai/move, MCP file_move). Free are only a folder under any name, a
// `.fxe` that stays a `.fxe`, and a key file that stays its own folder's
// (e2epolicy.RelocationEncrypts).
//
// Deliberately NOT asked (spec, clarification 2): those three, a plain file
// moved or copied under its own name (into an encrypted folder too: the
// transfer guard's business), a restore from the trash or from a version, and
// the document server's save. They bring back or move what is already
// encrypted, or encrypt nothing.

// e2eRefusalBody is the answer when the rule says no: the code a client
// branches on, the reason (e2epolicy.Reason) it words in its reader's
// language, and the server's own sentence for an API client's log.
type e2eRefusalBody struct {
	Error   string `json:"error"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// writeE2ERefusal answers err when it is the rule's refusal
// (*e2epolicy.RefusedError): 403 {"error":"e2e_not_allowed","reason":…,
// "message":…}. It reports whether it wrote; nil and every other error are
// left to the caller.
func writeE2ERefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var re *e2epolicy.RefusedError
	if !errors.As(err, &re) {
		return false
	}
	writeJSON(w, http.StatusForbidden, e2eRefusalBody{
		Error:   "e2e_not_allowed",
		Reason:  string(re.Reason),
		Message: srvtext.Text(langOf(r), "server.e2e.not_allowed."+string(re.Reason), nil),
	})
	return true
}

// isE2ERefusal reports whether err carries the rule's refusal — for a surface
// that maps errors to statuses (aiStatus).
func isE2ERefusal(err error) bool { return errors.Is(err, e2epolicy.ErrRefused) }

// isE2EUndecided reports whether err is a rule that could not be decided
// (e2epolicy.ErrUndecided), already logged where it was asked.
func isE2EUndecided(err error) bool { return errors.Is(err, e2epolicy.ErrUndecided) }

// checkE2ECreate asks the rule whether u may CREATE rel on st. CheckCreate
// answers nil for a name that encrypts nothing before it looks anything up,
// and refuses a nil person or storage. A rule that could not be decided is
// logged and answered as e2epolicy.ErrUndecided (DoorError), so no surface
// passes the store's error on. A nil service is the rule unwired (a handler
// built by hand in a test) and allows, like a nil ACL resolver (aclCanID).
func checkE2ECreate(ctx context.Context, svc *e2epolicy.Service, u *model.User, st *model.Storage, rel string) error {
	if svc == nil {
		return nil
	}
	return e2epolicy.DoorError(u, st, svc.CheckCreate(ctx, u, st, strings.Trim(rel, "/")))
}

// checkE2EWrite is checkE2ECreate for a door that has not asked yet whether
// its write creates rel: an upload, a save, an agent's write, an archive or
// its member — any write that puts a FILE at rel, a destination included.
//
// It creates rel unless a FILE is there (e2epolicy.FileThere): a folder with
// the name, or a Stat the backend could not answer, counts as a create.
func checkE2EWrite(ctx context.Context, svc *e2epolicy.Service, drv storage.Driver, u *model.User, st *model.Storage, rel string) error {
	if svc == nil || !e2epolicy.IsEncryptionName(rel) || e2epolicy.FileThere(ctx, drv, rel) {
		return nil
	}
	return checkE2ECreate(ctx, svc, u, st, rel)
}

// refuseE2ECreate is checkE2ECreate for the caller of an HTTP door that has
// decided its write creates rel (answerE2E).
func refuseE2ECreate(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service, st *model.Storage, rel string) bool {
	return answerE2E(w, r, st, checkE2ECreate(r.Context(), svc, auth.UserFrom(r.Context()), st, rel))
}

// refuseE2EWrite is checkE2EWrite for the caller of an HTTP door about to
// write a FILE at rel on drv, where a file may already be: the rule's own look
// says whether the write creates it (answerE2E).
func refuseE2EWrite(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service, drv storage.Driver, st *model.Storage, rel string) bool {
	return answerE2E(w, r, st, checkE2EWrite(r.Context(), svc, drv, auth.UserFrom(r.Context()), st, rel))
}

// answerE2E writes what an HTTP door answers for err, the rule's verdict on
// its write to st, and reports true when the request is done: the 403 for a
// refusal, a 500 when the rule could not be decided — an undecidable rule is
// not a yes. nil leaves the request to the door.
//
// The undecided rule was logged where it was asked (e2epolicy.DoorError):
// who and where, never the path. An error that did not pass there — a lookup
// this door made — is logged the same way here.
func answerE2E(w http.ResponseWriter, r *http.Request, st *model.Storage, err error) bool {
	if err == nil {
		return false
	}
	if writeE2ERefusal(w, r, err) {
		return true
	}
	err = e2epolicy.DoorError(auth.UserFrom(r.Context()), st, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	return true
}

// refuseE2ECreateAt is refuseE2ECreate for a door that holds the storage's id
// only. It hands refuseE2EWriteAt no driver: with nothing to look at,
// checkE2EWrite counts every write as a create, which is what this door has
// decided.
func refuseE2ECreateAt(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service, store db.Store, storageID int64, rel string) bool {
	return refuseE2EWriteAt(w, r, svc, nil, store, storageID, rel)
}

// refuseE2EWriteAt is refuseE2EWrite for a door that holds the storage's id
// only: the row is read when the rule is to be asked. A row that cannot be
// read leaves the rule undecided, a server failure (answerE2E), not a refusal.
func refuseE2EWriteAt(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service, drv storage.Driver, store db.Store, storageID int64, rel string) bool {
	if svc == nil || !e2epolicy.IsEncryptionName(rel) || e2epolicy.FileThere(r.Context(), drv, rel) {
		return false
	}
	st, err := store.GetStorage(r.Context(), storageID)
	if err != nil {
		// Only the id is known; it is all the log line names.
		return answerE2E(w, r, &model.Storage{ID: storageID}, fmt.Errorf("storage %d: %w", storageID, err))
	}
	return refuseE2ECreate(w, r, svc, st, rel)
}

// checkE2ERename is the rule for an HTTP door that gives src, on srcDrv, the
// new path dst on st: asked only when that is a new encryption
// (e2epolicy.RelocationEncrypts; sameStorage: src is on st too), and then as
// a create at dst.
//
// ⚠ A create, not checkE2EWrite's "a file there is a rewrite": no HTTP door
// that renames replaces what holds its destination. A rename refuses a taken
// name; a move or a copy lands on a free name beside it (ops.MoveDest,
// ops.UniqueDest) — and beside a `.fxe` that name is `…-copy.fxe`, a new
// `.fxe` all the same.
func checkE2ERename(ctx context.Context, svc *e2epolicy.Service, srcDrv storage.Driver, u *model.User, st *model.Storage, src, dst string, sameStorage bool) error {
	if svc == nil || !e2epolicy.RelocationEncrypts(ctx, srcDrv, src, dst, sameStorage) {
		return nil
	}
	return checkE2ECreate(ctx, svc, u, st, dst)
}

// refuseE2ERenameAt is checkE2ERename for an HTTP door that holds the
// destination storage's id (answerE2E): the row is read when the rule is
// asked. done: it answered the request.
//
// settled: the landing is settled for good — the names free it, the rule was
// asked and allowed, or the rule is not wired — rather than free only because
// src is a folder NOW. A door that queues the work hands the settled sources
// on (ops.WithEncryptionSettled): the worker looks again at every other one,
// and fails it if a file has taken the folder's place by then.
func refuseE2ERenameAt(w http.ResponseWriter, r *http.Request, svc *e2epolicy.Service, store db.Store, srcDrv storage.Driver, storageID int64, src, dst string, sameStorage bool) (done, settled bool) {
	// With no driver, src counts as a file: the names' own answer.
	if svc == nil || !e2epolicy.RelocationEncrypts(r.Context(), nil, src, dst, sameStorage) {
		return false, true
	}
	if !e2epolicy.RelocationEncrypts(r.Context(), srcDrv, src, dst, sameStorage) {
		return false, false
	}
	if refuseE2ECreateAt(w, r, svc, store, storageID, dst) {
		return true, false
	}
	return false, true
}

// e2eActor is the account a write is judged for when nobody signed in made
// it: an app's output (CommitSibling's actor), a file request's creator. nil
// when there is none, or none any more (sql.ErrNoRows), which the rule
// refuses. An account that cannot be looked up leaves the rule undecided
// (e2epolicy.ErrUndecided, logged for st): not a refusal.
func e2eActor(ctx context.Context, store db.Store, st *model.Storage, id *int64) (*model.User, error) {
	if id == nil || store == nil {
		return nil, nil
	}
	u, err := store.GetUser(ctx, *id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, e2epolicy.DoorError(&model.User{ID: *id}, st, fmt.Errorf("the account %d: %w", *id, err))
	}
	return u, nil
}

// refusesE2E is the rule for a file request (drop.go), asked before the
// submission folder or a byte is written.
//
// Every file of a drop is NEW — it lands in a fresh submission folder — so
// one named like an encrypted folder's key file or a `.fxe` is a new
// encryption, judged for the LINK CREATOR: it lands in their storage as their
// file, and the visitor has no account to judge (a link with no recorded
// creator has nobody, and is refused). The visitor reads the sentence the page
// has for any file the link does not take (dropRefusalText); the body still
// names e2e_not_allowed and the reason for a script. It answers itself and
// reports true.
//
// ⚠ Judged without spending (CheckCreateWithoutApproval, operator decision
// 2026-10-03): an approval is the creator's, for an encryption of their own,
// and a visitor must not use it up. Under the approval policy such a file is
// refused as approval_required, and the approval stays unused.
func (h *Drop) refusesE2E(w http.ResponseWriter, r *http.Request, sh *model.Share, st *model.Storage, subRel string, files []*multipart.FileHeader) bool {
	svc := h.Manager.E2EPolicy
	if svc == nil {
		return false
	}
	var (
		creator *model.User
		looked  bool
	)
	for _, fh := range files {
		name, ok := sanitizeUploadName(fh.Filename)
		if !ok || !e2epolicy.IsEncryptionName(name) {
			continue
		}
		var err error
		if !looked {
			creator, err = e2eActor(r.Context(), h.Store, st, sh.CreatedBy)
			looked = err == nil
		}
		if err == nil {
			err = e2epolicy.DoorError(creator, st, svc.CheckCreateWithoutApproval(r.Context(), creator, st, strings.Trim(path.Join(subRel, name), "/")))
		}
		if err == nil {
			continue
		}
		// The page's own answers: the refusal of a file the link does not
		// take, or its server failure. An undecided rule was logged where it
		// was asked, without the submission folder or the file's name.
		var re *e2epolicy.RefusedError
		if errors.As(err, &re) {
			h.refuse(w, r, http.StatusForbidden, map[string]any{"error": "e2e_not_allowed", "reason": string(re.Reason)})
		} else {
			h.refuse(w, r, http.StatusServiceUnavailable, map[string]any{"error": "storage_unavailable"})
		}
		return true
	}
	return false
}

// checkE2E is checkE2EWrite for an agent's write of a FILE at rel on s
// (WriteStream, Zip), with s's driver to look at. A driver that cannot be
// reached leaves nothing to look at, and the write counts as a create.
func (a *aiOps) checkE2E(ctx context.Context, s *model.Storage, rel string) error {
	drv, err := a.resolver(s.ID)
	if err != nil {
		drv = nil
	}
	return checkE2EWrite(ctx, a.e2e, drv, auth.UserFrom(ctx), s, rel)
}

// AttachE2EPolicy wires the rule into the agent REST surface: WriteStream
// (upload), Zip and Unzip ask it.
func (h *AI) AttachE2EPolicy(s *e2epolicy.Service) { h.ops.e2e = s }

// AttachE2EPolicy wires the rule into every ops core the MCP surface builds
// per call (getServer).
func (h *AIMCP) AttachE2EPolicy(s *e2epolicy.Service) { h.e2e = s }

// AttachE2EPolicy wires the rule into ShareX captures.
func (h *ShareX) AttachE2EPolicy(s *e2epolicy.Service) { h.ops.e2e = s }

// AttachE2EPolicy wires the rule into the ticket redeem (/u/{ticket}): the
// write runs as the minter, and is judged as theirs.
func (h *TicketUpload) AttachE2EPolicy(s *e2epolicy.Service) { h.ops.e2e = s }

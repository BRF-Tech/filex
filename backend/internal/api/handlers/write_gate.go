package handlers

import (
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// gate is the one question every person-facing HTTP write asks before it
// touches a storage — writegate.Check: filex's own names first, then app
// locks — and it answers the refusal itself: 403 RESERVED_NAME, or 423 with
// who holds the lock and why (lockedAnswer). Reports whether it answered.
//
// ⚠⚠ One call per door, both rules in it (2026-09-21). The explorer's verbs
// had a lock check of their own (aclLockWithin) and every other door had
// none: the document editor's save, an agent's delete of the folder around a
// frozen document and an archive extracted over it all changed a document
// the signing app had frozen. The reserved-name rule had just been added to
// the same doors one call at a time; putting the lock rule in that same call
// is what keeps a future door from honouring one and forgetting the other.
//
// Locks are read fresh for the storage (acl.Resolver.Locks), not from the
// caller's permission set: they bind everyone, the administrator included,
// and a lock taken a second ago counts.
//
// A request an AI door runs (ai_doors.go: an MCP tool or /api/ai route that
// reuses this handler) holds no encryption key, so its plain writes are judged
// as the AI surface's own (aiOps.gate): syspath.Keyless, which adds an
// encrypted folder's key file to the refused names. Without it a copy, a
// restore or a version rollback through the AI surface could write the one
// file the AI surface's own tools never may.
func gate(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, storageID int64, targets ...writegate.Target) bool {
	if keylessDoor(r.Context()) {
		for i := range targets {
			targets[i] = keyless(targets[i])
		}
	}
	err := writegate.Check(liveLocks(r, resolver, storageID), 0, targets...)
	if answerVaultGate(w, langOf(r), err) {
		return true
	}
	return answerGate(w, r, err)
}

// answerVaultGate writes the refusal of writegate's vault rule
// (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else), in lang ("" = the
// server's default language), and reports whether err was one:
//
//   - 403 VAULT_PATH: the write touches something strictly inside a vault
//     folder, where only the vault API (/api/files/e2e/vault/*) writes;
//   - 409 VAULT_KEYFILE: it would change a vault's key file other than by
//     its usual door, or remove or move it on its own.
func answerVaultGate(w http.ResponseWriter, lang string, err error) bool {
	var ve *writegate.VaultPathError
	switch {
	case err == nil:
		return false
	case errors.As(err, &ve) && ve.KeyFile, errors.Is(err, writegate.ErrVaultKeyFile):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "VAULT_KEYFILE",
			"code":    "VAULT_KEYFILE",
			"message": srvtext.Text(lang, "server.e2e.vault.keyfile", nil),
		})
		return true
	case errors.Is(err, writegate.ErrVaultPath):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":   "VAULT_PATH",
			"code":    "VAULT_PATH",
			"message": srvtext.Text(lang, "server.e2e.vault.path", nil),
		})
		return true
	}
	return false
}

// liveLocks is the storage's live lock table, or nil when no ACL resolver is
// wired (tests) — which writegate reads as "nothing is locked".
func liveLocks(r *http.Request, resolver *acl.Resolver, storageID int64) writegate.Locks {
	if resolver == nil {
		return nil
	}
	return resolver.Locks(r.Context(), storageID)
}

// answerGate writes the refusal for an error from writegate.Check (or from a
// service that passed one through) and reports whether it did.
func answerGate(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	// The vault rule, for the doors that pass a service's error on (the
	// queue, a share, an app's interface): worded in the reader's language.
	if answerVaultGate(w, langOf(r), err) {
		return true
	}
	var re *syspath.ReservedError
	if errors.As(err, &re) {
		writeReserved(w, r, re.Rel)
		return true
	}
	var le *writegate.LockedError
	if errors.As(err, &le) {
		lockedAnswer(w, r, le.Lock, le.Rel)
		return true
	}
	switch {
	case errors.Is(err, syspath.ErrReserved):
		// No name travels with the bare sentinel: the sentence that names
		// none, under the same code.
		body := errorBody(r, "reserved_name", nil, "code", "RESERVED_NAME")
		body["message"] = apierr.Text(langOf(r), "reserved_any", nil)
		writeJSON(w, http.StatusForbidden, body)
		return true
	case errors.Is(err, writegate.ErrLocked):
		writeError(w, r, http.StatusLocked, "locked", nil)
		return true
	}
	return false
}

// writeReserved is the one shape of the reserved-name refusal.
//
// 403 with `RESERVED_NAME`, not 404: the person named the path themselves, so
// there is nothing to hide, and "not found" for a folder they are trying to
// CREATE would be a lie.
func writeReserved(w http.ResponseWriter, r *http.Request, rel string) {
	name := syspath.Reserved(rel)
	writeError(w, r, http.StatusForbidden, "reserved_name", apierr.Params{"name": name}, "code", "RESERVED_NAME", "name", name)
}

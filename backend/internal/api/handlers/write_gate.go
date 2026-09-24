package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
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
func gate(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, storageID int64, targets ...writegate.Target) bool {
	return answerGate(w, writegate.Check(liveLocks(r, resolver, storageID), 0, targets...))
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
func answerGate(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var re *syspath.ReservedError
	if errors.As(err, &re) {
		writeReserved(w, re.Rel)
		return true
	}
	var le *writegate.LockedError
	if errors.As(err, &le) {
		lockedAnswer(w, le.Lock, le.Rel)
		return true
	}
	switch {
	case errors.Is(err, syspath.ErrReserved):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "code": "RESERVED_NAME"})
		return true
	case errors.Is(err, writegate.ErrLocked):
		writeJSON(w, http.StatusLocked, map[string]string{"error": "locked", "message": err.Error()})
		return true
	}
	return false
}

// writeReserved is the one shape of the reserved-name refusal.
//
// 403 with `RESERVED_NAME`, not 404: the person named the path themselves, so
// there is nothing to hide, and "not found" for a folder they are trying to
// CREATE would be a lie.
func writeReserved(w http.ResponseWriter, rel string) {
	name := syspath.Reserved(rel)
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": fmt.Sprintf("%q is reserved for filex's own use", name),
		"code":  "RESERVED_NAME",
		"name":  name,
	})
}

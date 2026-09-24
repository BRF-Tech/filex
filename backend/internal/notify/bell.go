package notify

import "github.com/brf-tech/filex/backend/internal/model"

// Bell says which broadcasts — rows addressed to nobody — a per-user read
// takes from the store. Rows addressed to the reader are always read.
//
// The broadcast half is decided IN SQL, not after the page is cut: a bell whose
// page is filled with rows the reader may not be shown comes back empty while
// the reader's own rows sit on page two. Which rows of the admitted kinds the
// reader then keeps (the tenant, the grants) is the HTTP layer's per-row pass —
// handlers/notifications.go.
type Bell int

const (
	// AdminBell is the bell of an admin nobody confines — a single-tenant
	// install's admin, or the supertenant's: every broadcast except routine
	// file activity nobody was addressed by (personalEvents).
	AdminBell Bell = iota
	// TenantAdminBell is a tenant admin's: only the broadcasts that name a
	// file (fileBroadcastEvents), because only those can be placed in a
	// tenant. An operator alarm names no storage and would be dropped after
	// it had already taken a place on the page.
	TenantAdminBell
	// MemberBell is everybody else's: only memberBroadcastEvents.
	MemberBell
)

// personalEvents is routine file activity. Every emitter addresses it to the
// person who did the thing (writehook.emit, handlers.emitFileEvent), so a row
// of one of these with no user is not a broadcast anybody was meant to receive:
// it is a surface that could not say who asked — the ops worker, until it
// learnt to pass its actor on, wrote every queued copy/move/delete and every
// staged upload this way. No bell reads them; the admin-global list keeps
// them, because that list is the audit.
var personalEvents = []EventType{
	EventFileUploaded,
	EventFileUpdated,
	EventFileMoved,
	EventFileTrashed,
	EventFileDeleted,
}

// fileBroadcastEvents are the broadcasts that name a file (they carry a
// NodeRef, so meta.node.storage_id places them in a tenant): an antivirus hit,
// an upload that never landed, and the notices that fall back to a broadcast
// when their emitter finds no owner to address — a drop, an escrow opening, a
// share or a comment with no actor on record.
//
// ⚠ An allowlist on purpose: a new broadcast kind reaches a tenant admin's
// bell only once somebody decides it can be placed.
var fileBroadcastEvents = []EventType{
	EventFileInfected,
	EventFileUploadFailed,
	EventDropReceived,
	EventE2EEscrowUsed,
	EventShareCreated,
	EventCommentAdded,
}

// memberBroadcastEvents are the only broadcasts a member may receive, and only
// when the file they name is one the member can see: an antivirus hit on a
// file they work with, an upload into their folder that never landed.
//
// Every other broadcast is for admins: an operator alarm names no storage at
// all, a drop or share notice with no owner carries the link's bearer token
// (a viewer holding a drop link can write into the folder), and an escrow
// notice with no owner is addressed to admins by its emitter (handlers/e2e.go).
//
// ⚠ An allowlist on purpose: a new broadcast kind reaches no member until
// somebody decides it should.
var memberBroadcastEvents = []EventType{
	EventFileInfected,
	EventFileUploadFailed,
}

// MemberMayReceive reports whether a broadcast of this event may reach a
// member at all (the file it names must still be one they can see).
func MemberMayReceive(event string) bool {
	for _, e := range memberBroadcastEvents {
		if string(e) == event {
			return true
		}
	}
	return false
}

// filter is the store's form of b for a per-user read.
func (b Bell) filter() model.BroadcastFilter {
	switch b {
	case MemberBell:
		return model.BroadcastFilter{Only: eventIDs(memberBroadcastEvents)}
	case TenantAdminBell:
		return model.BroadcastFilter{Only: eventIDs(fileBroadcastEvents)}
	default:
		return model.BroadcastFilter{Except: eventIDs(personalEvents)}
	}
}

// bellFilter is b's filter for a bell read and the zero filter for the
// admin-global one (userID nil), which is the audit and keeps every row.
func bellFilter(userID *int64, b Bell) model.BroadcastFilter {
	if userID == nil {
		return model.BroadcastFilter{}
	}
	return b.filter()
}

func eventIDs(events []EventType) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e)
	}
	return out
}

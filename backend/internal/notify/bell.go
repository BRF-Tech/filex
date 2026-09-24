package notify

import (
	"encoding/json"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Who reads a notification — the ONE audience rule, for every door a row goes
// out through: the bell and its badge, the full list behind it, the browser
// notification and the desktop toast (both read GET /api/notifications), the
// admin history's Scope column, and "mark read".
//
// A row ADDRESSED to somebody is theirs and nobody else's. A BROADCAST (no
// user) is decided in two layers:
//
//  1. By kind, here, in SQL (Bell → model.BroadcastFilter), so a bell's page is
//     filled with rows its reader can be shown rather than cut first and
//     emptied after (PR #42, Berk Başarır: 60 operator rows once left a member
//     `{"items":[],"total":0}` under a badge of 1).
//  2. Per row, in the HTTP layer (handlers/notifications.go → visibleTo): the
//     tenant the file's storage belongs to, and for a member the explorer's
//     own "may they see this path" (acl.Set.CanSee).
//
// The kinds, and why:
//
//   - Routine file activity (personalEvents) is always addressed to whoever
//     did it; a broadcast of one is an actor that got lost on the way (the ops
//     worker, before PR #42), not a message. No bell reads it; the admin
//     history keeps it.
//   - Operator alarms (operatorEvents, event.go) are administrators' at ANY
//     address — the v0.43.0 rule is by event (Bell.never).
//   - A notice meant for everybody (everyoneEvents: the admin page's test, an
//     app's instance-wide notice) reaches every bell — unless it names a file,
//     and then only the bells of those who can see that file.
//   - Every other broadcast is for administrators: a drop or share notice with
//     no owner carries the link's bearer token, an escrow notice with no owner
//     is addressed to admins by its emitter.
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

// everyoneEvents are the broadcasts meant for every person. When one names a
// file (an app's notice about a document) it reaches only those who can see
// that file, like any other; when it names nothing it reaches every bell that
// is not confined to a tenant — a confined reader cannot be shown a row that
// cannot be placed in their tenant.
var everyoneEvents = []EventType{
	EventAdminTest,
	EventPluginNotice,
}

// fileBroadcastEvents are the broadcasts that name a file (they carry a
// NodeRef, so meta.node.storage_id places them in a tenant): an antivirus hit,
// an upload that never landed, an app's notice about a document, and the
// notices that fall back to a broadcast when their emitter finds no owner to
// address — a drop, an escrow opening, a share or a comment with no actor on
// record.
//
// ⚠ An allowlist on purpose: a new broadcast kind reaches a tenant admin's
// bell only once somebody decides it can be placed.
var fileBroadcastEvents = []EventType{
	EventFileInfected,
	EventFileUploadFailed,
	EventPluginNotice,
	EventDropReceived,
	EventE2EEscrowUsed,
	EventShareCreated,
	EventCommentAdded,
}

// memberBroadcastEvents are the only broadcasts a member may receive: an
// antivirus hit on a file they work with, an upload into their folder that
// never landed, and the notices meant for everybody (everyoneEvents) — each
// only when the file it names, if it names one, is one the member can see.
//
// ⚠ An allowlist on purpose: a new broadcast kind reaches no member until
// somebody decides it should.
var memberBroadcastEvents = []EventType{
	EventFileInfected,
	EventFileUploadFailed,
	EventAdminTest,
	EventPluginNotice,
}

// Admits reports whether b reads a broadcast of this event at all — the Go
// twin of the SQL filter, for a caller holding one row rather than a query
// (marking a row read by its id). Which of the admitted rows a reader keeps is
// still the per-row pass in the HTTP layer.
func (b Bell) Admits(event string) bool {
	if hasEvent(b.never(), event) {
		return false
	}
	switch b {
	case MemberBell:
		return hasEvent(memberBroadcastEvents, event)
	case TenantAdminBell:
		return hasEvent(fileBroadcastEvents, event)
	default:
		return !hasEvent(personalEvents, event)
	}
}

// ForEveryone reports whether a broadcast of this event is meant for every
// person (everyoneEvents) — the kind a member keeps even when it names no file.
func ForEveryone(event string) bool { return hasEvent(everyoneEvents, event) }

// never is what b leaves out at EVERY address, not only among broadcasts: the
// operator alarms, for anybody who is not an administrator (v0.43.0's rule is
// by event — a quota alarm addressed to a plain user would still be the
// operator's). It rides the mute list's SQL filter, so the badge and the list
// agree.
func (b Bell) never() []EventType {
	if b == MemberBell {
		return operatorEvents
	}
	return nil
}

func hasEvent(events []EventType, event string) bool {
	for _, e := range events {
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

// bellFilter is b's filter for userID's bell, read state included, and the
// zero filter for the admin-global one (userID nil), which is the audit and
// keeps every row.
func bellFilter(userID *int64, b Bell) model.BroadcastFilter {
	if userID == nil {
		return model.BroadcastFilter{}
	}
	f := b.filter()
	f.ReaderID = *userID
	return f
}

func eventIDs(events []EventType) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e)
	}
	return out
}

// The admin history's Scope column for a broadcast: who it reaches, said by
// the same rule the bells apply (model.Notification.Audience).
const (
	// AudienceEveryone — every bell (a notice for everybody that names no file).
	AudienceEveryone = "everyone"
	// AudienceViewers — administrators, and the members who can see the file it
	// names.
	AudienceViewers = "viewers"
	// AudienceAdmins — administrators only.
	AudienceAdmins = "admins"
	// AudienceNobody — no bell at all: routine file activity recorded without
	// the person who did it. It is kept here, in the history, and nowhere else.
	AudienceNobody = "nobody"
)

// BroadcastAudience says who a broadcast of this row's kind reaches. "" for a
// row addressed to somebody — that row's scope is its owner.
func BroadcastAudience(n *model.Notification) string {
	if n == nil || n.UserID != nil {
		return ""
	}
	switch {
	case hasEvent(personalEvents, n.Event):
		return AudienceNobody
	case !MemberBell.Admits(n.Event):
		return AudienceAdmins
	case ForEveryone(n.Event) && !NamesAFile(n.MetaJSON):
		return AudienceEveryone
	default:
		return AudienceViewers
	}
}

// NamesAFile reports whether a stored row names a file — whether it carries a
// node with a name or a path. What a member may be shown of such a row is
// decided by that file, never by the kind alone.
func NamesAFile(meta json.RawMessage) bool {
	if len(meta) == 0 {
		return false
	}
	var m struct {
		Node *struct {
			Path string `json:"path"`
			Name string `json:"name"`
		} `json:"node"`
	}
	if json.Unmarshal(meta, &m) != nil || m.Node == nil {
		return false
	}
	return m.Node.Path != "" || m.Node.Name != ""
}

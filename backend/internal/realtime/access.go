package realtime

import (
	"encoding/json"
	"time"
)

// ── "What you may do may have changed" (#196) ───────────────────────────
//
// An explorer keeps the answers its right-click menu depends on - which of the
// per-folder permissions are held at a path (POST /api/files/manager
// ?action=allowed), may this person start encrypting there (POST
// /api/files/e2e/allowed) - so a menu opens on them at once instead of asking
// the server first (packages/core lib/menuAnswers). This frame is how that
// copy stays honest: when a grant, a role, a group, a permission rule, the
// tenant's encryption policy or an encryption approval changes, the people it
// can concern hear
//
//	{"type":"access.changed"}                  (they were named)
//	{"type":"access.changed","scope":"all"}    (everybody it can concern was told)
//
// and ask their answers again. The frame carries NOTHING else - no path, no
// user, no tenant, no reason - so it cannot tell anybody what changed or
// where; the answers it makes a client fetch are asked with the client's own
// credential and decided by the server, as every action still is.
//
// Who hears it, narrowest first:
//
//   - AccessChanged(ids...): every socket of the named accounts (a grant to
//     them, their request decided, their role changed).
//   - AccessChangedInTenant(id): every socket of one tenant's accounts (a
//     tenant administrator's rule, group or policy). ⚠ Never another tenant's,
//     and not the platform's own accounts either: a frame says, by its timing,
//     that something changed, and that is the tenant's business.
//   - AccessChangedEverywhere(): every open socket (a change at the platform
//     level - the platform's roles and policy, a single-tenant install).
//
// "all" frames tell the client to spread its questions over a moment instead
// of every explorer asking at once. A burst (a group edit writes several rows,
// each one invalidating the permissions) is one frame per socket: the first
// change arms a short timer and everything before it fires is merged into it.

// accessDelayDefault is how long a burst of access changes is gathered before
// the frame goes out.
const accessDelayDefault = 250 * time.Millisecond

// accessPending is what has been announced since the last frame.
type accessPending struct {
	everywhere bool
	tenants    map[int64]struct{}
	users      map[int64]struct{}
	timer      *time.Timer
}

type wireAccessChanged struct {
	Type  string `json:"type"` // always "access.changed"
	Scope string `json:"scope,omitempty"`
}

// AccessChanged tells the named accounts that what they may do may have
// changed. Naming nobody (or only ids <= 0) tells nobody. Never blocks.
func (h *Hub) AccessChanged(userIDs ...int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if h.access.users == nil {
			h.access.users = make(map[int64]struct{}, len(userIDs))
		}
		h.access.users[id] = struct{}{}
	}
	h.armAccessLocked()
}

// AccessChangedInTenant tells every account of one tenant (a provider id > 0).
// Sockets of other tenants and of the platform's own accounts hear nothing.
func (h *Hub) AccessChangedInTenant(providerID int64) {
	if providerID <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.access.tenants == nil {
		h.access.tenants = make(map[int64]struct{}, 1)
	}
	h.access.tenants[providerID] = struct{}{}
	h.armAccessLocked()
}

// AccessChangedEverywhere tells every open socket: a change at the platform
// level, or any change on a single-tenant install.
func (h *Hub) AccessChangedEverywhere() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.access.everywhere = true
	h.armAccessLocked()
}

// armAccessLocked starts the gathering timer when something is pending and
// none is running. Caller holds h.mu.
func (h *Hub) armAccessLocked() {
	p := &h.access
	if p.timer != nil || (!p.everywhere && len(p.tenants) == 0 && len(p.users) == 0) {
		return
	}
	p.timer = time.AfterFunc(h.accessDelay, h.flushAccess)
}

// flushAccess sends the gathered frame to each socket once: the "all" frame
// when everybody, or the socket's whole tenant, was told; else the plain one
// to a socket of a named account (two tabs, the desktop app and an embed of
// one account all hear it).
func (h *Hub) flushAccess() {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.access
	h.access = accessPending{}
	all, err := json.Marshal(wireAccessChanged{Type: "access.changed", Scope: "all"})
	if err != nil {
		return
	}
	one, err := json.Marshal(wireAccessChanged{Type: "access.changed"})
	if err != nil {
		return
	}
	for c := range h.connected {
		_, tenantTold := p.tenants[c.Tenant]
		_, named := p.users[c.UserID]
		switch {
		case p.everywhere || (c.Tenant > 0 && tenantTold):
			trySend(c, all)
		case named:
			trySend(c, one)
		}
	}
}

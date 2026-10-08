package handlers

import (
	"context"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ChangeEmitter is the minimal surface the file-mutation handlers need to
// publish folder-change events to realtime (WebSocket) subscribers. The
// realtime hub (*realtime.Hub) satisfies it. Keeping it an interface means the
// mutation handlers don't hard-depend on a concrete hub — a nil emitter simply
// disables live updates (the default in tests and when realtime is unwired).
type ChangeEmitter interface {
	EmitChange(storageID int64, dir string, ev realtime.ChangeEvent)
}

// changeEmitter is the process-wide, optional emitter. It stays nil until the
// server wires the hub via SetChangeEmitter at startup, so every call site is
// nil-safe and existing behaviour (and every existing test) is unchanged when
// realtime is absent.
//
// A package-level sink (rather than a field injected into each handler struct)
// is used deliberately: the mutating verbs live on *Manager (constructed in
// api.BuildRouter, struct defined in manager.go) and *Drop, and a single
// optional sink avoids threading an emitter field through both structs and
// their constructors while staying fully decoupled and testable.
var changeEmitter ChangeEmitter

// SetChangeEmitter installs the realtime emitter. Call once at startup after
// constructing the hub, e.g. handlers.SetChangeEmitter(hub). Passing nil
// disables emission.
func SetChangeEmitter(e ChangeEmitter) { changeEmitter = e }

// emitFolderChange publishes ev for the folder identified by (storageID, dir)
// when an emitter is wired. dir may be given in any spelling (relative or
// leading-slash, root as ""), the hub normalizes it to the room key. Safe to
// call unconditionally from mutation handlers.
func emitFolderChange(storageID int64, dir string, ev realtime.ChangeEvent) {
	if changeEmitter != nil {
		changeEmitter.EmitChange(storageID, dir, ev)
	}
}

// AccessEmitter tells people that what they may do may have changed
// (realtime.Hub): their explorers ask the answers their right-click menus
// depend on again (#196). Narrowest first: named accounts, one tenant's
// accounts, every open socket (realtime/access.go).
type AccessEmitter interface {
	AccessChanged(userIDs ...int64)
	AccessChangedInTenant(providerID int64)
	AccessChangedEverywhere()
}

// accessEmitter is the process-wide, optional emitter, like changeEmitter:
// nothing until the server wires the hub (SetAccessEmitter), and every call
// site is nil-safe. Atomic because BuildRouter, which wires it, runs once per
// test server and handler tests may build several.
var accessEmitter atomic.Pointer[AccessEmitter]

// SetAccessEmitter installs the emitter for access.changed frames; nil
// disables them.
func SetAccessEmitter(e AccessEmitter) {
	if e == nil {
		accessEmitter.Store(nil)
		return
	}
	accessEmitter.Store(&e)
}

// EmitAccessChanged is emitAccessChanged for the wiring outside this package
// (api.BuildRouter: an encryption approval spent at a create door).
func EmitAccessChanged(userIDs ...int64) { emitAccessChanged(userIDs...) }

// EmitAccessChangedFor is perm's invalidate hook (api.BuildRouter): the named
// accounts when the write named any, else the people of the writer's tenant
// (emitAccessChangedIn).
func EmitAccessChangedFor(ctx context.Context, userIDs ...int64) {
	if len(userIDs) > 0 {
		emitAccessChanged(userIDs...)
		return
	}
	emitAccessChangedIn(ctx)
}

// emitAccessChanged tells the named accounts that their access may have
// changed. Naming nobody tells nobody. The frame carries no path and no
// reason (realtime/access.go). Safe to call unconditionally.
func emitAccessChanged(userIDs ...int64) {
	if e := accessEmitter.Load(); e != nil && len(userIDs) > 0 {
		(*e).AccessChanged(userIDs...)
	}
}

// emitTenantAccessChanged tells every account of one tenant (provider id).
func emitTenantAccessChanged(providerID int64) {
	if e := accessEmitter.Load(); e != nil && providerID > 0 {
		(*e).AccessChangedInTenant(providerID)
	}
}

// emitAccessChangedIn tells the people a change made by ctx's caller can
// concern: the caller's tenant when it is a tenant's (⚠ never another
// tenant's sockets - the frame's timing alone says something changed there),
// everybody when it is the platform's (the supertenant) or the install has no
// tenants (no scope), and nobody for a scope that grants nothing.
func emitAccessChangedIn(ctx context.Context) {
	e := accessEmitter.Load()
	if e == nil {
		return
	}
	sc, ok := tenant.FromContext(ctx)
	switch {
	case !ok || sc == nil || sc.IsSupertenant:
		(*e).AccessChangedEverywhere()
	case sc.ProviderID > 0:
		(*e).AccessChangedInTenant(sc.ProviderID)
	}
}

// emitGroupAccessChanged tells the members of a group (a grant to the group
// changed). When the members cannot be read, the writer's tenant is told
// instead: a frame too many costs a question, a frame too few a stale menu.
func emitGroupAccessChanged(ctx context.Context, store db.Store, groupID int64) {
	if accessEmitter.Load() == nil {
		return
	}
	members, err := store.ListGroupMembers(ctx, groupID)
	if err != nil {
		emitAccessChangedIn(ctx)
		return
	}
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		if m != nil {
			ids = append(ids, m.UserID)
		}
	}
	emitAccessChanged(ids...)
}

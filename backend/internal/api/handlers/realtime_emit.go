package handlers

import "github.com/brf-tech/filex/backend/internal/realtime"

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

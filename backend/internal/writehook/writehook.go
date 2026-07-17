// Package writehook is the single post-write side-effect gate for every
// file-producing/mutating surface (manager, AI/MCP, ShareX, DAV, async
// ops worker).
//
// A surface that writes/deletes/moves a file calls exactly ONE hook
// here; the hook fans out to the two cross-cutting side effects that
// used to be wired ad hoc per call site:
//
//   - async antivirus scan enqueue ("Koru" ClamAV pipeline) — only for
//     persisted file nodes (the scan job re-reads the node by id);
//   - canonical webhook-v2 file event (file.uploaded / file.deleted /
//     file.moved / file.trashed) through the notify service, stamped
//     with the originating surface in meta.origin.
//
// The package is dependency-injected at bootstrap (Configure in
// api.BuildRouter) with the same nil-safe, package-level sink pattern
// as handlers.SetNotifySink / SetAntivirusEnqueue: unconfigured hooks
// are no-ops, so tests and unwired deployments never crash. It imports
// only auth/model/notify — no handlers, db, or storage — so any surface
// package (api/handlers, dav, …) can import it without a cycle.
package writehook

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// Origin values for the `origin` parameter — the frozen set every
// surface must pick from (meta.origin on the emitted event).
const (
	OriginManager = "manager" // browser SPA / manager + chunked upload
	OriginAI      = "ai"      // /api/ai REST + MCP tools (write, zip, unzip)
	OriginShareX  = "sharex"  // ShareX capture upload
	OriginDAV     = "dav"     // WebDAV surface
	OriginOps     = "ops"     // async ops worker (copy/move/delete)
)

// Package-wide sinks. Stay nil until Configure wires them at startup.
var (
	avEnqueue func(ctx context.Context, n *model.Node)
	sink      notify.Service
)

// Configure installs the process-wide dependencies. Call once at boot
// (api.BuildRouter). Either argument may be nil to disable that side
// effect (no ClamAV binary / no notify service).
func Configure(av func(ctx context.Context, n *model.Node), s notify.Service) {
	avEnqueue = av
	sink = s
}

// OnFileWritten is the single post-write gate: enqueue an antivirus scan
// for the freshly written node (persisted nodes only — the scan job
// re-fetches by id, so an id-less transient node is skipped) and emit
// one `file.uploaded` event carrying the origin surface.
//
// node may be a transient (unsaved, ID==0) row when the DB mirror could
// not be upserted — the event still fires because the bytes ARE on
// storage; only the scan is skipped. nil node / directory nodes no-op.
// meta is optional extra event metadata (e.g. {"chunked": true}).
func OnFileWritten(ctx context.Context, storageID int64, node *model.Node, origin string, meta ...map[string]any) {
	if node == nil || node.Type == model.NodeTypeDirectory {
		return
	}
	emit(ctx, notify.Event{
		Event: notify.EventFileUploaded,
		Body:  node.Path,
		Meta:  mergeMeta(origin, meta),
		Node:  &notify.NodeRef{StorageID: storageID, Path: node.Path, Name: node.Name, Size: node.Size},
	})
	if avEnqueue != nil && node.ID != 0 {
		avEnqueue(context.WithoutCancel(ctx), node)
	}
}

// OnFileDeleted emits one `file.deleted` event (permanent removal —
// trash purge or a hard delete on drivers without move support). For a
// soft delete into the trash use OnFileTrashed instead.
func OnFileDeleted(ctx context.Context, storageID int64, path, name, origin string, meta ...map[string]any) {
	emit(ctx, notify.Event{
		Event: notify.EventFileDeleted,
		Body:  path,
		Meta:  mergeMeta(origin, meta),
		Node:  &notify.NodeRef{StorageID: storageID, Path: path, Name: name},
	})
}

// OnFileMoved emits one `file.moved` event. The event's node points at
// the new location; meta carries from/to (+ any extra pairs, e.g.
// {"rename": true}).
func OnFileMoved(ctx context.Context, storageID int64, oldPath, newPath, name, origin string, meta ...map[string]any) {
	m := mergeMeta(origin, meta)
	m["from"] = oldPath
	m["to"] = newPath
	emit(ctx, notify.Event{
		Event: notify.EventFileMoved,
		Body:  newPath,
		Meta:  m,
		Node:  &notify.NodeRef{StorageID: storageID, Path: newPath, Name: name},
	})
}

// OnFileTrashed emits one `file.trashed` event (soft delete — the file
// was renamed into `.filex-trash/` and is restorable). Not part of the
// frozen three-function contract but the manager parity event for every
// soft delete; trashPath is the in-trash location.
func OnFileTrashed(ctx context.Context, storageID int64, path, name, trashPath, origin string, meta ...map[string]any) {
	m := mergeMeta(origin, meta)
	m["trash_path"] = trashPath
	emit(ctx, notify.Event{
		Event: notify.EventFileTrashed,
		Body:  path,
		Meta:  m,
		Node:  &notify.NodeRef{StorageID: storageID, Path: path, Name: name},
	})
}

// mergeMeta folds the variadic extra maps into one meta map and stamps
// the origin. Always returns a non-nil map the caller may extend.
func mergeMeta(origin string, extra []map[string]any) map[string]any {
	m := make(map[string]any, 4)
	for _, e := range extra {
		for k, v := range e {
			m[k] = v
		}
	}
	if origin != "" {
		m["origin"] = origin
	}
	return m
}

// emit mirrors handlers.emitFileEvent: fire-and-forget off the request
// path — actor + per-user scoping resolved from ctx, then the DB insert
// + webhook fan-out run in a goroutine on a context detached from the
// request's cancellation. Errors are logged, never surfaced.
func emit(ctx context.Context, e notify.Event) {
	if sink == nil {
		return
	}
	if e.Severity == "" {
		e.Severity = notify.SeverityInfo
	}
	if e.Actor == nil {
		if u := auth.UserFrom(ctx); u != nil {
			e.Actor = &notify.ActorRef{ID: u.ID, Email: u.Email}
		}
	}
	if e.UserID == nil && e.Actor != nil && e.Actor.ID != 0 {
		// Scope the in-app bell entry to the acting user so routine file
		// activity doesn't broadcast to every account; admins still see
		// every row through the admin-global list.
		uid := e.Actor.ID
		e.UserID = &uid
	}
	c := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("writehook: file event panic", slog.Any("recover", rec))
			}
		}()
		if _, err := sink.Send(c, e); err != nil {
			slog.Warn("writehook: file event send",
				slog.String("event", string(e.Event)),
				slog.String("err", err.Error()))
		}
	}()
}

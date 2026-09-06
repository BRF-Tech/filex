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
//   - canonical webhook-v2 file event (file.uploaded / file.updated /
//     file.deleted / file.moved / file.trashed) through the notify
//     service, stamped with the originating surface in meta.origin.
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
	OriginS3      = "s3"      // S3-compatible endpoint (internal/s3api)
	OriginSFTP    = "sftp"    // SFTP endpoint (internal/sftpsrv)
	OriginFTP     = "ftp"     // FTPS endpoint (internal/ftpsrv)
	OriginNFS     = "nfs"     // NFSv3 endpoint (internal/nfssrv)
	// OriginOnlyOffice is the document server writing an edited office
	// document back through the save callback (internal/onlyoffice). It is a
	// surface of its own and not "manager": the bytes are assembled and
	// posted by the document server, not by the browser that opened the file,
	// and a subscriber deciding whether to re-run a pipeline needs to be able
	// to tell an office save from a drag-and-drop upload.
	OriginOnlyOffice = "onlyoffice"
)

// WriteKind says whether the bytes that just landed created the file or
// replaced a file that was already there. It is what decides between
// notify.EventFileUploaded and notify.EventFileUpdated, and it is a REQUIRED
// argument of OnFileWritten rather than something the gate infers, so that a
// new write surface cannot join the fan-out without answering the question.
//
// ⚠ Every surface must derive it from the fact it ALREADY has — the cache-row
// lookup it does anyway (`existing != nil`), or, where the row is written
// before the bytes move (the staged upload path), a driver Stat of the target
// taken before the write. Do not invent a second notion of "did this exist".
type WriteKind int

const (
	// Created — nothing was addressable at this path before the write.
	// ⚠ Also the honest answer when the surface genuinely cannot tell (the
	// DB mirror was unreachable, so there is no row to compare against): it
	// is the value the event carried before file.updated existed, so an
	// unknown degrades to the old behaviour rather than to a wrong claim
	// that a file was edited.
	Created WriteKind = iota
	// Replaced — a file already existed at this path and its bytes changed.
	Replaced
)

// Event maps the kind onto the canonical event name.
func (k WriteKind) Event() notify.EventType {
	if k == Replaced {
		return notify.EventFileUpdated
	}
	return notify.EventFileUploaded
}

// String makes the kind readable in logs and test failures.
func (k WriteKind) String() string {
	if k == Replaced {
		return "replaced"
	}
	return "created"
}

// Package-wide sinks. Stay nil until Configure wires them at startup.
var (
	avEnqueue func(ctx context.Context, n *model.Node)
	// avEnqueueAfterSave is the DEBOUNCED twin of avEnqueue: it schedules ONE
	// scan per file per editing window and absorbs the saves that arrive
	// inside it. Used by the surfaces that write repeatedly during a single
	// editing session; see OnFileSaved.
	avEnqueueAfterSave func(ctx context.Context, n *model.Node)
	sink               notify.Service
)

// Configure installs the process-wide dependencies. Call once at boot
// (api.BuildRouter). Either argument may be nil to disable that side
// effect (no ClamAV binary / no notify service).
func Configure(av func(ctx context.Context, n *model.Node), s notify.Service) {
	avEnqueue = av
	sink = s
}

// ConfigureSaveScan installs the debounced scan sink OnFileSaved uses. Call
// once at boot, beside Configure. nil is legal and means "no debounced sink":
// OnFileSaved then falls back to the immediate one, because a missing sink
// must degrade to scanning too often, never to not scanning at all.
func ConfigureSaveScan(av func(ctx context.Context, n *model.Node)) { avEnqueueAfterSave = av }

// OnFileWritten is the single post-write gate: emit the canonical write event
// (kind decides file.uploaded vs file.updated, see WriteKind) and enqueue an
// antivirus scan for the freshly written node (persisted nodes only — the scan
// job re-fetches by id, so an id-less transient node is skipped).
//
// node may be a transient (unsaved, ID==0) row when the DB mirror could
// not be upserted — the event still fires because the bytes ARE on
// storage; only the scan is skipped. nil node / directory nodes no-op.
// meta is optional extra event metadata (e.g. {"chunked": true}).
func OnFileWritten(ctx context.Context, storageID int64, node *model.Node, origin string, kind WriteKind, meta ...map[string]any) {
	if node == nil || node.Type == model.NodeTypeDirectory {
		return
	}
	EmitWritten(ctx, storageID, node, origin, kind, meta...)
	if avEnqueue != nil && node.ID != 0 {
		avEnqueue(context.WithoutCancel(ctx), node)
	}
}

// OnFileSaved is OnFileWritten for a write that is one save inside an ONGOING
// editing session: same event, DEBOUNCED scan.
//
// ⚠ The divergence is in the SCAN, never in the event. A person editing a file
// saves it repeatedly, and one ClamAV pass per save would spend a full scan on
// a file nobody has finished writing — so the save schedules one scan per file
// per editing window and the saves that arrive inside the window are absorbed
// into it (queue.AntivirusScanner.EnqueueAfterSave). Nobody gets a different
// EVENT out of this: a webhook subscriber must not be able to tell an editor
// save from an upload of the same bytes.
//
// ⚠⚠ Use it only where more saves really are coming. A surface that writes a
// whole file once wants OnFileWritten: deferring THAT scan buys nothing and
// costs the file up to a full window unscanned. The two callers today are the
// browser's text editor (a Ctrl+S burst) and an OnlyOffice force-save (an
// interim save with the document still open); an OnlyOffice save that arrives
// because the session ENDED is a final state and takes OnFileWritten.
func OnFileSaved(ctx context.Context, storageID int64, node *model.Node, origin string, kind WriteKind, meta ...map[string]any) {
	if node == nil || node.Type == model.NodeTypeDirectory {
		return
	}
	EmitWritten(ctx, storageID, node, origin, kind, meta...)
	if node.ID == 0 {
		return
	}
	if avEnqueueAfterSave == nil {
		if avEnqueue != nil {
			avEnqueue(context.WithoutCancel(ctx), node)
		}
		return
	}
	avEnqueueAfterSave(context.WithoutCancel(ctx), node)
}

// EmitWritten is OnFileWritten without the antivirus enqueue — the same split
// protocolsync draws between Write and WriteRows, for the same reason: a
// surface that has already scheduled the scan itself must not get a second one.
//
// ⚠ Callers that want the debounced scan should reach for OnFileSaved above
// rather than this, which schedules NO scan at all. The one direct caller left
// is the text editor (api/handlers/save_text.go), which owns its own pair of
// enqueue sinks in the handlers package; it gets the same event either way.
func EmitWritten(ctx context.Context, storageID int64, node *model.Node, origin string, kind WriteKind, meta ...map[string]any) {
	if node == nil || node.Type == model.NodeTypeDirectory {
		return
	}
	emit(ctx, notify.Event{
		Event: kind.Event(),
		Body:  node.Path,
		Meta:  mergeMeta(origin, meta),
		Node:  &notify.NodeRef{StorageID: storageID, Path: node.Path, Name: node.Name, Size: node.Size},
	})
}

// OnUploadFailed emits one `file.upload_failed` event: the bytes did NOT
// reach the storage driver and the user has to be told.
//
// ⚠ It is the counterpart of OnFileWritten, and it exists because the staged
// upload path acknowledges the bytes long before it writes them. The client
// gets a 202 the moment the last chunk lands, the transfer happens afterwards
// in the ops worker, and until issue #16 a transfer that failed there produced
// one slog.Warn line and nothing else: the browser had already drawn a green
// tick, the file was listed at its full size, and the only way to find out it
// was never stored was to read the server log. An upload the user was told
// succeeded and that did not is exactly what a notification is for.
//
// userID scopes the bell entry to the person who uploaded, because the
// failure surfaces on a worker goroutine where there is no request user for
// emit to infer one from.
func OnUploadFailed(ctx context.Context, storageID int64, userID int64, p, name, origin, reason string, meta ...map[string]any) {
	m := mergeMeta(origin, meta)
	m["reason"] = reason
	e := notify.Event{
		Event:    notify.EventFileUploadFailed,
		Severity: notify.SeverityError,
		Title:    "Upload failed",
		Body:     p,
		Meta:     m,
		Node:     &notify.NodeRef{StorageID: storageID, Path: p, Name: name},
	}
	if userID != 0 {
		e.UserID = &userID
		e.Actor = &notify.ActorRef{ID: userID}
	}
	emit(ctx, e)
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

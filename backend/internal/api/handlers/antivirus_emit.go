package handlers

/* koru:k2 av */
// Package-level antivirus-scan sink (notify_emit.go pattern): the server
// bootstrap wires a single optional enqueue function once at startup, so
// the upload surfaces (upload finalize, manager vfUpload, public drop)
// stay decoupled from the queue job. A nil sink — no ClamAV binary, no
// queue, tests — disables scanning without touching any call site.

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/model"
)

// avEnqueue is the process-wide, optional scan-enqueue hook. Stays nil
// until the server wires it via SetAntivirusEnqueue in api.BuildRouter.
var avEnqueue func(ctx context.Context, n *model.Node)

// avEnqueueAfterSave is the DEBOUNCED twin, used only by the text editor's
// save path. Same nil-safe contract as avEnqueue.
var avEnqueueAfterSave func(ctx context.Context, n *model.Node)

// SetAntivirusEnqueue installs the enqueue function used after writes.
// Call once at startup; passing nil disables scanning.
func SetAntivirusEnqueue(fn func(ctx context.Context, n *model.Node)) { avEnqueue = fn }

// SetAntivirusEnqueueAfterSave installs the debounced enqueue used when the
// browser's text editor SAVES over a file that already exists.
//
// Why a second sink rather than a flag on the first: the two have different
// contracts, and confusing them is how the gap this closes got created. The
// immediate one scans now, and every upload surface wants it. This one
// schedules one scan per file per editing window and drops the requests that
// arrive inside it — correct for Ctrl+S, wrong for anything that writes a
// whole file once.
func SetAntivirusEnqueueAfterSave(fn func(ctx context.Context, n *model.Node)) {
	avEnqueueAfterSave = fn
}

// enqueueAntivirusScan schedules an async scan for a freshly written
// node. Never blocks or fails the write path: the enqueue itself is
// best-effort inside the job, and the context is detached from the
// request's cancellation so a fast client disconnect can't drop it.
func enqueueAntivirusScan(ctx context.Context, n *model.Node) {
	if avEnqueue == nil || n == nil {
		return
	}
	// A staged node is listed, but its bytes are still in filex's staging area
	// and not on the driver the scanner reads through. Scanning now would find
	// nothing and report a clean file; the writehook fires the scan again once
	// the transfer lands. One guard in the shared sink rather than one per
	// call site.
	if n.TransferState == model.TransferStateStaged {
		return
	}
	avEnqueue(context.WithoutCancel(ctx), n)
}

// enqueueAntivirusScanAfterSave schedules the debounced scan for a file the
// editor has just overwritten. Same best-effort, non-blocking, detached-context
// contract as enqueueAntivirusScan; the only difference is when the scan runs
// and that repeat requests inside the window are absorbed by the queue's
// coalescing key rather than piling up.
//
// ⚠ Falls back to the immediate sink when no debounced one is wired. A missing
// sink must never mean "no scan": that is exactly the hole being closed.
func enqueueAntivirusScanAfterSave(ctx context.Context, n *model.Node) {
	if n == nil {
		return
	}
	if n.TransferState == model.TransferStateStaged {
		return
	}
	if avEnqueueAfterSave == nil {
		enqueueAntivirusScan(ctx, n)
		return
	}
	avEnqueueAfterSave(context.WithoutCancel(ctx), n)
}

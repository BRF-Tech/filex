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

// SetAntivirusEnqueue installs the enqueue function used after writes.
// Call once at startup; passing nil disables scanning.
func SetAntivirusEnqueue(fn func(ctx context.Context, n *model.Node)) { avEnqueue = fn }

// enqueueAntivirusScan schedules an async scan for a freshly written
// node. Never blocks or fails the write path: the enqueue itself is
// best-effort inside the job, and the context is detached from the
// request's cancellation so a fast client disconnect can't drop it.
func enqueueAntivirusScan(ctx context.Context, n *model.Node) {
	if avEnqueue == nil || n == nil {
		return
	}
	avEnqueue(context.WithoutCancel(ctx), n)
}

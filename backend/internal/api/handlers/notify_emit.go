package handlers

/* bag:b3 event */
// Package-level notify sink for canonical file/share events (webhook
// v2). Mirrors the realtime changeEmitter pattern (realtime_emit.go):
// a single optional sink wired once at startup keeps the mutation
// handlers decoupled — a nil sink disables emission (tests, unwired
// deployments) without touching any call site.

import (
	"context"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// notifySink is the process-wide, optional notify service. Stays nil
// until the server wires it via SetNotifySink in api.BuildRouter.
var notifySink notify.Service

// SetNotifySink installs the notify service used for file-event
// emission. Call once at startup; passing nil disables emission.
func SetNotifySink(s notify.Service) { notifySink = s }

// emitFileEvent fires one canonical event without blocking the request
// path: the DB insert + webhook fan-out run in a goroutine on a context
// detached from the request's cancellation (the response returning must
// not abort the delivery). Errors are logged, never surfaced.
func emitFileEvent(ctx context.Context, e notify.Event) {
	if notifySink == nil {
		return
	}
	if e.Severity == "" {
		e.Severity = notify.SeverityInfo
	}
	if e.Actor == nil {
		e.Actor = eventActor(ctx)
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
				slog.Warn("notify: file event panic", slog.Any("recover", rec))
			}
		}()
		if _, err := notifySink.Send(c, e); err != nil {
			slog.Warn("notify: file event send",
				slog.String("event", string(e.Event)),
				slog.String("err", err.Error()))
		}
	}()
}

// eventActor extracts the acting user from the request context,
// best-effort (nil for anonymous/public surfaces).
func eventActor(ctx context.Context) *notify.ActorRef {
	if u := auth.UserFrom(ctx); u != nil {
		return &notify.ActorRef{ID: u.ID, Email: u.Email}
	}
	return nil
}

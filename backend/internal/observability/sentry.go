// Package observability wires optional Sentry-wire error reporting (e.g. the
// self-hosted GlitchTip at errors.example.com) into filex. Everything here is a
// no-op unless a DSN is configured, so a default build reports nothing.
//
// The primary integration is a slog.Handler that forwards WARN+ERROR log
// records to Sentry, so operational failures already surfaced via slog — the
// worker's "ops: step failed", storage errors, recovered panics — show up in
// GlitchTip without sprinkling capture calls through the codebase.
package observability

import (
	"context"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
)

// Init initializes the global Sentry client. Returns false (reporting stays
// off) when dsn is empty or init fails, so callers can skip the slog wrapper.
func Init(dsn, environment, release string) bool {
	if dsn == "" {
		return false
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release,
		AttachStacktrace: true,
		TracesSampleRate: 0, // errors only; no performance tracing volume
	})
	if err != nil {
		slog.Warn("observability: sentry init failed", slog.String("err", err.Error()))
		return false
	}
	return true
}

// Flush drains buffered events — call on shutdown. No-op when uninitialized.
func Flush() { sentry.Flush(2 * time.Second) }

// SlogHandler wraps a base slog.Handler and forwards WARN+ERROR records to
// Sentry (grouped by log message), so operational failures land in GlitchTip.
// WARN is only forwarded when it carries an `err` attribute — that filters out
// benign warnings while keeping real failures like "ops: step failed".
type SlogHandler struct {
	inner slog.Handler
	attrs []slog.Attr
}

// WrapSlog wraps inner so WARN/ERROR records are teed to Sentry.
func WrapSlog(inner slog.Handler) slog.Handler {
	return &SlogHandler{inner: inner}
}

func (h *SlogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.capture(r)
	}
	return h.inner.Handle(ctx, r)
}

func (h *SlogHandler) capture(r slog.Record) {
	data := make(map[string]any, r.NumAttrs()+len(h.attrs))
	hasErr := false
	for _, a := range h.attrs {
		data[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "err" || a.Key == "error" {
			hasErr = true
		}
		data[a.Key] = a.Value.String()
		return true
	})
	// WARN is only worth an issue when it reports an actual failure.
	if r.Level < slog.LevelError && !hasErr {
		return
	}
	level := sentry.LevelWarning
	if r.Level >= slog.LevelError {
		level = sentry.LevelError
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(level)
		if len(data) > 0 {
			scope.SetContext("log", sentry.Context(data))
		}
		sentry.CaptureMessage(r.Message)
	})
}

func (h *SlogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(as))
	merged = append(merged, h.attrs...)
	merged = append(merged, as...)
	return &SlogHandler{inner: h.inner.WithAttrs(as), attrs: merged}
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{inner: h.inner.WithGroup(name), attrs: h.attrs}
}

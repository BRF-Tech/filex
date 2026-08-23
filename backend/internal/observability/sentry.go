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
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
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
//
// Every attribute on the record travels with the event, because an event
// without them is not actionable: "thumb generate failed" is a question
// ("which file? which error?") until `path` and `err` are next to it. Short
// values become tags — searchable, shown in the issue list — and the full set
// (long values included) goes into the `log` context. Values whose key looks
// like a credential are replaced with a marker before either.
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

// filteredValue replaces anything that looks like a credential. Log call
// sites never mean to log a secret, but an `err` wrapping an HTTP request or a
// `config` dump can carry one, and a key named like a credential is the
// cheapest reliable signal.
const filteredValue = "[filtered]"

// maxTagLen is the longest value that still becomes a tag. Sentry caps tag
// values at 200 characters; anything longer (an ffmpeg transcript in `err`)
// belongs in the context only, where nothing is truncated.
const maxTagLen = 120

// sensitiveKey reports whether an attribute key names a credential. Matching
// is by fragment so `client_secret`, `Authorization`, `access_key`, `apiKey`
// and `cookie` are all caught; the one deliberate false positive is a bare
// `key` (an S3 object key is filtered too — the `path` beside it says the
// same thing).
func sensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range []string{"token", "password", "passwd", "secret", "authorization", "cookie", "credential", "private"} {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return k == "key" || strings.HasSuffix(k, "_key") || strings.HasSuffix(k, "-key") || strings.HasSuffix(k, ".key") || strings.HasSuffix(k, "apikey") || strings.HasSuffix(k, "accesskey") || strings.HasSuffix(k, "secretkey")
}

// tagValue returns the tag form of a value: the first line, trimmed to
// maxTagLen, and "" when nothing short enough remains. An `err` that starts
// with a one-line summary keeps that summary as a tag even when the whole
// message is pages long.
func tagValue(v string) string {
	if i := strings.IndexAny(v, "\r\n"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) > maxTagLen {
		return ""
	}
	return v
}

// eventFor builds the Sentry event for a record, or nil when the record is
// not worth an issue (a WARN without an error). Kept separate from capture so
// the mapping is testable without a transport.
func (h *SlogHandler) eventFor(r slog.Record) *sentry.Event {
	data := make(map[string]any, r.NumAttrs()+len(h.attrs))
	tags := make(map[string]string, r.NumAttrs()+len(h.attrs))
	hasErr := false
	add := func(a slog.Attr) {
		if a.Key == "" {
			return
		}
		if a.Key == "err" || a.Key == "error" {
			hasErr = true
		}
		v := a.Value.String()
		if sensitiveKey(a.Key) {
			v = filteredValue
		}
		data[a.Key] = v
		if t := tagValue(v); t != "" {
			tags[a.Key] = t
		}
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(a)
		return true
	})
	// WARN is only worth an issue when it reports an actual failure.
	if r.Level < slog.LevelError && !hasErr {
		return nil
	}
	ev := sentry.NewEvent()
	ev.Message = r.Message
	ev.Level = sentry.LevelWarning
	if r.Level >= slog.LevelError {
		ev.Level = sentry.LevelError
	}
	if src := sourceOf(r); src != "" {
		data["source"] = src
		tags["source"] = src
	}
	if len(data) > 0 {
		ev.Contexts["log"] = sentry.Context(data)
	}
	ev.Tags = tags
	return ev
}

// sourceOf renders the call site of a record as `file.go:line`. The event
// carries no stack trace (a message grouped by its text has no use for one),
// so this is the one pointer back into the code.
func sourceOf(r slog.Record) string {
	if r.PC == 0 {
		return ""
	}
	fs := runtime.CallersFrames([]uintptr{r.PC})
	f, _ := fs.Next()
	if f.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)
}

func (h *SlogHandler) capture(r slog.Record) {
	ev := h.eventFor(r)
	if ev == nil {
		return
	}
	sentry.CaptureEvent(ev)
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

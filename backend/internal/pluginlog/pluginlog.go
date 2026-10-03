// Package pluginlog is the log an administrator reads on a plugin's page - an
// app plugin's (internal/wasmplugin) and a storage plugin's (internal/plugin)
// alike - and the one rule that keeps it, and the server log behind it, from
// drowning in a single repeated line.
//
// ⚠ ONE helper for both kinds of plugin, on purpose (issue #104, the owner's
// call): a storage plugin that cannot answer for a folder is asked about it
// again on every sync pass, every few seconds on a busy storage, and each
// answer is the same line. Written as it came, that line pushed everything
// else out of the page's 500 lines within minutes and filled the server log
// with copies. So:
//
//   - On the page, a line that repeats one of the last 50 is not added again:
//     the earlier line counts it (Count) and moves to the end with the time of
//     the latest repeat (Last). A poller asking for what is newer than its
//     cursor gets the line once more, with the same ID, and replaces it.
//   - In the server log (Mirror), the same line is written at most once every
//     ServerEvery (5 minutes), with how many times it repeated in between.
//   - The page keeps the newest Size lines (500): plenty to read what happened
//     today, and a fixed amount of memory per plugin.
//
// The log is in memory and starts empty with the process, like the app
// plugins' ring it replaces: it is for reading what a plugin is doing now, not
// an archive. What must outlive a restart goes to the audit trail.
package pluginlog

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Size is how many lines a log keeps.
const Size = 500

// ServerEvery is the least time between two copies of the same line in the
// server log.
const ServerEvery = 5 * time.Minute

// maxMsg bounds one line: a plugin's message, or an error a backend returned,
// can be a page long.
const maxMsg = 2000

// maxKeys bounds the lines the server mirror remembers.
const maxKeys = 1000

// Line is one entry of the page's log.
type Line struct {
	// ID stays the same when the line repeats: a poller replaces the line it
	// already has by it.
	ID int64 `json:"id"`
	// Seq is the cursor (?after=): it moves on whenever the line is written or
	// repeats, so a poller sees the new count.
	Seq int64 `json:"seq"`
	// TS is when the line was first written.
	TS time.Time `json:"ts"`
	// Last is when it last repeated; absent for a line written once.
	Last *time.Time `json:"last,omitempty"`
	// Count is how many times in a row it was written; 1 for most lines.
	Count int    `json:"count"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// Log is one plugin's log. The zero value is ready to use and mirrors nothing
// to the server log.
type Log struct {
	mu    sync.Mutex
	lines []Line
	seq   int64

	// The server mirror (Mirror); logger nil = none.
	logger *slog.Logger
	attrs  []any
	every  time.Duration
	seen   map[string]*sent

	// now is time.Now; a test turns the clock.
	now func() time.Time
}

// sent is what the server mirror remembers about one line.
type sent struct {
	at         time.Time // when it was last written to the server log
	suppressed int       // repeats since then that were not
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Mirror makes the log also write its warn and error lines to logger, at most
// once every ServerEvery per line, with attrs (the plugin's name) on each.
// It returns the log, so a construction site stays one expression.
func (l *Log) Mirror(logger *slog.Logger, attrs ...any) *Log {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logger, l.attrs, l.every = logger, attrs, ServerEvery
	return l
}

func (l *Log) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Add writes one line at level ("debug", "info", "warn", "error").
func (l *Log) Add(level, msg string) {
	if l == nil {
		return
	}
	msg = clip(msg, maxMsg)
	l.mu.Lock()
	now := l.clock()
	l.seq++
	if i := l.recentLocked(level, msg); i >= 0 {
		// The same line again: counted on the line already there, which moves
		// to the end (so the slice stays in Seq order).
		ln := l.lines[i]
		ln.Count++
		ln.Seq = l.seq
		t := now
		ln.Last = &t
		l.lines = append(append(l.lines[:i], l.lines[i+1:]...), ln)
	} else {
		l.lines = append(l.lines, Line{ID: l.seq, Seq: l.seq, TS: now, Count: 1, Level: level, Msg: msg})
		if len(l.lines) > Size {
			l.lines = append(l.lines[:0:0], l.lines[len(l.lines)-Size:]...)
		}
	}
	emit, repeated := l.mirrorLocked(level, msg, now)
	logger, attrs := l.logger, l.attrs
	l.mu.Unlock()
	if emit {
		args := append([]any{}, attrs...)
		if repeated > 0 {
			args = append(args, slog.Int("repeated", repeated))
		}
		logger.Log(context.Background(), slogLevel(level), msg, args...)
	}
}

// recentWindow is how far back a repeat is looked for. Not only the line
// before: a sync pass that cannot answer for three folders writes three lines
// in turn, every pass, and "the one before" would never match.
const recentWindow = 50

// recentLocked is the index of the same line among the last recentWindow,
// or -1.
func (l *Log) recentLocked(level, msg string) int {
	for i, stop := len(l.lines)-1, max(len(l.lines)-recentWindow, 0); i >= stop; i-- {
		if l.lines[i].Level == level && l.lines[i].Msg == msg {
			return i
		}
	}
	return -1
}

// mirrorLocked decides whether msg goes to the server log now, and with how
// many repeats it is reporting.
func (l *Log) mirrorLocked(level, msg string, now time.Time) (bool, int) {
	if l.logger == nil || (level != "warn" && level != "error") {
		return false, 0
	}
	if l.seen == nil {
		l.seen = map[string]*sent{}
	}
	key := level + "\x00" + msg
	s, ok := l.seen[key]
	if ok && now.Sub(s.at) < l.every {
		s.suppressed++
		return false, 0
	}
	repeated := 0
	if ok {
		repeated = s.suppressed
	}
	if len(l.seen) >= maxKeys && !ok {
		// Forget what has been quiet for a whole interval: it would be
		// written straight away anyway.
		for k, v := range l.seen {
			if now.Sub(v.at) >= l.every {
				delete(l.seen, k)
			}
		}
	}
	l.seen[key] = &sent{at: now}
	return true, repeated
}

// After returns the lines whose Seq is above after, oldest first, and the
// cursor to ask with next.
func (l *Log) After(after int64) ([]Line, int64) {
	if l == nil {
		return []Line{}, after
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Line{}
	for _, ln := range l.lines {
		if ln.Seq > after {
			out = append(out, ln)
		}
	}
	// A repeat moved its line to a later Seq without moving it in the slice
	// (it is always the last line); the slice is still in Seq order.
	return out, l.seq
}

func slogLevel(level string) slog.Level {
	switch level {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// clip keeps the first n bytes of s on a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "... (" + strconv.Itoa(len(s)-cut) + " more bytes)"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Join writes a line from parts separated by ": ", skipping empty ones -
// "storage s3-arsiv: Proje/2024: plugin: timeout" - so every caller says
// where and what the same way.
func Join(parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ": ")
}

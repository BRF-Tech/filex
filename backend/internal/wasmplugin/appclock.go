package wasmplugin

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The apps' clock (FILEX_APP_CLOCK) ───────────────────────────────────
//
// What time an app believes it is. On every real installation that is the
// real time, and nothing here does anything.
//
// ⚠⚠ Why it exists at all: the screenshots. Every scene runs on one clock
// (e2e/shots/clock.mjs, 2026-09-15 10:30 UTC): the browser starts there and
// every time in an API answer is moved there on its way to the page. That
// cannot reach a time an app WRITES INTO ITS OWN TEXT - "Requested by Dana on
// Oct 7, 2026", "the file is frozen until Oct 14, 2026", "Signed 2026-10-07
// 01:00 UTC" under a signature box. Those are words, not times, by the time
// they leave the app, and the 0.53.0 README showed them beside fields dated
// September 15 (signing/sign-status-1440.png, notifications-list-1440.png,
// sign-define-1440.png). So the scenes start their filex with FILEX_APP_CLOCK
// and the app writes its words on the scene's clock.
//
// Three places, each where an app reads the time:
//
//  1. the guest's own clock - WASI's realtime clock, which is what time.Now()
//     inside the module reads (Compiled.Call → moduleConfig);
//  2. the host's times in a host function's answer - a lock's end, a link's
//     expiry, a certificate's not-after (jsonFn → reply), so an app that
//     prints "frozen until" next to its own "requested on" prints two dates
//     of one clock;
//  3. the wake-up's window, and the due times the app answers with, moved
//     back on the way in (Registry.Tick → tickInput / dueFromApp).
//
// What stays on the real clock: everything the host itself stores and
// compares (a lock is live until its real end, a link expires at its real
// end, a certificate's validity inside its DER), and an app's own state, which
// the host never reads the time out of.
//
// ⚠ Screenshots and tests only, never a server: an app on a moved clock
// stamps its signatures, its receipts and its reminders with a time that is
// not now. The server says so in its log at start (Registry.New).

// EnvAppClock names the variable: an RFC 3339 instant the apps' clock reads
// when the process starts, running at real speed from there. Empty or unset
// is the real clock.
const EnvAppClock = "FILEX_APP_CLOCK"

// AppClock is the apps' view of the time: the real clock moved by a fixed
// offset. The zero value is the real clock.
type AppClock struct {
	offset time.Duration
}

// AppClockAt is the clock that reads at when the real clock reads now.
func AppClockAt(at, now time.Time) AppClock { return AppClock{offset: at.Sub(now)} }

// AppClockFromEnv reads EnvAppClock. Unset or empty is the real clock; a value
// that is not an RFC 3339 instant is an error, and the caller keeps the real
// clock rather than guessing.
func AppClockFromEnv() (AppClock, error) {
	v := strings.TrimSpace(os.Getenv(EnvAppClock))
	if v == "" {
		return AppClock{}, nil
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return AppClock{}, fmt.Errorf("%s=%q is not an RFC 3339 time: %w", EnvAppClock, v, err)
	}
	return AppClockAt(at, time.Now()), nil
}

// On reports whether the clock is moved at all.
func (c AppClock) On() bool { return c.offset != 0 }

// Offset is how far the apps' clock is from the real one.
func (c AppClock) Offset() time.Duration { return c.offset }

// Now is the apps' time.
func (c AppClock) Now() time.Time { return time.Now().Add(c.offset) }

// ToApp moves a real instant onto the apps' clock. The zero time stays zero:
// it means "none" wherever it is sent.
func (c AppClock) ToApp(t time.Time) time.Time {
	if c.offset == 0 || t.IsZero() {
		return t
	}
	return t.Add(c.offset)
}

// FromApp moves an instant an app named back onto the real clock.
func (c AppClock) FromApp(t time.Time) time.Time {
	if c.offset == 0 || t.IsZero() {
		return t
	}
	return t.Add(-c.offset)
}

// walltime is wazero's sys.Walltime on this clock.
func (c AppClock) walltime() (int64, int32) {
	t := c.Now()
	return t.Unix(), int32(t.Nanosecond())
}

// moduleConfig is every call instance's module configuration: the guest's
// realtime clock is this clock, its monotonic clock and its random source the
// system's. ⚠ The monotonic clock is NOT moved: it measures durations (the
// call budget, a guest's own timeouts), and an offset there would measure
// nothing.
func (c AppClock) moduleConfig() wazero.ModuleConfig {
	mc := wazero.NewModuleConfig().WithSysNanotime().WithRandSource(randReader{})
	if c.offset == 0 {
		return mc.WithSysWalltime()
	}
	// 1 µs, what WithSysWalltime itself declares.
	return mc.WithWalltime(c.walltime, sys.ClockResolution(1000))
}

// reply moves the host's own times in a host function's answer onto the
// apps' clock: every time.Time and *time.Time, in maps and slices of the
// shapes the host functions answer with.
//
// ⚠ Only TYPED times move. A string is left as it is, and so is a
// json.RawMessage: that is the app's own data handed back to it (a link's page
// state, a state value), already on the apps' clock because the app wrote it.
// Moving it a second time would put it twice as far away.
func (c AppClock) reply(v any) any {
	if c.offset == 0 {
		return v
	}
	return c.moveTimes(v)
}

func (c AppClock) moveTimes(v any) any {
	switch x := v.(type) {
	case time.Time:
		return c.ToApp(x)
	case *time.Time:
		if x == nil {
			return x
		}
		t := c.ToApp(*x)
		return &t
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = c.moveTimes(e)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, e := range x {
			m, _ := c.moveTimes(e).(map[string]any)
			out[i] = m
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = c.moveTimes(e)
		}
		return out
	default:
		return v
	}
}

// tickInput moves a wake-up's times onto the apps' clock.
func (c AppClock) tickInput(in wire.TickInput) wire.TickInput {
	in.Now = c.ToApp(in.Now)
	in.WindowStart = c.ToApp(in.WindowStart)
	in.WindowEnd = c.ToApp(in.WindowEnd)
	return in
}

// dueFromApp moves the due times a wake-up answered with back onto the real
// clock, where the schedule keeps them.
func (c AppClock) dueFromApp(items []wire.ScheduleItem) {
	for i := range items {
		items[i].DueAt = c.FromApp(items[i].DueAt)
	}
}

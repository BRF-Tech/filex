package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// syncWindow is the `--window HH:MM-HH:MM` of `filex sync run`: the part of
// the day, in local time, when transfer rounds may start. A first sync of
// 52 GiB took nine hours and filled the server's line for everyone else the
// whole time; a window keeps that kind of work to the evening. The end is
// exclusive, and a window whose end is earlier than its start runs over
// midnight ("22:00-07:00").
type syncWindow struct {
	start, end int // minutes after midnight
}

// windowError is a window parseSyncWindow refuses: input as given, and
// whether it was refused for starting where it ends (empty) rather than for
// not being HH:MM-HH:MM. Its text is the English line the CLI always said;
// the event stream and `filex sync window --json` say it in the reader's
// language from the two cases (syncevents.go).
type windowError struct {
	input string
	empty bool
	msg   string
}

func (e *windowError) Error() string { return e.msg }

// parseSyncWindow reads "HH:MM-HH:MM" (an hour may have one digit, and
// spaces around the parts are allowed: " 7:00 - 9:00 "). An empty string is
// no window (any time), returned as nil.
//
// ⚠ The ONLY reader of a window: the desktop app asks `filex sync window
// --json` (judgeSyncWindow) instead of keeping a parser of its own, which
// was stricter than this one and showed "no window" for a window the engine
// ran with (B17).
func parseSyncWindow(s string) (*syncWindow, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return nil, &windowError{input: s, msg: fmt.Sprintf("bad --window %q: want HH:MM-HH:MM", s)}
	}
	start, err := parseClock(a)
	if err != nil {
		return nil, &windowError{input: s, msg: fmt.Sprintf("bad --window %q: %v", s, err)}
	}
	end, err := parseClock(b)
	if err != nil {
		return nil, &windowError{input: s, msg: fmt.Sprintf("bad --window %q: %v", s, err)}
	}
	if start == end {
		return nil, &windowError{input: s, empty: true,
			msg: fmt.Sprintf("bad --window %q: it starts where it ends; leave it out to sync at any time", s)}
	}
	return &syncWindow{start: start, end: end}, nil
}

func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) == 0 || len(h) > 2 || len(m) != 2 || !allDigits(h) || !allDigits(m) {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	return hh*60 + mm, nil
}

// allDigits: strconv.Atoi alone accepts a sign ("+7").
func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (w *syncWindow) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.start/60, w.start%60, w.end/60, w.end%60)
}

func minuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// contains reports whether a round may start at t.
func (w *syncWindow) contains(t time.Time) bool {
	m := minuteOfDay(t)
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end
}

// clockOn returns the time of day minute on t's date, in t's zone.
func clockOn(t time.Time, minute int) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, minute/60, minute%60, 0, 0, t.Location())
}

// closesAfter is when the window that contains t ends.
func (w *syncWindow) closesAfter(t time.Time) time.Time {
	end := clockOn(t, w.end)
	if !end.After(t) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}

// opensAfter is the next start of the window after t.
func (w *syncWindow) opensAfter(t time.Time) time.Time {
	start := clockOn(t, w.start)
	if !start.After(t) {
		start = start.AddDate(0, 0, 1)
	}
	return start
}

// syncWindowCmd is `filex sync window [HH:MM-HH:MM]`: the engine's own judgement
// of a --window - whether it accepts it, its canonical form, and when it opens
// or closes next. With --json it is what the desktop app asks before storing
// a window (desktop/src/sync.ts checkWindow); without, a line for a person.
func syncWindowCmd(langArg *string) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "window [HH:MM-HH:MM]",
		Short: "Check a sync window (--window of `sync run`) and say when it opens and closes",
		Long: "Reads a sync window the way `filex sync run --window` does - local time,\n" +
			"the end exclusive, a window ending earlier than it starts runs over\n" +
			"midnight, an hour may have one digit (7:00-9:00) - and says whether it is\n" +
			"open now and when it opens or closes next. No window (an empty argument)\n" +
			"is any time.\n\n" +
			"With --json the answer is one JSON object: ok, window (the canonical\n" +
			"HH:MM-HH:MM to store), start, end, over_midnight, open, opens_at or\n" +
			"closes_at (RFC 3339), and code/params/message (the message in the --lang\n" +
			"language). A refused window is ok:false with code window.bad or\n" +
			"window.empty, and the command still exits 0.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := ""
			if len(args) == 1 {
				spec = args[0]
			}
			ans := judgeSyncWindow(spec, nowFunc(), srvtext.Pick(langWant(*langArg)))
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				return enc.Encode(ans)
			}
			switch {
			case !ans.OK:
				return ans.err
			case ans.Window == "":
				fmt.Fprintln(out, "No sync window: sync runs at any time.")
			case ans.Open:
				fmt.Fprintf(out, "%s: open now; closes at %s.\n", ans.Window, ans.End)
			default:
				fmt.Fprintf(out, "%s: closed now; opens at %s.\n", ans.Window, ans.Start)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print the answer as JSON")
	return quiet(c)
}

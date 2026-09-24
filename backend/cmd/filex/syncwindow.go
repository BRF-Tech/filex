package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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

// parseSyncWindow reads "HH:MM-HH:MM". An empty string is no window (any
// time), returned as nil.
func parseSyncWindow(s string) (*syncWindow, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return nil, fmt.Errorf("bad --window %q: want HH:MM-HH:MM", s)
	}
	start, err := parseClock(a)
	if err != nil {
		return nil, fmt.Errorf("bad --window %q: %w", s, err)
	}
	end, err := parseClock(b)
	if err != nil {
		return nil, fmt.Errorf("bad --window %q: %w", s, err)
	}
	if start == end {
		return nil, fmt.Errorf("bad --window %q: it starts where it ends; leave it out to sync at any time", s)
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

// Package humandate writes a date the way filex's own screens write it, for
// an app that has no browser to ask.
//
// filex prints every date for a person in ONE format — the explorer's
// (packages/core useLocale `formatWhen`): "22 Eyl 2026" / "Sep 22, 2026", and
// with the time "22 Eyl 2026, 15:38" / "Sep 22, 2026, 3:38 PM". A screen the
// host draws gets it from the browser's Intl. An app writes its own sentences
// ("the link is valid until …", a mail, a notice), and before this package it
// wrote them with the ISO day — "2026-09-29" a column away from "22 Eyl 2026"
// (v0.43.0 wave 2). This is that format, in Go, for every app to share: an
// app must not grow a copy of its own.
//
// ⚠ A server has no reader's clock. A DAY is written as the UTC calendar day
// it names; a time is UTC, and a sentence that shows one should say "UTC"
// beside it. Where a value must stay exact and machine-readable (an audit
// trail, a form field's value), keep ISO 8601 — this is for people.
//
// Languages: en, tr, de, es, fr, matched on the base tag ("de-AT" → de);
// anything else is English, which is also what a Text falls back to. The
// strings are CLDR's abbreviated month names and patterns, as a browser's
// Intl.DateTimeFormat({month: 'short', day: 'numeric', year: 'numeric'})
// and ({hour: 'numeric', minute: '2-digit'}) print them.
package humandate

import (
	"fmt"
	"strings"
	"time"
)

type style struct {
	months [12]string
	// day writes the date from the day, the month's name and the year.
	day func(d int, m string, y int) string
	// clock writes the time of day.
	clock func(h, min int) string
}

func h24(h, m int) string { return fmt.Sprintf("%d:%02d", h, m) }

func h12(h, m int) string {
	suffix := "AM"
	if h >= 12 {
		suffix = "PM"
	}
	h %= 12
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d:%02d %s", h, m, suffix)
}

func dMy(d int, m string, y int) string { return fmt.Sprintf("%d %s %d", d, m, y) }

var styles = map[string]style{
	"en": {
		months: [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
		day:    func(d int, m string, y int) string { return fmt.Sprintf("%s %d, %d", m, d, y) },
		clock:  h12,
	},
	"tr": {
		months: [12]string{"Oca", "Şub", "Mar", "Nis", "May", "Haz", "Tem", "Ağu", "Eyl", "Eki", "Kas", "Ara"},
		day:    dMy,
		clock:  h24,
	},
	"de": {
		months: [12]string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."},
		day:    func(d int, m string, y int) string { return fmt.Sprintf("%d. %s %d", d, m, y) },
		clock:  h24,
	},
	"es": {
		months: [12]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"},
		day:    dMy,
		clock:  h24,
	},
	"fr": {
		months: [12]string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
		day:    dMy,
		clock:  h24,
	},
}

// Languages are the tags this package writes in its own words; any other
// is written in English.
func Languages() []string { return []string{"en", "tr", "de", "es", "fr"} }

func styleOf(lang string) style {
	s := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if st, ok := styles[s]; ok {
		return st
	}
	return styles["en"]
}

// Day is t's UTC calendar day in lang: "22 Eyl 2026", "Sep 22, 2026",
// "22. Sept. 2026". "" for the zero time.
func Day(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return calendar(lang, t.UTC())
}

// calendar writes t's date as t itself states it (its own zone).
func calendar(lang string, t time.Time) string {
	st := styleOf(lang)
	y, m, d := t.Date()
	return st.day(d, st.months[m-1], y)
}

// DayTime is Day with the UTC time of day: "22 Eyl 2026, 15:38",
// "Sep 22, 2026, 3:38 PM". Say "UTC" beside it where the hour matters.
func DayTime(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	u := t.UTC()
	return Day(lang, u) + ", " + styleOf(lang).clock(u.Hour(), u.Minute())
}

// Stamp reads a stored value — RFC 3339 or a bare YYYY-MM-DD day — and
// writes its day in lang: the day the stamp itself names, in the offset it
// was written with (what its first ten characters say), so a stamp for
// "the 29th, 01:00 +03:00" stays the 29th. A value that is neither comes
// back as it was ("—", an empty string), so a caller can pass what it has.
func Stamp(lang, stamp string) string {
	s := strings.TrimSpace(stamp)
	if t, err := time.Parse(time.RFC3339, s); err == nil && !t.IsZero() {
		return calendar(lang, t)
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return calendar(lang, t)
	}
	return stamp
}

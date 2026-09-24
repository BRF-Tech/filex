package humandate

import (
	"testing"
	"time"
)

// The strings are what a browser's Intl prints for the explorer's format
// (packages/core useLocale formatWhen) — measured with Node's ICU on
// 2026-09-22: a date an app writes must read like the date beside it.
func TestTheExplorersFormatInEveryLanguage(t *testing.T) {
	at := time.Date(2026, 9, 22, 15, 38, 0, 0, time.UTC)
	cases := []struct{ lang, day, dayTime string }{
		{"en", "Sep 22, 2026", "Sep 22, 2026, 3:38 PM"},
		{"tr", "22 Eyl 2026", "22 Eyl 2026, 15:38"},
		{"de", "22. Sept. 2026", "22. Sept. 2026, 15:38"},
		{"es", "22 sept 2026", "22 sept 2026, 15:38"},
		{"fr", "22 sept. 2026", "22 sept. 2026, 15:38"},
	}
	for _, c := range cases {
		if got := Day(c.lang, at); got != c.day {
			t.Errorf("Day(%s) = %q, want %q", c.lang, got, c.day)
		}
		if got := DayTime(c.lang, at); got != c.dayTime {
			t.Errorf("DayTime(%s) = %q, want %q", c.lang, got, c.dayTime)
		}
	}
}

func TestEveryMonthAndTheHourEdges(t *testing.T) {
	want := map[string][12]string{
		"tr": {"5 Oca 2026", "5 Şub 2026", "5 Mar 2026", "5 Nis 2026", "5 May 2026", "5 Haz 2026", "5 Tem 2026", "5 Ağu 2026", "5 Eyl 2026", "5 Eki 2026", "5 Kas 2026", "5 Ara 2026"},
		"de": {"5. Jan. 2026", "5. Feb. 2026", "5. März 2026", "5. Apr. 2026", "5. Mai 2026", "5. Juni 2026", "5. Juli 2026", "5. Aug. 2026", "5. Sept. 2026", "5. Okt. 2026", "5. Nov. 2026", "5. Dez. 2026"},
		"fr": {"5 janv. 2026", "5 févr. 2026", "5 mars 2026", "5 avr. 2026", "5 mai 2026", "5 juin 2026", "5 juil. 2026", "5 août 2026", "5 sept. 2026", "5 oct. 2026", "5 nov. 2026", "5 déc. 2026"},
	}
	for lang, months := range want {
		for m := 1; m <= 12; m++ {
			if got := Day(lang, time.Date(2026, time.Month(m), 5, 0, 0, 0, 0, time.UTC)); got != months[m-1] {
				t.Errorf("%s month %d: %q, want %q", lang, m, got, months[m-1])
			}
		}
	}
	midnight := time.Date(2026, 9, 22, 0, 5, 0, 0, time.UTC)
	if got := DayTime("en", midnight); got != "Sep 22, 2026, 12:05 AM" {
		t.Errorf("en midnight: %q", got)
	}
	if got := DayTime("tr", midnight); got != "22 Eyl 2026, 0:05" {
		t.Errorf("tr midnight (no leading zero, as the explorer): %q", got)
	}
	noon := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if got := DayTime("en", noon); got != "Sep 22, 2026, 12:00 PM" {
		t.Errorf("en noon: %q", got)
	}
}

func TestLanguageTagsAndFallback(t *testing.T) {
	at := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for tag, want := range map[string]string{
		"de-AT": "22. Sept. 2026", "TR": "22 Eyl 2026", "fr_CA": "22 sept. 2026",
		"ja": "Sep 22, 2026", "": "Sep 22, 2026",
	} {
		if got := Day(tag, at); got != want {
			t.Errorf("Day(%q) = %q, want %q", tag, got, want)
		}
	}
	if Day("tr", time.Time{}) != "" || DayTime("tr", time.Time{}) != "" {
		t.Error("the zero time is no date")
	}
}

// A stored stamp is written as the day it names — in the offset it was
// written with — and anything else is handed back untouched.
func TestStamp(t *testing.T) {
	for in, want := range map[string]string{
		"2026-09-29":                "29 Eyl 2026",
		"2026-09-29T11:03:30Z":      "29 Eyl 2026",
		"2026-09-29T01:00:00+03:00": "29 Eyl 2026",
		"—":                         "—",
		"":                          "",
		"not a date":                "not a date",
	} {
		if got := Stamp("tr", in); got != want {
			t.Errorf("Stamp(%q) = %q, want %q", in, got, want)
		}
	}
}

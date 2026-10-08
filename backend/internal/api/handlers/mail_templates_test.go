package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestShareMailText_PinExpiryLocale(t *testing.T) {
	// English file + PIN + 7-day expiry + size + site name.
	subj, body := shareMailText("en", "BRF", "report.pdf", false, 1500000, "https://f/s/tok", "1234", false, 7)
	if subj != "report.pdf has been shared with you" {
		t.Fatalf("en subject: %q", subj)
	}
	for _, want := range []string{"https://f/s/tok", "PIN: 1234", "valid for 7 day",
		"via BRF", "File: report.pdf", "Size: 1.5 MB"} {
		if !strings.Contains(body, want) {
			t.Errorf("en body missing %q in:\n%s", want, body)
		}
	}

	// Turkish folder, no PIN, no expiry → folder wording, no size, no-expiry line.
	subj, body = shareMailText("tr", "BRF Teknoloji", "Belgeler", true, 0, "https://f/s/tok", "", false, 0)
	if subj != "Belgeler klasörü sizinle paylaşıldı" {
		t.Fatalf("tr folder subject: %q", subj)
	}
	for _, want := range []string{"BRF Teknoloji üzerinden bir klasör", "Klasör: Belgeler", "süresi yoktur"} {
		if !strings.Contains(body, want) {
			t.Errorf("tr folder body missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "PIN") || strings.Contains(body, "Boyut") {
		t.Errorf("tr folder body should have no PIN/size line:\n%s", body)
	}

	// Turkish file subject uses "dosyası".
	if s, _ := shareMailText("tr", "BRF", "x.doc", false, 10, "l", "", false, 1); s != "x.doc dosyası sizinle paylaşıldı" {
		t.Errorf("tr file subject: %q", s)
	}
	// ⚠ An empty or unknown language is ENGLISH. It used to be Turkish — the
	// `else` of an en/tr pair — so a Spanish account's share mail arrived in
	// Turkish (measured 2026-09-22 before the server catalogue).
	for _, loc := range []string{"", "es", "de-DE", "xx"} {
		if _, b := shareMailText(loc, "BRF", "x", false, 0, "l", "", false, 1); !strings.Contains(b, "valid for 1 day.") {
			t.Errorf("locale %q should read English:\n%s", loc, b)
		}
	}
	// "en-US" style still English.
	if s, _ := accountCreatedText("en-US", "u", "e", "p"); s != "Your filex account was created" {
		t.Errorf("en-US should be English, got %q", s)
	}
	// The singular: English spells the one out; Turkish has no singular form
	// and keeps its own plain one (never the English singular).
	if _, b := shareMailText("tr", "", "x", false, 0, "l", "", false, 1); !strings.Contains(b, "Bu bağlantı 1 gün geçerlidir.") {
		t.Errorf("tr one day:\n%s", b)
	}
}

// A link mailed by share-mail that has a PIN says so, and that the sender
// gives the PIN separately; the PIN itself is never written (share_mail.go).
func TestShareMailText_AWithheldPINIsNamedNotWritten(t *testing.T) {
	_, body := shareMailText("en", "", "a.txt", false, 0, "https://f/s/tok", "", true, 1)
	if !strings.Contains(body, "This link is protected with a PIN.") {
		t.Errorf("a PIN-guarded link says so:\n%s", body)
	}
	if strings.Contains(body, "PIN: ") {
		t.Errorf("no PIN value is written:\n%s", body)
	}
	_, body = dropInviteMailText("tr", "", "Gelen", "https://f/d/tok", "", true, 1, 0, 0, nil)
	if !strings.Contains(body, "Bu bağlantı bir PIN ile korunuyor.") {
		t.Errorf("tr drop invite names the PIN:\n%s", body)
	}
	// The flow that holds the PIN (Invite, a link made a moment ago) writes it.
	if _, b := shareMailText("en", "", "a.txt", false, 0, "l", "4821", false, 1); !strings.Contains(b, "PIN: 4821") {
		t.Errorf("the invite's PIN is written:\n%s", b)
	}
}

// linkDaysLeft: 0 is "does not expire", so a link that does expire is never
// rounded down to it.
func TestLinkDaysLeft(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, c := range []struct {
		exp  *time.Time
		want int
	}{
		{nil, 0},
		{at(7*24*time.Hour - time.Second), 7},
		{at(30 * 24 * time.Hour), 30},
		{at(5 * time.Hour), 1},
		{at(36 * time.Hour), 2},
	} {
		if got := linkDaysLeft(c.exp, now); got != c.want {
			t.Errorf("linkDaysLeft(%v) = %d, want %d", c.exp, got, c.want)
		}
	}
}

// allowN counts n at once: all of them fit, or none is counted.
func TestIPLimiter_AllowN(t *testing.T) {
	l := newIPLimiter(10, time.Hour)
	if !l.allowN("u:1", 6) {
		t.Fatal("6 of 10 fit")
	}
	if l.allowN("u:1", 5) {
		t.Fatal("6+5 is past 10")
	}
	if !l.allowN("u:1", 4) {
		t.Fatal("the refused 5 were not counted: 6+4 fits")
	}
	if l.allowN("u:1", 1) || l.allow("u:1") {
		t.Fatal("the window is full")
	}
	if !l.allowN("u:2", 10) {
		t.Fatal("another key has its own window")
	}
	if l.allowN("u:3", 11) {
		t.Fatal("more than the limit never fits")
	}
}

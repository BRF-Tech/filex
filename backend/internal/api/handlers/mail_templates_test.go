package handlers

import (
	"strings"
	"testing"
)

func TestShareMailText_PinExpiryLocale(t *testing.T) {
	// English file + PIN + 7-day expiry + size + site name.
	subj, body := shareMailText("en", "BRF", "report.pdf", false, 1500000, "https://f/s/tok", "1234", 7)
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
	subj, body = shareMailText("tr", "BRF Teknoloji", "Belgeler", true, 0, "https://f/s/tok", "", 0)
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
	if s, _ := shareMailText("tr", "BRF", "x.doc", false, 10, "l", "", 1); s != "x.doc dosyası sizinle paylaşıldı" {
		t.Errorf("tr file subject: %q", s)
	}
	// ⚠ An empty or unknown language is ENGLISH. It used to be Turkish — the
	// `else` of an en/tr pair — so a Spanish account's share mail arrived in
	// Turkish (measured 2026-09-22 before the server catalogue).
	for _, loc := range []string{"", "es", "de-DE", "xx"} {
		if _, b := shareMailText(loc, "BRF", "x", false, 0, "l", "", 1); !strings.Contains(b, "valid for 1 day.") {
			t.Errorf("locale %q should read English:\n%s", loc, b)
		}
	}
	// "en-US" style still English.
	if s, _ := accountCreatedText("en-US", "u", "e", "p"); s != "Your filex account was created" {
		t.Errorf("en-US should be English, got %q", s)
	}
	// The singular: English spells the one out; Turkish has no singular form
	// and keeps its own plain one (never the English singular).
	if _, b := shareMailText("tr", "", "x", false, 0, "l", "", 1); !strings.Contains(b, "Bu bağlantı 1 gün geçerlidir.") {
		t.Errorf("tr one day:\n%s", b)
	}
}

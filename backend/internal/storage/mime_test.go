package storage

import "testing"

func TestRefineOfficeMime(t *testing.T) {
	cases := []struct {
		detected string
		filename string
		want     string
	}{
		// Office ZIPs get upgraded.
		{"application/zip", "deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"application/zip", "letter.DOCX", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"application/zip", "budget.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"application/zip", "notes.odt", "application/vnd.oasis.opendocument.text"},
		{"application/zip", "calc.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		{"application/zip", "deck.odp", "application/vnd.oasis.opendocument.presentation"},

		// Real ZIP stays a ZIP.
		{"application/zip", "archive.zip", "application/zip"},
		{"application/zip", "no-extension", "application/zip"},

		// Non-ZIP MIME passes through unchanged.
		{"image/png", "deck.pptx", "image/png"},
		{"", "deck.pptx", ""},
		{"application/octet-stream", "deck.pptx", "application/octet-stream"},
	}
	for _, c := range cases {
		got := RefineOfficeMime(c.detected, c.filename)
		if got != c.want {
			t.Errorf("RefineOfficeMime(%q, %q) = %q, want %q", c.detected, c.filename, got, c.want)
		}
	}
}

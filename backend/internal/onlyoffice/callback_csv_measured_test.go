package onlyoffice

// What a real ONLYOFFICE Document Server saved, byte for byte: Docs 9.4.0-129
// (onlyoffice/documentserver, JWT on, its defaults), 2026-10-05. The CSV is
// opened as filex opens one (UTF-8 and its own delimiter in
// document.options), the editor in the language filex passes, cells are
// typed through the cell name box and the editor is closed; the bytes are the
// file of the status 2 callback, taken by a harness outside filex.
//
// ⚠ They are the ground truth csvSame's rules answer to (csv_keep.go). A rule
// they contradict is a wrong rule, and a false "same" undoes a person's edit.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The fixture of the release gate (e2e/tests/199-csv-onlyoffice.spec.ts,
// GitHub #88): semicolons, CRLF, no byte order mark; `3` in B2 typed over as
// `42`, nothing else touched.
var docs94Fixture = "ad;adet;not;kod;tel;tarih\r\n" +
	"elma;3;\"a; b\";007;05320000001;01.02.2026\r\n" +
	"armut;5;şeker;042;05330000002;15.03.2026\r\n"

var docs94FixtureEdited = strings.Replace(docs94Fixture, "elma;3;", "elma;42;", 1)

// What ONLYOFFICE saved for it, by the editor's language, and what the file
// gets. In every one the leading zeros are gone; the dates are written the
// language's way (German writes these two as they were).
var docs94FixtureSaved = map[string]struct{ saved, want string }{
	"en": {
		saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,1/2/2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		want: docs94FixtureEdited,
	},
	"tr": {
		saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,1.02.2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		want: docs94FixtureEdited,
	},
	"de": {
		saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,01.02.2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		want: docs94FixtureEdited,
	},
	// French writes day/month/year, both in two digits: a form the English
	// editor keeps as typed (15/03/2026), so it is no rule, and the two
	// dates are French's.
	"fr": {
		saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,01/02/2026\n" +
			"armut,5,şeker,42,5330000002,15/03/2026\n",
		want: strings.NewReplacer("01.02.2026", "01/02/2026", "15.03.2026", "15/03/2026").Replace(docs94FixtureEdited),
	},
}

// Through the callback: 0.51.0 wrote these with `7`, `5320000001` and the
// dates as ONLYOFFICE wrote them.
func TestCSVCallback_TheFixtureOfTheReleaseGateKeepsItsCells(t *testing.T) {
	for lang, c := range docs94FixtureSaved {
		t.Run(lang, func(t *testing.T) {
			h := newCSVHarness(t, docs94Fixture)
			resp := h.save(t, c.saved, "csv", "7")
			assert.Equal(t, 0, resp["error"])
			assert.Equal(t, c.want, h.disk(t))
		})
	}
}

// Package officesavetest holds what a real ONLYOFFICE Document Server saved,
// byte for byte, for the tests of the packages that keep a saved CSV in its
// file's way (officesave, onlyoffice). Docs 9.4.0-129
// (onlyoffice/documentserver, JWT on, its defaults), 2026-10-05: the CSV is
// opened as filex opens one (UTF-8 and its own delimiter in
// document.options), the editor in the language filex passes, cells are typed
// through the cell name box and the editor is closed; the bytes are the file
// of the status 2 callback, taken by a harness outside filex.
//
// ⚠ They are the ground truth csvSame's rules answer to (officesave
// csv_keep.go). A rule they contradict is a wrong rule, and a false "same"
// undoes a person's edit.
package officesavetest

import "strings"

// Docs94Fixture is the fixture of the release gate
// (e2e/tests/199-csv-onlyoffice.spec.ts, GitHub #88): semicolons, CRLF, no
// byte order mark.
const Docs94Fixture = "ad;adet;not;kod;tel;tarih\r\n" +
	"elma;3;\"a; b\";007;05320000001;01.02.2026\r\n" +
	"armut;5;şeker;042;05330000002;15.03.2026\r\n"

// Docs94FixtureEdited is the fixture with `3` in B2 typed over as `42`,
// nothing else touched.
var Docs94FixtureEdited = strings.Replace(Docs94Fixture, "elma;3;", "elma;42;", 1)

// Saved is one measurement: what ONLYOFFICE saved, and what the file gets.
type Saved struct {
	Saved, Want string
}

// Docs94FixtureSaved is what ONLYOFFICE saved for the fixture, by the editor's
// language, and what the file gets. In every one the leading zeros are gone;
// the dates are written the language's way (German writes these two as they
// were).
var Docs94FixtureSaved = map[string]Saved{
	"en": {
		Saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,1/2/2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		Want: Docs94FixtureEdited,
	},
	"tr": {
		Saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,1.02.2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		Want: Docs94FixtureEdited,
	},
	"de": {
		Saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,01.02.2026\n" +
			"armut,5,şeker,42,5330000002,15.03.2026\n",
		Want: Docs94FixtureEdited,
	},
	// French writes day/month/year, both in two digits: a form the English
	// editor keeps as typed (15/03/2026), so it is no rule, and the two
	// dates are French's.
	"fr": {
		Saved: "\xEF\xBB\xBFad,adet,not,kod,tel,tarih\n" +
			"elma,42,a; b,7,5320000001,01/02/2026\n" +
			"armut,5,şeker,42,5330000002,15/03/2026\n",
		Want: strings.NewReplacer("01.02.2026", "01/02/2026", "15.03.2026", "15/03/2026").Replace(Docs94FixtureEdited),
	},
}

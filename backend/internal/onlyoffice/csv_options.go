package onlyoffice

// Opening a CSV in ONLYOFFICE (filex 0.51, docs/ONLYOFFICE.md → CSV files).
//
// ONLYOFFICE asks for the encoding and the delimiter in a dialog ("Choose CSV
// options") before it shows anything, unless the editor's config carries them
// in `document.options` (`codePage` and `delimiter`, or `delimiterChar`; sdkjs
// spreadsheet_api._onNeedParams). filex reads the start of the file
// (officesave.SniffCSV) and passes what it finds, so a CSV opens straight
// away. A file that is not UTF-8 gets no options: the dialog asks, because a
// code page judged wrong would show every letter outside ASCII as another
// one. How the save is written back is internal/officesave's (callback_csv.go).

import "github.com/brf-tech/filex/backend/internal/officesave"

// CSVSniffBytes is how much of a CSV is read to learn how it is written.
const CSVSniffBytes = officesave.CSVSniffBytes

// csvDelimiterCodes are ONLYOFFICE's numbers for the delimiters it names
// (sdkjs c_oAscCsvDelimiter). Any other one goes as `delimiterChar`.
var csvDelimiterCodes = map[byte]int{'\t': 1, ';': 2, ':': 3, ',': 4, ' ': 5}

// csvCodePageUTF8 is UTF-8's code page, as ONLYOFFICE takes it.
const csvCodePageUTF8 = 65001

// OpenOptions is the editor config's `document.options` that opens the CSV
// without ONLYOFFICE's "Choose CSV options" dialog: UTF-8 and its delimiter.
// Nil for a file that is not UTF-8: the dialog asks which encoding it is.
func OpenOptions(d officesave.CSVDialect) map[string]any {
	if !d.UTF8 {
		return nil
	}
	opts := map[string]any{"codePage": csvCodePageUTF8}
	if code, ok := csvDelimiterCodes[d.Comma]; ok {
		opts["delimiter"] = code
	} else {
		opts["delimiterChar"] = string(rune(d.Comma))
	}
	return opts
}

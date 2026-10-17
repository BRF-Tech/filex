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
	"testing"

	"github.com/brf-tech/filex/backend/internal/officesave/officesavetest"

	"github.com/stretchr/testify/assert"
)

// The vectors (and how they were taken) are officesavetest's, shared with
// officesave's own KeepCSV tests.
var (
	docs94Fixture       = officesavetest.Docs94Fixture
	docs94FixtureEdited = officesavetest.Docs94FixtureEdited
	docs94FixtureSaved  = officesavetest.Docs94FixtureSaved
)

// Through the callback: 0.51.0 wrote these with `7`, `5320000001` and the
// dates as ONLYOFFICE wrote them.
func TestCSVCallback_TheFixtureOfTheReleaseGateKeepsItsCells(t *testing.T) {
	for lang, c := range docs94FixtureSaved {
		t.Run(lang, func(t *testing.T) {
			h := newCSVHarness(t, docs94Fixture)
			resp := h.save(t, c.Saved, "csv", "7")
			assert.Equal(t, 0, resp["error"])
			assert.Equal(t, c.Want, h.disk(t))
		})
	}
}

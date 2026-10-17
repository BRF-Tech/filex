package onlyoffice

// The editor config's `document.options` for a CSV (csv_options.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/officesave"
)

func TestOpenOptions(t *testing.T) {
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 4}, OpenOptions(officesave.CSVDialect{Comma: ',', UTF8: true}))
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 2}, OpenOptions(officesave.CSVDialect{Comma: ';', UTF8: true}))
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 1}, OpenOptions(officesave.CSVDialect{Comma: '\t', UTF8: true}))
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiterChar": "|"}, OpenOptions(officesave.CSVDialect{Comma: '|', UTF8: true}))
	assert.Nil(t, OpenOptions(officesave.CSVDialect{Comma: ';', UTF8: false}), "not UTF-8: ONLYOFFICE asks")
	assert.Nil(t, OpenOptions(officesave.SniffCSV([]byte("ad;not\nelma;\xFEeker\n"))),
		"a code page filex recognises is still asked: a page judged wrong shows other letters")
}

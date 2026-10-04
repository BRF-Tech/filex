package onlyoffice

// A .csv opens in ONLYOFFICE without its "Choose CSV options" dialog: the
// editor config carries the file's encoding and delimiter (filex 0.51,
// csv.go). Measured on Docs 9.4: with `document.options` {codePage 65001,
// delimiter} the spreadsheet is ready in about 4 s with no dialog; without
// it the dialog stands over an empty grid and nothing loads until somebody
// answers it.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func csvConfigDoc(t *testing.T, name string, opts ...ConfigOption) map[string]any {
	t.Helper()
	svc := New(nil, nil, "https://docs.example", "shh", "https://filex.example", time.Hour)
	cfg, err := svc.BuildConfigForNode(context.Background(),
		&model.Node{ID: 1, Name: name, PathHash: "h", Size: 10}, nil, "en", "edit", opts...)
	require.NoError(t, err)
	return cfg.Config["document"].(map[string]any)
}

func TestConfig_ACSVOpensWithItsDelimiterAndEncoding(t *testing.T) {
	doc := csvConfigDoc(t, "liste.csv", WithHead([]byte("ad;adet\nelma;3\n")))
	assert.Equal(t, "csv", doc["fileType"])
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 2}, doc["options"])

	doc = csvConfigDoc(t, "bos.csv", WithHead([]byte{}))
	assert.Equal(t, map[string]any{"codePage": 65001, "delimiter": 4}, doc["options"], "an empty CSV opens as comma-separated UTF-8")
}

func TestConfig_NoOptionsWhereTheyWouldGuess(t *testing.T) {
	assert.NotContains(t, csvConfigDoc(t, "eski.csv", WithHead([]byte("ad;not\nelma;\xFEeker\n"))), "options",
		"not UTF-8: ONLYOFFICE's dialog asks which encoding it is")
	assert.NotContains(t, csvConfigDoc(t, "liste.csv"), "options", "nothing was read: nothing is said")
	assert.NotContains(t, csvConfigDoc(t, "rapor.xlsx", WithHead([]byte("PK\x03\x04"))), "options",
		"only a CSV takes them")
}

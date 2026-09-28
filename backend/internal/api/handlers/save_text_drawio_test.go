package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The draw.io viewer saves a diagram through save-text: what draw.io hands
// back is the diagram's XML. save-text allowed by extension and `.drawio` /
// `.dio` were not on the list, and a diagram New document makes is catalogued
// as application/vnd.jgraph.mxfile, which the catalogue check does not call
// text either — so every draw.io Save answered 415 "extension not allowed",
// a new diagram and a draft of one (issue #71) included.
func TestSaveText_ADrawioDiagramIsSaved(t *testing.T) {
	f := newStagedFixture(t)

	for _, name := range []string{"plan.drawio"} {
		require.Equal(t, http.StatusOK, f.mutate(t, "newfile", map[string]any{
			"path": "main://", "name": name, "type": "drawio", "exact_name": true,
		}), name)
		xml := `<mxfile><diagram name="Page-1">saved</diagram></mxfile>`
		require.Equal(t, http.StatusOK, f.saveText(t, "main://"+name, xml), name)
		got, err := os.ReadFile(filepath.Join(f.rootDir, name))
		require.NoError(t, err)
		assert.Equal(t, xml, string(got), name)
	}
}

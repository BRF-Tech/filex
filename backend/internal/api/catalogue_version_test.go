package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/version"
)

// The catalogue a translator downloads from a running filex names that
// binary's version — "0.42.2" on a v0.43.0 binary sent them hunting for a
// newer catalogue that was the one they had.
func TestCatalogueContext_NamesTheBinarysVersion(t *testing.T) {
	was := version.Version
	t.Cleanup(func() { version.Version = was })
	built := []byte(`{"filex":"0.42.2","about":"x","keys":{"a.b":{"in":"admin"}}}`)
	read := func(name string) ([]byte, error) {
		if name != catalogueContextPath {
			return nil, errors.New("no")
		}
		return built, nil
	}
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		catalogueContext(read)(rec, httptest.NewRequest(http.MethodGet, "/admin/"+catalogueContextPath, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
		return doc
	}

	version.Version = "v0.43.0"
	doc := get()
	assert.Equal(t, "0.43.0", doc["filex"])
	assert.Equal(t, map[string]any{"a.b": map[string]any{"in": "admin"}}, doc["keys"], "the rest travels unchanged")

	version.Version = "0.1.0-dev"
	assert.Equal(t, "0.42.2", get()["filex"], "a development binary states no version of its own")

	rec := httptest.NewRecorder()
	catalogueContext(func(string) ([]byte, error) { return nil, errors.New("none") })(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "a binary built without the web build has no catalogue")
}

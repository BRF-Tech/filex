package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakemagick"
)

// About draws HEIC as a row of its own, from the flat `heic` beside
// `imagemagick`, and both are in `thumbs`: an ImageMagick that cannot decode
// HEVC (Ubuntu 24.04 without libheif-plugin-libde265) is "ImageMagick: found,
// HEIC photos: not found", not "found" for both (0.50 test phase).
func TestCapabilities_HEICIsARowOfItsOwn(t *testing.T) {
	_, _, store := testutil.NewTestServer(t)
	t.Cleanup(enginebin.SetForTest(map[string]string{enginebin.ImageMagick: fakemagick.NoHEVC(t)}))

	rec := httptest.NewRecorder()
	handlers.NewCapabilities(capability.New(store), store, false).Get(rec, httptest.NewRequest(http.MethodGet, "/api/files/capabilities", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		ImageMagick bool            `json:"imagemagick"`
		HEIC        *bool           `json:"heic"`
		Thumbs      map[string]bool `json:"thumbs"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	assert.True(t, out.ImageMagick)
	require.NotNil(t, out.HEIC, "no flat `heic` for the About page")
	assert.False(t, *out.HEIC)
	assert.Equal(t, map[string]bool{"imagemagick": true, "heic": false}, map[string]bool{
		"imagemagick": out.Thumbs["imagemagick"], "heic": out.Thumbs["heic"],
	})
	_, published := out.Thumbs["heic"]
	assert.True(t, published, "thumbs.heic is published")
}

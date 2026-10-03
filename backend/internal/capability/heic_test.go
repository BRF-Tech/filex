package capability

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/fakemagick"
)

// `thumbs.heic` is its own answer, measured: ImageMagick on PATH is not
// ImageMagick that decodes a HEIC. 0.50 said HEIC could be drawn because
// ImageMagick was there, on an Ubuntu 24.04 whose libheif1 had no HEVC
// decoder plugin, and every HEIC failed (0.50 test phase, thumbnails scene).
func TestService_HEICIsTheDecodeProbeNotTheBinary(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()

	restore := enginebin.SetForTest(map[string]string{enginebin.ImageMagick: fakemagick.NoHEVC(t)})
	caps, err := New(store).Get(ctx)
	restore()
	require.NoError(t, err)
	assert.True(t, caps.Thumbs.ImageMagick, "ImageMagick is installed")
	assert.False(t, caps.Thumbs.HEIC, "and cannot decode the HEIC sample: HEIC is not available")

	restore = enginebin.SetForTest(map[string]string{enginebin.ImageMagick: fakemagick.Decoding(t)})
	caps, err = New(store).Get(ctx)
	restore()
	require.NoError(t, err)
	assert.True(t, caps.Thumbs.HEIC)

	raw, err := json.Marshal(caps)
	require.NoError(t, err)
	var wire struct {
		Thumbs map[string]any `json:"thumbs"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, true, wire.Thumbs["heic"], "published as thumbs.heic")

	restore = enginebin.SetForTest(map[string]string{})
	caps, err = New(store).Get(ctx)
	restore()
	require.NoError(t, err)
	assert.False(t, caps.Thumbs.HEIC, "no ImageMagick, no HEIC")
}

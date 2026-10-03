package thumbkit_test

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/thumbkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

func TestListCard_IsAnAnswerFilexTakes(t *testing.T) {
	out, err := thumbkit.PNG(thumbkit.ListCard("JAR · 3 files", []string{"META-INF/MANIFEST.MF", "com/example/App.class", "ünicode.txt"}, 40))
	require.NoError(t, err)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out.Image))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.LessOrEqual(t, cfg.Width, wire.ThumbnailMaxPixels)
	assert.LessOrEqual(t, len(out.Image), wire.ThumbnailMaxOutputBytes)
}

func TestPNG_RefusesWhatFilexWouldRefuse(t *testing.T) {
	_, err := thumbkit.PNG(image.NewRGBA(image.Rect(0, 0, wire.ThumbnailMaxPixels+1, 4)))
	assert.Error(t, err, "wider than the host takes")
	_, err = thumbkit.PNG(nil)
	assert.Error(t, err)
	out, err := thumbkit.PNG(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	require.NoError(t, err)
	_, err = png.Decode(bytes.NewReader(out.Image))
	assert.NoError(t, err)
}

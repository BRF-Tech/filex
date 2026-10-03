package thumb_test

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// A HEIC photo is drawn through ImageMagick, tiles and all. Runs where
// ImageMagick reads HEIC (the full image's imagemagick-heic, a workstation
// with libheif); skipped elsewhere. Measured in alpine:3.24 with
// imagemagick-heic, the full image's base.
//
// testdata/grid.heic is laid out the way a phone writes its photos: a grid of
// 512x512 tiles (2x2 here, 1024x768), each quarter a colour of its own (red,
// green / blue, yellow). A decoder that read only the first tile would give a
// red picture; this one has to put the four together. Made with
//
//	magick -size 1024x768 xc:#d02020 -fill #20a020 -draw "rectangle 512,0 1023,383" \
//	  -fill #2020d0 -draw "rectangle 0,384 511,767" -fill #e0c020 -draw "rectangle 512,384 1023,767" src.png
//	heif-enc --cut-tiles 512 -q 40 -o grid.heic src.png
func TestHEIF_DrawnThroughImageMagick(t *testing.T) {
	if enginebin.Probe().Path(enginebin.ImageMagick) == "" {
		t.Skip("ImageMagick is not installed here")
	}
	// ⚠ Decoding, not listing: `-list format` names HEIC on Ubuntu 24.04
	// without libheif's HEVC decoder, where this test then failed instead of
	// skipping (0.50 test phase). The server's own probe decides.
	if heic := enginebin.HEIC(); !heic.Decodes {
		t.Skip("this ImageMagick does not decode HEIC: " + heic.Detail)
	}
	ctx := context.Background()
	body, err := os.ReadFile(filepath.Join("testdata", "grid.heic"))
	require.NoError(t, err)

	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, HEIF: true})
	n := writeFileAs(t, store, st, root, "IMG_0001.HEIC", sniffed("IMG_0001.HEIC", body), body)
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
	// 1024x768 into the 320 box: 320x240, a quarter each 160x120.
	quarters := []struct {
		x, y int
		name string
		want func(c color.RGBA) bool
	}{
		{80, 60, "red", func(c color.RGBA) bool { return c.R > 160 && c.G < 90 && c.B < 90 }},
		{240, 60, "green", func(c color.RGBA) bool { return c.G > 120 && c.R < 90 && c.B < 90 }},
		{80, 180, "blue", func(c color.RGBA) bool { return c.B > 160 && c.R < 90 && c.G < 90 }},
		{240, 180, "yellow", func(c color.RGBA) bool { return c.R > 160 && c.G > 140 && c.B < 90 }},
	}
	for _, q := range quarters {
		c := cachedAt(t, pipe, n.ID, q.x, q.y)
		require.True(t, q.want(c), "the %s quarter at (%d,%d) is %v: the tiles were not put together", q.name, q.x, q.y, c)
	}
}

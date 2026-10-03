package thumb_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/testutil/fakemagick"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// avifHead is the start of an AVIF: an ISO box of brand avif.
var avifHead = append([]byte{0, 0, 0, 0x1c, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0, 'm', 'i', 'f', '1', 'a', 'v', 'i', 'f', 'm', 'i', 'a', 'f'}, make([]byte, 64)...)

// useMagick makes bin the ImageMagick this process resolved, for this test.
func useMagick(t *testing.T, bin string) {
	t.Helper()
	t.Cleanup(enginebin.SetForTest(map[string]string{enginebin.ImageMagick: bin}))
}

// ImageMagick is installed and cannot decode HEVC (Ubuntu 24.04 without
// libheif-plugin-libde265): a HEIC is skipped as "no_tool:heic_codec", the
// program it needs named, and ImageMagick is not even asked to draw it. An
// AVIF on the same ImageMagick is drawn: its decoder is another plugin.
//
// Before the probe the HEIC was handed to ImageMagick and recorded `failed`
// with "Unsupported codec" (found in the 0.50 test phase, every HEIC of the
// thumbnails scene).
func TestHEICCodec_UndecodableHEICIsSkippedNotFailed(t *testing.T) {
	bin := fakemagick.NoHEVC(t)
	useMagick(t, bin)
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, HEIF: true})

	n := writeFileAs(t, store, st, root, "IMG_0001.heic", sniffed("IMG_0001.heic", heicHead), heicHead)
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "skipped", row.State, row.Error)
	require.Equal(t, "no_tool:heic_codec", row.Error)
	_, statErr := os.Stat(pipe.CachePath(n.ID))
	require.True(t, os.IsNotExist(statErr), "no picture and no placeholder card")
	require.Equal(t, 1, fakemagick.Runs(t, bin), "ImageMagick ran for the probe only, not for the photo")

	a := writeFileAs(t, store, st, root, "kapak.avif", sniffed("kapak.avif", avifHead), avifHead)
	require.NoError(t, pipe.GenerateThumb(ctx, a))
	row, err = store.GetThumbnail(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
	require.True(t, near(cachedAt(t, pipe, a.ID, 32, 32), fakemagick.SampleColour), "the AVIF is ImageMagick's picture")
}

// The skip names what is missing, so the row is drawn as soon as an
// ImageMagick that decodes is there: the next listing asks for it again
// (Assess), and the render is real. A HEIC recorded `failed` (0.50 before the
// probe) would have been left alone until a repair.
func TestHEICCodec_DrawnOnceTheDecoderArrives(t *testing.T) {
	useMagick(t, fakemagick.NoHEVC(t))
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, HEIF: true})
	n := writeFileAs(t, store, st, root, "IMG_0002.heic", sniffed("IMG_0002.heic", heicHead), heicHead)
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)

	later := time.Now().Add(time.Hour)
	require.Equal(t, thumb.Leave, pipe.Assess(n, row, later), "still no decoder: asking again would only skip again")

	// libheif-plugin-libde265 installed (here: another ImageMagick that
	// decodes), and filex restarted or the binary upgraded.
	useMagick(t, fakemagick.Decoding(t))
	require.Equal(t, thumb.Render, pipe.Assess(n, row, later), "the decoder is here now")
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
	require.True(t, near(cachedAt(t, pipe, n.ID, 32, 32), fakemagick.SampleColour))
}

// Without ImageMagick at all the reason stays "no_tool:heif" (install
// ImageMagick), not the codec: the decoder is the second thing missing.
func TestHEICCodec_NoImageMagickIsStillHEIF(t *testing.T) {
	t.Cleanup(enginebin.SetForTest(map[string]string{}))
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	n := writeFileAs(t, store, st, root, "IMG_0003.heic", sniffed("IMG_0003.heic", heicHead), heicHead)
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "no_tool:heif", row.Error)
}

package thumb_test

// The thumbnail pipeline over a PLUGIN-backed storage.
//
// Why this file exists: the pipeline is the one consumer that reads a whole
// object and then decides, from the bytes alone, whether the file is usable.
// It is attached to a driver per storage (AttachStorage) and reads through it
// directly, so a plugin whose read stream is truncated, buffered wrong or
// closed early produces a "failed" thumbnail — and a failed thumbnail is not
// an error anybody sees, it is a grid of grey placeholders that looks like a
// design choice.
//
// Only the IMAGE path is exercised. It is pure Go (image/png in, image/jpeg
// out) and therefore hermetic; video, audio, PDF and office all shell out to
// ffmpeg / ghostscript / libreoffice, so they are left to a machine that has
// them — see the skip at the bottom for exactly what is not measured here.

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// pngBytes builds a real PNG rather than pasting a base64 blob, so the fixture
// says what it is and a decode failure cannot be blamed on the fixture.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// pluginPipeline wires a pipeline to a storage row backed by a live plugin and
// returns everything the assertions need.
func pluginPipeline(t *testing.T) (db.Store, *thumb.Pipeline, *model.Storage, *testplugin.Plugin) {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	p := testplugin.Start(t)

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "eklenti", Driver: p.Register(t), MountPath: "/eklenti", Enabled: true,
		ConfigJSON: []byte(`{"root":"/data"}`),
	})
	require.NoError(t, err)

	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	pipe.AttachStorage(st.ID, p.Driver(t))
	return store, pipe, st, p
}

func fileNode(t *testing.T, store db.Store, st *model.Storage, path, name string, size int64) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: name, Path: path,
		PathHash: pathkey.Hash(st.ID, path), Type: model.NodeTypeFile, Size: size,
	})
	require.NoError(t, err)
	return n
}

// The whole point: bytes that exist only on the plugin come back through the
// driver, decode, and land as a JPEG on disk with a "ready" row behind it.
func TestThumbnailFromAPluginStorage(t *testing.T) {
	store, pipe, st, p := pluginPipeline(t)
	src := pngBytes(t, 640, 480)
	p.SeedBytes("fotograf.png", src)
	n := fileNode(t, store, st, "/fotograf.png", "fotograf.png", int64(len(src)))

	require.NoError(t, pipe.GenerateThumb(context.Background(), n))

	row, err := store.GetThumbnail(context.Background(), n.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "ready", row.State, "thumbnail error: %s", row.Error)

	out, err := os.ReadFile(pipe.CachePath(n.ID))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(out, []byte{0xFF, 0xD8}), "cached thumbnail is not a JPEG")

	// Decoded, not just written: a truncated read produces a file that starts
	// with the right two bytes and is not an image.
	img, format, err := image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.LessOrEqual(t, img.Bounds().Dx(), 320, "thumbnail was not scaled down")
}

// A file the plugin does not have must be recorded as failed, with the reason
// kept. The alternative — a pipeline that swallows the error — leaves a row
// stuck in "pending" forever, which no retry ever picks up and no UI ever
// explains.
func TestThumbnailFailureFromAPluginIsRecorded(t *testing.T) {
	store, pipe, st, _ := pluginPipeline(t)
	n := fileNode(t, store, st, "/yok.png", "yok.png", 1234)

	require.Error(t, pipe.GenerateThumb(context.Background(), n))

	row, err := store.GetThumbnail(context.Background(), n.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "failed", row.State)
	require.NotEmpty(t, row.Error, "a failed thumbnail with no reason cannot be diagnosed")
}

// ⚠ NOT MEASURED, and deliberately so: the video, audio, PDF and office
// branches of the pipeline shell out to ffmpeg, ghostscript/poppler and
// libreoffice. Those binaries are not in this repo's CI image, and a test that
// quietly passed because the capability flag was off would be worse than no
// test — it would claim coverage of the path where a plugin's read stream is
// piped into another process, which is exactly the path most likely to break.
func TestThumbnailNonImagePathsOverAPluginAreUnmeasured(t *testing.T) {
	t.Skip("video/audio/PDF/office thumbnails need ffmpeg, ghostscript and libreoffice; not in CI. " +
		"The image path above covers the plugin read stream; the external-binary branches are unproven over a plugin.")
}

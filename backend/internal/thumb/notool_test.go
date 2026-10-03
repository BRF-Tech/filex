package thumb_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// ole2 is the head of every Word 97, Excel 97 and PowerPoint 97 file: the
// compound-file signature, which the content sniff does not know.
var ole2 = append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 504)...)

// heicHead is the start of a phone's HEIC photo: an ISO box of brand heic,
// which the sniff does not know either.
var heicHead = append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0, 'm', 'i', 'f', '1', 'h', 'e', 'i', 'c'}, make([]byte, 64)...)

// movHead is a QuickTime movie's first box (brand "qt  ").
var movHead = append([]byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p', 'q', 't', ' ', ' ', 0, 0, 0, 0, 'q', 't', ' ', ' '}, make([]byte, 64)...)

func sniffed(name string, body []byte) string {
	return storage.RefineOfficeMime(http.DetectContentType(body), name)
}

// A kind whose program the install lacks is skipped with the kind named, not
// failed and not drawn as the extension's placeholder card (0.49), and the
// type it is routed by is the extension's where the sniff could not name the
// file: a Word 97 document, a HEIC photo and a QuickTime movie all sniff as
// application/octet-stream.
func TestNoTool_SkippedWithTheKindNamed(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"rapor.doc", ole2, "no_tool:office"},
		{"tablo.xls", ole2, "no_tool:office"},
		{"IMG_0001.heic", heicHead, "no_tool:heif"},
		{"tatil.mov", movHead, "no_tool:video"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
			mime := sniffed(c.name, c.body)
			require.Equal(t, "application/octet-stream", mime, "the sniff learned this format; the test no longer models the upload")
			n := writeFileAs(t, store, st, root, c.name, mime, c.body)

			require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
			row, err := store.GetThumbnail(ctx, n.ID)
			require.NoError(t, err)
			require.Equal(t, "skipped", row.State)
			require.Equal(t, c.want, row.Error)
			_, statErr := os.Stat(pipe.CachePath(n.ID))
			require.True(t, os.IsNotExist(statErr), "no placeholder card is written for it")
		})
	}
}

// An office document needs OnlyOffice and nothing on this machine: with a
// PDF renderer and no OnlyOffice it is skipped as no_tool:office (before
// 0.50 LibreOffice drew it here, and the soffice path is gone).
func TestNoTool_OfficeNeedsOnlyOfficeNotAProgramHere(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true, PDF: true})
	n := writeFileAs(t, store, st, root, "rapor.doc", sniffed("rapor.doc", ole2), ole2)
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "no_tool:office", row.Error)
	_, statErr := os.Stat(pipe.CachePath(n.ID))
	require.True(t, os.IsNotExist(statErr), "no placeholder card")
}

// Once the program is installed (the probe runs at boot), the next listing
// asks for the file again, and the render is real.
func TestNoTool_DrawnOnceTheProgramArrives(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed here")
	}
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	_, self, _, _ := runtime.Caller(0)
	clip, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "..", "..", "e2e", "fixtures", "file-types", "sample.mp4"))
	require.NoError(t, err)
	// An MP4 named .mov: the sniff says video/mp4, the name decides.
	n := writeFileAs(t, store, st, root, "tatil.mov", sniffed("tatil.mov", clip), clip)
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "no_tool:video", row.Error)

	later := time.Now().Add(time.Hour)
	require.Equal(t, thumb.Leave, pipe.Assess(n, row, later), "still no FFmpeg: asking again would only skip again")

	withFFmpeg := thumb.New(store, pipe.CacheDir(), thumb.Capabilities{Image: true, SVG: true, Video: true, Audio: true})
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	withFFmpeg.AttachStorage(st.ID, drv)
	require.Equal(t, thumb.Render, withFFmpeg.Assess(n, row, later), "FFmpeg is here now")
	require.NoError(t, withFFmpeg.GenerateThumb(ctx, n))
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
	_, statErr := os.Stat(withFFmpeg.CachePath(n.ID))
	require.NoError(t, statErr)
}

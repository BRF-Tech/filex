package thumb_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// An SVG gets a thumbnail with no rsvg-convert anywhere (GitHub #79): the
// built-in engine draws it, and a transparent background shows the
// checkerboard rather than the black slab a JPEG encoder makes of it.
func TestSVG_DrawnByTheBuiltInEngine(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="10" fill="#1040d0"/></svg>`
	n := writeFile(t, store, st, root, "logo.svg", []byte(svg))

	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State, row.Error)
	centre := cachedPixel(t, pipe, n.ID)
	require.Greater(t, int(centre.B), 150, "the circle: %v", centre)

	// The corner is the SVG's transparent background: the checkerboard.
	requireChecker(t, pipe, n.ID, 2, 2)
}

// A file the engine cannot draw is recorded as failed with the engine's own
// reason, not skipped: there was an engine, the file is the problem.
func TestSVG_ABrokenFileFails(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
	n := writeFile(t, store, st, root, "bozuk.svg", []byte("<svg <<< this is not xml"))
	require.Error(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", row.State)
	require.Contains(t, row.Error, "svg")
}

// An SVG as it really reaches the catalogue: an upload and the local driver
// record the content sniff's type, and the sniff has no signature for SVG
// (text/xml with an XML declaration, text/plain without one). Both are drawn
// by the engine, not as the extension's placeholder card: 0.49 routed by that
// type and gave every uploaded or synced SVG the card.
func TestSVG_ASniffedSVGIsDrawnByTheEngine(t *testing.T) {
	ctx := context.Background()
	shape := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="30" fill="#1040d0"/></svg>`
	for name, body := range map[string]string{
		"bildirimli.svg": `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + shape,
		"yalın.svg":      shape,
	} {
		t.Run(name, func(t *testing.T) {
			store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true, SVG: true})
			sniffed := storage.RefineOfficeMime(http.DetectContentType([]byte(body)), name)
			require.NotContains(t, sniffed, "svg", "the sniff learned SVG; this test no longer models the upload")
			n := writeFileAs(t, store, st, root, name, sniffed, []byte(body))

			require.NoError(t, pipe.GenerateThumb(ctx, n))
			row, err := store.GetThumbnail(ctx, n.ID)
			require.NoError(t, err)
			require.Equal(t, "ready", row.State, row.Error)
			centre := cachedPixel(t, pipe, n.ID)
			require.Greater(t, int(centre.B), 150, "the circle, not the placeholder card: %v", centre)
			require.Less(t, int(centre.R), 90, "the circle, not the placeholder card: %v", centre)
		})
	}
}

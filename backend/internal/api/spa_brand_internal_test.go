package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
)

func pngDataURI(t *testing.T, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// A logo with its own opaque background keeps it to the edge of the maskable
// icon, rather than standing as a tile on white.
func TestBrandLogo_AnOpaqueLogoBackgroundContinuesToTheEdge(t *testing.T) {
	navy := color.NRGBA{R: 10, G: 20, B: 90, A: 255}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = []uint8{navy.R, navy.G, navy.B, navy.A}[i%4]
	}
	l := decodeBrandLogo(pngDataURI(t, img))
	require.NotNil(t, l)
	assert.Equal(t, navy, l.bg)

	data, ct := l.file(l.hash + "-maskable-192.png")
	require.NotNil(t, data)
	assert.Equal(t, "image/png", ct)
	out, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, navy, color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA))
}

// A picture that claims a canvas bigger than a logo is refused before it is
// decoded: a small file can still claim tens of thousands of pixels a side.
func TestBrandLogo_AnOversizedCanvasIsNotDecoded(t *testing.T) {
	assert.Nil(t, decodeBrandLogo(pngDataURI(t, image.NewNRGBA(image.Rect(0, 0, brandLogoMaxSide+1, 1)))))
	assert.NotNil(t, decodeBrandLogo(pngDataURI(t, image.NewNRGBA(image.Rect(0, 0, 8, 8)))))
}

// The shapes a logo arrives in: a percent-encoded SVG (what most tools write
// for an inline SVG), base64 with line breaks, and things that are not a
// picture at all.
func TestBrandLogo_TheShapesALogoArrivesIn(t *testing.T) {
	svg := decodeBrandLogo(`data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 1 1%22/%3E`)
	require.NotNil(t, svg)
	assert.Contains(t, string(svg.svg), "<svg")

	uri := pngDataURI(t, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
	payload := uri[len("data:image/png;base64,"):]
	wrapped := "data:image/png;base64," + payload[:10] + "\n" + payload[10:]
	assert.NotNil(t, decodeBrandLogo(wrapped))

	assert.Nil(t, decodeBrandLogo("data:image/png;base64,bm90IGEgcGljdHVyZQ=="), "not a picture")
	assert.Nil(t, decodeBrandLogo("data:image/svg+xml,hello"), "not an SVG")
	assert.Nil(t, (*brandIconCache)(nil).logo("data:image/png;base64,xx"))
	assert.Nil(t, newBrandIconCache().logo("/branding/logo.png"), "a site-relative logo names this server and is not fetched")
}

// Nothing branded: every document is the one that came in, byte for byte.
func TestShellBrander_NothingBrandedChangesNothing(t *testing.T) {
	r := httptest.NewRequest("GET", "/admin/", nil)
	for _, s := range []*shellBrander{
		nil,
		{resolve: func(*http.Request) handlers.BrandingConfig { return handlers.BrandingConfig{FooterText: "x"} }, icons: newBrandIconCache()},
	} {
		doc := []byte(`<title>filex</title>{url:"index.html",revision:"ab"}`)
		assert.Equal(t, doc, s.index(doc, r))
		assert.Equal(t, doc, s.serviceWorker(doc, r))
		m := []byte(`{"name":"filex - File Manager","id":"/admin/"}`)
		assert.Equal(t, m, s.manifest(m, r))
	}
}

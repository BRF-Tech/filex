package thumb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A thumbnail never loads a picture from another server (0.57): a file's
// preview holds such a picture back until the person asks for it, and the
// thumbnail of the same file must not fetch it behind their back. A Markdown
// file's thumbnail is its text (text.go), so the address of a picture in it is
// a line of text; an SVG's <image> that names a URL draws nothing (svg.go); an
// HTML file and a Mermaid diagram are the placeholder card of their kind.
// (An office document or an EPUB is drawn by the office engine:
// officeengine's TestEngine_NoRemoteFetch.) Measured here against a server
// that counts every request it is sent: none may arrive.
func TestThumb_NoRemotePictureIsFetched(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	t.Cleanup(srv.Close)
	pic := srv.URL + "/pixel.png?file=report"

	f := newArchiveFixture(t, nil)
	ctx := context.Background()

	md := f.put("report.md", []byte(strings.Join([]string{
		"# Report",
		"",
		"![chart](" + pic + ")",
		`<img src="` + pic + `" srcset="` + pic + ` 2x" alt="beacon">`,
		`<picture><source srcset="` + pic + `"><img src="` + pic + `"></picture>`,
		`<p style="background: url(` + pic + `)">styled</p>`,
		"",
		"Closing words.",
	}, "\n")))
	md.Mime = "text/markdown; charset=utf-8"
	require.NoError(t, f.p.GenerateThumb(ctx, md))
	require.Equal(t, "ready", f.row(md.ID).State, "a Markdown file's thumbnail is its text")

	svg := f.put("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40">`+
		`<rect width="40" height="40" fill="#0a0"/>`+
		`<image href="`+pic+`" width="40" height="40"/><image xlink:href="`+pic+`" width="40" height="40"/>`+
		`<foreignObject width="40" height="40"><img xmlns="http://www.w3.org/1999/xhtml" src="`+pic+`"/></foreignObject>`+
		`<style>rect { fill: url(`+pic+`) }</style></svg>`))
	svg.Mime = "image/svg+xml"
	require.NoError(t, f.p.GenerateThumb(ctx, svg))
	require.Equal(t, "ready", f.row(svg.ID).State)

	page := f.put("page.html", []byte(`<!doctype html><html><body><img src="`+pic+`"><link rel="stylesheet" href="`+pic+`"></body></html>`))
	page.Mime = "text/html; charset=utf-8"
	// Its card is the placeholder of its kind: nothing in it is read as markup.
	_ = f.p.GenerateThumb(ctx, page)

	// A Mermaid diagram is never drawn for a thumbnail: its card is its kind's.
	diagram := f.put("flow.mmd", []byte("flowchart LR\n  A@{ img: \""+pic+"\", w: 60, h: 60 }\n  B[\"<img src='"+pic+"'>\"] --> A\n"))
	diagram.Mime = "text/plain; charset=utf-8"
	_ = f.p.GenerateThumb(ctx, diagram)

	require.Zero(t, hits.Load(), "a thumbnail asked the picture's server for it")
}

package officeengine

import (
	"archive/zip"
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The office engine draws a document's thumbnail and converts it on the
// server, and a document can name a picture elsewhere: a .docx's linked
// picture (a relationship with TargetMode="External"), an HTML or EPUB
// chapter's <img>, a style sheet. 0.57's rule is that a preview loads no
// picture from another server by itself, and a thumbnail never does: the
// engine must not fetch them either.
//
// It cannot, by construction: x2t and the layout are WebAssembly modules
// run by wazero with WASI preview 1 alone and the job's directory mounted -
// no socket, no host function of filex's - and this package, which is all
// the engine process runs (Serve), imports no network package.
// TestEngine_ImportsNoNetwork holds the second; TestEngine_NoRemoteFetch
// measures it with the real modules against a server that counts every
// request.

// TestEngine_ImportsNoNetwork: no file of this package (the engine process's
// whole code) imports a network package, and no module is given a host
// module of its own or wazero's experimental sockets.
func TestEngine_ImportsNoNetwork(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		require.NoError(t, err, f)
		seen++
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "net" || strings.HasPrefix(path, "net/") || path == "crypto/tls" ||
				strings.Contains(path, "wazero/experimental/sock") {
				t.Errorf("%s imports %s: the office engine reaches nothing but its job's files", f, path)
			}
		}
		// The one host module is "env": emscripten's longjmp and memory
		// notes, and a stop for a function the build lacks (env.go).
		for _, m := range hostModuleRe.FindAllStringSubmatch(string(src), -1) {
			require.Equal(t, `"env"`, m[1], "%s gives a module a host module other than env", f)
		}
	}
	require.Greater(t, seen, 5, "the package's files were read")
}

var hostModuleRe = regexp.MustCompile(`NewHostModuleBuilder\(([^)]*)\)`)

// A .docx whose only picture is linked from `pic` (with "", a .docx of the
// same text and no picture: the control).
func linkedDocx(t *testing.T, pic string) []byte {
	t.Helper()
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	drawing := ""
	if pic != "" {
		rels += `<Relationship Id="rIdPic" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="` + pic + `" TargetMode="External"/>`
		drawing = `<w:p><w:r><w:drawing><wp:inline><wp:extent cx="914400" cy="914400"/><wp:docPr id="1" name="far"/>` +
			`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="1" name="far"/><pic:cNvPicPr/></pic:nvPicPr>` +
			`<pic:blipFill><a:blip r:link="rIdPic"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>` +
			`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="914400" cy="914400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`
	}
	rels += `</Relationships>`
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": rels,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
			`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
			`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>` +
			`<w:p><w:r><w:t>A linked picture:</w:t></w:r></w:p>` + drawing + `</w:body></w:document>`,
	}
	return zipOfFiles(t, files)
}

// An EPUB 3 book of one chapter that names a picture, a style sheet and a
// font at `at` (with nil, the same chapter naming none of them: the control).
func remoteEpub(t *testing.T, at func(string) string) []byte {
	t.Helper()
	head, picture := "", ""
	if at != nil {
		head = `<link rel="stylesheet" href="` + at("book.css") + `"/><style>@font-face { font-family: far; src: url(` + at("font.woff") + `) }</style>`
		picture = `<p><img src="` + at("epub.png") + `" alt="far"/></p>`
	}
	return zipOfFiles(t, map[string]string{
		"mimetype": "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` +
			`<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">` +
			`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:far</dc:identifier><dc:title>Far</dc:title><dc:language>en</dc:language></metadata>` +
			`<manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="ch1"/></spine></package>`,
		"OEBPS/ch1.xhtml": `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title>` +
			head + `</head><body><h1>Chapter one</h1>` + picture + `</body></html>`,
	})
}

func zipOfFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	order := []string{"mimetype", "[Content_Types].xml"}
	for name := range files {
		if name != "mimetype" && name != "[Content_Types].xml" {
			order = append(order, name)
		}
	}
	for _, name := range order {
		body, ok := files[name]
		if !ok {
			continue
		}
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestEngine_NoRemoteFetch: with the Office editor's real modules, a .docx
// with a linked picture, an HTML page naming a picture, a background and a
// style sheet elsewhere and an EPUB chapter naming a picture, a style sheet
// and a font elsewhere are converted to PDF, and the .docx and the EPUB
// drawn as thumbnails, as the same documents naming nothing elsewhere (the
// controls, run first in the same processes): the same pages, a thumbnail
// of the same size - the .docx's the same picture, its linked picture left
// out as a Document Server without a network leaves it - and in about the
// same time. The server those things are on is asked for nothing.
//
// The time is the point: until x2t v9.4.0.129+4 (the Office editor 0.4.0's
// patches/09) the .docx and the HTML page never finished. x2t's downloader
// waits for a thread to fetch the address, WebAssembly starts none, and x2t
// slept in a loop until the job's deadline (measured 2026-10-16 with the
// earlier engine: neither done in 2 minutes, the controls 3 to 15 s). So a
// document naming another server may take at most twice its control's time
// and 10 s more; the control runs in the same test, so a slow machine slows
// both.
func TestEngine_NoRemoteFetch(t *testing.T) {
	r, _ := realEngine(t, "nothing else")
	var hits atomic.Int64
	var asked atomic.Value
	asked.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		asked.Store(req.URL.String())
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	t.Cleanup(srv.Close)
	at := func(name string) string { return srv.URL + "/" + name }

	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, b, 0o644))
		return p
	}
	cases := []struct {
		name, from      string
		control, remote []byte
		// thumb: drawn as a thumbnail too; same: the same picture as the
		// control's (nothing of the remote document's own is left out).
		thumb, same bool
	}{
		{"linked.docx", "docx", linkedDocx(t, ""), linkedDocx(t, at("linked.png")), true, true},
		{"page.html", "html",
			[]byte(`<!doctype html><html><body><h1>Page</h1><p>x</p></body></html>`),
			[]byte(`<!doctype html><html><head><link rel="stylesheet" href="` + at("page.css") + `"></head>` +
				`<body><h1>Page</h1><img src="` + at("page.png") + `"><p style="background:url(` + at("bg.png") + `)">x</p></body></html>`),
			false, false},
		// The chapter's <img> has alt text, which takes the picture's place.
		{"far.epub", "epub", remoteEpub(t, nil), remoteEpub(t, at), true, false},
	}
	within := func(name, what string, took, control time.Duration) {
		t.Helper()
		assert.LessOrEqual(t, took, 2*control+10*time.Second,
			"%s %s took %s, the same without anything elsewhere %s: the engine waited for another server", name, what, took, control)
	}
	for _, c := range cases {
		control := write("control-"+c.name, c.control)
		remote := write(c.name, c.remote)

		toPDF := func(src string) (time.Duration, int) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			dst := filepath.Join(dir, filepath.Base(src)+".pdf")
			started := time.Now()
			err := r.Convert(ctx, Conversion{Src: src, From: c.from, To: "pdf", Dst: dst, MaxBytes: 64 << 20})
			took := time.Since(started)
			t.Logf("MEASURE %s -> pdf: %s, %v", filepath.Base(src), took.Round(time.Millisecond), err)
			require.NoError(t, err, "%s -> pdf", filepath.Base(src))
			b, err := os.ReadFile(dst)
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(b, []byte("%PDF-")), "%s: a PDF", filepath.Base(src))
			return took, len(pageRe.FindAll(b, -1))
		}
		controlTook, controlPages := toPDF(control)
		took, pages := toPDF(remote)
		assert.Equal(t, controlPages, pages, "%s: the control's pages", c.name)
		within(c.name, "to PDF", took, controlTook)

		if !c.thumb {
			continue
		}
		thumb := func(src string) (time.Duration, image.Image) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			started := time.Now()
			b, err := r.DrawThumbnail(ctx, Thumbnail{Src: src, From: c.from, Size: 320, MaxBytes: 4 << 20})
			took := time.Since(started)
			t.Logf("MEASURE thumbnail %s: %d bytes, %s, %v", filepath.Base(src), len(b), took.Round(time.Millisecond), err)
			require.NoError(t, err, "thumbnail of %s", filepath.Base(src))
			img, err := png.Decode(bytes.NewReader(b))
			require.NoError(t, err, "thumbnail of %s", filepath.Base(src))
			return took, img
		}
		controlTook, controlImg := thumb(control)
		took, img := thumb(remote)
		assert.Equal(t, controlImg.Bounds(), img.Bounds(), "%s: the control's thumbnail size", c.name)
		if c.same {
			assert.True(t, samePixels(controlImg, img), "%s: the control's thumbnail, the linked picture left out", c.name)
		}
		within(c.name, "as a thumbnail", took, controlTook)
	}
	require.Zero(t, hits.Load(), "the engine asked another server for %v", asked.Load())
}

// samePixels: two pictures of the same size with the same colour at every
// point.
func samePixels(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, aa := a.At(x, y).RGBA()
			br, bg, bb, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb || aa != ba {
				return false
			}
		}
	}
	return true
}

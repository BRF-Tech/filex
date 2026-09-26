// Package newdoc answers one question: what are the BYTES of a brand-new,
// empty document of type X?
//
// # Why this is not trivial
//
// For a .md or a .csv the answer is "none" — a zero-byte file is a valid,
// empty text file and every editor opens it. For a .docx it is not: an Office
// document is a ZIP of XML parts, and a zero-byte file named report.docx is
// rejected by Word, by OnlyOffice and by LibreOffice alike. The same is true
// of .xlsx, .pptx and the OpenDocument trio. So "create an empty file" splits
// into two genuinely different jobs and this package owns the hard one.
//
// # Why the bytes are embedded rather than generated
//
// The obvious alternative is to shell out to `soffice --headless
// --convert-to` at create time. It was rejected because it is not available
// where the product runs: the full image (ghcr.io/brf-tech/filex:vX) carries
// LibreOffice, but `:slim` and the bare `filex` binary deliberately carry no
// external tools at all — that is the documented point of those
// distributions. A feature that silently works on one image and 500s on
// another is worse than a feature with a narrower promise, so the bytes come
// from the binary itself: a few KB of XML, identical on every distribution,
// with no process to spawn and nothing to be absent.
//
// # Why the templates are XML files and not .docx blobs
//
// The parts live under templates/ as readable XML and are zipped in memory on
// demand. A committed .docx would be an opaque binary in the diff — nobody
// could review a change to it, and nobody could tell a corrupted one from a
// deliberate one. As text, every part is reviewable and the ZIP container is
// reconstructed by code that is itself tested.
//
// ⚠ The ZIP is built with an explicit part manifest, NOT by walking the
// embedded tree. Two of the formats care about entry ORDER: an OpenDocument
// file must carry `mimetype` as its FIRST entry, STORED (uncompressed) and
// with no extra field, because that is what puts the media type at byte
// offset 38 where every magic-byte sniffer looks for it. A lexical walk puts
// META-INF first and the file stops being recognisable as an ODF document
// while still being a perfectly valid ZIP — which is exactly the kind of
// failure that passes every test that only asks "does it unzip?".
package newdoc

import (
	"archive/zip"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

// ⚠ `all:` is mandatory, not decorative: the OOXML formats put their
// relationship parts in directories called `_rels`, and a plain `//go:embed
// templates` pattern skips every path element beginning with `_` or `.`. With
// the wrong pattern the package still builds and still produces a ZIP — one
// with no relationships in it, which no Office application will open.
//
//go:embed all:templates
var templatesFS embed.FS

// ErrUnknownType is returned for an extension this build cannot create.
var ErrUnknownType = errors.New("newdoc: unknown document type")

// Group is the coarse family a type belongs to. It exists for the picker's
// section headings; it is NOT the capability gate (see Type.Requires).
type Group string

const (
	// GroupDocument — office documents. Real bytes, real editor.
	GroupDocument Group = "document"
	// GroupText — text-shaped files where empty is the correct content.
	GroupText Group = "text"
	// GroupDiagram — diagram sources.
	GroupDiagram Group = "diagram"
)

// Requirement names the external service a client needs before it should
// offer this type to a person.
//
// ⚠ The server states the dependency; it does not resolve it. Whether
// OnlyOffice is actually reachable is already answered by
// /api/files/capabilities (external.onlyoffice), and an embedder may point at
// a document server this process cannot see — so the client is the only place
// that knows the true answer. What the client must NOT do is re-derive the
// dependency from a hardcoded extension list of its own: that list would rot
// the moment this registry grows a type.
type Requirement string

const (
	// RequiresNothing — a built-in editor handles it (code / markdown).
	RequiresNothing Requirement = ""
	// RequiresOnlyOffice — only the OnlyOffice surface can open it.
	RequiresOnlyOffice Requirement = "onlyoffice"
	// RequiresDrawio — only the drawio surface can open it.
	RequiresDrawio Requirement = "drawio"
)

// Type is one creatable document type, as published to clients.
//
// There is deliberately no label here. Labels are localised, the catalogues
// live in the frontend package, and a server-rendered English string would be
// the one piece of this dialog that could not be translated.
type Type struct {
	Ext      string      `json:"ext"`
	Group    Group       `json:"group"`
	MIME     string      `json:"mime"`
	Requires Requirement `json:"requires,omitempty"`
	// ExtRequired says the file must carry this extension (#56). True for the
	// containers — an office document or a diagram is a ZIP or an XML file
	// that its editor finds BY extension, so "report" with no extension is a
	// document nothing opens. False for text: the new file is zero bytes and
	// is just as valid called LICENSE, Makefile or test.conf, so the person
	// names it whatever they like.
	//
	// ⚠ Never `omitempty`. A client tells a server from before #56 by this
	// key being ABSENT (that server appends the extension to every type), so
	// `false` has to be on the wire.
	ExtRequired bool `json:"ext_required"`
}

// part is one entry in the produced ZIP.
type part struct {
	// name is the path INSIDE the archive.
	name string
	// src is the path inside templatesFS. Empty when literal is used.
	src string
	// literal is inline content, for parts that must not be affected by a
	// checkout's line-ending policy (see odfMimetype).
	literal string
	// store writes the entry uncompressed.
	store bool
}

// tmpl describes how to materialise one extension.
type tmpl struct {
	typ Type
	// parts is empty for text types (zero bytes is the whole answer) and for
	// single-file types, which use `single` instead.
	parts []part
	// single is a verbatim embedded file (no ZIP container).
	single string
}

// odfMimetype returns the ODF `mimetype` part for a media type.
//
// ⚠ The content is inline Go rather than a file on disk on purpose. The value
// must be EXACTLY the media type with no trailing newline: a sniffer compares
// a fixed number of bytes starting at offset 38, and one stray byte — a
// newline a text editor adds on save, or a CR a Windows checkout inserts —
// turns a valid OpenDocument file into an unrecognised ZIP. A Go string
// literal cannot acquire either.
func odfMimetype(media string) part {
	return part{name: "mimetype", literal: media, store: true}
}

func odfParts(media string, dir string) []part {
	return []part{
		odfMimetype(media),
		{name: "META-INF/manifest.xml", src: dir + "/META-INF/manifest.xml"},
		{name: "content.xml", src: dir + "/content.xml"},
		{name: "styles.xml", src: dir + "/styles.xml"},
		{name: "meta.xml", src: dir + "/meta.xml"},
	}
}

func ooxmlPart(dir, name string) part {
	return part{name: name, src: dir + "/" + name}
}

func ooxmlParts(dir string, names ...string) []part {
	out := make([]part, 0, len(names))
	for _, n := range names {
		out = append(out, ooxmlPart(dir, n))
	}
	return out
}

const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimeOdt  = "application/vnd.oasis.opendocument.text"
	mimeOds  = "application/vnd.oasis.opendocument.spreadsheet"
	mimeOdp  = "application/vnd.oasis.opendocument.presentation"
)

func textType(ext, mime string) tmpl {
	return tmpl{typ: Type{Ext: ext, Group: GroupText, MIME: mime}}
}

// container is a type whose bytes are a format of their own (an office ZIP,
// a diagram's XML), so the file keeps its extension — see Type.ExtRequired.
func container(ext string, group Group, mime string, requires Requirement) Type {
	return Type{Ext: ext, Group: group, MIME: mime, Requires: requires, ExtRequired: true}
}

// registry is the ordered list of everything this build can create. The order
// is the order the picker draws, so it is a design decision and not an
// accident of map iteration.
//
// ⚠ Adding a row here is the ONLY thing needed to offer a new type: the
// picker renders whatever the server sends, falling back to the bare
// extension when it has no localised label yet.
var registry = []tmpl{
	{
		typ:   container("docx", GroupDocument, mimeDocx, RequiresOnlyOffice),
		parts: ooxmlParts("docx", "[Content_Types].xml", "_rels/.rels", "docProps/app.xml", "docProps/core.xml", "word/document.xml", "word/_rels/document.xml.rels", "word/styles.xml"),
	},
	{
		typ:   container("xlsx", GroupDocument, mimeXlsx, RequiresOnlyOffice),
		parts: ooxmlParts("xlsx", "[Content_Types].xml", "_rels/.rels", "docProps/app.xml", "docProps/core.xml", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"),
	},
	{
		typ: container("pptx", GroupDocument, mimePptx, RequiresOnlyOffice),
		parts: ooxmlParts("pptx", "[Content_Types].xml", "_rels/.rels", "docProps/app.xml", "docProps/core.xml",
			"ppt/presentation.xml", "ppt/_rels/presentation.xml.rels",
			"ppt/slideMasters/slideMaster1.xml", "ppt/slideMasters/_rels/slideMaster1.xml.rels",
			"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/_rels/slideLayout1.xml.rels",
			"ppt/slides/slide1.xml", "ppt/slides/_rels/slide1.xml.rels",
			"ppt/theme/theme1.xml"),
	},
	{
		typ:   container("odt", GroupDocument, mimeOdt, RequiresOnlyOffice),
		parts: odfParts(mimeOdt, "odt"),
	},
	{
		typ:   container("ods", GroupDocument, mimeOds, RequiresOnlyOffice),
		parts: odfParts(mimeOds, "ods"),
	},
	{
		typ:   container("odp", GroupDocument, mimeOdp, RequiresOnlyOffice),
		parts: odfParts(mimeOdp, "odp"),
	},

	textType("md", "text/markdown; charset=utf-8"),
	textType("txt", "text/plain; charset=utf-8"),
	textType("csv", "text/csv; charset=utf-8"),
	textType("json", "application/json"),
	textType("yaml", "application/yaml"),
	textType("xml", "application/xml"),
	textType("html", "text/html; charset=utf-8"),
	textType("css", "text/css; charset=utf-8"),
	textType("js", "text/javascript; charset=utf-8"),
	textType("ts", "text/plain; charset=utf-8"),
	textType("py", "text/x-python; charset=utf-8"),
	textType("sh", "text/x-shellscript; charset=utf-8"),

	{
		typ:    container("drawio", GroupDiagram, "application/vnd.jgraph.mxfile", RequiresDrawio),
		single: "drawio/diagram.xml",
	},
}

var byExt = func() map[string]tmpl {
	m := make(map[string]tmpl, len(registry))
	for _, t := range registry {
		m[t.typ.Ext] = t
	}
	return m
}()

// Types returns every type this build can create, in picker order.
func Types() []Type {
	out := make([]Type, 0, len(registry))
	for _, t := range registry {
		out = append(out, t.typ)
	}
	return out
}

// Lookup resolves an extension (with or without a leading dot, any case).
func Lookup(ext string) (Type, bool) {
	t, ok := byExt[normalizeExt(ext)]
	if !ok {
		return Type{}, false
	}
	return t.typ, true
}

func normalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

var (
	cacheMu sync.Mutex
	cache   = map[string][]byte{}
)

// Bytes returns the content of a brand-new file of this type.
//
// The returned slice is shared with an internal cache and MUST NOT be
// modified; callers stream it and never own it. An empty, non-nil slice is a
// correct answer — it is what a new .md is.
func Bytes(ext string) ([]byte, error) {
	key := normalizeExt(ext)
	t, ok := byExt[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, ext)
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()
	if b, hit := cache[key]; hit {
		return b, nil
	}

	var (
		b   []byte
		err error
	)
	switch {
	case t.single != "":
		b, err = fs.ReadFile(templatesFS, "templates/"+t.single)
	case len(t.parts) > 0:
		b, err = buildZip(t.parts)
	default:
		b = []byte{}
	}
	if err != nil {
		return nil, err
	}
	cache[key] = b
	return b, nil
}

// A fixed MS-DOS timestamp (2000-01-01 00:00:00), so the same template always
// produces byte-identical output. Two reasons it is written into the legacy
// ModifiedDate/ModifiedTime fields rather than into FileHeader.Modified:
// determinism, and — measured, not assumed — archive/zip appends a 9-byte
// "extended timestamp" extra field whenever Modified is set. That extra field
// would push the ODF `mimetype` content past byte 38 and quietly cost the file
// its identity. See odfMimetype.
const (
	dosDate uint16 = (2000-1980)<<9 | 1<<5 | 1
	dosTime uint16 = 0
)

func buildZip(parts []part) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		body := []byte(p.literal)
		if p.src != "" {
			b, err := fs.ReadFile(templatesFS, "templates/"+p.src)
			if err != nil {
				return nil, fmt.Errorf("newdoc: template part %s: %w", p.src, err)
			}
			body = b
		}
		h := &zip.FileHeader{Name: p.name, Method: zip.Deflate}
		if p.store {
			h.Method = zip.Store
		}
		h.ModifiedDate, h.ModifiedTime = dosDate, dosTime
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// embeddedFiles lists every file actually compiled into the binary. Used by
// the test that proves the manifest and the tree agree — an orphan template is
// a part somebody meant to reference and did not, and the resulting document
// is broken in a way no unit test of the ZIP writer can see.
func embeddedFiles() []string {
	var out []string
	_ = fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, strings.TrimPrefix(p, "templates/"))
		return nil
	})
	sort.Strings(out)
	return out
}

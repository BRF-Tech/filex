package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"sort"
	"strings"
)

func init() { Register(officeExtractor{}) }

// officeExtractor handles the OOXML trio — docx / xlsx / pptx — with no
// external dependency: they are ZIP archives of XML, so archive/zip +
// encoding/xml is enough.
//
//	docx → word/document.xml        (paragraph text nodes <w:t>)
//	xlsx → xl/sharedStrings.xml     (shared string items <t>)
//	pptx → ppt/slides/slide*.xml    (run text nodes <a:t>)
//
// The kind is sniffed from the archive members (Extract has no ext/mime),
// so one extractor covers all three. Corrupt/foreign archives yield ""
// with a nil error per the extraction contract.
type officeExtractor struct{}

var officeMimes = map[string]struct{}{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {},
}

func (officeExtractor) Supports(mime, ext string) bool {
	switch ext {
	case "docx", "xlsx", "pptx":
		return true
	}
	_, ok := officeMimes[mime]
	return ok
}

// memberXMLCap bounds how much RAW XML is read per archive member — a
// decompression-bomb guard on top of the source-size cap (a 5 MiB zip can
// inflate far beyond it). Text collection also stops at `limit`, so this
// only matters for pathological members with no early text.
const memberXMLCap int64 = 16 << 20

// maxSlides bounds how many pptx slides are walked.
const maxSlides = 200

func (officeExtractor) Extract(_ context.Context, r io.Reader, limit int64) (string, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	data, rerr := io.ReadAll(r)
	if rerr != nil {
		return "", rerr // transport failure — the queue may retry
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil // not a readable zip → nothing to extract
	}

	var docXML *zip.File
	var sharedXML *zip.File
	var slides []*zip.File
	for _, f := range zr.File {
		switch {
		case f.Name == "word/document.xml":
			docXML = f
		case f.Name == "xl/sharedStrings.xml":
			sharedXML = f
		case strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml"):
			slides = append(slides, f)
		}
	}

	var sb strings.Builder
	switch {
	case docXML != nil:
		collectXMLText(&sb, docXML, limit, "p")
	case sharedXML != nil:
		collectXMLText(&sb, sharedXML, limit, "si")
	case len(slides) > 0:
		sort.Slice(slides, func(i, j int) bool { return slideLess(slides[i].Name, slides[j].Name) })
		if len(slides) > maxSlides {
			slides = slides[:maxSlides]
		}
		for _, sl := range slides {
			if int64(sb.Len()) >= limit {
				break
			}
			collectXMLText(&sb, sl, limit, "p")
		}
	default:
		return "", nil // zip, but not an OOXML document
	}
	return clamp(sanitize(sb.String()), limit), nil
}

// collectXMLText streams one archive member, appending character data found
// inside <t>-style text elements (w:t / a:t / plain t — all share the local
// name "t") to sb, and a newline at the end of every paragraph-style element
// (local name paraLocal). Stops early once sb reaches limit; XML errors end
// collection with whatever was gathered (best-effort).
func collectXMLText(sb *strings.Builder, f *zip.File, limit int64, paraLocal string) {
	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()

	dec := xml.NewDecoder(io.LimitReader(rc, memberXMLCap))
	inText := 0
	for int64(sb.Len()) < limit {
		tok, err := dec.Token()
		if err != nil {
			return // io.EOF or malformed XML — keep what we have
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText++
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				if inText > 0 {
					inText--
				}
			case paraLocal:
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
			}
		case xml.CharData:
			if inText > 0 {
				sb.Write(t)
			}
		}
	}
}

// slideLess orders "ppt/slides/slideN.xml" numerically (slide2 before
// slide10), falling back to lexicographic when parsing fails.
func slideLess(a, b string) bool {
	na, oka := slideNum(a)
	nb, okb := slideNum(b)
	if oka && okb {
		return na < nb
	}
	return a < b
}

func slideNum(name string) (int, bool) {
	s := strings.TrimSuffix(strings.TrimPrefix(name, "ppt/slides/slide"), ".xml")
	n := 0
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

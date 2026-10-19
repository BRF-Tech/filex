package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/search/extract/extracttest"
)

// ---------- registry / allowlist ----------

func TestFor_Allowlist(t *testing.T) {
	cases := []struct {
		name string
		mime string
		ext  string
		want string // "" = no extractor
	}{
		{"txt by ext", "", "txt", "text"},
		{"md upper-dot ext", "", ".MD", "text"},
		{"go source", "", "go", "text"},
		{"yaml", "", "yml", "text"},
		{"text mime with params", "text/plain; charset=utf-8", "", "text"},
		{"json mime", "application/json", "", "text"},
		{"pdf ext", "", "pdf", "pdf"},
		{"pdf mime", "application/pdf", "", "pdf"},
		{"docx ext", "", "docx", "office"},
		{"xlsx mime", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "", "office"},
		{"pptx ext", "", "PPTX", "office"},
		{"png image", "image/png", "png", ""},
		{"mp4 video", "video/mp4", "mp4", ""},
		{"binary blob", "application/octet-stream", "bin", ""},
		{"empty everything", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := For(tc.mime, tc.ext)
			got := ""
			switch e.(type) {
			case textExtractor:
				got = "text"
			case pdfExtractor:
				got = "pdf"
			case officeExtractor:
				got = "office"
			case nil:
				got = ""
			default:
				got = "other"
			}
			if got != tc.want {
				t.Fatalf("For(%q, %q) = %q extractor, want %q", tc.mime, tc.ext, got, tc.want)
			}
			if Supported(tc.mime, tc.ext) != (tc.want != "") {
				t.Fatalf("Supported(%q, %q) disagrees with For", tc.mime, tc.ext)
			}
		})
	}
}

// ---------- text ----------

func TestText_Extract(t *testing.T) {
	ctx := context.Background()
	ex := textExtractor{}

	cases := []struct {
		name  string
		in    []byte
		limit int64
		want  string
	}{
		{"plain", []byte("merhaba dünya\nikinci satır"), 0, "merhaba dünya\nikinci satır"},
		{"invalid utf8 dropped", []byte("ok\xff\xfe devam"), 0, "ok devam"},
		{"nul stripped", []byte("a\x00b"), 0, "ab"},
		{"limit cut", []byte("0123456789"), 4, "0123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ex.Extract(ctx, bytes.NewReader(tc.in), tc.limit)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestText_LimitDoesNotSplitRune(t *testing.T) {
	// "aç" = 'a' + 2-byte 'ç'; limit 2 lands mid-rune → the partial
	// sequence must be dropped, not emitted as garbage.
	got, err := textExtractor{}.Extract(context.Background(), strings.NewReader("aç"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a" {
		t.Fatalf("got %q, want %q", got, "a")
	}
}

// ---------- office (docx/xlsx/pptx) ----------

// buildZip assembles an in-memory zip from name → content pairs.
func buildZip(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestOffice_Extract(t *testing.T) {
	ctx := context.Background()
	ex := officeExtractor{}

	docx := buildZip(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types/>`,
		"word/document.xml": `<?xml version="1.0"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Sözleşme taslağı</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Bütçe: 1500 TL</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	})
	xlsx := buildZip(t, map[string]string{
		"xl/sharedStrings.xml": `<?xml version="1.0"?>` +
			`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">` +
			`<si><t>Fatura no</t></si><si><t>Tutar 42</t></si></sst>`,
	})
	pptx := buildZip(t, map[string]string{
		"ppt/slides/slide2.xml": `<?xml version="1.0"?>` +
			`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
			`<p:cSld><a:p><a:r><a:t>ikinci slayt</a:t></a:r></a:p></p:cSld></p:sld>`,
		"ppt/slides/slide1.xml": `<?xml version="1.0"?>` +
			`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
			`<p:cSld><a:p><a:r><a:t>Açılış sunumu</a:t></a:r></a:p></p:cSld></p:sld>`,
	})

	cases := []struct {
		name     string
		data     []byte
		contains []string
	}{
		{"docx", docx, []string{"Sözleşme taslağı", "Bütçe: 1500 TL"}},
		{"xlsx", xlsx, []string{"Fatura no", "Tutar 42"}},
		{"pptx", pptx, []string{"Açılış sunumu", "ikinci slayt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ex.Extract(ctx, bytes.NewReader(tc.data), 0)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("extracted %q does not contain %q", got, want)
				}
			}
		})
	}

	// pptx slide order must be numeric (slide1 before slide2 even though
	// the zip lists slide2 first).
	got, err := ex.Extract(ctx, bytes.NewReader(pptx), 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(got, "Açılış sunumu") > strings.Index(got, "ikinci slayt") {
		t.Fatalf("slides out of order: %q", got)
	}
}

func TestOffice_GarbageIsEmptyNotError(t *testing.T) {
	ctx := context.Background()
	ex := officeExtractor{}
	for name, data := range map[string][]byte{
		"not a zip":         []byte("definitely not a zip"),
		"zip but not ooxml": buildZip(t, map[string]string{"readme.txt": "hi"}),
	} {
		got, err := ex.Extract(ctx, bytes.NewReader(data), 0)
		if err != nil || got != "" {
			t.Fatalf("%s: want (\"\", nil), got (%q, %v)", name, got, err)
		}
	}
}

func TestOffice_LimitRespected(t *testing.T) {
	big := buildZip(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			strings.Repeat(`<w:p><w:r><w:t>kelime kelime kelime</w:t></w:r></w:p>`, 200) +
			`</w:body></w:document>`,
	})
	got, err := officeExtractor{}.Extract(context.Background(), bytes.NewReader(big), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 100 {
		t.Fatalf("limit ignored: got %d bytes", len(got))
	}
}

// ---------- pdf ----------

func TestPDF_Extract(t *testing.T) {
	data := extracttest.MinimalPDF("Hello filex content search")
	got, err := pdfExtractor{}.Extract(context.Background(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Text-layer extraction may normalize spacing; match on a token.
	if !strings.Contains(got, "filex") {
		t.Fatalf("extracted %q, want it to contain %q", got, "filex")
	}
}

func TestPDF_GarbageIsEmptyNotError(t *testing.T) {
	got, err := pdfExtractor{}.Extract(context.Background(), strings.NewReader("%PDF-1.4 truncated garbage"), 0)
	if err != nil || got != "" {
		t.Fatalf("want (\"\", nil), got (%q, %v)", got, err)
	}
}

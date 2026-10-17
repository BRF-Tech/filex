package officeengine

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine process's own pieces that need no WebAssembly: the conversion
// order x2t is given, what takes the layout, the csv's delimiter, the
// return codes, and the read-only view of the app's zips.

// The order is written as a Document Server writes it, every path one the
// engine chose under /w, never a document's name.
func TestParams_AsADocumentServerWritesThem(t *testing.T) {
	p := x2tJob{from: "input.csv", to: "Editor.bin", formatTo: 0x2002, noBase64: true,
		text: &textOptions{codepage: x2tUTF8, delimiter: 2}, json: `{"a":"x&y"}`}.params()
	for _, want := range []string{
		"<m_sFileFrom>/w/input.csv</m_sFileFrom>",
		"<m_sFileTo>/w/Editor.bin</m_sFileTo>",
		"<m_sTempDir>/w/tmp</m_sTempDir>",
		"<m_sThemeDir>/w/themes</m_sThemeDir>",
		"<m_bIsNoBase64>true</m_bIsNoBase64>",
		"<m_nFormatTo>8194</m_nFormatTo>",
		"<m_nCsvTxtEncoding>46</m_nCsvTxtEncoding>",
		"<m_nCsvDelimiter>2</m_nCsvDelimiter>",
		"<m_sJsonParams>{&quot;a&quot;:&quot;x&amp;y&quot;}</m_sJsonParams>",
	} {
		assert.Contains(t, p, want)
	}
	assert.NotContains(t, p, "m_sFontDir", "no fonts asked for")

	thumb := x2tJob{from: "Editor.bin", to: "output.png", formatTo: formatPNG, fonts: true,
		thumb: &thumbOptions{format: 4, aspect: 1, first: true, width: 320, height: 320}}.params()
	for _, want := range []string{
		"<m_sFontDir>/w/fonts/</m_sFontDir>",
		"<m_bIsNoBase64>false</m_bIsNoBase64>",
		"<m_oThumbnail><format>4</format><aspect>1</aspect><first>true</first><zip>false</zip><width>320</width><height>320</height></m_oThumbnail>",
	} {
		assert.Contains(t, thumb, want)
	}
}

// What the layout makes and what x2t makes alone; what neither makes.
func TestPlanFor(t *testing.T) {
	for _, c := range []struct {
		from, to string
		mode     string
		format   int
		ext      string
	}{
		{"docx", "pdf", "pdf", formatPDF, "pdf"},
		{"docx", "pdfa", "pdf", formatPDFA, "pdf"},
		{"xlsx", "pdf", "pdf", formatPDF, "pdf"},
		{"pptx", "png", "image", formatPNG, "png"},
		{"docx", "html", "html", formatHTML, "html"},
		{"docx", "epub", "html", formatEPUB, "epub"},
		{"docx", "md", "html", formatMD, "md"},
		{"docx", "odt", "", 0x0043, "odt"},
		{"doc", "docx", "", 0x0041, "docx"},
		{"docx", "dotx", "", 0x004c, "dotx"},
		{"csv", "xlsx", "", 0x0101, "xlsx"},
		{"xlsx", "csv", "", 0x0104, "csv"},
		{"pptx", "odp", "", 0x0083, "odp"},
	} {
		p, err := planFor(c.from, c.to)
		require.NoError(t, err, "%s -> %s", c.from, c.to)
		assert.Equal(t, c.mode, p.mode, "%s -> %s", c.from, c.to)
		assert.Equal(t, c.format, p.formatTo, "%s -> %s", c.from, c.to)
		assert.Equal(t, c.ext, p.ext, "%s -> %s", c.from, c.to)
	}
	for _, c := range [][2]string{
		{"xlsx", "html"}, // the Document Server answers -7 too
		{"pptx", "epub"},
		{"docx", "docx"},
		{"odg", "pdf"}, // no editor reads it
		{"docx", "fb2"},
		{"docx", "../x"},
	} {
		_, err := planFor(c[0], c[1])
		e, ok := AsError(err)
		require.True(t, ok, "%s -> %s: %v", c[0], c[1], err)
		assert.Equal(t, CodeParams, e.Code, "%s -> %s", c[0], c[1])
	}
}

func TestSniffDelimiter(t *testing.T) {
	for in, want := range map[string]int{
		"a,b,c\n1,2,3":            4,
		"a;b;c\n1;2;3":            2,
		"a\tb\tc\n":               1,
		`"x;y",b,c` + "\n":        4,
		"elma;1,5\narmut;2,25\n":  2,
		"just one column\nnext\n": 0,
	} {
		assert.Equal(t, want, sniffDelimiter([]byte(in)), "%q", in)
	}
	assert.Equal(t, &textOptions{codepage: 46, delimiter: 2}, textFor("csv", 0, []byte("a;b\n")))
	assert.Equal(t, &textOptions{codepage: 46, delimiter: 5}, textFor("csv", 5, []byte("a;b\n")), "the delimiter asked for wins")
	assert.Equal(t, &textOptions{codepage: 46, delimiter: 4}, textFor("csv", 0, nil), "a csv written: comma, the Document Server's default")
	assert.Equal(t, &textOptions{codepage: 46}, textFor("txt", 0, nil))
	assert.Nil(t, textFor("docx", 2, nil))
}

func TestCodeOfExit(t *testing.T) {
	for exit, want := range map[uint32]int{
		80: CodeCorrupt, 81: CodeUnknown, 82: CodeParams, 83: CodeTimeout, 84: CodeCorrupt, 85: CodePassword,
		86: CodeCorrupt, 87: CodeCorrupt, 88: CodeParams, 89: CodeDetect, 90: CodePassword, 91: CodePassword,
		92: CodeCorrupt, 93: CodeTooLarge, 94: CodeTooLarge, 95: CodeDetect, 96: CodeTooLarge, 1: CodeCorrupt,
	} {
		assert.Equal(t, want, codeOfExit(exit), "x2t %d", exit)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// The layout reads the editor's files from the installed interface bundle
// and its own from engine.zip, in place: grafted under /e, seekable (QuickJS
// seeks to the end of a script for its size), nothing unpacked on disk.
func TestZipTree_ServesTheGraftedPartsSeekable(t *testing.T) {
	ui := zipOf(t, map[string]string{
		"index.html":                      "<html>",
		"editor/sdkjs/word/sdk-all.js":    "var sdk = 1;",
		"editor/sdkjs/common/AllFonts.js": "var fonts = [];",
		"editor/fonts/000":                "FONT",
		"editor/web-apps/apps/api/api.js": "api",
	})
	engine := zipOf(t, map[string]string{"layout/worker.js": "print('ready')", "x2t-server.wasm": "\x00asm"})
	uz, err := zip.NewReader(bytes.NewReader(ui), int64(len(ui)))
	require.NoError(t, err)
	ez, err := zip.NewReader(bytes.NewReader(engine), int64(len(engine)))
	require.NoError(t, err)

	tree := newZipTree()
	n, err := tree.graft(ez, "layout", "layout")
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only layout/: the modules are not the layout's to read")
	n, err = tree.graft(uz, "editor/sdkjs", "sdkjs")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	_, err = tree.graft(uz, "editor/fonts", "fonts")
	require.NoError(t, err)

	f, err := tree.Open("sdkjs/word/sdk-all.js")
	require.NoError(t, err)
	end, err := f.(io.Seeker).Seek(0, io.SeekEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(len("var sdk = 1;")), end)
	_, err = f.(io.Seeker).Seek(0, io.SeekStart)
	require.NoError(t, err)
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "var sdk = 1;", string(b))

	root, err := fs.ReadDir(tree, ".")
	require.NoError(t, err)
	var names []string
	for _, e := range root {
		names = append(names, e.Name())
		assert.True(t, e.IsDir())
	}
	assert.Equal(t, []string{"fonts", "layout", "sdkjs"}, names, "nothing else of either zip")

	for _, hidden := range []string{"index.html", "web-apps/apps/api/api.js", "x2t-server.wasm", "editor/fonts/000", "../editor/fonts/000"} {
		_, err := tree.Open(hidden)
		assert.Error(t, err, hidden)
	}
}

// An entry whose name climbs out of its directory is refused, not grafted.
func TestZipTree_RefusesAnUncleanName(t *testing.T) {
	bad := zipOf(t, map[string]string{"layout/../../etc/passwd": "x"})
	zr, err := zip.NewReader(bytes.NewReader(bad), int64(len(bad)))
	require.NoError(t, err)
	_, err = newZipTree().graft(zr, "layout", "layout")
	assert.Error(t, err)
}

// A package that is not the bytes installed is not opened.
func TestOpenPinned_RefusesAnotherHash(t *testing.T) {
	body := zipOf(t, map[string]string{"layout/worker.js": "x"})
	file := filepath.Join(t.TempDir(), "engine.zip")
	require.NoError(t, os.WriteFile(file, body, 0o600))
	sum := sha256.Sum256(body)

	zr, err := openPinned(file, strings.ToUpper(hex.EncodeToString(sum[:])))
	require.NoError(t, err, "the hash is compared without case")
	zr.Close()
	_, err = openPinned(file, strings.Repeat("0", 64))
	assert.ErrorContains(t, err, "not the installed")
}

package handlers_test

// GET /api/files/pdf/{name} (GitHub #124): a PDF's preview on the one API path
// filex's pages may frame. It must answer exactly what the manager's preview
// answers for a PDF and nothing else - not a download, not another file type,
// not a file outside the caller's reach.

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

const pdfBody = "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"

// pdfGet asks the PDF path for p with tok ("" = no credential) and the extra
// query and headers given.
func pdfGet(t *testing.T, base, tok, name, p string, extra url.Values, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	q := url.Values{"path": {p}}
	for k, v := range extra {
		q[k] = v
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/files/pdf/"+url.PathEscape(name)+"?"+q.Encode(), nil)
	require.NoError(t, err)
	if tok != "" {
		req.Header.Set("X-Filex-Token", tok)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

func TestPDFPreview_ServesThePDFInline(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/report.pdf", pdfBody)

	resp, body := pdfGet(t, f.URL, f.Tok, "report.pdf", "main://docs/report.pdf", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, pdfBody, body)
	assert.Equal(t, "application/pdf", resp.Header.Get("Content-Type"))
	assert.Empty(t, resp.Header.Get("Content-Disposition"), "a preview, not an attachment")
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))

	// An upper-case extension is a PDF too.
	f.put(t, f.Tok, "main://docs/SCAN.PDF", pdfBody)
	resp, body = pdfGet(t, f.URL, f.Tok, "SCAN.PDF", "main://docs/SCAN.PDF", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, pdfBody, body)
}

// The verb is the path's, never the query's: ?action=download (or the legacy
// ?q=download) cannot turn the framed path into an attachment.
func TestPDFPreview_TheActionIsAlwaysPreview(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/report.pdf", pdfBody)

	for _, extra := range []url.Values{
		{"action": {"download"}},
		{"q": {"download"}},
		{"action": {"index"}},
		{"action": {"delete"}, "q": {"download"}},
	} {
		resp, body := pdfGet(t, f.URL, f.Tok, "report.pdf", "main://docs/report.pdf", extra, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%v: %s", extra, body)
		assert.Equal(t, pdfBody, body, "%v", extra)
		assert.Empty(t, resp.Header.Get("Content-Disposition"), "%v", extra)
	}
}

// Only a PDF: an HTML page, a picture or a folder named here is refused before
// the storage is asked, so the one path a page may frame never serves a
// document of another kind.
func TestPDFPreview_RefusesWhatIsNotAPDF(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/page.html", "<script>alert(1)</script>")
	f.put(t, f.Tok, "main://docs/report.pdf.txt", "not a pdf")

	for _, p := range []string{"main://docs/page.html", "main://docs/report.pdf.txt", "main://docs", "main://docs/", ""} {
		resp, body := pdfGet(t, f.URL, f.Tok, "report.pdf", p, nil, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%q: %s", p, body)
		assert.NotContains(t, body, "<script>", p)
		assert.NotContains(t, body, "not a pdf", p)
	}
}

// Range, as on the manager's preview: the browser's viewer asks for windows.
func TestPDFPreview_HonoursRange(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/report.pdf", pdfBody)

	resp, body := pdfGet(t, f.URL, f.Tok, "report.pdf", "main://docs/report.pdf", nil, map[string]string{"Range": "bytes=0-3"})
	require.Equal(t, http.StatusPartialContent, resp.StatusCode, body)
	assert.Equal(t, "%PDF", body)
	assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
}

// The caller's reach is the manager's: no credential, no file; a token
// confined to a folder previews inside it and nothing outside it; a traversal
// is refused.
func TestPDFPreview_KeepsTheCallersReach(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://kutu/ic.pdf", pdfBody)
	f.put(t, f.Tok, "main://disari/gizli.pdf", "%PDF-1.4 gizli")
	confined := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,root:main://kutu")

	resp, body := pdfGet(t, f.URL, "", "gizli.pdf", "main://disari/gizli.pdf", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, body)
	assert.NotContains(t, body, "gizli")

	resp, body = pdfGet(t, f.URL, confined, "ic.pdf", "main://kutu/ic.pdf", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, pdfBody, body)

	resp, body = pdfGet(t, f.URL, confined, "gizli.pdf", "main://disari/gizli.pdf", nil, nil)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, body)
	assert.NotContains(t, body, "gizli")

	resp, body = pdfGet(t, f.URL, f.Tok, "gizli.pdf", "main://kutu/../disari/gizli.pdf", nil, nil)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, body)
	assert.NotContains(t, body, "%PDF-1.4 gizli")
}

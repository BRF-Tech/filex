package handlers_test

// End-to-end tests for "download this selection as one archive": the
// authenticated mint (POST /api/files/archive/download) and the credential-free
// stream (GET /z/{ticket}), driven through the real router built by aiFixture.
//
// The unit of proof here is an OPENED archive: names, and the bytes inside each
// member. A handler that answers 200 with a well-formed but empty zip passes
// every status-code assertion ever written.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// seedFiles writes content through the AI upload surface (the fixture's own
// write path) so the storage and the catalogue agree.
func seedFiles(t *testing.T, client *http.Client, base, tok string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		resp := aiReq(t, client, "POST", base+"/api/ai/upload", tok, map[string]any{"path": p, "content": body})
		require.Equal(t, http.StatusOK, resp.StatusCode, "seeding %s", p)
		resp.Body.Close()
	}
}

// mintArchive asks for a ticket and returns the decoded response.
func mintArchive(t *testing.T, client *http.Client, base, tok string, paths []string) (int, map[string]any) {
	t.Helper()
	resp := aiReq(t, client, "POST", base+"/api/files/archive/download", tok, map[string]any{"paths": paths})
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode == http.StatusOK {
		testutil.ReadJSON(t, resp, &out)
	} else {
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &out)
	}
	return resp.StatusCode, out
}

// fetchArchive redeems a ticket URL with NO credentials at all — which is the
// point of the ticket, and is also how a browser navigation arrives.
func fetchArchive(t *testing.T, base, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(base + url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, raw
}

// openZip unpacks an archive into name → body, failing the test if it will not
// open. "It opens" is half the assertion; the bodies are the other half.
func openZip(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err, "the response is not a readable zip (%d bytes)", len(raw))
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err, "member %s", f.Name)
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		require.NoError(t, err, "member %s", f.Name)
		out[f.Name] = string(b)
	}
	return out
}

func TestArchiveDownload_SelectionOfFilesAndAFolder(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://docs/one.txt":          "ONE",
		"main://docs/two.txt":          "TWO",
		"main://docs/nested/deep.txt":  "DEEP",
		"main://docs/nested/other.bin": "OTHER",
		"main://docs/untouched.txt":    "NOT SELECTED",
	})

	code, info := mintArchive(t, client, srv.URL, tok, []string{
		"main://docs/one.txt", "main://docs/two.txt", "main://docs/nested",
	})
	require.Equal(t, http.StatusOK, code, "mint failed: %v", info)
	assert.Equal(t, float64(4), info["files"], "two files plus the folder's two = 4 members")
	url, _ := info["url"].(string)
	require.True(t, strings.HasPrefix(url, "/z/"), "url = %q", url)

	resp, raw := fetchArchive(t, srv.URL, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/zip", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "attachment")

	got := openZip(t, raw)
	assert.Equal(t, "ONE", got["one.txt"])
	assert.Equal(t, "TWO", got["two.txt"])
	// A selected FOLDER keeps its own name in the archive, so unpacking two
	// folders side by side does not merge them into one heap.
	assert.Equal(t, "DEEP", got["nested/deep.txt"])
	assert.Equal(t, "OTHER", got["nested/other.bin"])
	_, unselected := got["untouched.txt"]
	assert.False(t, unselected, "a file that was not selected must not be in the archive: %v", got)
}

// ⚠ The regression this pins is the one this repo has already paid for once: a
// Content-Length that does not match the bytes. For a deflated archive there is
// no length to know before the last member, so there must be no header at all.
func TestArchiveDownload_DeclaresNoContentLength(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://a.txt": strings.Repeat("compress me ", 500),
		"main://b.txt": strings.Repeat("me too ", 500),
	})
	_, info := mintArchive(t, client, srv.URL, tok, []string{"main://a.txt", "main://b.txt"})
	url, _ := info["url"].(string)

	resp, raw := fetchArchive(t, srv.URL, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(-1), resp.ContentLength,
		"a streamed archive has no length until it is finished; declaring one truncates the body")
	// And the bytes really are the whole archive.
	got := openZip(t, raw)
	assert.Len(t, got["a.txt"], 6000)
	assert.Len(t, got["b.txt"], 3500)
	// Belt and braces: the archive is smaller than its contents, i.e. the
	// members really were deflated and a summed-sizes header would have been
	// wrong by a mile in the OTHER direction too.
	assert.Less(t, len(raw), 9500, "the archive should be compressed")
}

// The archive's name should say something. A folder gives its own name; a set
// of files from one folder gives that folder's name.
func TestArchiveDownload_NamesTheArchive(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://Faturalar/a.txt": "A",
		"main://Faturalar/b.txt": "B",
	})

	_, one := mintArchive(t, client, srv.URL, tok, []string{"main://Faturalar"})
	assert.Equal(t, "Faturalar.zip", one["name"])

	_, many := mintArchive(t, client, srv.URL, tok,
		[]string{"main://Faturalar/a.txt", "main://Faturalar/b.txt"})
	assert.Equal(t, "Faturalar.zip", many["name"], "files from one folder are named after that folder")
}

// A ticket is good once. Handing the same URL back a second time must not serve
// the archive again — that is the property that makes a credential-free redeem
// acceptable in the first place.
func TestArchiveDownload_TicketIsSingleUse(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://x.txt": "X"})
	_, info := mintArchive(t, client, srv.URL, tok, []string{"main://x.txt"})
	url, _ := info["url"].(string)

	resp, raw := fetchArchive(t, srv.URL, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "X", openZip(t, raw)["x.txt"])

	second, _ := fetchArchive(t, srv.URL, url)
	assert.Equal(t, http.StatusNotFound, second.StatusCode, "a consumed ticket must not serve a second archive")
}

func TestArchiveDownload_UnknownTicketIs404(t *testing.T) {
	srv, _, _, _ := aiFixture(t)
	resp, _ := fetchArchive(t, srv.URL, "/z/definitely-not-a-real-ticket")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A selection that resolves to nothing readable must say so at the click, not
// hand over a valid, empty, cheerful-looking archive.
func TestArchiveDownload_EmptySelectionIsRefused(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	resp := aiReq(t, client, "POST", srv.URL+"/api/files/archive/download", tok,
		map[string]any{"paths": []string{"main://does/not/exist.txt"}})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestArchiveDownload_NoPathsIsBadRequest(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	resp := aiReq(t, client, "POST", srv.URL+"/api/files/archive/download", tok, map[string]any{"paths": []string{}})
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// The mint is where authority lives, so the mint has to be authenticated. An
// anonymous POST must not be able to turn a path list into a public URL.
func TestArchiveDownload_MintRequiresAuth(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://secret.txt": "S"})

	body, _ := json.Marshal(map[string]any{"paths": []string{"main://secret.txt"}})
	resp, err := http.Post(srv.URL+"/api/files/archive/download", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.GreaterOrEqual(t, resp.StatusCode, 400, "an unauthenticated mint must be refused, got %d", resp.StatusCode)
	assert.Less(t, resp.StatusCode, 500)
}

// One file that vanishes between the mint and the stream costs that file and
// nothing else — and it is NAMED, not silently absent.
func TestArchiveDownload_VanishedMemberIsReportedNotFatal(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://keep1.txt": "K1",
		"main://gone.txt":  "G",
		"main://keep2.txt": "K2",
	})
	_, info := mintArchive(t, client, srv.URL, tok,
		[]string{"main://keep1.txt", "main://gone.txt", "main://keep2.txt"})
	url, _ := info["url"].(string)

	// Delete it AFTER the ticket was minted — the race the manifest exists for.
	del := aiReq(t, client, "POST", srv.URL+"/api/ai/delete", tok, map[string]any{"path": "main://gone.txt"})
	require.Less(t, del.StatusCode, 300, "delete failed")
	del.Body.Close()

	resp, raw := fetchArchive(t, srv.URL, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got := openZip(t, raw)
	assert.Equal(t, "K1", got["keep1.txt"])
	assert.Equal(t, "K2", got["keep2.txt"])
	note, ok := got["_FILEX-INCOMPLETE.txt"]
	require.True(t, ok, "a file that went missing must be reported inside the archive, got %v", keys(got))
	assert.Contains(t, note, "gone.txt")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ArchiveName is the naming rule on its own — the cases the HTTP tests above
// cannot reach cheaply.
func TestArchiveName(t *testing.T) {
	cases := []struct {
		name     string
		paths    []string
		override string
		want     string
	}{
		{"one folder", []string{"main://a/Belgeler"}, "", "Belgeler.zip"},
		{"one file", []string{"main://a/rapor.pdf"}, "", "rapor.pdf.zip"},
		{"same parent", []string{"main://a/b/x.txt", "main://a/b/y.txt"}, "", "b.zip"},
		{"different parents", []string{"main://a/x.txt", "main://c/y.txt"}, "", "filex-2-items.zip"},
		{"storage roots", []string{"main://x.txt", "main://y.txt"}, "", "filex-2-items.zip"},
		{"caller override", []string{"main://a/x.txt"}, "Seçtiklerim", "Seçtiklerim.zip"},
		{"override already has the suffix", []string{"main://a/x.txt"}, "Yedek.zip", "Yedek.zip"},
		// A name is a filename, not a path: separators would let a caller
		// choose where the browser writes.
		{"override with separators", []string{"main://a/x.txt"}, "../../etc/passwd", "etcpasswd.zip"},
		// Turkish letters survive. httpx.ContentDisposition carries them in
		// filename*, so mangling them here would be loss for no gain.
		{"turkish", []string{"main://Şubat Faturaları"}, "", "Şubat Faturaları.zip"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, handlers.ArchiveName(c.paths, c.override))
		})
	}
}

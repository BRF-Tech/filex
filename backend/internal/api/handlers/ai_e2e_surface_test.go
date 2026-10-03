package handlers_test

// The AI surface (REST /api/ai and the MCP file tools) and end-to-end
// encryption: task #113, measured on v0.48.1 before any of this existed.
//
//   - file_read of an encrypted kasa/rapor.pdf answered `mime: application/pdf`,
//     `encoding: base64`, no error: the agent took it for a broken PDF;
//   - file_list of depo://kasa showed the folder's key file;
//   - file_write depo://kasa/duz-metin.txt was accepted: plaintext in an
//     encrypted folder;
//   - file_share minted a public link to an encrypted file.
//
// ⚠ MCP answers are read for what they SAY. A JSON-RPC error also comes back
// without `isError` (e9f691df: the admin MCP output bug hid behind exactly
// that), so every tool call here asserts there is no `error` member and then
// the tool's own content: the entry fields, the error text, the code.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// e2eAIFixture is verbFixture's depo storage (RBAC off: a member is an editor
// everywhere) with an encrypted folder in it, on disk AND in the catalogue -
// the server recognises one by the marker's catalogue row and by nothing
// else:
//
//	kasa/                 encrypted folder (holds the key file)
//	kasa/.filex-e2e.json  the key file
//	kasa/rapor.pdf        ciphertext
//	kasa/alt/derin.txt    ciphertext, one level down
//	docs/tek.fxe          a single encrypted file, outside any folder
//	docs/a.txt            plaintext (verbFixture's)
type e2eAIFixture struct {
	*verbFixture
	tok string // member, read,write,delete,mcp
}

func newE2eAIFixture(t *testing.T) *e2eAIFixture {
	t.Helper()
	f := newVerbFixture(t)
	ctx := context.Background()
	st := f.depo

	write := func(rel, body string) {
		p := filepath.Join(f.depoRoot, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	write("kasa/.filex-e2e.json", `{"v":2,"salt":"c2FsdA=="}`)
	write("kasa/rapor.pdf", "filexe2e\x01ciphertext-not-a-pdf")
	write("kasa/alt/derin.txt", "filexe2e\x01ciphertext")
	write("docs/tek.fxe", "filexfxe\x01ciphertext")

	node := func(rel string, typ model.NodeType, parent *int64) *model.Node {
		p := "/" + rel
		n, err := f.store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, ParentID: parent, Name: filepath.Base(rel), Path: p,
			Type: typ, Size: 10, Etag: "e-" + rel, Mime: "application/octet-stream",
			PathHash: pathkey.Hash(st.ID, p), SeenAt: time.Now(),
		})
		require.NoError(t, err, rel)
		return n
	}
	kasa := node("kasa", model.NodeTypeDirectory, nil)
	node("kasa/.filex-e2e.json", model.NodeTypeFile, &kasa.ID)
	node("kasa/rapor.pdf", model.NodeTypeFile, &kasa.ID)
	alt := node("kasa/alt", model.NodeTypeDirectory, &kasa.ID)
	node("kasa/alt/derin.txt", model.NodeTypeFile, &alt.ID)
	docs := node("docs", model.NodeTypeDirectory, nil)
	node("docs/tek.fxe", model.NodeTypeFile, &docs.ID)
	node("docs/a.txt", model.NodeTypeFile, &docs.ID)

	return &e2eAIFixture{verbFixture: f, tok: testutil.NewAPIToken(t, f.store, f.memberID, "read,write,delete,mcp")}
}

func (f *e2eAIFixture) onDisk(rel string) bool {
	_, err := os.Stat(filepath.Join(f.depoRoot, filepath.FromSlash(rel)))
	return err == nil
}

// e2eAIEntry is the slice of an aiEntry these tests read.
type e2eAIEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Encrypted bool   `json:"encrypted"`
	E2eRoot   string `json:"e2e_root"`
}

// e2eAIRest sends one JSON call to the AI REST surface and decodes the body.
func (f *e2eAIFixture) rest(t *testing.T, method, p string, body any) (int, map[string]any, string) {
	t.Helper()
	payload := ""
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		payload = string(b)
	}
	code, raw := f.call(t, nil, f.tok, method, p, payload)
	var out map[string]any
	_ = json.Unmarshal([]byte(raw), &out)
	return code, out, raw
}

func entriesOf(t *testing.T, raw []byte) map[string]e2eAIEntry {
	t.Helper()
	var body struct {
		Entries []e2eAIEntry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &body), string(raw))
	out := map[string]e2eAIEntry{}
	for _, e := range body.Entries {
		out[e.Name] = e
	}
	return out
}

// e2eAITool is one answered MCP tool call, read for what it says.
type e2eAITool struct {
	IsError    bool
	Text       string
	Structured json.RawMessage
	Raw        string
}

func (f *e2eAIFixture) tool(t *testing.T, name string, args any) e2eAITool {
	t.Helper()
	a, err := json.Marshal(args)
	require.NoError(t, err)
	code, body := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", f.tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(a)+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	// Streamable HTTP may frame the answer as an SSE event.
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	require.True(t, start >= 0 && end > start, body)
	var rpc struct {
		Error  *json.RawMessage `json:"error"`
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(body[start:end+1]), &rpc), body)
	require.Nil(t, rpc.Error, "%s: a JSON-RPC error, not a tool answer: %s", name, body)
	require.NotNil(t, rpc.Result, "%s: no result: %s", name, body)
	out := e2eAITool{IsError: rpc.Result.IsError, Structured: rpc.Result.Structured, Raw: body}
	for _, c := range rpc.Result.Content {
		out.Text += c.Text
	}
	return out
}

// TestAIE2E_RowsSayWhatIsEncrypted - file_list, file_info, file_search and
// their REST twins say `encrypted` and `e2e_root` by the explorer's rules, and
// never list the key file.
func TestAIE2E_RowsSayWhatIsEncrypted(t *testing.T) {
	f := newE2eAIFixture(t)

	code, _, raw := f.rest(t, http.MethodGet, "/api/ai/files?path="+url.QueryEscape("depo://kasa"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	in := entriesOf(t, []byte(raw))
	assert.NotContains(t, in, ".filex-e2e.json", "the key file is never listed: %s", raw)
	require.Contains(t, in, "rapor.pdf", raw)
	assert.True(t, in["rapor.pdf"].Encrypted, "a file in an encrypted folder: %s", raw)
	assert.Equal(t, "depo://kasa", in["rapor.pdf"].E2eRoot, raw)
	require.Contains(t, in, "alt", raw)
	assert.True(t, in["alt"].Encrypted, "a folder inside one: %s", raw)
	assert.Equal(t, "depo://kasa", in["alt"].E2eRoot, raw)

	code, _, raw = f.rest(t, http.MethodGet, "/api/ai/files?path="+url.QueryEscape("depo://"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	top := entriesOf(t, []byte(raw))
	require.Contains(t, top, "kasa", raw)
	assert.True(t, top["kasa"].Encrypted, "the encrypted folder itself: %s", raw)
	assert.Equal(t, "depo://kasa", top["kasa"].E2eRoot, raw)
	require.Contains(t, top, "docs", raw)
	assert.False(t, top["docs"].Encrypted, "an ordinary folder: %s", raw)
	assert.Empty(t, top["docs"].E2eRoot, raw)

	code, _, raw = f.rest(t, http.MethodGet, "/api/ai/files?path="+url.QueryEscape("depo://docs"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	docs := entriesOf(t, []byte(raw))
	assert.True(t, docs["tek.fxe"].Encrypted, "a single encrypted file: %s", raw)
	assert.Empty(t, docs["tek.fxe"].E2eRoot, "a .fxe carries its own key slots: %s", raw)
	assert.False(t, docs["a.txt"].Encrypted, raw)

	// A folder encrypted seconds ago: its key file is on the storage but not
	// in the catalogue yet. The listing itself shows it, as the explorer's
	// cold-cache listing does (e2eMarkerAmong).
	require.NoError(t, os.MkdirAll(filepath.Join(f.depoRoot, "yeni-kasa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.depoRoot, "yeni-kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.depoRoot, "yeni-kasa", "x.bin"), []byte("filexe2e"), 0o644))
	code, _, raw = f.rest(t, http.MethodGet, "/api/ai/files?path="+url.QueryEscape("depo://yeni-kasa"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	cold := entriesOf(t, []byte(raw))
	assert.NotContains(t, cold, ".filex-e2e.json", raw)
	assert.True(t, cold["x.bin"].Encrypted, raw)
	assert.Equal(t, "depo://yeni-kasa", cold["x.bin"].E2eRoot, raw)

	code, body, raw := f.rest(t, http.MethodGet, "/api/ai/info?path="+url.QueryEscape("depo://kasa/alt/derin.txt"), nil)
	require.Equal(t, http.StatusOK, code, raw)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, true, entry["encrypted"], raw)
	assert.Equal(t, "depo://kasa", entry["e2e_root"], raw)

	// Search, both doors: the rows carry the marks, the key file is no hit.
	code, _, raw = f.rest(t, http.MethodGet, "/api/ai/search?path="+url.QueryEscape("depo://")+"&q=rapor", nil)
	require.Equal(t, http.StatusOK, code, raw)
	hits := entriesOf(t, []byte(raw))
	require.Contains(t, hits, "rapor.pdf", raw)
	assert.True(t, hits["rapor.pdf"].Encrypted, raw)
	code, _, raw = f.rest(t, http.MethodGet, "/api/ai/search?path="+url.QueryEscape("depo://")+"&q=filex-e2e", nil)
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotContains(t, entriesOf(t, []byte(raw)), ".filex-e2e.json", raw)

	// MCP: the same rows, read from the structured answer.
	res := f.tool(t, "file_list", map[string]any{"path": "depo://kasa"})
	require.False(t, res.IsError, res.Raw)
	mcpIn := entriesOf(t, res.Structured)
	assert.NotContains(t, mcpIn, ".filex-e2e.json", res.Raw)
	assert.True(t, mcpIn["rapor.pdf"].Encrypted, res.Raw)
	assert.Equal(t, "depo://kasa", mcpIn["rapor.pdf"].E2eRoot, res.Raw)

	res = f.tool(t, "file_info", map[string]any{"path": "depo://docs/tek.fxe"})
	require.False(t, res.IsError, res.Raw)
	var info struct {
		Entry e2eAIEntry `json:"entry"`
	}
	require.NoError(t, json.Unmarshal(res.Structured, &info), res.Raw)
	assert.True(t, info.Entry.Encrypted, res.Raw)

	res = f.tool(t, "file_search", map[string]any{"path": "depo://", "query": "rapor", "content": false})
	require.False(t, res.IsError, res.Raw)
	found := entriesOf(t, res.Structured)
	require.Contains(t, found, "rapor.pdf", res.Raw)
	assert.True(t, found["rapor.pdf"].Encrypted, res.Raw)
	assert.Equal(t, "depo://kasa", found["rapor.pdf"].E2eRoot, res.Raw)
	res = f.tool(t, "file_search", map[string]any{"path": "depo://", "query": "filex-e2e", "content": false})
	require.False(t, res.IsError, res.Raw)
	assert.NotContains(t, entriesOf(t, res.Structured), ".filex-e2e.json", res.Raw)
}

// TestAIE2E_ReadingCiphertextIsRefused - file_read and GET /api/ai/download
// answer E2E_ENCRYPTED for an encrypted file instead of its ciphertext.
func TestAIE2E_ReadingCiphertextIsRefused(t *testing.T) {
	f := newE2eAIFixture(t)

	for _, p := range []string{"depo://kasa/rapor.pdf", "depo://kasa/alt/derin.txt", "depo://docs/tek.fxe"} {
		code, body, raw := f.rest(t, http.MethodGet, "/api/ai/download?path="+url.QueryEscape(p), nil)
		assert.Equal(t, http.StatusConflict, code, "%s: %s", p, raw)
		assert.Equal(t, "E2E_ENCRYPTED", body["code"], "%s: %s", p, raw)
		assert.NotContains(t, raw, "filexe2e", "%s: no ciphertext in the answer", p)
		assert.NotContains(t, raw, "filexfxe", "%s: no ciphertext in the answer", p)

		res := f.tool(t, "file_read", map[string]any{"path": p})
		assert.True(t, res.IsError, "%s: %s", p, res.Raw)
		assert.True(t, strings.HasPrefix(res.Text, "E2E_ENCRYPTED: "), "%s: the code leads the text: %q", p, res.Text)
		assert.Contains(t, res.Text, "filex decrypt", "%s: says where it can be read", p)
		assert.NotContains(t, res.Raw, `"encoding":"base64"`, "%s: %s", p, res.Raw)
	}

	// An archive that is ciphertext is not opened either.
	code, body, raw := f.rest(t, http.MethodPost, "/api/ai/unzip", map[string]any{"src": "depo://kasa/rapor.pdf", "dest": "depo://docs/acilan"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_ENCRYPTED", body["code"], raw)

	// An ordinary file is read as before.
	res := f.tool(t, "file_read", map[string]any{"path": "depo://docs/a.txt"})
	require.False(t, res.IsError, res.Raw)
	var read struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(res.Structured, &read), res.Raw)
	assert.Equal(t, "content of a.txt", read.Content)
}

// TestAIE2E_TheKeyFileIsNeverWrittenFromHere - the key file is not deleted,
// moved, overwritten or planted through the AI surface (syspath.Keyless).
func TestAIE2E_TheKeyFileIsNeverWrittenFromHere(t *testing.T) {
	f := newE2eAIFixture(t)
	const key = "depo://kasa/.filex-e2e.json"

	res := f.tool(t, "file_delete", map[string]any{"path": key})
	assert.True(t, res.IsError, res.Raw)
	assert.True(t, strings.HasPrefix(res.Text, "RESERVED_NAME: "), "%q", res.Text)
	assert.Contains(t, res.Text, "key file", res.Raw)
	assert.True(t, f.onDisk("kasa/.filex-e2e.json"), "the key file must still be there")

	code, body, raw := f.rest(t, http.MethodPost, "/api/ai/delete", map[string]any{"path": key})
	assert.Equal(t, http.StatusForbidden, code, raw)
	assert.Equal(t, "RESERVED_NAME", body["code"], raw)

	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/move", map[string]any{"src": key, "dst": "depo://docs/anahtar.json"})
	assert.Equal(t, http.StatusForbidden, code, "moving it out: %s", raw)
	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/upload", map[string]any{"path": key, "content": "{}", "allow_plaintext": true})
	assert.Equal(t, http.StatusForbidden, code, "overwriting it, allow_plaintext or not: %s", raw)
	assert.True(t, f.onDisk("kasa/.filex-e2e.json"))

	// Planting one turns an ordinary folder into a "locked" one.
	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/upload", map[string]any{"path": "depo://docs/.filex-e2e.json", "content": "{}"})
	assert.Equal(t, http.StatusForbidden, code, "planting one by upload: %s", raw)
	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/move", map[string]any{"src": "depo://docs/a.txt", "dst": "depo://docs/.filex-e2e.json"})
	assert.Equal(t, http.StatusForbidden, code, "planting one by rename: %s", raw)
	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/upload/ticket", map[string]any{"path": "depo://docs/.filex-e2e.json"})
	assert.Equal(t, http.StatusForbidden, code, "planting one by ticket, refused at mint: %s", raw)
	assert.False(t, f.onDisk("docs/.filex-e2e.json"))
	assert.True(t, f.onDisk("docs/a.txt"))

	// Nor by extracting an archive that carries one: that member is skipped,
	// the rest lands.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"not.txt", ".filex-e2e.json"} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte("{}"))
	}
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(f.depoRoot, "docs", "anahtarli.zip"), buf.Bytes(), 0o644))
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/unzip", map[string]any{"src": "depo://docs/anahtarli.zip", "dest": "depo://docs/acilan"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.Equal(t, float64(1), body["extracted"], raw)
	assert.True(t, f.onDisk("docs/acilan/not.txt"))
	assert.False(t, f.onDisk("docs/acilan/.filex-e2e.json"), "an archive cannot plant a key file")
}

// TestAIE2E_PlaintextIntoAnEncryptedFolderNeedsConsent - every write door of
// the surface refuses E2E_PLAINTEXT_REFUSED by default and takes
// allow_plaintext as the caller's deliberate choice.
func TestAIE2E_PlaintextIntoAnEncryptedFolderNeedsConsent(t *testing.T) {
	f := newE2eAIFixture(t)

	// REST upload, JSON.
	code, body, raw := f.rest(t, http.MethodPost, "/api/ai/upload", map[string]any{"path": "depo://kasa/duz-metin.txt", "content": "gizli degil"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_PLAINTEXT_REFUSED", body["code"], raw)
	assert.Contains(t, raw, "allow_plaintext", "the refusal names the way through: %s", raw)
	assert.False(t, f.onDisk("kasa/duz-metin.txt"))

	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/upload", map[string]any{"path": "depo://kasa/alt/duz-metin.txt", "content": "bilerek", "allow_plaintext": true})
	require.Equal(t, http.StatusOK, code, raw)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "depo://kasa", entry["e2e_root"], raw)
	assert.True(t, f.onDisk("kasa/alt/duz-metin.txt"))

	// REST upload, multipart: the flag is a form field.
	multi := func(allow bool) (int, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		require.NoError(t, mw.WriteField("path", "depo://kasa/coklu.txt"))
		if allow {
			require.NoError(t, mw.WriteField("allow_plaintext", "true"))
		}
		fw, err := mw.CreateFormFile("file", "coklu.txt")
		require.NoError(t, err)
		_, _ = fw.Write([]byte("x"))
		require.NoError(t, mw.Close())
		return fxReq(t, http.MethodPost, f.srv.URL+"/api/ai/upload", f.tok, &buf, mw.FormDataContentType())
	}
	code, raw = multi(false)
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Contains(t, raw, "E2E_PLAINTEXT_REFUSED", raw)
	code, raw = multi(true)
	assert.Equal(t, http.StatusOK, code, raw)

	// MCP file_write.
	res := f.tool(t, "file_write", map[string]any{"path": "depo://kasa/mcp.txt", "content": "x"})
	assert.True(t, res.IsError, res.Raw)
	assert.True(t, strings.HasPrefix(res.Text, "E2E_PLAINTEXT_REFUSED: "), "%q", res.Text)
	assert.False(t, f.onDisk("kasa/mcp.txt"))
	res = f.tool(t, "file_write", map[string]any{"path": "depo://kasa/mcp.txt", "content": "x", "allow_plaintext": true})
	require.False(t, res.IsError, res.Raw)
	var wrote struct {
		Entry e2eAIEntry `json:"entry"`
	}
	require.NoError(t, json.Unmarshal(res.Structured, &wrote), res.Raw)
	assert.Equal(t, "depo://kasa", wrote.Entry.E2eRoot, res.Raw)
	assert.True(t, f.onDisk("kasa/mcp.txt"))

	// Upload ticket: refused at mint, and the consent travels to the redeem.
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/upload/ticket", map[string]any{"path": "depo://kasa/buyuk.bin"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_PLAINTEXT_REFUSED", body["code"], raw)
	res = f.tool(t, "file_upload_ticket", map[string]any{"path": "depo://kasa/buyuk.bin"})
	assert.True(t, strings.HasPrefix(res.Text, "E2E_PLAINTEXT_REFUSED: "), "%q", res.Text)
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/upload/ticket", map[string]any{"path": "depo://kasa/buyuk.bin", "allow_plaintext": true})
	require.Equal(t, http.StatusOK, code, raw)
	ticket, _ := body["ticket"].(string)
	require.NotEmpty(t, ticket, raw)
	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/u/"+ticket, strings.NewReader("buyuk"))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the redeem carries the minter's consent")
	assert.True(t, f.onDisk("kasa/buyuk.bin"))

	// Zip into it, unzip into it.
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/zip", map[string]any{"sources": []string{"depo://docs/a.txt"}, "dest": "depo://kasa/arsiv.zip"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_PLAINTEXT_REFUSED", body["code"], raw)
	assert.False(t, f.onDisk("kasa/arsiv.zip"))
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/zip", map[string]any{"sources": []string{"depo://docs/a.txt"}, "dest": "depo://kasa/arsiv.zip", "allow_plaintext": true})
	require.Equal(t, http.StatusOK, code, raw)
	entry, _ = body["entry"].(map[string]any)
	assert.Equal(t, "depo://kasa", entry["e2e_root"], raw)

	// A folder and a move inside the encrypted folder write no content: they
	// are not refused, and their answers say where they are.
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/mkdir", map[string]any{"path": "depo://kasa/yeni"})
	require.Equal(t, http.StatusOK, code, raw)
	entry, _ = body["entry"].(map[string]any)
	assert.Equal(t, true, entry["encrypted"], raw)
	assert.Equal(t, "depo://kasa", entry["e2e_root"], raw)
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/move", map[string]any{"src": "depo://kasa/rapor.pdf", "dst": "depo://kasa/alt/rapor.pdf"})
	require.Equal(t, http.StatusOK, code, raw)
	entry, _ = body["entry"].(map[string]any)
	assert.Equal(t, "depo://kasa", entry["e2e_root"], raw)

	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/zip", map[string]any{"sources": []string{"depo://docs/a.txt", "depo://docs/b.txt"}, "dest": "depo://docs/plain.zip"})
	require.Equal(t, http.StatusOK, code, raw)
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/unzip", map[string]any{"src": "depo://docs/plain.zip", "dest": "depo://kasa/acilan"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_PLAINTEXT_REFUSED", body["code"], raw)
	assert.False(t, f.onDisk("kasa/acilan/a.txt"))
	res = f.tool(t, "file_unzip", map[string]any{"src": "depo://docs/plain.zip", "dest_dir": "depo://kasa/acilan"})
	assert.True(t, strings.HasPrefix(res.Text, "E2E_PLAINTEXT_REFUSED: "), "%q", res.Text)
	res = f.tool(t, "file_unzip", map[string]any{"src": "depo://docs/plain.zip", "dest_dir": "depo://kasa/acilan", "allow_plaintext": true})
	require.False(t, res.IsError, res.Raw)
	var unz struct {
		Extracted int `json:"extracted"`
	}
	require.NoError(t, json.Unmarshal(res.Structured, &unz), res.Raw)
	assert.Equal(t, 2, unz.Extracted, res.Raw)

	// A ticket minted for an ordinary folder that is encrypted before the
	// upload arrives: the redeem asks again, and says so.
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/upload/ticket", map[string]any{"path": "depo://docs/sonra.bin"})
	require.Equal(t, http.StatusOK, code, raw)
	later, _ := body["ticket"].(string)
	require.NoError(t, os.WriteFile(filepath.Join(f.depoRoot, "docs", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644))
	docsRow, err := f.store.GetNodeByPath(context.Background(), f.depo.ID, pathkey.Hash(f.depo.ID, "/docs"))
	require.NoError(t, err)
	_, err = f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.depo.ID, ParentID: &docsRow.ID, Name: ".filex-e2e.json", Path: "/docs/.filex-e2e.json",
		Type: model.NodeTypeFile, Size: 7, Etag: "m", PathHash: pathkey.Hash(f.depo.ID, "/docs/.filex-e2e.json"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	req, err = http.NewRequest(http.MethodPut, f.srv.URL+"/u/"+later, strings.NewReader("sonra"))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	var refused map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&refused))
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode, "%v", refused)
	assert.Equal(t, "e2e_plaintext_refused", refused["error"], "%v", refused)
	assert.Contains(t, refused["hint"], "allow_plaintext", "%v", refused)
	assert.False(t, f.onDisk("docs/sonra.bin"))
}

// TestAIE2E_UnzipChecksWhereEveryMemberLands - an ordinary destination with
// an encrypted folder under it: a member that would land in that folder is
// refused before anything is written.
func TestAIE2E_UnzipChecksWhereEveryMemberLands(t *testing.T) {
	f := newE2eAIFixture(t)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"serbest.txt", "kasa/sizan.txt"} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte("duz metin"))
	}
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(f.depoRoot, "docs", "karisik.zip"), buf.Bytes(), 0o644))

	code, body, raw := f.rest(t, http.MethodPost, "/api/ai/unzip", map[string]any{"src": "depo://docs/karisik.zip", "dest": "depo://"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_PLAINTEXT_REFUSED", body["code"], raw)
	assert.False(t, f.onDisk("kasa/sizan.txt"), "the member that would land in kasa/")
	assert.False(t, f.onDisk("serbest.txt"), "and nothing else: judged before anything is written")
}

// TestAIE2E_ZipAndMoveKeepTheBoundary - packing or moving an encrypted file
// out of its folder is the copy the transfer guard refuses (E2E_BOUNDARY,
// 409: the move used to answer 500), while the encrypted folder itself is
// packed whole, its key file with it.
func TestAIE2E_ZipAndMoveKeepTheBoundary(t *testing.T) {
	f := newE2eAIFixture(t)

	code, body, raw := f.rest(t, http.MethodPost, "/api/ai/zip", map[string]any{"sources": []string{"depo://kasa/rapor.pdf"}, "dest": "depo://docs/disari.zip"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_BOUNDARY", body["code"], raw)
	assert.False(t, f.onDisk("docs/disari.zip"))
	res := f.tool(t, "file_zip", map[string]any{"sources": []string{"depo://kasa/alt"}, "dest": "depo://docs/disari.zip"})
	assert.True(t, strings.HasPrefix(res.Text, "E2E_BOUNDARY: "), "%q", res.Text)

	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/move", map[string]any{"src": "depo://kasa/rapor.pdf", "dst": "depo://docs/rapor.pdf"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_BOUNDARY", body["code"], raw)
	assert.True(t, f.onDisk("kasa/rapor.pdf"))

	code, _, raw = f.rest(t, http.MethodPost, "/api/ai/zip", map[string]any{"sources": []string{"depo://kasa"}, "dest": "depo://docs/kasa.zip"})
	require.Equal(t, http.StatusOK, code, raw)
	b, err := os.ReadFile(filepath.Join(f.depoRoot, "docs", "kasa.zip"))
	require.NoError(t, err)
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	require.NoError(t, err)
	names := map[string]bool{}
	for _, m := range zr.File {
		names[m.Name] = true
	}
	assert.True(t, names["kasa/.filex-e2e.json"], "the key file travels with its folder (filex decrypt reads it): %v", names)
	assert.True(t, names["kasa/rapor.pdf"], "%v", names)
}

// TestAIE2E_NoPublicLinkIntoAnEncryptedFolder - one rule on every door
// (publicLinkRefusal): an encrypted folder and anything in it is never
// linked, a single encrypted file is, and says so.
func TestAIE2E_NoPublicLinkIntoAnEncryptedFolder(t *testing.T) {
	f := newE2eAIFixture(t)
	ctx := context.Background()

	for _, p := range []string{"depo://kasa/rapor.pdf", "depo://kasa"} {
		code, body, raw := f.rest(t, http.MethodPost, "/api/files/share", map[string]any{"path": p})
		assert.Equal(t, http.StatusConflict, code, "explorer, %s: %s", p, raw)
		assert.Equal(t, "E2E_ENCRYPTED", body["code"], raw)

		code, body, raw = f.rest(t, http.MethodPost, "/api/ai/share", map[string]any{"path": p})
		assert.Equal(t, http.StatusConflict, code, "/api/ai/share, %s: %s", p, raw)
		assert.Equal(t, "E2E_ENCRYPTED", body["code"], raw)

		res := f.tool(t, "file_share", map[string]any{"path": p})
		assert.True(t, res.IsError, res.Raw)
		assert.True(t, strings.HasPrefix(res.Text, "E2E_ENCRYPTED: "), "%q", res.Text)
		assert.NotContains(t, res.Raw, "/s/", res.Raw)
	}
	// A file request on the folder would store a visitor's upload in it
	// unencrypted.
	code, body, raw := f.rest(t, http.MethodPost, "/api/files/share", map[string]any{"path": "depo://kasa", "kind": "drop"})
	assert.Equal(t, http.StatusConflict, code, raw)
	assert.Equal(t, "E2E_ENCRYPTED", body["code"], raw)

	for _, rel := range []string{"/kasa", "/kasa/rapor.pdf"} {
		n, err := f.store.GetNodeByPath(ctx, f.depo.ID, pathkey.Hash(f.depo.ID, rel))
		require.NoError(t, err)
		links, err := f.store.ListSharesByNode(ctx, n.ID)
		require.NoError(t, err)
		assert.Empty(t, links, "no link was minted for %s", rel)
	}

	// A .fxe carries its own key slots: linked, as it is, on every door.
	code, _, raw = f.rest(t, http.MethodPost, "/api/files/share", map[string]any{"path": "depo://docs/tek.fxe"})
	assert.Equal(t, http.StatusOK, code, raw)
	code, body, raw = f.rest(t, http.MethodPost, "/api/ai/share", map[string]any{"path": "depo://docs/tek.fxe"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.Equal(t, true, body["encrypted"], "the agent is told its recipient needs the password: %s", raw)
	res := f.tool(t, "file_share", map[string]any{"path": "depo://docs/tek.fxe"})
	require.False(t, res.IsError, res.Raw)
	var link struct {
		URL       string `json:"url"`
		Encrypted bool   `json:"encrypted"`
	}
	require.NoError(t, json.Unmarshal(res.Structured, &link), res.Raw)
	assert.Contains(t, link.URL, "/s/", res.Raw)
	assert.True(t, link.Encrypted, res.Raw)
}

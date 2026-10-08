package handlers_test

// The account's ceiling (users.quota_bytes) is asked before the bytes land at
// every single-file write door, not only the explorer's upload: the agent
// API's write funnel (/api/ai/upload, MCP file_write, ShareX, /u/{ticket} all
// reach aiOps.WriteStream), the text editor's save, and the staged ingest the
// large ones go through. Each refusal is the explorer upload's answer, and
// nothing is written.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/quota"
)

func setQuota(t *testing.T, f *stagedFixture, n int64) {
	t.Helper()
	require.NoError(t, quota.New(f.store).SetQuota(context.Background(), f.userID, n))
}

func tokenPost(t *testing.T, f *stagedFixture, tok, url, ct string, body []byte) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestWriteQuota_TheAgentUploadAsksTheCeiling(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)
	setQuota(t, f, 100)

	b, _ := json.Marshal(map[string]any{"path": "main://over.txt", "content": strings.Repeat("o", 500)})
	code, out := tokenPost(t, f, tok, f.srv.URL+"/api/ai/upload", "application/json", b)
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	assert.Equal(t, "QUOTA_EXCEEDED", out["code"], "%v", out)
	assert.NoFileExists(t, filepath.Join(f.rootDir, "over.txt"))

	b, _ = json.Marshal(map[string]any{"path": "main://fits.txt", "content": "a short note"})
	code, out = tokenPost(t, f, tok, f.srv.URL+"/api/ai/upload", "application/json", b)
	require.Equal(t, http.StatusOK, code, "a write within the ceiling: %v", out)
}

func TestWriteQuota_ShareXAsksTheCeiling(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)
	setQuota(t, f, 100)

	body, ct := multipartBody(t, "file", "shot.png", bytes.Repeat([]byte("s"), 500), map[string]string{"folder": "sharex"})
	code, out := tokenPost(t, f, tok, f.srv.URL+"/api/sharex/upload", ct, body.Bytes())
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	entries, _ := os.ReadDir(filepath.Join(f.rootDir, "sharex"))
	assert.Empty(t, entries, "a capture over the ceiling was written")
}

// The ticket's upload is billed to whoever minted it.
func TestWriteQuota_AnUploadTicketAsksTheMintersCeiling(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)
	b, _ := json.Marshal(map[string]any{"path": "main://ticketed.bin"})
	code, out := tokenPost(t, f, tok, f.srv.URL+"/api/ai/upload/ticket", "application/json", b)
	require.Equal(t, http.StatusOK, code, "%v", out)
	ticket, _ := out["ticket"].(string)
	require.NotEmpty(t, ticket, "%v", out)
	setQuota(t, f, 100)

	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/u/"+ticket, bytes.NewReader(bytes.Repeat([]byte("u"), 500)))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	got := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusInsufficientStorage, resp.StatusCode, "%v", got)
	assert.Equal(t, "quota_exceeded", got["error"], "%v", got)
	assert.NoFileExists(t, filepath.Join(f.rootDir, "ticketed.bin"))
}

// The text editor: a save over the ceiling is refused, and an edit that does
// not grow the file still saves for an account that is exactly at it.
func TestWriteQuota_TheTextEditorAsksTheCeiling(t *testing.T) {
	f := newSurfaceFixture(t)
	save := func(content string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]any{"path": "main://notes.txt", "content": content})
		resp, err := f.client.Post(f.srv.URL+"/api/files/save-text", "application/json", bytes.NewReader(b))
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, out := save(strings.Repeat("a", 100))
	require.Equal(t, http.StatusOK, code, "%v", out)
	used, _, err := f.store.GetUserUsage(context.Background(), f.userID)
	require.NoError(t, err)
	setQuota(t, f, used)

	code, out = save(strings.Repeat("b", 100))
	require.Equal(t, http.StatusOK, code, "an edit that does not grow the file, at the ceiling: %v", out)

	code, out = save(strings.Repeat("c", 150))
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	assert.Equal(t, "QUOTA_EXCEEDED", out["code"], "%v", out)
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("b", 100), string(onDisk), "the refused save was written")
}

// A large agent upload goes through the staged ingest (IngestStream), which
// counts what the account still has open in staging, as the resumable
// upload's begin does: a reservation and the new file together are over the
// ceiling even though the new file alone is not.
func TestWriteQuota_AStagedIngestCountsWhatIsStillOpen(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)
	setQuota(t, f, 10000)

	code, begun := f.begin(t, map[string]any{"path": "main://", "name": "reserved.bin", "size": 8000})
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, code, "%v", begun)

	payload := randomBytes(stagedSurfaceThreshold + 100)
	body, ct := multipartBody(t, "file", "agent.bin", payload, map[string]string{"path": "main://agent.bin"})
	code, out := tokenPost(t, f, tok, f.srv.URL+"/api/ai/upload", ct, body.Bytes())
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	assert.Equal(t, "QUOTA_EXCEEDED", out["code"], "%v", out)
	assert.Nil(t, f.nodeAt(t, "/agent.bin"), "a staged node was published over the ceiling")
}

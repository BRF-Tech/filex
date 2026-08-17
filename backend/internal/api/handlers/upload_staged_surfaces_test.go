package handlers_test

// The whole-body surfaces on the staged path: the public drop link, the AI /
// REST write and ShareX.
//
// None of them can chunk — each is one request carrying the whole file — but
// each still gets the second half of the staged contract: the bytes land in
// filex's own staging area, the node is created and listed at once, and the
// driver write happens afterwards in the ops worker. That is what stops a slow
// backend from holding the request open, and it is one shared helper
// (StagedUpload.IngestStream) rather than three copies.
//
// Assertions are on effects, not on calls: a node that exists, a transfer_state
// that flips, and — the one that actually matters — the bytes on disk matching
// the bytes sent.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// stagedSurfaceThreshold is the chunk size the fixtures below run with, so a
// few KB counts as "large" and the tests stay fast.
const stagedSurfaceThreshold = 4096

// newSurfaceFixture is the staged fixture with a small chunk size, which is
// also the staging threshold for the whole-body surfaces.
func newSurfaceFixture(t *testing.T) *stagedFixture {
	t.Helper()
	return newStagedFixtureWith(t, func(d *api.Deps) {
		d.Cfg.Upload.ChunkSize = stagedSurfaceThreshold
	})
}

// multipartBody builds a one-file multipart body with optional extra fields.
func multipartBody(t *testing.T, field, filename string, content []byte, extra map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range extra {
		require.NoError(t, mw.WriteField(k, v))
	}
	part, err := mw.CreateFormFile(field, filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return &body, mw.FormDataContentType()
}

// waitForStored polls until the node's bytes are on the driver. The staged path
// answers before the transfer, which is the point — so a test that wants the
// final file has to wait for it rather than assume.
//
// ⚠ It waits for the SIZE, not merely for the file to exist: the local driver
// creates the file and then fills it, so an existence check alone reads a
// half-written file and produces a checksum mismatch that has nothing to do
// with the code under test.
func waitForStored(t *testing.T, f *stagedFixture, rel string, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	full := filepath.Join(f.rootDir, filepath.FromSlash(rel))
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(full); err == nil && fi.Size() == int64(want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never reached the driver at %d bytes", rel, want)
}

// ── the public drop link ────────────────────────────────────────────────────

// A large drop is answered as soon as filex holds the bytes; the node is
// listable immediately, and the transfer lands afterwards with the right bytes.
func TestDropLink_LargeUpload_GoesThroughStaging(t *testing.T) {
	f := newSurfaceFixture(t)

	// A folder to drop into, and a drop link pointing at it.
	require.Equal(t, http.StatusOK, f.newFolder(t, "main://", "inbox"))
	token := f.createDropLink(t, "main://inbox")

	payload := randomBytes(stagedSurfaceThreshold*3 + 111)
	body, ct := multipartBody(t, "file[]", "capture.bin", payload, map[string]string{"uploader_name": "ada"})
	resp, err := f.client.Post(f.srv.URL+"/d/"+token, ct, body)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)

	sub, _ := out["folder"].(string)
	require.NotEmpty(t, sub)
	rel := "inbox/" + sub + "/capture.bin"

	// Listed straight away — before a byte has reached the driver.
	node := f.nodeAt(t, "/"+rel)
	require.NotNil(t, node, "a staged drop must be listed immediately")
	assert.EqualValues(t, len(payload), node.Size)

	waitForStored(t, f, rel, len(payload))
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(payload), sha256Hex(onDisk), "the transferred bytes must be the bytes sent")

	// And the staging row is gone once the transfer succeeded.
	assert.Eventually(t, func() bool {
		rows, _ := f.store.ListIdleStagedUploads(context.Background(), time.Now().Add(time.Hour), 100)
		return len(rows) == 0
	}, 10*time.Second, 50*time.Millisecond, "a completed transfer must delete its staging row")
}

// A small drop keeps the synchronous write: staging a 5-byte note would cost a
// background op and buy nothing.
func TestDropLink_SmallUpload_StaysSynchronous(t *testing.T) {
	f := newSurfaceFixture(t)
	require.Equal(t, http.StatusOK, f.newFolder(t, "main://", "inbox"))
	token := f.createDropLink(t, "main://inbox")

	body, ct := multipartBody(t, "file[]", "note.txt", []byte("hello"), nil)
	resp, err := f.client.Post(f.srv.URL+"/d/"+token, ct, body)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	sub, _ := out["folder"].(string)
	rel := "inbox/" + sub + "/note.txt"
	// Already on the driver when the response came back — no waiting.
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(onDisk))

	node := f.nodeAt(t, "/"+rel)
	require.NotNil(t, node)
	assert.NotEqual(t, model.TransferStateStaged, node.TransferState)
}

// ── the AI / REST surface ───────────────────────────────────────────────────

// A large multipart agent upload is streamed into staging — it never sits in
// memory, and the reply does not wait on the driver.
func TestAIUpload_LargeMultipart_GoesThroughStaging(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)

	payload := randomBytes(stagedSurfaceThreshold*2 + 77)
	body, ct := multipartBody(t, "file", "agent.bin", payload, map[string]string{"path": "main://agent.bin"})
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/ai/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)

	node := f.nodeAt(t, "/agent.bin")
	require.NotNil(t, node, "a staged agent upload must be listed immediately")
	assert.EqualValues(t, len(payload), node.Size)

	waitForStored(t, f, "agent.bin", len(payload))
	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "agent.bin"))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(payload), sha256Hex(onDisk))
}

// A small write stays synchronous — most agent writes really are a note or a
// JSON blob, and a background op would buy nothing. The threshold is the same
// number on every surface, so there is one rule to remember rather than six.
func TestAIUpload_SmallJSONBody_StaysSynchronous(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)

	payload := []byte("just a note")
	b, _ := json.Marshal(map[string]any{"path": "main://note.txt", "content": string(payload)})
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/ai/upload", bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Filex-Token", tok)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	onDisk, err := os.ReadFile(filepath.Join(f.rootDir, "note.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(payload), string(onDisk))
}

// ── ShareX ──────────────────────────────────────────────────────────────────

// A big capture (a screen recording is a capture too) goes through staging and
// still gets its public inline link back in the same response.
func TestShareXUpload_LargeCapture_GoesThroughStagingAndStillShares(t *testing.T) {
	f := newSurfaceFixture(t)
	tok := f.issueAIToken(t)

	payload := randomBytes(stagedSurfaceThreshold*2 + 5)
	body, ct := multipartBody(t, "file", "clip.bin", payload, map[string]string{"folder": "sharex"})
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/sharex/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)
	// The share is minted against the node the staged commit published, so the
	// link exists even though the bytes are still moving.
	assert.Contains(t, out["url"], "/s/")
	assert.Contains(t, out["url"], "inline=1")

	// Find the capture (its name carries a random prefix) and wait for it.
	var stored string
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(filepath.Join(f.rootDir, "sharex"))
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, rerr := os.ReadFile(filepath.Join(f.rootDir, "sharex", e.Name()))
			if rerr == nil && len(b) == len(payload) {
				stored = sha256Hex(b)
				return true
			}
		}
		return false
	}, 15*time.Second, 25*time.Millisecond, "the capture never reached the driver")
	assert.Equal(t, sha256Hex(payload), stored)
}

// ── fixture helpers ─────────────────────────────────────────────────────────

// newFolder creates a directory through the authenticated manager API.
func (f *stagedFixture) newFolder(t *testing.T, parent, name string) int {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"path": parent, "name": name})
	resp, err := f.client.Post(f.srv.URL+"/api/files/manager?action=newfolder", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// createDropLink mints a public /d/{token} upload link on a folder.
func (f *stagedFixture) createDropLink(t *testing.T, path string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"path": path, "kind": "drop"})
	resp, err := f.client.Post(f.srv.URL+"/api/files/share", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", out)
	if sh, ok := out["share"].(map[string]any); ok {
		if tok, ok := sh["token"].(string); ok && tok != "" {
			return tok
		}
	}
	tok, _ := out["token"].(string)
	require.NotEmpty(t, tok, "no drop token in %v", out)
	return tok
}

// issueAIToken seeds an api_tokens row for the fixture's admin.
func (f *stagedFixture) issueAIToken(t *testing.T) string {
	t.Helper()
	plain := fmt.Sprintf("tok_staged_%d", time.Now().UnixNano())
	_, err := f.store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    f.userID,
		Label:     "staged-surfaces",
		TokenHash: apitoken.HashToken(plain),
	})
	require.NoError(t, err)
	return plain
}

// nodeAt looks up the DB node for a storage-relative path.
func (f *stagedFixture) nodeAt(t *testing.T, dbPath string) *model.Node {
	t.Helper()
	n, err := f.store.GetNodeByPath(context.Background(), f.storage.ID, pathkey.Hash(f.storage.ID, dbPath))
	if err != nil {
		return nil
	}
	return n
}

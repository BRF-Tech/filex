package handlers_test

// Chunk 7 — "count what users actually store".
//
// Every test here drives a REAL HTTP surface against a REAL local storage
// driver and then reads `users.usage_bytes` back out of the database. That is
// the bar §5 trap 7 sets: assert the bytes counted, not that a helper was
// called. Before this change `quota.AddUsage` and `Store.SetNodeOwner` had no
// callers anywhere in the tree, so a test asserting "the quota service was
// invoked" would have been green all along while the number stayed zero.
//
// Surfaces exercised: staged upload (begin/put/commit), the classic multipart
// upload, save-text, WebDAV PUT, the public file drop, the AI/REST upload and
// the ShareX capture — plus delete → purge for the release. None of them is
// instrumented individually; they all reach internal/quotastore through the
// store, and that is exactly the property under test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/trash"
)

// usage reads the fixture admin's usage_bytes straight from the DB.
func (f *stagedFixture) usage(t *testing.T) int64 {
	t.Helper()
	used, _, err := f.store.GetUserUsage(context.Background(), f.userID)
	require.NoError(t, err)
	return used
}

// ── the write paths ─────────────────────────────────────────────────────────

func TestQuota_StagedUpload_IsCounted(t *testing.T) {
	f := newStagedFixture(t)
	require.EqualValues(t, 0, f.usage(t))

	body := bytes.Repeat([]byte("s"), 3000)
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "staged.bin", "size": len(body), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, out)
	id := out["id"].(string)
	code, _ = f.putChunk(t, id, 0, int64(len(body)), int64(len(body)), body)
	require.Equal(t, http.StatusOK, code)
	code, out = f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, out)
	f.waitForOp(t, int64(out["op_id"].(float64)))

	assert.EqualValues(t, len(body), f.usage(t))
}

func TestQuota_MultipartUpload_IsCounted(t *testing.T) {
	f := newStagedFixture(t)
	f.uploadMultipart(t, "plain.bin", bytes.Repeat([]byte("m"), 2048))
	assert.EqualValues(t, 2048, f.usage(t))
}

func TestQuota_SaveText_IsCounted(t *testing.T) {
	f := newStagedFixture(t)
	body := strings.Repeat("x", 512)
	require.Equal(t, http.StatusOK, f.saveText(t, "main://note.txt", body))
	assert.EqualValues(t, len(body), f.usage(t))
}

func TestQuota_WebDAVPut_IsCounted(t *testing.T) {
	f := newStagedFixture(t)
	body := bytes.Repeat([]byte("d"), 1234)

	req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/dav/main/dav.bin", bytes.NewReader(body))
	require.NoError(t, err)
	req.SetBasicAuth(f.adminEml, f.adminPw)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Contains(t, []int{http.StatusCreated, http.StatusNoContent, http.StatusOK}, resp.StatusCode)

	assert.EqualValues(t, len(body), f.usage(t),
		"WebDAV writes the same bytes into the same storage, so it answers to the same ceiling")
}

func TestQuota_AIUpload_IsCounted(t *testing.T) {
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
	require.Equal(t, http.StatusOK, resp.StatusCode)

	waitForStored(t, f, "agent.bin", len(payload))
	assert.EqualValues(t, len(payload), f.usage(t),
		"a token write is a user's write — the token is bound to an account")
}

func TestQuota_ShareX_IsCounted(t *testing.T) {
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
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Eventually(t, func() bool { return f.usage(t) == int64(len(payload)) },
		15*time.Second, 50*time.Millisecond,
		"a capture is stored bytes like any other write")
}

// A public drop link has no logged-in uploader, but the bytes land in the link
// creator's storage — so they are billed to the creator. Without this the drop
// is the one write surface with no owner and therefore no ceiling at all: a way
// to fill somebody's disk for free.
func TestQuota_PublicDrop_IsBilledToTheLinkOwner(t *testing.T) {
	f := newSurfaceFixture(t)
	require.Equal(t, http.StatusOK, f.newFolder(t, "main://", "inbox"))
	token := f.createDropLink(t, "main://inbox")

	payload := randomBytes(777)
	body, ct := multipartBody(t, "file[]", "dropped.bin", payload, map[string]string{"uploader_name": "anon"})

	// A fresh client: the dropper is anonymous, not the logged-in admin.
	resp, err := (&http.Client{}).Post(f.srv.URL+"/d/"+token, ct, body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.EqualValues(t, len(payload), f.usage(t))
}

// Overwriting moves usage by the delta rather than adding the whole object a
// second time.
func TestQuota_Overwrite_MovesByTheDelta(t *testing.T) {
	f := newStagedFixture(t)
	f.uploadMultipart(t, "same.bin", bytes.Repeat([]byte("a"), 1000))
	require.EqualValues(t, 1000, f.usage(t))

	f.uploadMultipart(t, "same.bin", bytes.Repeat([]byte("b"), 1500))
	assert.EqualValues(t, 1500, f.usage(t), "one file on disk, one file's worth of quota")
}

// ── release ─────────────────────────────────────────────────────────────────

// Trash keeps counting; the purge is the only release. This is the rule the
// trash work documented but could never observe, because nothing was counted
// in the first place.
func TestQuota_ReleasedOnlyAtPurge(t *testing.T) {
	f := newStagedFixture(t)
	f.uploadMultipart(t, "doomed.bin", bytes.Repeat([]byte("z"), 4096))
	require.EqualValues(t, 4096, f.usage(t))

	require.Equal(t, http.StatusOK,
		f.mutate(t, "delete", map[string]any{
			"path":  "main://",
			"items": []map[string]string{{"path": "main://doomed.bin"}},
		}))
	assert.EqualValues(t, 4096, f.usage(t), "trashed bytes still occupy the storage")

	svc := trash.New(f.store, f.deps.StorageResolver, f.deps.Quota)
	rows, _, err := f.store.ListTrashed(context.Background(), &f.storage.ID, 100, 0)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "the delete must have produced a trash row")
	require.NoError(t, svc.PurgeOne(context.Background(), rows[0].ID))

	assert.EqualValues(t, 0, f.usage(t), "purge is the release point")
}

// ── helpers for the non-staged surfaces ─────────────────────────────────────

func (f *stagedFixture) uploadMultipart(t *testing.T, name string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", "main://"))
	part, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = part.Write(body)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/manager?action=upload", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(out))
}

func (f *stagedFixture) saveText(t *testing.T, path, content string) int {
	t.Helper()
	buf, _ := json.Marshal(map[string]any{"path": path, "content": content})
	resp, err := f.client.Post(f.srv.URL+"/api/files/save-text", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func (f *stagedFixture) mutate(t *testing.T, action string, body map[string]any) int {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := f.client.Post(
		fmt.Sprintf("%s/api/files/manager?action=%s", f.srv.URL, action),
		"application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Logf("mutate %s → %d %s", action, resp.StatusCode, string(out))
	}
	return resp.StatusCode
}

// uploadMultipartCode is uploadMultipart without the success assertion, for
// the tests that expect a refusal.
func (f *stagedFixture) uploadMultipartCode(t *testing.T, name string, body []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", "main://"))
	part, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = part.Write(body)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/manager?action=upload", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

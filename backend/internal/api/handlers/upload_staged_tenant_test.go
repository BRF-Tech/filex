package handlers_test

// A staged (resumable) upload pins its storage by id at Begin, under the
// tenant scope, and is driven for up to the staging TTL afterwards. Every
// later call asks the caller's tenant again: a storage the platform operator
// moved to another tenant mid-upload takes no more chunks and no commit.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mtStagedBegin(t *testing.T, f *mtFix, c *http.Client, dir, name string, size int64) string {
	t.Helper()
	buf, _ := json.Marshal(map[string]any{"path": dir, "name": name, "size": size})
	resp, err := c.Post(f.URL+"/api/files/upload/begin", "application/json", bytes.NewReader(buf))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, http.StatusOK, resp.StatusCode, "begin: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)
	return id
}

func mtStagedPut(t *testing.T, f *mtFix, c *http.Client, id string, body []byte) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, f.URL+"/api/files/upload/"+id, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func mtStagedCommit(t *testing.T, f *mtFix, c *http.Client, id string) (int, map[string]any) {
	t.Helper()
	resp, err := c.Post(f.URL+"/api/files/upload/"+id+"/commit", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestStagedUploadTenant_AStorageMovedMidUploadTakesNoMore(t *testing.T) {
	payload := []byte("alpha's staged bytes")
	size := int64(len(payload))

	t.Run("control: nothing moved, chunk and commit are taken", func(t *testing.T) {
		f := newMTFix(t, true)
		id := mtStagedBegin(t, f, f.A, "alpha://docs", "kept.bin", size)
		code, out := mtStagedPut(t, f, f.A, id, payload)
		require.Equal(t, http.StatusOK, code, "%v", out)
		code, out = mtStagedCommit(t, f, f.A, id)
		assert.Equal(t, http.StatusAccepted, code, "%v", out)
	})

	t.Run("moved before the chunk", func(t *testing.T) {
		f := newMTFix(t, true)
		ctx := context.Background()
		id := mtStagedBegin(t, f, f.A, "alpha://docs", "moved.bin", size)

		require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvA, f.StA.ID))
		require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvB, f.StA.ID))

		code, out := mtStagedPut(t, f, f.A, id, payload)
		assert.Equal(t, http.StatusNotFound, code, "a chunk for a storage that is bravo's now: %v", out)
		assert.Equal(t, "upload not found", out["error"])
		code, out = mtStagedCommit(t, f, f.A, id)
		assert.Equal(t, http.StatusNotFound, code, "%v", out)
	})

	t.Run("moved between the last chunk and the commit", func(t *testing.T) {
		f := newMTFix(t, true)
		ctx := context.Background()
		id := mtStagedBegin(t, f, f.A, "alpha://docs", "late.bin", size)
		code, out := mtStagedPut(t, f, f.A, id, payload)
		require.Equal(t, http.StatusOK, code, "%v", out)

		require.NoError(t, f.Store.UnlinkProviderStorage(ctx, f.ProvA, f.StA.ID))
		require.NoError(t, f.Store.LinkProviderStorage(ctx, f.ProvB, f.StA.ID))

		code, out = mtStagedCommit(t, f, f.A, id)
		assert.Equal(t, http.StatusNotFound, code, "a commit into a storage that is bravo's now: %v", out)
		assert.Equal(t, "upload not found", out["error"])
		assert.Empty(t, fileContent(t, f.RootA, "docs/late.bin"), "nothing written")
	})
}

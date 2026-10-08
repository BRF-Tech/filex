package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The presigned S3 multipart upload (POST /api/files/upload/init, /finalize
// and /abort) was removed in 0.54: no filex client spoke it any more, and the
// staged upload (upload_staged.go) is the one large-file path on every driver.
// A route that answers anything but "no such route" is that upload back.
func TestLegacyPresignedUpload_RoutesAreGone(t *testing.T) {
	f := newStagedFixture(t)
	body, _ := json.Marshal(map[string]any{
		"storage_id": f.storage.ID,
		"path":       "main://",
		"filename":   "big.bin",
		"size":       50 << 20,
		"upload_id":  "u-1",
	})
	for _, p := range []string{"/api/files/upload/init", "/api/files/upload/finalize", "/api/files/upload/abort"} {
		resp, err := f.client.Post(f.srv.URL+p, "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, resp.StatusCode,
			"POST %s must not reach a handler any more", p)
	}
}

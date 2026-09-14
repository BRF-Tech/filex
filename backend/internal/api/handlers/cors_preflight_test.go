package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An explorer embedded on ANOTHER origin uploads a file larger than one chunk
// (8 MiB by default) as a series of PUTs, each carrying `Content-Range`. That
// header was not in the default allow-list, so the preflight for the first
// chunk came back without permission for it and the browser refused the
// upload — while every file under 8 MiB went through, which reads as a size
// limit rather than CORS. The defaults now allow every header the explorer
// itself sends.
func TestCORS_ChunkUploadPreflightAllowsContentRange(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/api/files/upload/abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://app.example.test")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-range,content-type")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	allowed := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"authorization", "content-type", "content-range"} {
		if !strings.Contains(allowed, h) {
			t.Fatalf("preflight for a chunk PUT does not allow %q (Access-Control-Allow-Headers: %q, status %d)",
				h, resp.Header.Get("Access-Control-Allow-Headers"), resp.StatusCode)
		}
	}
}

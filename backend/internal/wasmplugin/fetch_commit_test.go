package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/wasmfixture"
)

// A store link names the commit the store approved: the repository is read
// THERE - the manifest and every file it names relative to the repository -
// while `{tag}` still stands for the tag (a release asset is the tag's), and
// the app keeps following the tag (its source).
func TestFetchGitHub_AtACommitReadsTheRepositoryThereAndTheReleaseAtTheTag(t *testing.T) {
	wasmfixture.Require(t, fixtureWasm)
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	sum := sha256.Sum256(wasm)
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["wasm"] = map[string]any{"url": "dist/{tag}/echo.wasm", "sha256": hex.EncodeToString(sum[:])}
	manifest, _ := json.Marshal(m)
	commit := strings.Repeat("c0ffee", 7)[:40]

	var mu sync.Mutex
	var asked []string
	files := map[string][]byte{
		"/Owner/echo/" + commit + "/filex-app.json":                manifest,
		"/Owner/echo/" + commit + "/dist/v1.2.0/echo.wasm":         wasm,
		"/Owner/echo/v1.2.0/filex-app.json":                        []byte(`{"name":"not-this"}`),
		"/Owner/echo/v1.2.0/dist/v1.2.0/echo.wasm":                 []byte("not this either"),
		"/Owner/echo/" + commit + "/dist/" + commit + "/echo.wasm": []byte("nor this"),
	}
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(src.Close)
	reg, _ := newPackRegistry(t, func(o *Options) {
		o.LoopbackSources = true
		o.GitHubRawBase = src.URL
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	in, err := reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/echo", Ref: "v1.2.0", Commit: commit})
	require.NoError(t, err, "asked: %v", asked)
	assert.Equal(t, string(manifest), string(in.Manifest), "the manifest is the commit's")
	assert.Equal(t, "https://github.com/Owner/echo@v1.2.0", in.SourceURL, "the app follows the tag")
	mu.Lock()
	assert.Equal(t, []string{"/Owner/echo/" + commit + "/filex-app.json", "/Owner/echo/" + commit + "/dist/v1.2.0/echo.wasm"}, asked)
	mu.Unlock()

	for _, bad := range []string{"main", "abc1234", strings.ToUpper(commit)} {
		_, err := reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/echo", Ref: "v1.2.0", Commit: bad})
		assert.Error(t, err, "commit %q", bad)
	}
	_, err = reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/echo", Commit: commit})
	assert.Error(t, err, "a commit without the tag it is for")
}

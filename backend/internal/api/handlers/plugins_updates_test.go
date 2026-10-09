package handlers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// A storage plugin's source over the admin API (M5): named with PATCH,
// read by "Check for updates" (which installs nothing), and approved with
// {"from_source": true} — held to the feed's sha256.
func TestPluginsAdmin_SourceCheckAndApprovedUpgrade(t *testing.T) {
	build := []byte("a build")
	sum := sha256.Sum256(build)
	feed := map[string]any{"name": "myfs", "version": "1.1.0", "notes": "Faster.", "binaries": map[string]any{}}
	src := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/filex-storage.json":
			_ = json.NewEncoder(w).Encode(feed)
		case "/myfs":
			_, _ = w.Write(build)
		default:
			http.NotFound(w, r)
		}
	}))
	defer src.Close()

	_, store := dbtest.NewTestDB(t)
	dir := t.TempDir()
	m, err := plugin.New(plugin.Options{Store: store, Dir: dir, SecretKey: "test-secret-key", HTTP: src.Client(), Platform: "testos/testarch"})
	require.NoError(t, err)
	t.Cleanup(m.Shutdown)
	ctx := context.Background()
	row, err := store.CreatePlugin(ctx, &model.Plugin{Name: "myfs", Kind: model.PluginKindBinary, Binary: "myfs",
		SHA256: strings.Repeat("a", 64), Version: "1.0.0", Driver: "myfs"})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "myfs"), 0o755))
	require.NoError(t, m.Load(ctx))

	h := handlers.NewPlugins(m, false)
	r := chi.NewRouter()
	r.Route("/api/admin/plugins", func(r chi.Router) {
		r.Post("/updates/check", h.CheckUpdates)
		r.Patch("/{id}", h.Patch)
		r.Post("/{id}/upgrade", h.Upgrade)
		r.Get("/", h.List)
	})
	do := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	id := strconv.FormatInt(row.ID, 10)

	code, _ := do(http.MethodPatch, "/api/admin/plugins/"+id, `{"source":"http://plain.example/f.json"}`)
	assert.Equal(t, http.StatusBadRequest, code, "a source is https")
	code, got := do(http.MethodPatch, "/api/admin/plugins/"+id, `{"source":"`+src.URL+`/filex-storage.json"}`)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, src.URL+"/filex-storage.json", got["source"])

	feed["binaries"] = map[string]any{"testos/testarch": map[string]any{"url": src.URL + "/myfs", "sha256": strings.Repeat("0", 64)}}
	code, got = do(http.MethodPost, "/api/admin/plugins/updates/check", `{}`)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, []any{"myfs"}, got["report"].(map[string]any)["available"])
	p := got["plugins"].([]any)[0].(map[string]any)
	assert.Equal(t, "available", p["update"].(map[string]any)["status"])
	assert.Equal(t, "1.0.0", p["version"], "the check installs nothing")

	code, _ = do(http.MethodPost, "/api/admin/plugins/"+id+"/upgrade", `{"from_source":false}`)
	assert.Equal(t, http.StatusBadRequest, code)
	code, got = do(http.MethodPost, "/api/admin/plugins/"+id+"/upgrade", `{"from_source":true}`)
	assert.Equal(t, http.StatusBadRequest, code, "bytes that are not the ones the feed names")
	assert.Equal(t, "upgrade_failed", got["error"], "a code, and the server's sentence (0.55)")
	assert.Contains(t, got["detail"], "sha256 mismatch")
	assert.Contains(t, got["message"], "sha256 mismatch", "the reason is in the sentence")
	after, err := store.GetPlugin(ctx, row.ID)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("a", 64), after.SHA256, "nothing was swapped")

	// The right hash: the ordinary upgrade runs (this row's plugin is off, so
	// the new file is only put in place and recorded).
	feed["binaries"] = map[string]any{"testos/testarch": map[string]any{"url": src.URL + "/myfs", "sha256": hex.EncodeToString(sum[:])}}
	code, got = do(http.MethodPost, "/api/admin/plugins/"+id+"/upgrade", `{"from_source":true}`)
	require.Equal(t, http.StatusOK, code, got)
	after, err = store.GetPlugin(ctx, row.ID)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(sum[:]), after.SHA256)
}

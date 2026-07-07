package handlers_test

// Multi-storage routing + driver-fallback tests. These guard the bug we
// caught hard: when two+ storages are configured and a path is sent
// without `<adapter>://`, the manager handler used to silently fall back
// to storages[0] and 404 on every op against the non-default storage.
// The SFC fix qualifies paths everywhere; this suite verifies the
// backend honours the prefix and that the cold-cache fallback works.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// twoStorageFixture wires two local-driver storages (A + B) into the
// manager, returning the handler, store, both driver+row pairs, and
// the temp roots so tests can poke the FS directly.
type twoStorageFixture struct {
	mh    *handlers.Manager
	store db.Store
	drvA  *local.Driver
	drvB  *local.Driver
	stA   *model.Storage
	stB   *model.Storage
	rootA string
	rootB string
}

func newTwoStorageFixture(t *testing.T) *twoStorageFixture {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	rootA := t.TempDir()
	rootB := t.TempDir()

	drvA := &local.Driver{}
	require.NoError(t, drvA.Init(context.Background(), map[string]any{"root": rootA}))
	drvB := &local.Driver{}
	require.NoError(t, drvB.Init(context.Background(), map[string]any{"root": rootB}))

	stA, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "alpha",
		Driver:     "local",
		MountPath:  "/alpha",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(rootA) + `"}`),
	})
	require.NoError(t, err)
	stB, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "beta",
		Driver:     "local",
		MountPath:  "/beta",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(rootB) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		switch id {
		case stA.ID:
			return drvA, nil
		case stB.ID:
			return drvB, nil
		}
		return nil, fmt.Errorf("unknown id %d", id)
	}
	return &twoStorageFixture{
		mh:    handlers.NewManager(store, resolver),
		store: store,
		drvA:  drvA, drvB: drvB,
		stA: stA, stB: stB,
		rootA: rootA, rootB: rootB,
	}
}

// callList drives the GET dispatcher with the passed query params.
func callList(t *testing.T, mh *handlers.Manager, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/files/manager?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	mh.List(rec, req)
	return rec
}

// callMutateOn drives the POST dispatcher (Mutate) on the given handler.
func callMutateOn(t *testing.T, mh *handlers.Manager, action string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/files/manager?action="+action, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mh.Mutate(rec, req)
	return rec
}

// ---------- index routing ----------

func TestManagerIndex_RoutesByAdapter_Alpha(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "only-on-alpha.txt"), []byte("a"), 0o644))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Adapter string           `json:"adapter"`
		Files   []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "alpha", resp.Adapter)
	names := []string{}
	for _, f := range resp.Files {
		names = append(names, f["basename"].(string))
	}
	assert.Contains(t, names, "only-on-alpha.txt")
}

func TestManagerIndex_RoutesByAdapter_Beta(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootB, "only-on-beta.txt"), []byte("b"), 0o644))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"beta://"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Adapter string           `json:"adapter"`
		Files   []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "beta", resp.Adapter)
	names := []string{}
	for _, f := range resp.Files {
		names = append(names, f["basename"].(string))
	}
	assert.Contains(t, names, "only-on-beta.txt")
}

func TestManagerIndex_NoPrefix_FallsBackToFirstStorage(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "alpha-marker.txt"), []byte("a"), 0o644))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{""}, // empty — defaults to storages[0]
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Adapter string `json:"adapter"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	// alpha was created first → it's storages[0].
	assert.Equal(t, "alpha", resp.Adapter)
}

func TestManagerIndex_UnknownAdapter_404(t *testing.T) {
	fx := newTwoStorageFixture(t)
	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"ghost://"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------- driver-fallback for cold-cache dirs ----------

func TestManagerIndex_DriverFallback_NewlyCreatedDir(t *testing.T) {
	fx := newTwoStorageFixture(t)

	// Make a dir on disk WITHOUT inserting the matching DB row. This
	// simulates the gap between `mkdir` and the next sync poll.
	require.NoError(t, os.Mkdir(filepath.Join(fx.rootA, "fresh"), 0o755))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://fresh"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Adapter string           `json:"adapter"`
		Files   []map[string]any `json:"files"`
		Dirname string           `json:"dirname"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "alpha", resp.Adapter)
	assert.Equal(t, "alpha://fresh", resp.Dirname)
	assert.Equal(t, 0, len(resp.Files))
}

func TestManagerIndex_DriverFallback_WithFiles(t *testing.T) {
	fx := newTwoStorageFixture(t)

	require.NoError(t, os.MkdirAll(filepath.Join(fx.rootA, "external"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(fx.rootA, "external", "out-of-band.txt"),
		[]byte("hello"),
		0o644,
	))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://external"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	assert.Equal(t, "out-of-band.txt", resp.Files[0]["basename"])
	assert.Equal(t, "file", resp.Files[0]["type"])
	assert.Equal(t, "alpha://external/out-of-band.txt", resp.Files[0]["path"])
}

func TestManagerIndex_DriverFallback_TrulyMissing_404(t *testing.T) {
	fx := newTwoStorageFixture(t)
	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://does-not-exist"},
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------- multi-storage isolation: mutations stay on their adapter ----------

func TestManagerMutate_Rename_StaysOnAdapter(t *testing.T) {
	fx := newTwoStorageFixture(t)
	// Seed both roots with the SAME basename to make a mistake visible.
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "shared.txt"), []byte("alpha"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootB, "shared.txt"), []byte("beta"), 0o644))

	// Rename only the BETA copy.
	rec := callMutateOn(t, fx.mh, "rename", map[string]any{
		"path": "beta://",
		"item": "beta://shared.txt",
		"name": "shared-renamed.txt",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Beta: original gone, renamed present.
	_, err := os.Stat(filepath.Join(fx.rootB, "shared.txt"))
	assert.True(t, os.IsNotExist(err), "beta: original should be gone")
	body, err := os.ReadFile(filepath.Join(fx.rootB, "shared-renamed.txt"))
	require.NoError(t, err)
	assert.Equal(t, "beta", string(body))

	// Alpha: untouched.
	body, err = os.ReadFile(filepath.Join(fx.rootA, "shared.txt"))
	require.NoError(t, err)
	assert.Equal(t, "alpha", string(body), "alpha must not have been touched")
}

func TestManagerMutate_Delete_StaysOnAdapter(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "shared.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootB, "shared.txt"), []byte("b"), 0o644))

	rec := callMutateOn(t, fx.mh, "delete", map[string]any{
		"path":  "beta://",
		"items": []map[string]any{{"path": "beta://shared.txt"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, err := os.Stat(filepath.Join(fx.rootB, "shared.txt"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(fx.rootA, "shared.txt"))
	require.NoError(t, err, "alpha file must survive a beta delete")
}

func TestManagerMutate_NewFolder_RoutesByAdapter(t *testing.T) {
	fx := newTwoStorageFixture(t)
	rec := callMutateOn(t, fx.mh, "newfolder", map[string]any{
		"path": "beta://",
		"name": "beta-only",
	})
	require.Equal(t, http.StatusOK, rec.Code)

	_, err := os.Stat(filepath.Join(fx.rootB, "beta-only"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(fx.rootA, "beta-only"))
	assert.True(t, os.IsNotExist(err))
}

// ---------- preview/download routing ----------

func TestManagerPreview_RoutesByAdapter(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "a.txt"), []byte("alpha-bytes"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootB, "b.txt"), []byte("beta-bytes"), 0o644))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"preview"},
		"path":   []string{"alpha://a.txt"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	assert.Equal(t, "alpha-bytes", string(body))

	rec = callList(t, fx.mh, url.Values{
		"action": []string{"preview"},
		"path":   []string{"beta://b.txt"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ = io.ReadAll(rec.Body)
	assert.Equal(t, "beta-bytes", string(body))
}

func TestManagerPreview_WithoutPrefix_HitsFirstStorage(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "shared.txt"), []byte("alpha"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootB, "shared.txt"), []byte("beta"), 0o644))

	// Caller drops the prefix → backend defaults to alpha (first
	// storage). This is the codified fallback behaviour the SFC fix
	// avoids by always sending the full `<adapter>://` path.
	rec := callList(t, fx.mh, url.Values{
		"action": []string{"preview"},
		"path":   []string{"shared.txt"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	assert.Equal(t, "alpha", string(body))
}

func TestManagerDownload_AttachmentDisposition(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "report.csv"), []byte("a,b\n1,2\n"), 0o644))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"download"},
		"path":   []string{"alpha://report.csv"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	cd := rec.Header().Get("Content-Disposition")
	assert.Contains(t, cd, "attachment")
	assert.Contains(t, cd, "report.csv")
}

// ---------- phantom prefixes must 404, not render as empty dirs ----------

// blobishDriver mimics an S3-style blob store: List on ANY prefix succeeds
// (empty result for unknown prefixes — no "directory" concept), Stat only
// answers for real object keys.
type blobishDriver struct {
	objects map[string]storage.Object
}

func (d *blobishDriver) Init(context.Context, map[string]any) error { return nil }
func (d *blobishDriver) Name() string                               { return "blobish" }
func (d *blobishDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	prefix := strings.Trim(p, "/")
	out := []storage.Object{}
	for k, o := range d.objects {
		if prefix == "" || strings.HasPrefix(k, prefix+"/") {
			out = append(out, o)
		}
	}
	return out, nil
}
func (d *blobishDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	if o, ok := d.objects[strings.Trim(p, "/")]; ok {
		return o, nil
	}
	return storage.Object{}, storage.ErrNotFound
}
func (d *blobishDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}
func (d *blobishDriver) Capabilities() storage.Capabilities { return storage.Capabilities{} }

func newBlobFixture(t *testing.T, objects map[string]storage.Object) *handlers.Manager {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "blob",
		Driver:     "s3",
		MountPath:  "/blob",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	drv := &blobishDriver{objects: objects}
	resolver := func(id int64) (storage.Driver, error) {
		if id == st.ID {
			return drv, nil
		}
		return nil, fmt.Errorf("unknown id %d", id)
	}
	return handlers.NewManager(store, resolver)
}

func TestManagerIndex_PhantomPrefix_404OnBlobStore(t *testing.T) {
	mh := newBlobFixture(t, map[string]storage.Object{
		"real/file.txt": {Path: "real/file.txt", Name: "file.txt", Kind: storage.KindFile, Size: 3},
	})

	// Nonexistent prefix: the blob driver lists it as empty-success, but
	// nothing proves the dir exists → must be 404, not an empty listing.
	rec := callList(t, mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"blob://olmayan-klasor"},
	})
	require.Equal(t, http.StatusNotFound, rec.Code)

	// A prefix with real children stays browsable.
	rec = callList(t, mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"blob://real"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Files, 1)

	// The storage root is always browsable, even when the bucket is empty.
	rec = callList(t, newBlobFixture(t, map[string]storage.Object{}), url.Values{
		"action": []string{"index"},
		"path":   []string{"blob://"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestManagerIndex_EmptyLocalDir_StillListable(t *testing.T) {
	// A real (cold-cache) empty directory on a stat-capable driver must keep
	// returning 200 — the phantom-prefix guard confirms it via Stat.
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.Mkdir(filepath.Join(fx.rootA, "bosklasor"), 0o755))

	rec := callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://bosklasor"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Files, 0)

	// And a nonexistent dir on the same driver stays 404 (List errors).
	rec = callList(t, fx.mh, url.Values{
		"action": []string{"index"},
		"path":   []string{"alpha://hic-yok"},
	})
	require.Equal(t, http.StatusNotFound, rec.Code)
}

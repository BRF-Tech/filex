package handlers_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// closeProbe is a local driver that counts its Close calls.
type closeProbe struct {
	*local.Driver
	closed *atomic.Int32
}

func (c closeProbe) Close() error {
	c.closed.Add(1)
	return nil
}

// ── P-8: a "Test connection" does not leave its driver behind ───────────────

// The admin's Test and Discover build a driver from a configuration that was
// never saved - credentials included - to look at the storage once. The
// driver is closed when the answer is written: a plugin driver releases the
// instance holding that configuration, a connection-keeping driver its
// connection.
func TestStoragesAdmin_TestAndDiscoverCloseTheirDriver(t *testing.T) {
	f := newAppFixture(t, nil)
	var closed atomic.Int32
	name := "closeprobe-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	storage.Register(name, func() storage.Driver { return closeProbe{Driver: &local.Driver{}, closed: &closed} })
	t.Cleanup(func() { storage.Unregister(name) })

	cfg := map[string]any{"root": t.TempDir()}
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/storages/test", map[string]any{"driver": name, "config": cfg})
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Eventually(t, func() bool { return closed.Load() == 1 }, 3*time.Second, 20*time.Millisecond,
		"Test closed the driver it built")

	storage.RegisterDescriptor(storage.Descriptor{Driver: name, Label: name,
		Fields: []storage.Field{{Key: "root", Type: storage.FieldString, Label: "Root", Required: true, Root: true}}})
	t.Cleanup(func() { storage.UnregisterDescriptor(name) })
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/storages/discover", map[string]any{"driver": name, "config": cfg})
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Eventually(t, func() bool { return closed.Load() == 2 }, 3*time.Second, 20*time.Millisecond,
		"Discover closed the driver it built")
}

// ── P-9: the storage-plugin admin bodies are bounded ────────────────────────

func newPluginsAdminRouter(t *testing.T, maxBinary int64) (*chi.Mux, int64) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	m, err := plugin.New(plugin.Options{Store: store, Dir: t.TempDir(), SecretKey: "test-secret-key", MaxBinaryBytes: maxBinary})
	require.NoError(t, err)
	t.Cleanup(m.Shutdown)
	row, err := store.CreatePlugin(context.Background(), &model.Plugin{Name: "myfs", Kind: model.PluginKindBinary, Binary: "myfs",
		SHA256: strings.Repeat("a", 64), Version: "1.0.0", Driver: "myfs"})
	require.NoError(t, err)
	h := handlers.NewPlugins(m, false)
	r := chi.NewRouter()
	r.Route("/api/admin/plugins", func(r chi.Router) {
		r.Post("/", h.Install)
		r.Patch("/{id}", h.Patch)
		r.Post("/{id}/upgrade", h.Upgrade)
	})
	return r, row.ID
}

// An administrator's request body is read up to what it can sensibly be: a
// JSON body is a few fields, a multipart body is one binary the manager would
// refuse past its cap anyway. Past that the answer is 413 before the body is
// read into memory or spilled to disk.
func TestPluginsAdmin_BodiesAreBounded(t *testing.T) {
	r, id := newPluginsAdminRouter(t, 1<<20)
	pad := strings.Repeat("x", 2<<20)
	send := func(method, path, ct string, body []byte) int {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	one := "/api/admin/plugins/" + strconv.FormatInt(id, 10)

	assert.Equal(t, http.StatusRequestEntityTooLarge,
		send(http.MethodPost, "/api/admin/plugins/", "application/json", []byte(`{"name":"big","url":"https://example.com/x","pad":"`+pad+`"}`)), "install JSON")
	assert.Equal(t, http.StatusRequestEntityTooLarge,
		send(http.MethodPatch, one, "application/json", []byte(`{"enabled":false,"pad":"`+pad+`"}`)), "patch")
	assert.Equal(t, http.StatusRequestEntityTooLarge,
		send(http.MethodPost, one+"/upgrade", "application/json", []byte(`{"from_source":true,"pad":"`+pad+`"}`)), "upgrade JSON")

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("name", "big"))
	fw, err := w.CreateFormFile("file", "big")
	require.NoError(t, err)
	_, _ = fw.Write(bytes.Repeat([]byte{7}, 3<<20))
	require.NoError(t, w.Close())
	assert.Equal(t, http.StatusRequestEntityTooLarge,
		send(http.MethodPost, "/api/admin/plugins/", w.FormDataContentType(), buf.Bytes()), "install multipart past the binary cap")
}

// ── P-10: a share download is redirected to https only ──────────────────────

// presignAt is a local driver that hands out a presigned download address of
// the test's choosing, the way a storage plugin may.
type presignAt struct {
	*local.Driver
	url string
}

func (p presignAt) Capabilities() storage.Capabilities {
	c := p.Driver.Capabilities()
	c.Presign = true
	return c
}

func (p presignAt) PresignDownload(context.Context, string, time.Duration) (string, error) {
	return p.url, nil
}

func (p presignAt) PresignUpload(context.Context, string, int64) (storage.PresignedUpload, error) {
	return storage.PresignedUpload{}, fmt.Errorf("not offered")
}

// A presigned address comes from the storage driver - for a plugin, from
// somebody else's code. A visitor of a share link is sent there only when it
// is an https:// address; anything else (plain http, javascript:, data:, a
// relative path) is served through filex instead.
func TestShareDownload_RedirectsOnlyToHTTPS(t *testing.T) {
	_, store, drv, st, root := newMutateFixture(t)
	node := fileNode(t, store, st, root, "out/report.txt", "the bytes")
	svc := share.NewService(store)
	svc.AttachSecret("0123456789abcdef0123456789abcdef")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)

	for _, c := range []struct {
		url      string
		redirect bool
	}{
		{"https://objects.example.com/report.txt?sig=1", true},
		{"http://objects.example.com/report.txt?sig=1", false},
		{"javascript:alert(1)", false},
		{"//objects.example.com/report.txt", false},
		{"/api/admin/users", false},
	} {
		pres := presignAt{Driver: drv, url: c.url}
		resolver := func(id int64) (storage.Driver, error) {
			if id != st.ID {
				return nil, fmt.Errorf("unknown id %d", id)
			}
			return pres, nil
		}
		h := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
		rt := chi.NewRouter()
		rt.Get("/s/{token}", h.HandleDownload)
		req := httptest.NewRequest(http.MethodGet, "/s/"+sh.Token, nil)
		rec := httptest.NewRecorder()
		rt.ServeHTTP(rec, req)
		if c.redirect {
			assert.Equal(t, http.StatusFound, rec.Code, c.url)
			assert.Equal(t, c.url, rec.Header().Get("Location"))
			continue
		}
		assert.Equal(t, http.StatusOK, rec.Code, "%s: served through filex", c.url)
		assert.Empty(t, rec.Header().Get("Location"), c.url)
		assert.Equal(t, "the bytes", rec.Body.String(), c.url)
	}
}

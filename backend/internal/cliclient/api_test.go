package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer mimics the slices of the real REST API the CLI consumes.
// It records the last mutating call so tests can assert wire shapes.
type fakeServer struct {
	t *testing.T

	token string // expected bearer; "" disables the auth check

	// recorded state
	lastAction   string
	lastBody     map[string]any
	lastQuery    map[string]string
	uploadDest   string
	uploadName   string
	uploadBytes  []byte
	uploads      []uploadRecord // every upload, in arrival order
	downloadBody []byte
	dirs         map[string]bool // adapter://rel → is a listable dir
}

// uploadRecord is one received upload (dest dir + filename + content).
type uploadRecord struct {
	Dest string
	Name string
	Body []byte
}

func newFakeServer(t *testing.T) (*fakeServer, *httptest.Server) {
	fs := &fakeServer{
		t:            t,
		token:        "good-token",
		downloadBody: []byte("remote file contents"),
		dirs:         map[string]bool{"docs://": true, "docs://inbox": true, "docs://archive": true},
	}
	srv := httptest.NewServer(http.HandlerFunc(fs.handle))
	t.Cleanup(srv.Close)
	return fs, srv
}

func (fs *fakeServer) writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (fs *fakeServer) authorized(r *http.Request) bool {
	return fs.token == "" || r.Header.Get("Authorization") == "Bearer "+fs.token
}

func (fs *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/auth/login":
		var req struct{ Email, Password, Totp string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Email == "burak@brf.sh" && req.Password == "s3cret" {
			fs.writeJSON(w, 200, map[string]any{"token": "sess-abc", "user": map[string]any{"email": req.Email}})
			return
		}
		if req.Email == "totp@brf.sh" && req.Password == "s3cret" && req.Totp == "" {
			fs.writeJSON(w, 401, map[string]any{"error": "two-factor code required", "totp_required": true})
			return
		}
		fs.writeJSON(w, 401, map[string]any{"error": "invalid credentials"})

	case "/api/files/manager":
		if !fs.authorized(r) {
			fs.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		q := r.URL.Query()
		action := q.Get("action")
		fs.lastQuery = map[string]string{"action": action, "path": q.Get("path")}
		switch {
		case r.Method == http.MethodGet && action == "index":
			p := q.Get("path")
			if p == "" {
				p = "docs://"
			}
			if !fs.dirs[p] {
				fs.writeJSON(w, 404, map[string]string{"error": "directory not found"})
				return
			}
			fs.writeJSON(w, 200, map[string]any{
				"adapter":   "docs",
				"storages":  []string{"docs", "s3-test"},
				"dirname":   p,
				"read_only": false,
				"files": []map[string]any{
					{"path": "docs://inbox", "basename": "inbox", "type": "dir", "size": 0},
					{"path": "docs://rapor.pdf", "basename": "rapor.pdf", "type": "file", "size": 123456, "mime_type": "application/pdf", "last_modified": int64(1752700000000)},
				},
			})
		case r.Method == http.MethodGet && action == "download":
			if q.Get("path") == "docs://rapor.pdf" {
				w.WriteHeader(200)
				_, _ = w.Write(fs.downloadBody)
				return
			}
			fs.writeJSON(w, 404, map[string]string{"error": "not found"})
		case r.Method == http.MethodPost && action == "upload":
			require.NoError(fs.t, r.ParseMultipartForm(32<<20))
			fs.lastAction = "upload"
			fs.uploadDest = r.FormValue("path")
			fhs := r.MultipartForm.File["file[]"]
			require.Len(fs.t, fhs, 1)
			fs.uploadName = fhs[0].Filename
			f, err := fhs[0].Open()
			require.NoError(fs.t, err)
			defer f.Close()
			fs.uploadBytes, err = io.ReadAll(f)
			require.NoError(fs.t, err)
			fs.uploads = append(fs.uploads, uploadRecord{Dest: fs.uploadDest, Name: fs.uploadName, Body: fs.uploadBytes})
			fs.writeJSON(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
		case r.Method == http.MethodPost && action == "newfolder":
			// Recorded like the generic verbs AND registered in dirs so a
			// later index probe sees the freshly created folder (the
			// recursive-upload tests depend on that). "yasak" is the
			// designated always-fails name for error-path tests.
			fs.lastAction = action
			fs.lastBody = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&fs.lastBody)
			parent, _ := fs.lastBody["path"].(string)
			name, _ := fs.lastBody["name"].(string)
			if name == "yasak" {
				fs.writeJSON(w, 403, map[string]string{"error": "forbidden folder name"})
				return
			}
			full := parent + "/" + name
			if strings.HasSuffix(parent, "://") {
				full = parent + name
			}
			fs.dirs[full] = true
			fs.writeJSON(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
		case r.Method == http.MethodPost:
			fs.lastAction = action
			fs.lastBody = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&fs.lastBody)
			fs.writeJSON(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
		default:
			fs.writeJSON(w, 501, map[string]string{"error": "action not implemented"})
		}

	case "/api/files/search":
		if !fs.authorized(r) {
			fs.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		q := r.URL.Query()
		fs.lastQuery = map[string]string{
			"q": q.Get("q"), "scope": q.Get("scope"),
			"storage_id": q.Get("storage_id"), "limit": q.Get("limit"),
		}
		fs.writeJSON(w, 200, map[string]any{"results": []map[string]any{
			{"id": 7, "storage_id": 1, "name": "rapor.pdf", "path": "/inbox/rapor.pdf", "type": "file", "size": 123, "snippet": "…«rapor» satırı…", "matched": "both"},
		}})

	case "/api/files/share":
		if !fs.authorized(r) {
			fs.writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		fs.lastAction = "share"
		fs.lastBody = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&fs.lastBody)
		inner := map[string]any{
			"id": 1, "uuid": "tok123", "token": "tok123",
			"url": "https://fm.example.com/s/tok123", "has_pin": false,
		}
		if v, ok := fs.lastBody["password"].(bool); ok && v {
			inner["has_pin"] = true
			inner["password_pin"] = "PIN12345"
		}
		if v, ok := fs.lastBody["expires_at"].(string); ok {
			inner["expires_at"] = v
		}
		fs.writeJSON(w, 200, map[string]any{"share": inner, "url": inner["url"], "token": "tok123"})

	default:
		fs.writeJSON(w, 404, map[string]string{"error": "no route: " + r.URL.Path})
	}
}

func testClient(srv *httptest.Server, token string) *Client {
	return New(Conn{URL: srv.URL, Token: token})
}

// ─────────────────── login ───────────────────

// TestLogin_SavesConfig0600 drives the full login flow: exchange
// credentials for a token, persist it, and verify owner-only mode.
func TestLogin_SavesConfig0600(t *testing.T) {
	_, srv := newFakeServer(t)
	api := New(Conn{URL: srv.URL})

	lr, err := api.Login(context.Background(), "burak@brf.sh", "s3cret", "")
	require.NoError(t, err)
	assert.Equal(t, "sess-abc", lr.Token)

	cfgPath := filepath.Join(t.TempDir(), "cli.yaml")
	require.NoError(t, SaveFileConfig(cfgPath, FileConfig{URL: srv.URL, Token: lr.Token}))

	cfg, err := LoadFileConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, srv.URL, cfg.URL)
	assert.Equal(t, "sess-abc", cfg.Token)

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

// TestLogin_InvalidCredentials maps the 401 onto an APIError.
func TestLogin_InvalidCredentials(t *testing.T) {
	_, srv := newFakeServer(t)
	api := New(Conn{URL: srv.URL})

	_, err := api.Login(context.Background(), "burak@brf.sh", "wrong", "")
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
	assert.Contains(t, err.Error(), "invalid credentials")
}

// TestLogin_TotpRequiredHint surfaces the --totp hint on 2FA accounts.
func TestLogin_TotpRequiredHint(t *testing.T) {
	_, srv := newFakeServer(t)
	api := New(Conn{URL: srv.URL})

	_, err := api.Login(context.Background(), "totp@brf.sh", "s3cret", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--totp")
}

// ─────────────────── ls ───────────────────

func TestList_HappyPath(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	res, err := api.List(context.Background(), "docs://")
	require.NoError(t, err)
	assert.Equal(t, "docs", res.Adapter)
	assert.Equal(t, []string{"docs", "s3-test"}, res.Storages)
	require.Len(t, res.Files, 2)
	assert.Equal(t, "dir", res.Files[0].Type)
	assert.Equal(t, "rapor.pdf", res.Files[1].Basename)
	assert.Equal(t, int64(123456), res.Files[1].Size)
	assert.NotEmpty(t, res.Raw)
}

func TestList_Unauthorized(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "stale-token")

	_, err := api.List(context.Background(), "docs://")
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
}

// ─────────────────── upload ───────────────────

// TestUpload_IntoExistingDir keeps the local basename when the remote
// target is a listable directory.
func TestUpload_IntoExistingDir(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	local := filepath.Join(t.TempDir(), "notlar.txt")
	require.NoError(t, os.WriteFile(local, []byte("merhaba filex"), 0o644))

	dest, _, err := api.Upload(context.Background(), local, "docs://inbox")
	require.NoError(t, err)
	assert.Equal(t, "docs://inbox/notlar.txt", dest.String())
	assert.Equal(t, "docs://inbox", fs.uploadDest)
	assert.Equal(t, "notlar.txt", fs.uploadName)
	assert.Equal(t, []byte("merhaba filex"), fs.uploadBytes)
}

// TestUpload_RenameTarget treats a non-directory remote as the full
// target path — the last segment becomes the uploaded filename.
func TestUpload_RenameTarget(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	local := filepath.Join(t.TempDir(), "notlar.txt")
	require.NoError(t, os.WriteFile(local, []byte("içerik"), 0o644))

	dest, _, err := api.Upload(context.Background(), local, "docs://inbox/yeni-ad.txt")
	require.NoError(t, err)
	assert.Equal(t, "docs://inbox/yeni-ad.txt", dest.String())
	assert.Equal(t, "docs://inbox", fs.uploadDest)
	assert.Equal(t, "yeni-ad.txt", fs.uploadName)
}

func TestUpload_Unauthorized(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "stale-token")

	local := filepath.Join(t.TempDir(), "x.txt")
	require.NoError(t, os.WriteFile(local, []byte("x"), 0o644))

	_, _, err := api.Upload(context.Background(), local, "docs://inbox/")
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
}

// ─────────────────── download ───────────────────

func TestDownload_WritesBytes(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	var buf bytes.Buffer
	n, err := api.Download(context.Background(), "docs://rapor.pdf", &buf)
	require.NoError(t, err)
	assert.Equal(t, int64(len(fs.downloadBody)), n)
	assert.Equal(t, fs.downloadBody, buf.Bytes())
}

func TestDownload_NotFound(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	var buf bytes.Buffer
	_, err := api.Download(context.Background(), "docs://yok.pdf", &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestDownload_Unauthorized(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "")
	api.Token = "" // no token at all

	var buf bytes.Buffer
	_, err := api.Download(context.Background(), "docs://rapor.pdf", &buf)
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
}

// ─────────────────── mkdir / rm / mv ───────────────────

func TestMkdir_WireShape(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	_, err := api.Mkdir(context.Background(), "docs://inbox/yeni klasör")
	require.NoError(t, err)
	assert.Equal(t, "newfolder", fs.lastAction)
	assert.Equal(t, "docs://inbox", fs.lastBody["path"])
	assert.Equal(t, "yeni klasör", fs.lastBody["name"])
}

func TestRemove_WireShape(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	_, err := api.Remove(context.Background(), "docs://inbox/eski.txt")
	require.NoError(t, err)
	assert.Equal(t, "delete", fs.lastAction)
	assert.Equal(t, "docs://inbox", fs.lastBody["path"])
	items := fs.lastBody["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "docs://inbox/eski.txt", items[0].(map[string]any)["path"])
}

// TestMove_IntoExistingDir uses the move verb and keeps the basename.
func TestMove_IntoExistingDir(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	dest, _, err := api.Move(context.Background(), "docs://inbox/rapor.pdf", "docs://archive")
	require.NoError(t, err)
	assert.Equal(t, "docs://archive/rapor.pdf", dest.String())
	assert.Equal(t, "move", fs.lastAction)
	assert.Equal(t, "docs://archive", fs.lastBody["path"])
}

// TestMove_SameDirRename maps a same-parent target onto the rename verb.
func TestMove_SameDirRename(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	dest, _, err := api.Move(context.Background(), "docs://inbox/a.txt", "docs://inbox/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "docs://inbox/b.txt", dest.String())
	assert.Equal(t, "rename", fs.lastAction)
	assert.Equal(t, "docs://inbox/a.txt", fs.lastBody["item"])
	assert.Equal(t, "b.txt", fs.lastBody["name"])
}

// TestMove_CrossAdapterRejected fails fast before any wire call.
func TestMove_CrossAdapterRejected(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	_, _, err := api.Move(context.Background(), "docs://a.txt", "s3-test://a.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross-adapter")
}

// ─────────────────── search ───────────────────

func TestSearch_ScopePassthrough(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	res, err := api.Search(context.Background(), "rapor", "content", 3, 10)
	require.NoError(t, err)
	assert.Equal(t, "rapor", fs.lastQuery["q"])
	assert.Equal(t, "content", fs.lastQuery["scope"])
	assert.Equal(t, "3", fs.lastQuery["storage_id"])
	assert.Equal(t, "10", fs.lastQuery["limit"])
	require.Len(t, res.Results, 1)
	assert.Equal(t, "both", res.Results[0].Matched)
	assert.Contains(t, res.Results[0].Snippet, "«rapor»")
}

func TestSearch_BadScope(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	_, err := api.Search(context.Background(), "x", "fulltext", 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--scope")
}

func TestSearch_Unauthorized(t *testing.T) {
	_, srv := newFakeServer(t)
	api := testClient(srv, "stale-token")

	_, err := api.Search(context.Background(), "x", "all", 0, 0)
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
}

// ─────────────────── share ───────────────────

func TestShare_PinAndExpiry(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	res, err := api.Share(context.Background(), "docs://rapor.pdf", true, 7)
	require.NoError(t, err)
	assert.Equal(t, "https://fm.example.com/s/tok123", res.URL)
	assert.Equal(t, "PIN12345", res.PIN)
	require.NotNil(t, res.ExpiresAt)

	assert.Equal(t, "docs://rapor.pdf", fs.lastBody["path"])
	assert.Equal(t, true, fs.lastBody["password"])
	_, hasExp := fs.lastBody["expires_at"].(string)
	assert.True(t, hasExp, "expires_at must be an RFC3339 string")
}

func TestShare_PlainLink(t *testing.T) {
	fs, srv := newFakeServer(t)
	api := testClient(srv, "good-token")

	res, err := api.Share(context.Background(), "docs://rapor.pdf", false, 0)
	require.NoError(t, err)
	assert.Empty(t, res.PIN)
	assert.Nil(t, res.ExpiresAt)
	_, hasPw := fs.lastBody["password"]
	assert.False(t, hasPw, "password key must be omitted when --pin is off")
	_, hasExp := fs.lastBody["expires_at"]
	assert.False(t, hasExp)
}

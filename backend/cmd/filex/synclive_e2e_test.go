package main

// End to end, in one process: the real router (realtime hub, ws-ticket, the
// manager API, the text editor's save), the real CLI client and change stream,
// the real engine and the real file-system watcher, driven through the same
// `filex sync run --watch` command the desktop app starts. Nothing is faked;
// the storage is a local folder and the database an in-memory SQLite.

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filesync"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// liveServer is a whole filex behind httptest, counting the requests the
// engine makes so the echo can be measured.
type liveServer struct {
	srv       *httptest.Server
	token     string
	downloads atomic.Int64 // manager?action=download
	lists     atomic.Int64 // manager?action=index
	uploads   atomic.Int64 // manager?action=upload
}

func newLiveServer(t *testing.T) *liveServer {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(fmt.Sprintf(`{"root":%q}`, filepath.ToSlash(root))),
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		return drv, nil
	}
	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(context.Background(), nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local" // deliberately NOT the address the client uses
	cfg.DataDir = t.TempDir()
	router := api.BuildRouter(&api.Deps{
		Cfg: cfg, Store: store, Worker: syncpkg.New(store), Caps: capability.New(store),
		Share: share.NewService(store), StorageResolver: resolver, LocalAuth: localDrv,
	})
	ls := &liveServer{}
	ls.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/files/manager" && r.Header.Get("X-Test-Read") == "" {
			switch r.URL.Query().Get("action") {
			case "download":
				ls.downloads.Add(1)
			case "index":
				ls.lists.Add(1)
			case "upload":
				ls.uploads.Add(1)
			}
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(ls.srv.Close)

	email, pw := testutil.SeedAdmin(t, store)
	body, _ := json.Marshal(map[string]string{"email": email, "password": pw})
	resp, err := http.Post(ls.srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var lr struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&lr))
	require.NotEmpty(t, lr.Token)
	ls.token = lr.Token
	_ = db.Store(store)
	return ls
}

func (ls *liveServer) do(t *testing.T, method, path string, body any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, ls.srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+ls.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "%s %s: %s", method, path, b)
}

// saveText is the web editor's save: POST /api/files/save-text.
func (ls *liveServer) saveText(t *testing.T, remote, content string) {
	ls.do(t, http.MethodPost, "/api/files/save-text", map[string]string{"path": remote, "content": content})
}

func (ls *liveServer) read(remote string) string {
	req, _ := http.NewRequest(http.MethodGet, ls.srv.URL+"/api/files/manager?action=download&path="+url.QueryEscape(remote), nil)
	req.Header.Set("Authorization", "Bearer "+ls.token)
	req.Header.Set("X-Test-Read", "1") // not counted as the engine's traffic
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// syncBuffer is an output sink the test can read while the command writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitUntil(t *testing.T, within time.Duration, what string, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if cond() {
			return time.Since(start)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
	return 0
}

func TestLiveSyncEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end")
	}
	ls := newLiveServer(t)
	ls.do(t, http.MethodPost, "/api/files/manager?action=newfolder", map[string]string{"path": "main://", "name": "proj"})
	ls.saveText(t, "main://proj/note.txt", "seed\n")

	home := t.TempDir()
	t.Setenv("FILEX_SYNC_DIR", filepath.Join(home, "sync"))
	t.Setenv("FILEX_CLI_CONFIG", filepath.Join(home, "cli.yaml"))
	t.Setenv("FILEX_UPLOAD_STATE", filepath.Join(home, "uploads"))
	t.Setenv("FILEX_URL", ls.srv.URL)
	t.Setenv("FILEX_TOKEN", ls.token)
	mirror := filepath.Join(home, "mirror")
	st := &filesync.Store{Dir: filepath.Join(home, "sync")}
	_, err := st.AddPair(filesync.Pair{Local: mirror, Remote: "main://proj", Account: "t"})
	require.NoError(t, err)

	// A poll interval of an hour: everything below must arrive by the live
	// path, or not at all.
	out := &syncBuffer{}
	cmd := syncCmd()
	cmd.SetArgs([]string{"run", "--account", "t", "--watch", "1h", "--quiet"})
	cmd.SetOut(out)
	cmd.SetErr(out)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		if t.Failed() {
			t.Logf("engine output:\n%s", out.String())
		}
	})

	note := filepath.Join(mirror, "note.txt")
	readLocal := func() string { b, _ := os.ReadFile(note); return string(b) }
	waitUntil(t, 10*time.Second, "the first pass", func() bool { return readLocal() == "seed\n" })
	waitUntil(t, 10*time.Second, "the stream to connect", func() bool { return strings.Contains(out.String(), "live: connected") })

	// ── browser → disk ──
	for i := 0; i < 3; i++ {
		want := fmt.Sprintf("saved in the browser #%d\n", i)
		start := time.Now()
		ls.saveText(t, "main://proj/note.txt", want)
		lag := waitUntil(t, 5*time.Second, "the browser save on disk", func() bool { return readLocal() == want })
		t.Logf("browser save → local file: %s (from before the save request)", time.Since(start).Round(time.Millisecond))
		_ = lag
	}

	// A new file in a NEW sub-folder, created in the browser.
	ls.do(t, http.MethodPost, "/api/files/manager?action=newfolder", map[string]string{"path": "main://proj", "name": "sub"})
	ls.saveText(t, "main://proj/sub/deep.md", "deep\n")
	waitUntil(t, 5*time.Second, "a file in a new folder", func() bool {
		b, _ := os.ReadFile(filepath.Join(mirror, "sub", "deep.md"))
		return string(b) == "deep\n"
	})

	// ── disk → server ──
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 3; i++ {
		want := fmt.Sprintf("saved on the laptop #%d\n", i)
		downloadsBefore := ls.downloads.Load()
		listsBefore, uploadsBefore := ls.lists.Load(), ls.uploads.Load()
		start := time.Now()
		require.NoError(t, os.WriteFile(note, []byte(want), 0o644))
		waitUntil(t, 5*time.Second, "the local save on the server", func() bool { return ls.read("main://proj/note.txt") == want })
		t.Logf("local save → server: %s", time.Since(start).Round(time.Millisecond))
		// ⚠ Echo: the server announces the engine's own upload back to it.
		// Answering that must not download the same bytes again.
		time.Sleep(700 * time.Millisecond)
		require.Equal(t, downloadsBefore, ls.downloads.Load(), "the engine downloaded its own upload back")
		// What one local save costs on the wire, the echo of it included.
		t.Logf("one local save cost: %d upload(s), %d listing(s), %d download(s)",
			ls.uploads.Load()-uploadsBefore, ls.lists.Load()-listsBefore, ls.downloads.Load()-downloadsBefore)
		require.Equal(t, want, readLocal(), "the local file must still be the local save")
	}
	require.NotContains(t, out.String(), "server copy", "no conflict may come out of plain sequential edits: %s", out.String())
}

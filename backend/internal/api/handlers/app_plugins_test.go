package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// The echo fixture lives with the runtime tests; these tests skip when it has
// not been built (CI builds it before go test).
const echoDir = "../../wasmplugin/testdata/echo"

type appFixture struct {
	srv   *httptest.Server
	admin *http.Client
	store db.Store
	reg   *wasmplugin.Registry
	ops   *ops.Service
	st    *model.Storage
	root  string
	// sql is the same handle the ops queue was built on, kept so a test can
	// COUNT the two tables a submit writes (app_plugin_jobs, pending_ops).
	// db.Store has no "list every job" method and deliberately so -- nothing
	// in the product wants one -- but a test that claims a refusal queued
	// NOTHING has to be able to look, and counting the rows is the only
	// reading that cannot be fooled by a job row whose ops row failed.
	sql *sql.DB
	// extra are storages a test added after the first (addStorage); the
	// resolver the router and the registry share reads them.
	extra map[int64]storage.Driver
}

// addStorage adds a second local storage the fixture's resolver knows —
// a destination for a result that may not go beside its source.
func (f *appFixture) addStorage(t *testing.T, name string) (*model.Storage, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := f.store.CreateStorage(ctx, &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, RBACEnabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	f.extra[st.ID] = drv
	return st, root
}

func newAppFixture(t *testing.T, cfgMutate func(*config.Config)) *appFixture {
	t.Helper()
	if _, err := os.Stat(filepath.Join(echoDir, "echo.wasm")); err != nil {
		if os.Getenv("FILEX_REQUIRE_WASM_FIXTURE") != "" {
			t.Fatal("echo.wasm missing on CI")
		}
		t.Skip("echo.wasm not built (bash scripts/build-wasm-fixture.sh)")
	}
	ctx := context.Background()

	// Mirror internal/server.New's store stack (see newMTFix), because the
	// ops queue wants the raw *sql.DB the harness does not hand out.
	sqlDB, raw := testutil.NewTestDB(t)
	accounting := quotastore.New(raw)
	var store db.Store = identitystore.New(accounting)

	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/main", Enabled: true, RBACEnabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	extra := map[int64]storage.Driver{}
	resolver := func(id int64) (storage.Driver, error) {
		if d, ok := extra[id]; ok {
			return d, nil
		}
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}

	opsSvc := ops.New(sqlDB, resolver)
	require.NoError(t, opsSvc.Migrate(ctx))

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}
	if cfgMutate != nil {
		cfgMutate(&cfg)
	}

	// ⚠ ONE share service, wired BOTH to the router and to the plugin
	// registry, exactly as internal/server.New does it. Without Options.Share
	// the registry refuses `share_create` ("share service is not wired"), so a
	// fixture that leaves it out cannot exercise a single public link an app
	// opens — which is how a page job's parameters went untested.
	shareSvc := share.NewService(store)
	shareSvc.AttachSecret("0123456789abcdef0123456789abcdef")

	reg, err := wasmplugin.New(wasmplugin.Options{
		Store: store, Share: shareSvc,
		Dir: filepath.Join(t.TempDir(), "app-plugins"), SecretKey: "0123456789abcdef0123456789abcdef",
		StorageResolver: resolver, Demo: cfg.Demo.Mode,
	})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	reg.SetPublicURL(cfg.PublicURL)
	opsSvc.SetPluginRunner(reg)
	opsSvc.SetDecorator(reg.DecorateOps)

	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg:             cfg,
		Store:           tenantstore.New(store),
		Quota:           accounting.Quota(),
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           shareSvc,
		StorageResolver: resolver,
		Ops:             opsSvc,
		AppPlugins:      reg,
		LocalAuth:       localDrv,
	}))
	t.Cleanup(srv.Close)

	client := freshClient(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return &appFixture{srv: srv, admin: client, store: store, reg: reg, ops: opsSvc, st: st, root: root, sql: sqlDB, extra: extra}
}

func (f *appFixture) writeFile(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// installEcho uploads the fixture through the multipart route, dry run first.
func (f *appFixture) installEcho(t *testing.T) int64 {
	t.Helper()
	return f.installEchoWith(t, nil)
}

// installEchoWith is installEcho with the manifest changed first — one more
// permission, say — through the very same dry run, refusal and install.
func (f *appFixture) installEchoWith(t *testing.T, mutate func(m map[string]any)) int64 {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(echoDir, "manifest.json"))
	require.NoError(t, err)
	if mutate != nil {
		var raw map[string]any
		require.NoError(t, json.Unmarshal(manifest, &raw))
		mutate(raw)
		manifest, err = json.Marshal(raw)
		require.NoError(t, err)
	}
	wasm, err := os.ReadFile(filepath.Join(echoDir, "echo.wasm"))
	require.NoError(t, err)
	var m struct {
		Permissions []string `json:"permissions"`
	}
	_ = json.Unmarshal(manifest, &m)

	body := func(grant []string) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		mf, _ := w.CreateFormFile("manifest", "filex-app.json")
		mf.Write(manifest)
		wf, _ := w.CreateFormFile("wasm", "plugin.wasm")
		wf.Write(wasm)
		if grant != nil {
			g, _ := json.Marshal(map[string]any{"permissions": grant})
			w.WriteField("grant", string(g))
		}
		w.Close()
		return &buf, w.FormDataContentType()
	}

	// Dry run: the review, nothing installed.
	buf, ct := body(nil)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", buf)
	req.Header.Set("Content-Type", ct)
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var dry struct {
		Permissions []struct{ ID, Label string } `json:"permissions"`
		WasmSHA256  string                       `json:"wasm_sha256"`
	}
	require.NoError(t, json.Unmarshal(raw, &dry))
	assert.Len(t, dry.Permissions, len(m.Permissions))
	assert.NotEmpty(t, dry.Permissions[0].Label)

	// A grant that leaves permissions out is refused with the diff.
	buf, ct = body([]string{"files:read"})
	req, _ = http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", buf)
	req.Header.Set("Content-Type", ct)
	resp, err = f.admin.Do(req)
	require.NoError(t, err)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))
	assert.Contains(t, string(raw), "permissions_incomplete")
	assert.Contains(t, string(raw), "files:write")

	// The real thing.
	buf, ct = body(m.Permissions)
	req, _ = http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", buf)
	req.Header.Set("Content-Type", ct)
	resp, err = f.admin.Do(req)
	require.NoError(t, err)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	var st struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &st))
	assert.Equal(t, "running", st.State)
	return st.ID
}

func (f *appFixture) drain(t *testing.T, opID int64) map[string]any {
	t.Helper()
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.ops.Run(runCtx)
	defer f.ops.Stop()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+fmt.Sprintf("/api/files/ops/%d", opID), nil)
		var op map[string]any
		require.NoError(t, json.Unmarshal(raw, &op))
		switch op["status"] {
		case "ok", "failed", "partial", "cancelled":
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("op never finished")
	return nil
}

func TestAppPlugins_AdminInstall_UserRuns_OutputLandsWithBookkeeping(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)

	// The list shows it, with the runtime facts.
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var list struct {
		Runtime struct {
			Enabled     bool              `json:"enabled"`
			Engines     map[string]bool   `json:"engines"`
			EngineNames map[string]string `json:"engine_names"`
		} `json:"runtime"`
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	assert.True(t, list.Runtime.Enabled)
	assert.Contains(t, list.Runtime.Engines, "ffmpeg")
	// Every engine with the name a person reads, so the panel keeps no list
	// of its own (it printed "libreoffice" beside the review's "LibreOffice").
	assert.Equal(t, "LibreOffice", list.Runtime.EngineNames["libreoffice"])
	assert.Equal(t, "librsvg", list.Runtime.EngineNames["rsvg"])
	require.Len(t, list.Plugins, 1)
	assert.Equal(t, "echo", list.Plugins[0].Name)

	// The menu offers the action.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/actions", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"key":"plugin:echo/upper"`)

	// Run it on a text file.
	f.writeFile(t, "docs/note.txt", "hello world")
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"paths": []string{"main://docs/note.txt"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID     int64  `json:"id"`
			Kind   string `json:"kind"`
			Plugin string `json:"plugin"`
			Label  string `json:"label"`
		} `json:"op"`
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	assert.Equal(t, "plugin-action", ans.Op.Kind)
	assert.Equal(t, "echo", ans.Op.Plugin)
	assert.Equal(t, "Upper-case", ans.Op.Label)
	assert.Len(t, ans.JobID, 32)

	op := f.drain(t, ans.Op.ID)
	assert.Equal(t, "ok", op["status"], op)
	assert.Equal(t, "echo", op["plugin"], "the tray row is decorated")
	assert.Equal(t, "done", op["message"])
	outs, _ := json.Marshal(op["outputs"])
	assert.JSONEq(t, `[{"path":"main://docs/note-upper.txt"}]`, string(outs), "adapter-qualified, as the explorer navigates")

	// Bytes on the storage, and a node row the explorer will list.
	got, err := os.ReadFile(filepath.Join(f.root, "docs", "note-upper.txt"))
	require.NoError(t, err)
	assert.Equal(t, "HELLO WORLD", string(got))
	node, err := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, "/docs/note-upper.txt"))
	require.NoError(t, err)
	require.NotNil(t, node)
	assert.Equal(t, int64(11), node.Size)

	// An audit line names who ran what on which file.
	entries, err := f.store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	found := false
	for _, e := range entries {
		if e.Action == "app_plugin.action_run" && e.TargetID == "echo" {
			found = true
		}
	}
	assert.True(t, found, "audit entry")

	// A finished op cannot be cancelled.
	status, _ = doReq(t, f.admin, http.MethodPost, f.srv.URL+fmt.Sprintf("/api/files/ops/%d/cancel", ans.Op.ID), nil)
	assert.Equal(t, http.StatusConflict, status)

	// Detail, settings, overrides, logs answer.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+fmt.Sprintf("/api/admin/app-plugins/%d", id), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"granted"`)
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+fmt.Sprintf("/api/admin/app-plugins/%d/logs?after=0", id), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "upper: ")
}

func TestAppPlugins_Run_RefusesWhatDoesNotApply(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "img.png", "not really")
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"img.png"}})
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(raw))
	assert.Contains(t, string(raw), "not_applicable")

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/nope/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"img.png"}})
	assert.Equal(t, http.StatusNotFound, status, string(raw))

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"missing.txt"}})
	assert.Equal(t, http.StatusNotFound, status, string(raw))
}

func TestAppPlugins_Run_ViewerCannotRunAWritingAction(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "x")
	u := seedSharedUser(t, f.store, "viewer@test.local", "ViewerPass1!")
	grant(t, f.store, f.st, u, "", model.GrantViewer, true)
	client := freshClient(t)
	testutil.LoginAs(t, f.srv, client, "viewer@test.local", "ViewerPass1!")

	status, raw := doReq(t, client, http.MethodGet, f.srv.URL+"/api/files/plugins/actions", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), "plugin:echo/upper", "the menu is the same; the gate is at run")

	status, raw = doReq(t, client, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"docs/a.txt"}})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Contains(t, string(raw), "permission_denied")
}

func TestAppPlugins_Run_ReadOnlyStorageRefusesWritingActions(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.st.ReadOnly = true
	require.NoError(t, f.store.UpdateStorage(context.Background(), f.st))
	f.writeFile(t, "a.txt", "x")
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"a.txt"}})
	assert.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "read_only")
}

func TestAppPlugins_View_OpenAndSubmit(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/views/echo/hello", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"type":"text"`)

	// A view opened on a file sees it adapter-qualified (what pdf-fields
	// src.path takes).
	f.writeFile(t, "docs/a.txt", "x")
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/views/echo/hello?path=main://docs/a.txt", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "hi open main://docs/a.txt")
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"event": "submit"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"done":true`)
}

func TestAppPlugins_Disabled_UserRoutes404_AdminListExplains(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.AppPlugins = nil
		d.AppPluginsDisabledReason = "app plugins are disabled on this instance (FILEX_APP_PLUGINS_DISABLED)"
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	status, _ := doReq(t, client, http.MethodGet, srv.URL+"/api/files/plugins/actions", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/app-plugins", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), `"enabled":false`)
	assert.Contains(t, string(raw), "FILEX_APP_PLUGINS_DISABLED")
	status, _ = doReq(t, client, http.MethodPost, srv.URL+"/api/admin/app-plugins", map[string]any{"github_repo": "x/y"})
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestAppPlugins_Demo_InstallRefused(t *testing.T) {
	f := newAppFixture(t, func(c *config.Config) { c.Demo.Mode = true })
	manifest, _ := os.ReadFile(filepath.Join(echoDir, "manifest.json"))
	wasm, _ := os.ReadFile(filepath.Join(echoDir, "echo.wasm"))
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	mf.Write(manifest)
	wf, _ := w.CreateFormFile("wasm", "plugin.wasm")
	wf.Write(wasm)
	w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// Two guards stand in the way, and either is enough: the admin-wide demo
	// read-only middleware answers first; the registry's own Demo flag would
	// answer demo_refused if a route ever bypassed it.
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))
	assert.True(t, strings.Contains(string(raw), "demo"), string(raw))
}

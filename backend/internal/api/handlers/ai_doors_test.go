package handlers_test

// The explorer's operations behind the AI surface (task #114): file_copy,
// app_actions / app_run / file_convert, ops_list / op_get / op_cancel,
// trash_list / trash_restore, file_versions / file_version_restore /
// file_snapshot, archive_create / archive_extract, share_list,
// file_request_create - and their REST twins under /api/ai.
//
// Measured on v0.48.1 before any of this existed (docs/research coverage
// audit, 2026-09-28): `unknown tool "file_convert"`, `file_unzip paket.tar.gz`
// → "not a zip", no way to list one's own links, no copy, no trash, no
// versions; an agent copied by file_read + file_write (8 MiB, metadata lost,
// the bytes through the conversation). Every test here goes red on that code
// because the tool or the route is not there - and each asserts what the
// operation DID (bytes on disk, rows, the answer's code), not a 2xx.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// doorFix is a single-tenant instance with two local storages ("main",
// "yan"), the operations queue RUNNING (a queued copy really copies), the
// trash and version history wired, an administrator and two members.
type doorFix struct {
	URL                string
	Store              db.Store
	Main, Yan, Rbac    *model.Storage
	RootMain, RootYan  string
	RootRbac           string
	MemberID, Member2  int64
	AdminID            int64
	Tok, Tok2, AdminTk string // member, second member, admin - each read,write,delete,mcp
}

func newDoorFix(t *testing.T) *doorFix {
	t.Helper()
	ctx := context.Background()

	sqlDB, raw := testutil.NewTestDB(t)
	accounting := quotastore.New(raw)
	var store db.Store = identitystore.New(accounting)

	rootMain, rootYan, rootRbac := t.TempDir(), t.TempDir(), t.TempDir()
	drvMain, drvYan, drvRbac := &local.Driver{}, &local.Driver{}, &local.Driver{}
	require.NoError(t, drvMain.Init(ctx, map[string]any{"root": rootMain}))
	require.NoError(t, drvYan.Init(ctx, map[string]any{"root": rootYan}))
	require.NoError(t, drvRbac.Init(ctx, map[string]any{"root": rootRbac}))
	mk := func(name, root string, rbac bool) *model.Storage {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, RBACEnabled: rbac,
			ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`),
		})
		require.NoError(t, err)
		return st
	}
	// main and yan have no access control (a member is an editor there);
	// rbac has it, for the item-permission tools.
	stMain, stYan, stRbac := mk("main", rootMain, false), mk("yan", rootYan, false), mk("rbac", rootRbac, true)
	resolver := func(id int64) (storage.Driver, error) {
		switch id {
		case stMain.ID:
			return drvMain, nil
		case stYan.ID:
			return drvYan, nil
		case stRbac.ID:
			return drvRbac, nil
		}
		return nil, fmt.Errorf("unknown storage id %d", id)
	}

	opsSvc := ops.New(sqlDB, resolver)
	require.NoError(t, opsSvc.Migrate(ctx))

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}

	notifSvc := notify.New(store, notify.Config{})
	t.Cleanup(notifSvc.Stop)

	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg:             cfg,
		Store:           tenantstore.New(store),
		Quota:           accounting.Quota(),
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		StorageResolver: resolver,
		Ops:             opsSvc,
		Trash:           trash.New(store, resolver, accounting.Quota()),
		Versions:        versioning.New(store, resolver),
		Notify:          notifSvc,
		LocalAuth:       localDrv,
	}))
	t.Cleanup(srv.Close)
	workerCtx, cancel := context.WithCancel(context.Background())
	go opsSvc.Run(workerCtx)
	t.Cleanup(func() {
		cancel()
		opsSvc.Stop()
	})

	adminID, _ := testutil.SeedAdminUser(t, store)
	member := func(email string) int64 {
		testutil.SeedRegularUser(t, store, email, "DoorPass!123")
		u, err := store.GetUserByEmail(ctx, email)
		require.NoError(t, err)
		return u.ID
	}
	f := &doorFix{
		URL: srv.URL, Store: store, Main: stMain, Yan: stYan, Rbac: stRbac,
		RootMain: rootMain, RootYan: rootYan, RootRbac: rootRbac,
		MemberID: member("door-member@test.local"), Member2: member("door-member2@test.local"),
		AdminID: adminID,
	}
	f.Tok = testutil.NewAPIToken(t, store, f.MemberID, "read,write,delete,mcp")
	f.Tok2 = testutil.NewAPIToken(t, store, f.Member2, "read,write,delete,mcp")
	f.AdminTk = testutil.NewAPIToken(t, store, adminID, "read,write,delete,mcp")
	return f
}

// put writes a file through /api/ai/upload, so it is on disk AND catalogued.
func (f *doorFix) put(t *testing.T, tok, p, content string) {
	t.Helper()
	restCall(t, f.URL, tok, "/api/ai/upload", map[string]any{"path": p, "content": content})
}

func (f *doorFix) read(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// tool calls one MCP tool and reads the answer for what it says.
func (f *doorFix) tool(t *testing.T, tok, name string, args any) e2eAITool {
	t.Helper()
	return doorCall(t, f.URL, tok, name, args)
}

// doorCall calls one MCP tool on the server at base and reads the answer for
// what it says - a JSON-RPC error (an unknown tool) fails the test.
func doorCall(t *testing.T, base, tok, name string, args any) e2eAITool {
	t.Helper()
	a, err := json.Marshal(args)
	require.NoError(t, err)
	code, body := mcpPost(t, &http.Client{}, base+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(a)+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	require.True(t, start >= 0 && end > start, body)
	var rpc struct {
		Error  *json.RawMessage `json:"error"`
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(body[start:end+1]), &rpc), body)
	require.Nil(t, rpc.Error, "%s: a JSON-RPC error, not a tool answer (no such tool?): %s", name, body)
	require.NotNil(t, rpc.Result, "%s: no result: %s", name, body)
	out := e2eAITool{IsError: rpc.Result.IsError, Structured: rpc.Result.Structured, Raw: body}
	for _, c := range rpc.Result.Content {
		out.Text += c.Text
	}
	return out
}

// doorResult unpacks a door tool's structured answer: {status, result}.
func doorResult(t *testing.T, tl e2eAITool) (int, map[string]any) {
	t.Helper()
	var out struct {
		Status int            `json:"status"`
		Result map[string]any `json:"result"`
	}
	require.NoError(t, json.Unmarshal(tl.Structured, &out), tl.Raw)
	return out.Status, out.Result
}

// rest sends one /api/ai call and decodes the answer.
func (f *doorFix) rest(t *testing.T, tok, method, p string, body any) (int, map[string]any) {
	t.Helper()
	resp := aiReq(t, http.DefaultClient, method, f.URL+p, tok, body)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// opIDOf reads op.id out of a queued answer ({op: {id}}).
func opIDOf(t *testing.T, res map[string]any) int64 {
	t.Helper()
	op, ok := res["op"].(map[string]any)
	require.True(t, ok, "no op in %v", res)
	id, ok := op["id"].(float64)
	require.True(t, ok && id > 0, "no op id in %v", res)
	return int64(id)
}

// waitOp follows an operation with op_get until it has ended.
func (f *doorFix) waitOp(t *testing.T, tok string, id int64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		tl := f.tool(t, tok, "op_get", map[string]any{"id": id})
		require.False(t, tl.IsError, tl.Text)
		_, op := doorResult(t, tl)
		switch op["status"] {
		case ops.StatusOK, ops.StatusPartial, ops.StatusFailed, ops.StatusCancelled:
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %d did not end: %v", id, op)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAIDoors_FileCopyNeverOverwritesAndCrossesStorages - file_copy is the
// explorer's paste: a taken name lands beside it, the source stays, and a
// copy to another storage carries the bytes.
func TestAIDoors_FileCopyNeverOverwritesAndCrossesStorages(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/a.txt", "hello")
	f.put(t, f.Tok, "main://docs/b.txt", "resident")

	tl := f.tool(t, f.Tok, "file_copy", map[string]any{"src": "main://docs/a.txt", "dst": "main://docs/b.txt"})
	require.False(t, tl.IsError, tl.Text)
	status, res := doorResult(t, tl)
	assert.Equal(t, http.StatusAccepted, status)
	op := f.waitOp(t, f.Tok, opIDOf(t, res))
	require.Equal(t, ops.StatusOK, op["status"], "%v", op)

	got, ok := f.read(t, f.RootMain, "docs/b.txt")
	require.True(t, ok)
	assert.Equal(t, "resident", got, "the file already at dst is never overwritten")
	got, ok = f.read(t, f.RootMain, "docs/b-copy.txt")
	require.True(t, ok, "the copy lands on a free name beside the taken one")
	assert.Equal(t, "hello", got)
	_, ok = f.read(t, f.RootMain, "docs/a.txt")
	assert.True(t, ok, "a copy leaves its source where it was")

	// Across storages, through the REST twin.
	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/copy", map[string]any{"src": "main://docs/a.txt", "dst": "yan://gelen/a.txt"})
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	op = f.waitOp(t, f.Tok, opIDOf(t, out))
	require.Equal(t, ops.StatusOK, op["status"], "%v", op)
	got, ok = f.read(t, f.RootYan, "gelen/a.txt")
	require.True(t, ok, "the bytes travelled to the other storage")
	assert.Equal(t, "hello", got)

	// A source that is not there: the queued copy does not end "ok".
	tl = f.tool(t, f.Tok, "file_copy", map[string]any{"src": "main://docs/yok.txt", "dst": "main://docs/x.txt"})
	require.False(t, tl.IsError, tl.Text)
	_, res = doorResult(t, tl)
	op = f.waitOp(t, f.Tok, opIDOf(t, res))
	assert.NotEqual(t, ops.StatusOK, op["status"], "copying nothing is not a success: %v", op)
	_, ok = f.read(t, f.RootMain, "docs/x.txt")
	assert.False(t, ok)
}

// TestAIDoors_OpsAreOnlyTheCallersOwn - #58's rule on the new tools: a member
// follows, lists and cancels only the operations they queued; another
// member's id answers NOT FOUND, as one that never existed.
func TestAIDoors_OpsAreOnlyTheCallersOwn(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/a.txt", "hello")
	tl := f.tool(t, f.Tok, "file_copy", map[string]any{"src": "main://docs/a.txt", "dst": "main://docs/c.txt"})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	id := opIDOf(t, res)
	f.waitOp(t, f.Tok, id)

	other := f.tool(t, f.Tok2, "op_get", map[string]any{"id": id})
	require.True(t, other.IsError, "another member must not read this operation: %s", other.Raw)
	assert.Contains(t, other.Text, "HTTP 404", other.Text)

	listed := f.tool(t, f.Tok2, "ops_list", map[string]any{})
	require.False(t, listed.IsError, listed.Text)
	_, l := doorResult(t, listed)
	rows, _ := l["ops"].([]any)
	for _, r := range rows {
		assert.NotEqual(t, float64(id), r.(map[string]any)["id"], "ops_list shows another member's operation")
	}
	mine := f.tool(t, f.Tok, "ops_list", map[string]any{})
	_, l = doorResult(t, mine)
	rows, _ = l["ops"].([]any)
	found := false
	for _, r := range rows {
		if r.(map[string]any)["id"] == float64(id) {
			found = true
		}
	}
	assert.True(t, found, "ops_list lists the caller's own operation: %v", l)

	// Stopping someone else's is the same NOT FOUND; stopping one's own that
	// has ended says FINISHED.
	stop := f.tool(t, f.Tok2, "op_cancel", map[string]any{"id": id})
	require.True(t, stop.IsError, stop.Raw)
	assert.Contains(t, stop.Text, "HTTP 404")
	stop = f.tool(t, f.Tok, "op_cancel", map[string]any{"id": id})
	require.True(t, stop.IsError, stop.Raw)
	assert.True(t, strings.HasPrefix(stop.Text, "FINISHED:"), "the code leads the text: %q", stop.Text)

	// REST twin.
	code, _ := f.rest(t, f.Tok2, http.MethodGet, fmt.Sprintf("/api/ai/ops/%d", id), nil)
	assert.Equal(t, http.StatusNotFound, code)
	code, out := f.rest(t, f.Tok, http.MethodGet, fmt.Sprintf("/api/ai/ops/%d", id), nil)
	assert.Equal(t, http.StatusOK, code, "%v", out)
}

// TestAIDoors_TrashListAndQueuedRestore - what file_delete trashed,
// trash_list shows and trash_restore brings back, on the queue.
func TestAIDoors_TrashListAndQueuedRestore(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/sil.txt", "geri gel")
	require.False(t, f.tool(t, f.Tok, "file_delete", map[string]any{"path": "main://docs/sil.txt"}).IsError)
	_, ok := f.read(t, f.RootMain, "docs/sil.txt")
	require.False(t, ok, "deleted")

	tl := f.tool(t, f.Tok, "trash_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	entries, _ := res["entries"].([]any)
	var id float64
	for _, e := range entries {
		m := e.(map[string]any)
		if m["name"] == "sil.txt" {
			id, _ = m["id"].(float64)
		}
	}
	require.NotZero(t, id, "trash_list lists the deleted file: %v", res)

	tl = f.tool(t, f.Tok, "trash_restore", map[string]any{"node_ids": []int64{int64(id)}})
	require.False(t, tl.IsError, tl.Text)
	status, res := doorResult(t, tl)
	require.Equal(t, http.StatusAccepted, status, "%v", res)
	queued, _ := res["ops"].([]any)
	require.Len(t, queued, 1, "%v", res)
	opID := int64(queued[0].(map[string]any)["id"].(float64))
	op := f.waitOp(t, f.Tok, opID)
	require.Equal(t, ops.StatusOK, op["status"], "%v", op)
	got, ok := f.read(t, f.RootMain, "docs/sil.txt")
	require.True(t, ok, "restored where it was")
	assert.Equal(t, "geri gel", got)

	// An empty batch is the caller's mistake (400), not a server fault.
	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/trash/restore", map[string]any{"node_ids": []int64{}})
	assert.Equal(t, http.StatusBadRequest, code, "%v", out)
}

// TestAIDoors_VersionsSnapshotAndRestore - file_snapshot keeps the content,
// file_versions lists it, file_version_restore brings it back.
func TestAIDoors_VersionsSnapshotAndRestore(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/rapor.txt", "birinci")

	tl := f.tool(t, f.Tok, "file_snapshot", map[string]any{"path": "main://docs/rapor.txt"})
	require.False(t, tl.IsError, tl.Text)
	f.put(t, f.Tok, "main://docs/rapor.txt", "ikinci")
	got, _ := f.read(t, f.RootMain, "docs/rapor.txt")
	require.Equal(t, "ikinci", got)

	tl = f.tool(t, f.Tok, "file_versions", map[string]any{"path": "main://docs/rapor.txt"})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	versions, _ := res["versions"].([]any)
	require.NotEmpty(t, versions, "the snapshot is in the history: %v", res)
	// The oldest version holds "birinci" (the snapshot), whatever a newer
	// overwrite-guard version holds.
	oldest := versions[len(versions)-1].(map[string]any)
	for _, v := range versions {
		m := v.(map[string]any)
		if m["id"].(float64) < oldest["id"].(float64) {
			oldest = m
		}
	}
	vid := int64(oldest["id"].(float64))

	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/versions/restore", map[string]any{"path": "main://docs/rapor.txt", "version_id": vid})
	require.Equal(t, http.StatusOK, code, "%v", out)
	got, _ = f.read(t, f.RootMain, "docs/rapor.txt")
	assert.Equal(t, "birinci", got, "the version came back")

	// A storage root has no versions: the caller's mistake, 400.
	code, _ = f.rest(t, f.Tok, http.MethodGet, "/api/ai/versions?path=main://", nil)
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestAIDoors_ArchiveExtractTarGzAndSkipsTheKeyFile - archive_extract opens
// the TAR family the ZIP-only file_unzip could not ("not a zip", measured),
// and from this keyless surface an archive member named like an encrypted
// folder's key file is skipped, as file_unzip skips it: it would make an
// ordinary folder look encrypted.
func TestAIDoors_ArchiveExtractTarGzAndSkipsTheKeyFile(t *testing.T) {
	f := newDoorFix(t)
	tgz := buildTarGz(t, map[string]string{"paket/oku.txt": "tar icinden", "paket/.filex-e2e.json": `{"v":2}`})
	restCall(t, f.URL, f.Tok, "/api/ai/upload", map[string]any{"path": "main://gelen/paket.tar.gz", "content_base64": base64.StdEncoding.EncodeToString(tgz)})

	tl := f.tool(t, f.Tok, "archive_extract", map[string]any{"path": "main://gelen/paket.tar.gz", "dest": "main://acilan"})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	op := f.waitOp(t, f.Tok, opIDOf(t, res))
	require.Contains(t, []any{ops.StatusOK, ops.StatusPartial}, op["status"], "%v", op)

	got, ok := f.read(t, f.RootMain, "acilan/paket/oku.txt")
	require.True(t, ok, "the tar.gz member was extracted")
	assert.Equal(t, "tar icinden", got)
	_, planted := f.read(t, f.RootMain, "acilan/paket/.filex-e2e.json")
	assert.False(t, planted, "a key file is never written from the AI surface")
}

// TestAIDoors_FileRequestAndShareList - file_request_create mints an upload
// link into a folder; share_list lists the caller's own links (and only
// theirs), with the token file_unshare takes.
func TestAIDoors_FileRequestAndShareList(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://gelen-kutusu/.keep-me.txt", "x")
	f.put(t, f.Tok2, "main://baskasi/b.txt", "y")

	tl := f.tool(t, f.Tok, "file_request_create", map[string]any{"path": "main://gelen-kutusu", "max_uploads": 3})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), "/d/", "a file request is an upload link (/d/): %s", tl.Raw)

	// The other member's own link must not show up in this member's list.
	code, out := f.rest(t, f.Tok2, http.MethodPost, "/api/ai/share", map[string]any{"path": "main://baskasi/b.txt"})
	require.Equal(t, http.StatusOK, code, "%v", out)
	otherToken, _ := out["token"].(string)
	require.NotEmpty(t, otherToken)

	tl = f.tool(t, f.Tok, "share_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), `"kind":"drop"`, "the file request is listed as one: %s", tl.Raw)
	assert.Contains(t, string(tl.Structured), "/d/", "its link is the upload address, not a /s/ link that answers not_found: %s", tl.Raw)
	assert.NotContains(t, string(tl.Structured), otherToken, "another member's link is never listed")

	// A file request needs a folder.
	tl = f.tool(t, f.Tok, "file_request_create", map[string]any{"path": "main://baskasi/b.txt"})
	assert.True(t, tl.IsError, "a file is not a folder to upload into: %s", tl.Raw)
}

// TestAIDoors_AppToolsSayWhenAppsAreOff - on an instance without app plugins
// the app tools answer with the explorer's code first, never a server fault
// or an unknown tool.
func TestAIDoors_AppToolsSayWhenAppsAreOff(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://docs/veri.csv", "a,b\n1,2\n")
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"file_convert", map[string]any{"path": "main://docs/veri.csv", "target": "xlsx"}},
		{"app_actions", map[string]any{"path": "main://docs/veri.csv"}},
		{"app_run", map[string]any{"plugin": "convert", "action": "convert", "paths": []string{"main://docs/veri.csv"}, "params": map[string]any{"target": "xlsx"}}},
	} {
		tl := f.tool(t, f.Tok, c.tool, c.args)
		require.True(t, tl.IsError, "%s: %s", c.tool, tl.Raw)
		assert.True(t, strings.HasPrefix(tl.Text, "APP_PLUGINS_DISABLED"), "%s: the code leads the text: %q", c.tool, tl.Text)
	}
	// file_convert's REST twin answers the same, and a missing target is 400.
	code, _ := f.rest(t, f.Tok, http.MethodPost, "/api/ai/convert", map[string]any{"path": "main://docs/veri.csv"})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestAIDoors_AConfinedTokenStaysInItsRoot - a `root:` token's bare paths
// resolve under its root and nothing outside it is copied, listed or packed.
func TestAIDoors_AConfinedTokenStaysInItsRoot(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://kutu/ic.txt", "icerde")
	f.put(t, f.Tok, "main://disari/gizli.txt", "disarida")
	confined := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,write,delete,mcp,root:main://kutu")

	tl := f.tool(t, confined, "file_copy", map[string]any{"src": "main://disari/gizli.txt", "dst": "kopya.txt"})
	require.True(t, tl.IsError, "a file outside the root is not copied in: %s", tl.Raw)
	_, ok := f.read(t, f.RootMain, "kutu/kopya.txt")
	assert.False(t, ok)

	tl = f.tool(t, confined, "file_copy", map[string]any{"src": "ic.txt", "dst": "ic-2.txt"})
	require.False(t, tl.IsError, "a bare path resolves under the root: %s", tl.Text)
	_, res := doorResult(t, tl)
	f.waitOp(t, confined, opIDOf(t, res))
	got, ok := f.read(t, f.RootMain, "kutu/ic-2.txt")
	require.True(t, ok)
	assert.Equal(t, "icerde", got)

	tl = f.tool(t, confined, "archive_create", map[string]any{"sources": []string{"main://disari/gizli.txt"}, "dest": "paket.zip"})
	require.True(t, tl.IsError, "packing a file outside the root into it: %s", tl.Raw)

	// The trash of a confined token is its root's.
	require.False(t, f.tool(t, f.Tok, "file_delete", map[string]any{"path": "main://disari/gizli.txt"}).IsError)
	tl = f.tool(t, confined, "trash_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.NotContains(t, string(tl.Structured), "gizli.txt", "a trashed file outside the root is not listed")
}

// TestAIDoors_EachToolAsksItsVerb - a read,mcp token is offered the door
// tools that read and none that change something; the REST twins refuse it.
func TestAIDoors_EachToolAsksItsVerb(t *testing.T) {
	f := newDoorFix(t)
	reader := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,mcp")
	code, body := mcpPost(t, &http.Client{}, f.URL+"/api/ai/mcp", reader, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	for _, name := range []string{"app_actions", "ops_list", "op_get", "trash_list", "file_versions", "share_list"} {
		assert.Contains(t, body, `"`+name+`"`, "read,mcp is offered %s", name)
	}
	for _, name := range []string{"file_copy", "app_run", "file_convert", "op_cancel", "trash_restore", "file_version_restore", "file_snapshot", "archive_create", "archive_extract", "file_request_create"} {
		assert.NotContains(t, body, `"`+name+`"`, "read,mcp must not be offered %s", name)
	}
	code, out := f.rest(t, reader, http.MethodPost, "/api/ai/copy", map[string]any{"src": "main://a", "dst": "main://b"})
	assert.Equal(t, http.StatusForbidden, code, "%v", out)
	assert.Contains(t, fmt.Sprint(out), "token missing scope: write")
}

// TestAIDoors_EncryptedFoldersKeepTheirRules - the AI surface's own E2E codes
// on the new tools (ai_e2e.go): nothing leaves an encrypted folder by copy or
// archive, nothing unencrypted lands in one without consent, ciphertext is
// not opened, and no upload link points into one.
func TestAIDoors_EncryptedFoldersKeepTheirRules(t *testing.T) {
	f := newE2eAIFixture(t)
	refused := func(name string, args map[string]any, code string) {
		t.Helper()
		tl := f.tool(t, name, args)
		require.True(t, tl.IsError, "%s must be refused: %s", name, tl.Raw)
		assert.True(t, strings.HasPrefix(tl.Text, code), "%s: want %s first, got %q", name, code, tl.Text)
	}
	refused("file_copy", map[string]any{"src": "depo://kasa/rapor.pdf", "dst": "depo://docs/rapor.pdf"}, "E2E_BOUNDARY")
	refused("file_copy", map[string]any{"src": "depo://docs/a.txt", "dst": "depo://kasa/.filex-e2e.json"}, "RESERVED_NAME")
	refused("archive_create", map[string]any{"sources": []string{"depo://kasa/rapor.pdf"}, "dest": "depo://docs/k.zip"}, "E2E_BOUNDARY")
	refused("archive_create", map[string]any{"sources": []string{"depo://docs/a.txt"}, "dest": "depo://kasa/a.zip"}, "E2E_PLAINTEXT_REFUSED")
	refused("archive_extract", map[string]any{"path": "depo://kasa/rapor.pdf"}, "E2E_ENCRYPTED")
	refused("app_actions", map[string]any{"path": "depo://kasa/rapor.pdf"}, "E2E_ENCRYPTED")
	refused("file_request_create", map[string]any{"path": "depo://kasa"}, "E2E_ENCRYPTED")
	assert.False(t, f.onDisk("docs/rapor.pdf"), "the ciphertext did not leave its folder")
}

// TestAIDoors_EachWriteLeavesTheRowItsRESTTwinLeaves - the door tools that
// change something leave ONE audit row each, the row their REST twin leaves,
// with what the handler said about it and `via: mcp`.
func TestAIDoors_EachWriteLeavesTheRowItsRESTTwinLeaves(t *testing.T) {
	f := newDoorFix(t)
	f.put(t, f.Tok, "main://m/a.txt", "x")
	f.put(t, f.Tok, "main://r/a.txt", "x")

	type step struct {
		tool, path, action string
		args               func(side string) map[string]any
		rest               func(side string) map[string]any
	}
	steps := []step{
		{"file_copy", "/api/ai/copy", "ai.file.copy",
			func(s string) map[string]any {
				return map[string]any{"src": "main://" + s + "/a.txt", "dst": "main://" + s + "/b.txt"}
			},
			func(s string) map[string]any {
				return map[string]any{"src": "main://" + s + "/a.txt", "dst": "main://" + s + "/b.txt"}
			}},
		{"file_snapshot", "/api/ai/versions/snapshot", "ai.file.version_snapshot",
			func(s string) map[string]any { return map[string]any{"path": "main://" + s + "/a.txt"} },
			func(s string) map[string]any { return map[string]any{"path": "main://" + s + "/a.txt"} }},
		{"file_request_create", "/api/ai/share/request", "ai.share.create",
			func(s string) map[string]any { return map[string]any{"path": "main://" + s} },
			func(s string) map[string]any { return map[string]any{"path": "main://" + s} }},
	}
	for _, st := range steps {
		before := len(mcpAuditRows(t, f.Store))
		tl := f.tool(t, f.Tok, st.tool, st.args("m"))
		require.False(t, tl.IsError, "%s: %s", st.tool, tl.Text)
		mcpRows := newRows(t, f.Store, before)
		require.Len(t, mcpRows, 1, "%s leaves exactly one row: %+v", st.tool, mcpRows)

		before = len(mcpAuditRows(t, f.Store))
		code, out := f.rest(t, f.Tok, http.MethodPost, st.path, st.rest("r"))
		require.Less(t, code, 300, "%s: %v", st.path, out)
		restRows := newRows(t, f.Store, before)
		require.Len(t, restRows, 1, "%s leaves exactly one row", st.path)

		m, r := mcpRows[0], restRows[0]
		assert.Equal(t, st.action, r.Action, st.path)
		assert.Equal(t, r.Action, m.Action, "%s is filed as its REST twin %s is", st.tool, st.path)
		assert.Equal(t, r.TargetType, m.TargetType, st.tool)
		assert.Equal(t, r.UserID, m.UserID, st.tool)
		assert.Equal(t, "mcp", m.Metadata["via"], st.tool)
		assert.Equal(t, "api", r.Metadata["via"], st.path)
		assert.Equal(t, keysExcept(r.Metadata, "via"), keysExcept(m.Metadata, "via"), "%s carries the detail its REST twin carries", st.tool)
	}

	// A read leaves nothing.
	before := len(mcpAuditRows(t, f.Store))
	f.tool(t, f.Tok, "ops_list", map[string]any{})
	f.tool(t, f.Tok, "trash_list", map[string]any{})
	f.tool(t, f.Tok, "share_list", map[string]any{})
	f.tool(t, f.Tok, "file_versions", map[string]any{"path": "main://m/a.txt"})
	assert.Empty(t, newRows(t, f.Store, before), "a read through a door tool writes no audit row")
}

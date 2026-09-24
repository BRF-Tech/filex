package handlers_test

// End-to-end tests for the AI REST surface and the MCP server against a
// real local-FS storage driver. Builds the full router so the
// APITokenMiddleware + RequireScope chain runs, then drives the documented
// JSON contract the work.example.com FilexClient depends on.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// aiFixture spins up the full router backed by an in-memory store + a
// tmp-dir local storage named "main", and returns the test server plus a
// full-access token bound to a fresh admin user.
func aiFixture(t *testing.T) (*httptest.Server, *http.Client, db.Store, string) {
	srv, client, store, tok, _ := aiFixtureConfigured(t, false)
	return srv, client, store, tok
}

func aiFixtureWithOps(t *testing.T) (*httptest.Server, *http.Client, db.Store, string, *ops.Service) {
	return aiFixtureConfigured(t, true)
}

func aiFixtureConfigured(t *testing.T, withOps bool) (*httptest.Server, *http.Client, db.Store, string, *ops.Service) {
	t.Helper()

	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": dir}))

	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(dir, `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(context.Background(), nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}
	var opsSvc *ops.Service
	if withOps {
		opsSvc = ops.New(sqlDB, resolver)
		require.NoError(t, opsSvc.Migrate(context.Background()))
	}

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		Ops:             opsSvc,
		StorageResolver: resolver,
		LocalAuth:       localDrv,
	}
	srv := httptest.NewServer(api.BuildRouter(deps))
	t.Cleanup(srv.Close)
	if opsSvc != nil {
		workerCtx, cancel := context.WithCancel(context.Background())
		go opsSvc.Run(workerCtx)
		t.Cleanup(func() {
			cancel()
			opsSvc.Stop()
		})
	}

	uid, _ := testutil.SeedAdminUser(t, store)
	// Every scope, named: an empty list grants NOTHING since v0.43.0
	// (model.APIToken.HasScope fails closed; apitoken.ParseIssued).
	tok := issueToken(t, store, uid, fullScopes, nil)

	return srv, &http.Client{}, store, tok, opsSvc
}

// aiReq issues an authenticated request to the AI namespace.
func aiReq(t *testing.T, client *http.Client, method, url, tok string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestAI_UploadReadListInfo(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// Upload a text file.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":    "main://notes/hello.txt",
		"content": "hello ai",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var up map[string]any
	testutil.ReadJSON(t, resp, &up)
	resp.Body.Close()
	entry, _ := up["entry"].(map[string]any)
	require.NotNil(t, entry)
	assert.Equal(t, "hello.txt", entry["name"])
	assert.Equal(t, float64(len("hello ai")), entry["size"])

	// Download the bytes back.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://notes/hello.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "hello ai", string(got))

	// Info.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://notes/hello.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	testutil.ReadJSON(t, resp, &info)
	resp.Body.Close()
	ie, _ := info["entry"].(map[string]any)
	assert.Equal(t, "file", ie["type"])

	// List the directory.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://notes", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls map[string]any
	testutil.ReadJSON(t, resp, &ls)
	resp.Body.Close()
	entries, _ := ls["entries"].([]any)
	require.Len(t, entries, 1)
}

func TestAI_MkdirMoveDelete(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// Mkdir.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://docs"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Upload then move.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://docs/a.txt", "content": "x",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://docs/a.txt", "dst": "main://docs/b.txt",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// b.txt exists, a.txt gone.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/b.txt", tok, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/a.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Delete (soft) — afterwards info should 404.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/delete", tok, map[string]any{"path": "main://docs/b.txt"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/b.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// TestAI_Move_SameStorage_DoesNotOverwriteWhatIsAlreadyThere — a rename onto a
// name the folder already holds.
//
// ⚠⚠ The identical defect the cross-storage arm had, one `if` higher up:
// `mv.Move(relSrc, relDst)` went straight to the driver, and a driver's Move
// onto an occupied path REPLACES what is there — a local rename does, and an
// object store's copy-then-delete does. Nothing trashed the displaced file, so
// it was simply gone. `ops.MoveDest` is the rule the queued worker and the
// manager's `?action=move` already obey; internal/ops/cross.go records the
// 2026-09-14 measurement that made it their rule, and this surface was the one
// caller that never got it.
func TestAI_Move_SameStorage_DoesNotOverwriteWhatIsAlreadyThere(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	sink := installWriteSink(t)

	// Different lengths on purpose: the size a row and an event carry is how
	// this test can tell WHICH file each one is really talking about.
	for _, f := range []struct{ path, body string }{
		{"main://docs/a.txt", "yenisi"},
		{"main://docs/b.txt", "eski"},
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
			"path": f.path, "content": f.body,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	}
	collectEvents(t, sink, 2) // the two uploads, drained

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://docs/a.txt", "dst": "main://docs/b.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "main://docs/b-copy.txt", entry["path"],
		"the answer names the path the file is on, not the one that was asked for")
	assert.Equal(t, "b-copy.txt", entry["name"])
	assert.Equal(t, "file", entry["type"])

	for p, want := range map[string]string{
		"main://docs/b.txt":      "eski",
		"main://docs/b-copy.txt": "yenisi",
	} {
		resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path="+p, tok, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assert.Equal(t, want, string(got), p)
	}
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/a.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "it was still a move: the old name is free again")
	resp.Body.Close()

	// ⭐ The catalogue followed the bytes. A row left at the REQUESTED path and
	// none at the real one is the quiet half of this bug: the listing and the
	// search index would describe a file that is not there and know nothing of
	// the one that is.
	ctx := context.Background()
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, storages)
	sid := storages[0].ID
	moved, err := store.GetNodeByPath(ctx, sid, pathkey.Hash(sid, "/docs/b-copy.txt"))
	require.NoError(t, err)
	require.NotNil(t, moved, "no cache row where the file actually landed")
	assert.Equal(t, int64(len("yenisi")), moved.Size, "the row is the file that moved, not the one that stayed")
	resident, err := store.GetNodeByPath(ctx, sid, pathkey.Hash(sid, "/docs/b.txt"))
	require.NoError(t, err)
	require.NotNil(t, resident, "the file that was already there keeps its row")
	assert.Nil(t, resident.DeletedAt)
	assert.Equal(t, int64(len("eski")), resident.Size)

	// …and so did the event.
	ev := collectEvents(t, sink, 1)[0]
	assert.Equal(t, notify.EventFileMoved, ev.Event)
	require.NotNil(t, ev.Node)
	assert.Equal(t, "/docs/b-copy.txt", ev.Node.Path,
		"a `file.moved` naming the requested path tells every subscriber the file is somewhere it is not")
	assert.Equal(t, "b-copy.txt", ev.Node.Name)
	assert.Equal(t, "/docs/b-copy.txt", ev.Meta["to"])
	require.NotNil(t, ev.Target)
	assert.Equal(t, "docs/b-copy.txt", ev.Target.Path)
}

// TestAI_Move_SameStorage_OntoItsOwnPathIsANoOp — de-colliding must not turn
// "move a onto a" into "make a second a".
//
// ⭐ `ops.MoveDest` answers `src` for this case on purpose and the AI move stops
// there: no driver call, no `file.moved`. A bare collision check would find the
// file itself sitting at the destination and helpfully rename it to
// `a-copy.txt` — a gesture that does nothing, renaming the user's file. The
// driver call is the worse half: on an object store a Move onto the same key is
// copy-then-delete of one object, which is how a file stops existing.
func TestAI_Move_SameStorage_OntoItsOwnPathIsANoOp(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://docs/a.txt", "content": "durur",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://docs/a.txt", "dst": "main://docs/a.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "main://docs/a.txt", entry["path"], "it is where it already was")
	assert.Equal(t, "a.txt", entry["name"])

	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://docs/a.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "the file is still at its own name")
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "durur", string(got), "same path, same bytes")

	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://docs", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls map[string]any
	testutil.ReadJSON(t, resp, &ls)
	resp.Body.Close()
	entries, _ := ls["entries"].([]any)
	assert.Len(t, entries, 1, "one file in, one file out — no `a-copy.txt` beside it")
}

// TestAI_Move_DeCollisionStaysInsideTheCallersGrants — the de-collision may not
// write anywhere the caller could not have written itself.
//
// ⚠⚠ Closing the overwrite hole opened the risk of a different one. The grant
// check at the top of Move asks about the path the CALLER named; the
// de-collision then retargets the write to a SIBLING of it. Grants are path
// prefixes and a prefix may be a single FILE (acl.Set.effective), so a caller
// holding "editor on docs/b.txt" and nothing else would have had `b-copy.txt`
// written on its behalf — a path no grant of theirs covers. Re-checking after
// the resolve is what keeps the fix from trading one data bug for an
// authorisation one.
func TestAI_Move_DeCollisionStaysInsideTheCallersGrants(t *testing.T) {
	srv, client, store, admin := aiFixture(t)
	ctx := context.Background()

	// Seed with the fixture's admin token, while the storage is still RBAC-off.
	for _, f := range []struct{ path, body string }{
		{"main://docs/a.txt", "tasinacak"},
		{"main://docs/b.txt", "yerinde"},
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", admin, map[string]any{
			"path": f.path, "content": f.body,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	}

	// Turn grants on; without this every RoleUser is editor everywhere
	// (acl.Set.effective short-circuits on !RBACEnabled) and the test proves
	// nothing.
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, storages)
	st := storages[0]
	st.RBACEnabled = true
	require.NoError(t, store.UpdateStorage(ctx, st))

	// A plain user with exactly two FILE-scoped grants: the file it moves and
	// the name it aims at. Nothing covers `docs/`, so nothing covers a sibling.
	hash, err := authlocal.HashPassword("TestGranteePass!1")
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, "grantee@test.local", hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	for _, p := range []string{"docs/a.txt", "docs/b.txt"} {
		_, gerr := store.CreateFileGrant(ctx, &model.FileGrant{
			StorageID: st.ID, PathPrefix: p, IsDir: false,
			UserID: u.ID, Level: model.GrantEditor,
		})
		require.NoError(t, gerr, p)
	}
	utok := issueToken(t, store, u.ID, "read,write,delete,mcp", nil)

	// b.txt is taken, so this would de-collide onto docs/b-copy.txt.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", utok, map[string]any{
		"src": "main://docs/a.txt", "dst": "main://docs/b.txt",
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, readBody(t, resp))

	// ⭐ And the refusal cost nothing: it lands before the driver is touched.
	for p, want := range map[string]string{
		"main://docs/a.txt": "tasinacak",
		"main://docs/b.txt": "yerinde",
	} {
		resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path="+p, admin, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assert.Equal(t, want, string(got), p)
	}
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/b-copy.txt", admin, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "the sibling was never written")
	resp.Body.Close()
}

// TestAI_Move_SameStorage_AFolderAnswersAsAFolder — the entry says what it
// really moved.
//
// ⚠ Both of Move's returns hard-coded `Type: "file"`, so moving a FOLDER
// answered `"type":"file"`. An agent believes the surface it is talking to: it
// reads "file", calls file_read on the path and is told "is a directory" by the
// same server that had just described it as a file. The words are `dir` and
// `file` — whatever `aiTypeOf` says, which is also what file_list and file_info
// answer, because an agent matches on the spelling.
func TestAI_Move_SameStorage_AFolderAnswersAsAFolder(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://proje/README.md", "content": "# proje",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://proje", "dst": "main://arsiv",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "dir", entry["type"], "a folder was moved, so the answer says folder")
	assert.Equal(t, "main://arsiv", entry["path"])

	// The spelling must be the one every other producer uses — read it back
	// off file_info rather than trusting the literal in this test.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://arsiv", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	testutil.ReadJSON(t, resp, &info)
	resp.Body.Close()
	ie, _ := info["entry"].(map[string]any)
	assert.Equal(t, ie["type"], entry["type"], "move and info must spell a directory the same way")

	// …and a plain file still answers "file".
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://arsiv/README.md", "dst": "main://arsiv/OKU.md",
	})
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ = body["entry"].(map[string]any)
	assert.Equal(t, "file", entry["type"])
}

func TestAI_UploadBase64Binary(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	// 0xDEADBEEF is not valid UTF-8 → must round-trip via base64.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":           "main://bin.dat",
		"content_base64": "3q2+7w==", // DE AD BE EF
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://bin.dat", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, got)
}

// ---------- MCP server smoke test ----------

// TestAI_MCP_InitializeAndListTools drives the streamable HTTP endpoint with
// raw JSON-RPC to confirm the transport, auth, and tool registration work.
func TestAI_MCP_InitializeAndListTools(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	post := func(payload string) (int, string) {
		req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(payload))
		req.Header.Set("X-Filex-Token", tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// initialize
	code, body := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	require.Equal(t, http.StatusOK, code, "mcp initialize body=%s", body)
	assert.Contains(t, body, `"serverInfo"`)
	assert.Contains(t, body, `"filex"`)

	// tools/list
	code, body = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, "mcp tools/list body=%s", body)
	for _, name := range []string{"file_list", "file_read", "file_write", "file_delete", "file_move", "file_mkdir", "file_search", "file_info"} {
		assert.Contains(t, body, name, "tools/list should advertise %s", name)
	}
}

func TestAI_MCP_NoToken_400(t *testing.T) {
	srv, client, _, _ := aiFixture(t)
	// getServer returns nil without a valid principal → SDK serves 4xx.
	req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// APITokenMiddleware rejects with 401 before the SDK handler runs.
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestAI_MCP_CallToolWrite exercises a real tools/call → file_write round
// trip and verifies the file lands on disk via the REST download path.
func TestAI_MCP_CallToolWrite(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	payload := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"file_write","arguments":{"path":"main://mcp.txt","content":"via mcp"}}}`
	req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(payload))
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "tools/call body=%s", string(b))
	assert.NotContains(t, string(b), `"isError":true`, "write tool should succeed: %s", string(b))

	// Confirm the bytes are really there.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://mcp.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "via mcp", string(got))
}

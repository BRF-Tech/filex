package main

// `filex client` against a fake server that speaks the routes the web
// explorer uses: the operations queue (/api/files/{move,copy} + /ops/{id}),
// the trash, versions, tags, app actions, archives and the caller's links.
//
// Every test drives the real cobra tree (clientCmd) with --url/--token, so it
// compiles against any build of the CLI and fails on behaviour.
//
// RED PROOF (task #118, before 0.50): `mv` refused a move between two storages
// with "cross-adapter move is not supported by the server" (the server has
// done it since 0.48.1), moved and renamed in two calls, and reported success
// for a move the server never finished; `cp`, `trash`, `versions`, `tag`,
// `actions`, `run`, `archive` and `share ls|rm` did not exist.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wireCall is one request the fake server received.
type wireCall struct {
	Method string
	Path   string // URL path
	Action string // ?action= on /api/files/manager
	Query  map[string]string
	Body   map[string]any
}

// fileServer is a fake filex with folders, an operations queue and the
// per-file extras. finalOp is the state every queued op ends in.
type fileServer struct {
	mu    sync.Mutex
	calls []wireCall

	// folders: `adapter://rel` → its entries (basename → catalogue id; 0 =
	// listed from the storage, uncatalogued). A folder is listable iff present.
	folders map[string]map[string]int64

	finalOp map[string]any // merged into the op GET /api/files/ops/{id} answers

	tags      map[string]any // GET /api/files/manager/tags?node_id= answer
	runAnswer map[string]any // POST …/run answer (default: a queued op)
	runStatus int

	archiveErr  map[string]any // POST /api/files/archive/extract refusal body
	archiveCode int
}

func newFileServer(t *testing.T) (*fileServer, *httptest.Server) {
	t.Helper()
	fs := &fileServer{
		folders: map[string]map[string]int64{
			"docs://":        {"inbox": 2, "archive": 3},
			"docs://inbox":   {"a.txt": 10, "rapor.pdf": 31, "veri.csv": 12, "p.7z": 13},
			"docs://archive": {"taken.txt": 20},
			"depo://docs":    {"b.txt": 40},
			"rbac://docs":    {},
		},
		finalOp: map[string]any{"status": "ok"},
	}
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	return fs, srv
}

func (fs *fileServer) record(r *http.Request) wireCall {
	c := wireCall{Method: r.Method, Path: r.URL.Path, Action: r.URL.Query().Get("action"), Query: map[string]string{}}
	for k := range r.URL.Query() {
		c.Query[k] = r.URL.Query().Get(k)
	}
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &c.Body)
	}
	fs.mu.Lock()
	fs.calls = append(fs.calls, c)
	fs.mu.Unlock()
	return c
}

func (fs *fileServer) reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (fs *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		fs.reply(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	c := fs.record(r)
	switch {
	case c.Path == "/api/files/manager" && r.Method == http.MethodGet && c.Action == "index":
		entries, ok := fs.folders[c.Query["path"]]
		if !ok {
			fs.reply(w, 404, map[string]string{"error": "directory not found"})
			return
		}
		files := []map[string]any{}
		for name, id := range entries {
			e := map[string]any{"path": strings.TrimSuffix(c.Query["path"], "/") + "/" + name, "basename": name, "type": "file", "size": 5}
			if id > 0 {
				e["id"] = id
			}
			files = append(files, e)
		}
		fs.reply(w, 200, map[string]any{"adapter": "docs", "storages": []string{"docs", "depo", "rbac"}, "files": files})
	case c.Path == "/api/files/manager" && r.Method == http.MethodPost:
		fs.reply(w, 200, map[string]any{"adapter": "docs", "files": []any{}})
	case c.Path == "/api/files/move" || c.Path == "/api/files/copy":
		kind := strings.TrimPrefix(c.Path, "/api/files/")
		fs.reply(w, 202, map[string]any{"op": map[string]any{"id": 7, "kind": kind, "status": "pending", "total": 1}})
	case strings.HasPrefix(c.Path, "/api/files/ops/"):
		op := map[string]any{"id": 7, "kind": "move", "total": 1, "done": 1}
		for k, v := range fs.finalOp {
			op[k] = v
		}
		fs.reply(w, 200, op)
	case c.Path == "/api/files/manager/trash":
		fs.reply(w, 200, map[string]any{"entries": []map[string]any{
			{"id": 41, "storage_id": 1, "storage_name": "docs", "path": "/inbox/eski.txt", "name": "eski.txt", "size": 5, "deleted_at": "2026-09-30T10:00:00Z", "deleted_by_self": true},
		}, "total": 1, "limit": 50, "offset": 0})
	case c.Path == "/api/files/manager/restore":
		fs.reply(w, 200, map[string]any{"ok": true})
	case c.Path == "/api/files/versions" && r.Method == http.MethodGet:
		fs.reply(w, 200, map[string]any{"node_id": 31, "versions": []map[string]any{
			{"id": 5, "node_id": 31, "version_n": 2, "size": 4, "created_at": "2026-09-30T10:00:00Z"},
		}})
	case c.Path == "/api/files/versions/restore":
		fs.reply(w, 200, map[string]any{"ok": true})
	case c.Path == "/api/files/manager/tags" && r.Method == http.MethodGet:
		fs.reply(w, 200, fs.tags)
	case c.Path == "/api/files/manager/tags" && r.Method == http.MethodPost:
		fs.reply(w, 200, map[string]any{"ok": true, "node_id": c.Body["node_id"], "items": c.Body["items"]})
	case c.Path == "/api/files/manager/tags/all":
		fs.reply(w, 200, map[string]any{"items": []map[string]any{{"name": "proje", "kind": "team"}}})
	case c.Path == "/api/files/manager/tagged":
		fs.reply(w, 200, map[string]any{"nodes": []map[string]any{{"id": 31, "storage": "docs", "path": "/inbox/rapor.pdf", "name": "rapor.pdf", "type": "file", "size": 5}}})
	case c.Path == "/api/files/plugins/actions":
		fs.reply(w, 200, map[string]any{"actions": []map[string]any{
			{"plugin": "convert", "id": "convert", "label": map[string]string{"en": "Convert", "tr": "Dönüştür"}, "applies": map[string]any{"ext": []string{"csv"}}},
		}})
	case strings.HasPrefix(c.Path, "/api/files/plugins/actions/") && strings.HasSuffix(c.Path, "/run"):
		if fs.runAnswer != nil {
			fs.reply(w, fs.runStatus, fs.runAnswer)
			return
		}
		fs.reply(w, 202, map[string]any{"op": map[string]any{"id": 7, "kind": "plugin-action", "status": "pending"}, "job_id": "j1"})
	case c.Path == "/api/files/archive/extract" || c.Path == "/api/files/archive/create":
		if fs.archiveErr != nil {
			fs.reply(w, fs.archiveCode, fs.archiveErr)
			return
		}
		fs.reply(w, 202, map[string]any{"op": map[string]any{"id": 7, "kind": "archive", "status": "pending"}})
	case c.Path == "/api/shares":
		fs.reply(w, 200, map[string]any{"items": []map[string]any{
			{"share": map[string]any{"id": 12, "token": "t12", "kind": "download", "has_pin": true, "download_count": 3}, "node_path": "/inbox/rapor.pdf", "storage_name": "docs", "url": "https://fm.example.com/s/t12"},
		}, "total": 1})
	case strings.HasPrefix(c.Path, "/api/files/share/") && r.Method == http.MethodDelete:
		fs.reply(w, 200, map[string]any{"ok": true})
	case c.Path == "/api/files/share" && r.Method == http.MethodPost:
		fs.reply(w, 200, map[string]any{"share": map[string]any{"id": 13, "token": "t13", "url": "https://fm.example.com/s/t13"}})
	default:
		fs.reply(w, 404, map[string]string{"error": "no route: " + r.Method + " " + c.Path})
	}
}

// mutations are the calls that change something: every POST, PUT and DELETE.
func (fs *fileServer) mutations() []wireCall {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []wireCall
	for _, c := range fs.calls {
		if c.Method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func (fs *fileServer) callsTo(path string) []wireCall {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []wireCall
	for _, c := range fs.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// runClient runs `filex client --url <srv> --token tok <args...>` with stdin.
func runClient(t *testing.T, srv *httptest.Server, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FILEX_CLI_CONFIG", filepath.Join(t.TempDir(), "cli.yaml"))
	t.Setenv("FILEX_URL", "")
	t.Setenv("FILEX_TOKEN", "")
	c := clientCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetIn(strings.NewReader(stdin))
	c.SetArgs(append([]string{"--url", srv.URL, "--token", "tok"}, args...))
	err := c.Execute()
	return out.String(), err
}

// ─────────────────── mv / cp ───────────────────

// A move between two storages is ONE request to the queue - the server has
// done it since 0.48.1. The CLI used to refuse it before sending anything.
func TestClientMv_AcrossStorages_IsOneQueuedMove(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "mv", "depo://docs/b.txt", "rbac://docs")
	require.NoError(t, err, out)

	muts := fs.mutations()
	require.Len(t, muts, 1, "one queued move, nothing else changes")
	assert.Equal(t, "/api/files/move", muts[0].Path)
	assert.Equal(t, []any{"depo://docs/b.txt"}, muts[0].Body["source"])
	assert.Equal(t, "rbac://docs", muts[0].Body["target"])
	assert.NotContains(t, muts[0].Body, "name", "into a folder: the item keeps its own name")
	require.NotEmpty(t, fs.callsTo("/api/files/ops/7"), "the CLI follows the op to its end")
	assert.Contains(t, out, "into rbac://docs")
}

// A move to another folder under another name is one step on the server
// (`name`), never a move followed by a rename that can stop half-way.
func TestClientMv_OtherFolderAndName_IsOneStep(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "mv", "docs://inbox/a.txt", "docs://archive/b.txt")
	require.NoError(t, err, out)

	muts := fs.mutations()
	require.Len(t, muts, 1, "a move and a rename in two calls is not atomic: %+v", muts)
	assert.Equal(t, "/api/files/move", muts[0].Path)
	assert.Equal(t, "docs://archive", muts[0].Body["target"])
	assert.Equal(t, "b.txt", muts[0].Body["name"])
	assert.Contains(t, out, "-> docs://archive/b.txt")
}

// A full target path whose name is taken is refused before anything moves:
// the queue would otherwise park the item beside it as taken-copy.txt.
func TestClientMv_TakenTargetPath_RefusedBeforeAnythingMoves(t *testing.T) {
	fs, srv := newFileServer(t)

	_, err := runClient(t, srv, "", "mv", "docs://inbox/a.txt", "docs://archive/taken.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Empty(t, fs.mutations(), "nothing may be sent for a taken target")
}

// In the same folder of the same storage `mv` stays the rename verb (409
// NAME_TAKEN, case-only renames). Regression guard.
func TestClientMv_SameFolder_StaysARename(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "mv", "docs://inbox/a.txt", "docs://inbox/b.txt")
	require.NoError(t, err, out)
	muts := fs.mutations()
	require.Len(t, muts, 1)
	assert.Equal(t, "rename", muts[0].Action)
	assert.Equal(t, "b.txt", muts[0].Body["name"])
}

// The exit status says what happened to the files: a move the server's
// operation failed is a failed command, with the server's reason.
func TestClientMv_FailedOperation_FailsTheCommand(t *testing.T) {
	fs, srv := newFileServer(t)
	fs.finalOp = map[string]any{"status": "failed", "failed": 1, "error": "destination storage is full"}

	_, err := runClient(t, srv, "", "mv", "docs://inbox/a.txt", "docs://archive")
	require.Error(t, err, "the old manager verb answered 200 and the CLI said Moved")
	assert.Contains(t, err.Error(), "destination storage is full")
}

func TestClientCp_IntoAFolder_QueuesACopy(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "cp", "docs://inbox/a.txt", "depo://docs")
	require.NoError(t, err, out)
	muts := fs.mutations()
	require.Len(t, muts, 1)
	assert.Equal(t, "/api/files/copy", muts[0].Path)
	assert.Equal(t, []any{"docs://inbox/a.txt"}, muts[0].Body["source"])
	assert.Equal(t, "depo://docs", muts[0].Body["target"])
	assert.Contains(t, out, "Copied docs://inbox/a.txt into depo://docs")
}

// Several sources go into a folder; anything else is refused before a copy.
func TestClientCp_SeveralSources_NeedAnExistingFolder(t *testing.T) {
	fs, srv := newFileServer(t)

	_, err := runClient(t, srv, "", "cp", "docs://inbox/a.txt", "docs://inbox/veri.csv", "docs://archive/new.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "existing folder")
	assert.Empty(t, fs.mutations())

	out, err := runClient(t, srv, "", "cp", "docs://inbox/a.txt", "docs://inbox/veri.csv", "docs://archive")
	require.NoError(t, err, out)
	assert.Len(t, fs.callsTo("/api/files/copy"), 2, "one queued copy per source")
}

// ─────────────────── trash / versions ───────────────────

func TestClientTrash_ListsAndRestoresByID(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "trash", "ls")
	require.NoError(t, err, out)
	assert.Contains(t, out, "41")
	assert.Contains(t, out, "docs://inbox/eski.txt", "the entry's original place, as adapter://path")
	assert.Contains(t, out, "you", "an entry the caller deleted says so")

	out, err = runClient(t, srv, "", "trash", "restore", "41")
	require.NoError(t, err, out)
	restores := fs.callsTo("/api/files/manager/restore")
	require.Len(t, restores, 1)
	assert.EqualValues(t, 41, restores[0].Body["node_id"])
}

// A file's versions are addressed by its catalogue id, which the CLI reads
// off its folder's listing: the person names the file, not a number.
func TestClientVersions_FindTheFileByItsPath(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "versions", "ls", "docs://inbox/rapor.pdf")
	require.NoError(t, err, out)
	gets := fs.callsTo("/api/files/versions")
	require.Len(t, gets, 1)
	assert.Equal(t, "31", gets[0].Query["node_id"])
	assert.Contains(t, out, "5", "the version id a restore names")

	out, err = runClient(t, srv, "", "versions", "restore", "docs://inbox/rapor.pdf", "5")
	require.NoError(t, err, out)
	restores := fs.callsTo("/api/files/versions/restore")
	require.Len(t, restores, 1)
	assert.EqualValues(t, 31, restores[0].Body["node_id"])
	assert.EqualValues(t, 5, restores[0].Body["version_id"])
	assert.Equal(t, true, restores[0].Body["snapshot_current"], "the live content is kept as a version first")
}

// ─────────────────── tags ───────────────────

// Adding a tag keeps the ones already there: the server's POST REPLACES the
// set, so a CLI that sent only the new name would wipe the team's tags.
func TestClientTag_AddKeepsTheTagsAlreadyThere(t *testing.T) {
	fs, srv := newFileServer(t)
	fs.tags = map[string]any{"node_id": 31, "items": []map[string]any{{"name": "proje", "kind": "team"}}}

	out, err := runClient(t, srv, "", "tag", "add", "docs://inbox/rapor.pdf", "acil")
	require.NoError(t, err, out)
	posts := fs.mutations()
	require.Len(t, posts, 1)
	assert.EqualValues(t, 31, posts[0].Body["node_id"])
	assert.Equal(t, []any{
		map[string]any{"name": "proje", "kind": "team"},
		map[string]any{"name": "acil", "kind": "personal"},
	}, posts[0].Body["items"])

	_, err = runClient(t, srv, "", "tag", "rm", "docs://inbox/rapor.pdf", "PROJE", "--personal")
	require.NoError(t, err)
	posts = fs.mutations()
	assert.Equal(t, []any{map[string]any{"name": "proje", "kind": "team"}}, posts[len(posts)-1].Body["items"],
		"--personal removes only a personal tag of that name; the team tag stays")
}

// ─────────────────── run / actions ───────────────────

func TestClientRun_SendsParamsAndPrintsWhatItWrote(t *testing.T) {
	fs, srv := newFileServer(t)
	fs.finalOp = map[string]any{"status": "ok", "message": "converted to Excel (.xlsx)", "outputs": []map[string]any{{"path": "docs://inbox/veri.xlsx"}}}

	out, err := runClient(t, srv, "", "run", "convert", "convert", "docs://inbox/veri.csv", "--param", "target=xlsx", "--param-json", "pages=3")
	require.NoError(t, err, out)
	runs := fs.callsTo("/api/files/plugins/actions/convert/convert/run")
	require.Len(t, runs, 1)
	assert.Equal(t, []any{"docs://inbox/veri.csv"}, runs[0].Body["paths"])
	assert.Equal(t, map[string]any{"target": "xlsx", "pages": float64(3)}, runs[0].Body["params"])
	assert.Contains(t, out, "Wrote docs://inbox/veri.xlsx")

	out, err = runClient(t, srv, "", "actions")
	require.NoError(t, err, out)
	assert.Contains(t, out, "convert")
	assert.Contains(t, out, "Convert")
}

// An action with a form answers the form when no parameters are given; the
// CLI says which flag carries the fields instead of reporting success.
func TestClientRun_FormWithoutParams_SaysWhatIsMissing(t *testing.T) {
	fs, srv := newFileServer(t)
	fs.runStatus, fs.runAnswer = 200, map[string]any{"surface": map[string]any{"title": "Request signatures"}}

	_, err := runClient(t, srv, "", "run", "sign", "request", "docs://inbox/rapor.pdf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--param")
	_, has := fs.callsTo("/api/files/plugins/actions/sign/request/run")[0].Body["params"]
	assert.False(t, has, "no parameters given, none sent")
}

// ─────────────────── archive ───────────────────

// The password comes from standard input, never from the command line (where
// the shell history and the process list keep it).
func TestClientArchive_PasswordFromStdin(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "gizli parola\n", "archive", "extract", "docs://inbox/p.7z", "docs://archive", "--password-stdin")
	require.NoError(t, err, out)
	ex := fs.callsTo("/api/files/archive/extract")
	require.Len(t, ex, 1)
	assert.Equal(t, "docs://inbox/p.7z", ex[0].Body["path"])
	assert.Equal(t, "docs://archive", ex[0].Body["dest"])
	assert.Equal(t, "gizli parola", ex[0].Body["password"])
	require.NotEmpty(t, fs.callsTo("/api/files/ops/7"))

	out, err = runClient(t, srv, "", "archive", "create", "docs://archive/out.7z", "docs://inbox", "--format", "7z")
	require.NoError(t, err, out)
	cr := fs.callsTo("/api/files/archive/create")
	require.Len(t, cr, 1)
	assert.Equal(t, "docs://archive/out.7z", cr[0].Body["dest"])
	assert.Equal(t, []any{"docs://inbox"}, cr[0].Body["sources"])
	assert.Equal(t, "7z", cr[0].Body["format"])
	assert.NotContains(t, cr[0].Body, "password")
}

// An encrypted archive's 401 is the archive's, not the session's: the CLI asks
// for the password and does not send the person to `filex client login`.
func TestClientArchive_EncryptedArchive_AsksForThePasswordNotALogin(t *testing.T) {
	fs, srv := newFileServer(t)
	fs.archiveCode, fs.archiveErr = 401, map[string]any{"error": "password required", "code": "PASSWORD_REQUIRED"}

	_, err := runClient(t, srv, "", "archive", "extract", "docs://inbox/p.7z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--password-stdin")
	assert.NotContains(t, err.Error(), "filex client login")
}

// ─────────────────── share ls / rm ───────────────────

func TestClientShare_ListsAndRevokesYourLinks(t *testing.T) {
	fs, srv := newFileServer(t)

	out, err := runClient(t, srv, "", "share", "ls")
	require.NoError(t, err, out)
	assert.Contains(t, out, "12")
	assert.Contains(t, out, "docs://inbox/rapor.pdf")
	assert.Contains(t, out, "https://fm.example.com/s/t12")

	out, err = runClient(t, srv, "", "share", "rm", "12")
	require.NoError(t, err, out)
	dels := fs.callsTo("/api/files/share/12")
	require.Len(t, dels, 1)
	assert.Equal(t, http.MethodDelete, dels[0].Method)

	// `share <path>` still creates a link.
	out, err = runClient(t, srv, "", "share", "docs://inbox/rapor.pdf")
	require.NoError(t, err, out)
	assert.Contains(t, out, "https://fm.example.com/s/t13")
}

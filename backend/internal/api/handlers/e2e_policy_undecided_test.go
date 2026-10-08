package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// When a lookup behind the rule fails, the rule cannot be decided, and a
// door answers that as a server failure of its own form, never as a policy
// refusal: a broken store must not read as the policy doing its job. One log
// line says who and where (the storage and the person) and what failed, never
// the path: a file's name can say as much as its contents.

// undecidedFix is the full router over one local storage, "main", for a
// signed-in administrator's API key. policyFails, when set, is what the
// rule's store answers for the e2e.policy setting; wrap, when set, wraps the
// store every handler is given.
type undecidedFix struct {
	url   string
	store db.Store
	st    *model.Storage
	root  string
	admin int64
	tok   string
}

func newUndecidedFix(t *testing.T, policyFails error, wrap func(db.Store) db.Store) *undecidedFix {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})
	cfg := config.Default()
	cfg.PublicURL = "http://test.local"

	handlerStore := store
	if wrap != nil {
		handlerStore = wrap(store)
	}
	policyStore := store
	if policyFails != nil {
		policyStore = dbtest.SettingFails(store, model.SettingE2EPolicy, policyFails)
	}
	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg: cfg, Store: handlerStore, Worker: syncpkg.New(store), Caps: capability.New(store),
		Share: share.NewService(store), StorageResolver: resolver, LocalAuth: localDrv,
		E2EPolicy: e2epolicy.New(e2epolicy.Options{Store: policyStore}),
	}))
	t.Cleanup(srv.Close)
	uid, _ := testutil.SeedAdminUser(t, store)
	return &undecidedFix{url: srv.URL, store: store, st: st, root: root, admin: uid, tok: issueToken(t, store, uid, fullScopes, nil)}
}

// slogged captures what slog writes for the rest of the test (the log
// package's output and flags are put back by hand: slog.SetDefault moves
// them). The buffer is locked: the queue or a server goroutine may log.
func slogged(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	prev, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// undecidedLines is what was logged about an undecided rule.
func undecidedLines(logs string) []string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "e2e policy: could not decide") {
			out = append(out, line)
		}
	}
	return out
}

// The agent's REST door, its MCP tool and an upload ticket answer an
// undecided rule as their own server failure: 500, a tool's error result in
// the rule's words, a ticket's 503. None passes the store's error on, nor lets
// its words ("… not found") make it a 404 or a 409, and none logs the path.
func TestE2EPolicy_AnUndecidedRuleIsAServerFailureAtTheAgentDoors(t *testing.T) {
	logs := slogged(t)
	f := newUndecidedFix(t, errors.New("settings row not found"), nil)

	resp := aiReq(t, &http.Client{}, http.MethodPost, f.url+"/api/ai/upload", f.tok, map[string]any{"path": "main://yeni.fxe", "content": "x"})
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, "the agent's upload: %s", raw)
	var got struct{ Error string }
	_ = json.Unmarshal(raw, &got)
	assert.Equal(t, "could not check the encryption policy", got.Error, "%s", raw)

	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "file_write", "arguments": map[string]any{"path": "main://ajan.fxe", "content": "x"}},
	})
	require.NoError(t, err)
	status, body := mcpPost(t, &http.Client{}, f.url+"/api/ai/mcp", f.tok, string(call))
	assert.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"isError":true`, "the MCP write went through: %s", body)
	assert.Contains(t, body, "could not check the encryption policy", body)

	// An upload ticket's redeem writes through the same funnel, and answers
	// its own server failure: 503 storage_unavailable, the ticket kept.
	status, minted := fxPost(t, f.url+"/api/ai/upload/ticket", f.tok, map[string]any{"path": "main://bilet.fxe"})
	require.Equal(t, http.StatusOK, status, minted)
	var ticket struct{ Ticket string }
	require.NoError(t, json.Unmarshal([]byte(minted), &ticket))
	req, err := http.NewRequest(http.MethodPut, f.url+"/u/"+ticket.Ticket, strings.NewReader("x"))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	redeemed, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "the ticket's redeem: %s", redeemed)
	assert.Contains(t, string(redeemed), "storage_unavailable")

	for _, out := range []string{string(raw), body, string(redeemed)} {
		assert.NotContains(t, out, "not found", "the store's error reached the caller")
	}
	names := []string{"yeni.fxe", "ajan.fxe", "bilet.fxe"}
	for _, name := range names {
		assert.NoFileExists(t, filepath.Join(f.root, name))
		assert.NotContains(t, logs(), name, "the log names the path")
	}
	lines := undecidedLines(logs())
	require.Len(t, lines, len(names), "one line for each undecided rule: %s", logs())
	for _, line := range lines {
		for _, want := range []string{"level=ERROR", "storage_id=" + strconv.FormatInt(f.st.ID, 10), "user_id=" + strconv.FormatInt(f.admin, 10), "settings row not found"} {
			assert.Contains(t, line, want)
		}
	}
}

// An HTTP route answers 500 for an undecided rule, and logs one line without
// the path.
func TestE2EPolicy_AnUndecidedRuleIsAServerFailureAtAnHTTPRoute(t *testing.T) {
	logs := slogged(t)
	f := newUndecidedFix(t, errors.New("database is locked"), nil)
	status, body := fxPost(t, f.url+"/api/files/save-text", f.tok, map[string]any{"path": "main://Gizli/" + e2eKeyFile, "content": kfOne})
	assert.Equal(t, http.StatusInternalServerError, status, body)
	// The one refusal shape (internal/apierr): the code, and the server's
	// sentence in `message` - never the rule's English error in `error`.
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got), body)
	assert.Equal(t, "e2e_policy_undecided", got["error"], body)
	assert.NotEmpty(t, got["message"], body)
	assert.NotContains(t, body, "database is locked", "the store's error reached the caller")
	assert.NoFileExists(t, filepath.Join(f.root, "Gizli", e2eKeyFile))
	lines := undecidedLines(logs())
	require.Len(t, lines, 1, "%s", logs())
	assert.Contains(t, lines[0], "database is locked")
	assert.NotContains(t, lines[0], "Gizli", "the log line names the path")
}

// userFails is a store whose GetUser fails for one account.
type userFails struct {
	db.Store
	id  int64
	err error
}

func (s *userFails) GetUser(ctx context.Context, id int64) (*model.User, error) {
	if id == s.id {
		return nil, s.err
	}
	return s.Store.GetUser(ctx, id)
}

// A lookup a door makes before it can ask the rule (here the file request's
// creator, whose rule a visitor's file is judged by) is no refusal either: the
// visitor gets the page's own server failure, 503 storage_unavailable, not a
// 403, and the log line names neither the folder nor the file. A creator who
// is gone is nobody, and that is still a refusal.
func TestE2EPolicy_AFailedLookupBeforeTheRuleIsNoRefusal(t *testing.T) {
	ctx := context.Background()
	logs := slogged(t)
	broken := &userFails{err: errors.New("connection reset by peer")}
	f := newUndecidedFix(t, nil, func(s db.Store) db.Store {
		broken.Store = s
		return broken
	})
	creator, err := f.store.CreateUser(ctx, "creator@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	broken.id = creator.ID
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "Gelen"), 0o755))
	gelen, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "Gelen", Path: "/Gelen", PathHash: pathkey.Hash(f.st.ID, "/Gelen"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	link, err := share.NewService(f.store).Create(ctx, share.CreateOpts{NodeID: gelen.ID, Kind: model.ShareKindDrop, CreatedBy: &creator.ID})
	require.NoError(t, err)
	drop := func(name string) (int, string) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		part, err := w.CreateFormFile("file[]", name)
		require.NoError(t, err)
		_, _ = part.Write([]byte(fxeBody))
		require.NoError(t, w.Close())
		return fxReq(t, http.MethodPost, f.url+"/d/"+link.Token, "", &b, w.FormDataContentType())
	}

	status, body := drop("gizli.fxe")
	assert.Equal(t, http.StatusServiceUnavailable, status, "a creator nobody could look up: %s", body)
	assert.Contains(t, body, `"storage_unavailable"`, body)
	assert.NotContains(t, body, "e2e_not_allowed", "answered as the rule's refusal: %s", body)
	lines := undecidedLines(logs())
	require.Len(t, lines, 1, "%s", logs())
	assert.Contains(t, lines[0], "connection reset by peer")
	assert.Contains(t, lines[0], "user_id="+strconv.FormatInt(creator.ID, 10))
	for _, leak := range []string{"Gelen", "gizli.fxe", "anon"} {
		assert.NotContains(t, logs(), leak, "the log names the drop")
	}

	broken.err = sql.ErrNoRows
	status, body = drop("gizli.fxe")
	assert.Equal(t, http.StatusForbidden, status, "a creator who is gone: %s", body)
	assert.Contains(t, body, string(e2epolicy.ReasonPermission), body)
	left, err := filepath.Glob(filepath.Join(f.root, "Gelen", "*", "gizli.fxe"))
	require.NoError(t, err)
	assert.Empty(t, left, "a refused drop landed")
}

// A member an archive extraction skips for the rule is logged without its
// name, by the explorer's extraction (a job of the queue) and the agent's
// unzip alike: a file's name can say as much as its contents.
func TestE2EPolicy_ASkippedMemberIsNotNamedInTheLog(t *testing.T) {
	f := newMTFix(t, false)
	admin, err := f.Store.GetUserByEmail(context.Background(), "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Acik"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, f.URL, tok, "alpha://Acik", "paket.zip", string(buildZip(t, map[string]string{"alt/gizli-rapor.fxe": fxeBody, "icerik.txt": "plain"})))
	encryptionOff(t, f.Store)
	logs := slogged(t)

	status, body = fxPost(t, f.URL+"/api/ai/unzip", tok, map[string]any{"src": "alpha://Acik/paket.zip", "dest": "alpha://AjanCikti"})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "Acik/paket.zip", "dest": "Cikti"})
	require.Equal(t, http.StatusAccepted, status, body)
	f.drainOps(t)
	assert.FileExists(t, filepath.Join(f.RootA, "Cikti", "icerik.txt"), "precondition: the extraction ran")
	assertNotWritten(t, f, "Cikti/alt/gizli-rapor.fxe")
	assertNotWritten(t, f, "AjanCikti/alt/gizli-rapor.fxe")

	skipped := 0
	for _, line := range strings.Split(logs(), "\n") {
		assert.NotContains(t, line, "gizli-rapor", "a log line names the member")
		if strings.Contains(line, "the encryption rule refused") {
			skipped++
		}
	}
	assert.Equal(t, 2, skipped, "each skip is still logged: %s", logs())
}

package dav

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"

	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// ───────────────────────────── test harness ───────────────────────────────

type harness struct {
	store    db.Store
	h        *Handler
	srv      *httptest.Server
	resolver func(int64) (storage.Driver, error)

	adminEmail string
	adminPass  string
}

// newHarness stands up an in-memory store, a local-driver storage rooted in
// a temp dir, and the /dav handler on an httptest server.
func newHarness(t *testing.T) *harness {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	adminEmail, adminPass := dbtest.SeedAdmin(t, store)

	drivers := map[int64]storage.Driver{}
	var mu sync.Mutex
	resolver := func(id int64) (storage.Driver, error) {
		mu.Lock()
		defer mu.Unlock()
		if d, ok := drivers[id]; ok {
			return d, nil
		}
		st, err := store.GetStorage(context.Background(), id)
		if err != nil {
			return nil, err
		}
		d, err := storage.Get(st.Driver)
		if err != nil {
			return nil, err
		}
		cfg := map[string]any{}
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return nil, err
		}
		if err := d.Init(context.Background(), cfg); err != nil {
			return nil, err
		}
		drivers[id] = d
		return d, nil
	}

	h := NewHandler(Config{
		Enabled:  true,
		Store:    store,
		Resolver: resolver,
		ACL:      acl.New(store),
	})
	mux := http.NewServeMux()
	mux.Handle(Prefix+"/", h)
	mux.Handle(Prefix, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &harness{store: store, h: h, srv: srv, resolver: resolver, adminEmail: adminEmail, adminPass: adminPass}
}

// addStorage creates an enabled local storage rooted in a fresh temp dir.
func (ha *harness) addStorage(t *testing.T, name string, readOnly, rbac bool) *model.Storage {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	st, err := ha.store.CreateStorage(context.Background(), &model.Storage{
		Name:       name,
		Driver:     "local",
		MountPath:  "/" + name,
		ConfigJSON: cfg,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
		ReadOnly:   readOnly,
	})
	require.NoError(t, err)
	if rbac {
		st.RBACEnabled = true
		require.NoError(t, ha.store.UpdateStorage(context.Background(), st))
	}
	return st
}

// mintToken creates an API token bound to userID and returns its plaintext.
func (ha *harness) mintToken(t *testing.T, userID int64, scopes string) string {
	t.Helper()
	raw := fmt.Sprintf("davtesttoken-%d-%d", userID, time.Now().UnixNano())
	_, err := ha.store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    userID,
		Label:     "dav-test",
		TokenHash: apitoken.HashToken(raw),
		Scopes:    scopes,
	})
	require.NoError(t, err)
	return raw
}

// req performs one WebDAV request with optional Basic credentials.
func (ha *harness) req(t *testing.T, method, path, user, pass, body string, hdr map[string]string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	r, err := http.NewRequest(method, ha.srv.URL+path, rdr)
	require.NoError(t, err)
	if user != "" {
		r.SetBasicAuth(user, pass)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := ha.srv.Client().Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func (ha *harness) nodeByPath(t *testing.T, storageID int64, rel string) *model.Node {
	t.Helper()
	n, _ := ha.store.GetNodeByPath(context.Background(), storageID, pathkey.Hash(storageID, normalizeDBPath(rel)))
	return n
}

// ─────────────────────────────── auth tests ───────────────────────────────

func TestBasicAuthRequired(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)

	resp := ha.req(t, "PROPFIND", "/dav/", "", "", "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("WWW-Authenticate"), `Basic realm="filex"`)

	resp = ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, "wrong-password", "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestBasicAuthWithPasswordAndToken(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)

	// Account password.
	resp := ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "depo")

	// API token as the Basic password.
	admin, err := ha.store.GetUserByEmail(context.Background(), ha.adminEmail)
	require.NoError(t, err)
	tok := ha.mintToken(t, admin.ID, "")
	resp = ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, tok, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)

	// Token bound to a DIFFERENT user's email → refused.
	dbtest.SeedRegularUser(t, ha.store, "other@test.local", "OtherPass!1")
	resp = ha.req(t, "PROPFIND", "/dav/", "other@test.local", tok, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRootConfinedTokenRefused(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)
	admin, err := ha.store.GetUserByEmail(context.Background(), ha.adminEmail)
	require.NoError(t, err)
	tok := ha.mintToken(t, admin.ID, "read,write,root:depo://sub")

	resp := ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, tok, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestTokenScopeGating(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)
	admin, err := ha.store.GetUserByEmail(context.Background(), ha.adminEmail)
	require.NoError(t, err)
	readTok := ha.mintToken(t, admin.ID, "read")

	// read scope may PROPFIND…
	resp := ha.req(t, "PROPFIND", "/dav/depo/", ha.adminEmail, readTok, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	// …but not PUT (missing write) nor DELETE (missing delete).
	resp = ha.req(t, http.MethodPut, "/dav/depo/x.txt", ha.adminEmail, readTok, "nope", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp = ha.req(t, http.MethodDelete, "/dav/depo/x.txt", ha.adminEmail, readTok, "", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ─────────────────────────── file operation tests ─────────────────────────

func TestPutGetRoundTripAndDBSync(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)

	resp := ha.req(t, http.MethodPut, "/dav/depo/hello.txt", ha.adminEmail, ha.adminPass, "merhaba dünya", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, http.MethodGet, "/dav/depo/hello.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "merhaba dünya", bodyString(t, resp))

	// Node cache row exists (best-effort sync ran).
	n := ha.nodeByPath(t, st.ID, "hello.txt")
	require.NotNil(t, n)
	require.Equal(t, model.NodeTypeFile, n.Type)
	require.Equal(t, int64(len("merhaba dünya")), n.Size)

	// Overwrite updates the same row.
	resp = ha.req(t, http.MethodPut, "/dav/depo/hello.txt", ha.adminEmail, ha.adminPass, "v2", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	n2 := ha.nodeByPath(t, st.ID, "hello.txt")
	require.NotNil(t, n2)
	require.Equal(t, n.ID, n2.ID)
	require.Equal(t, int64(2), n2.Size)
}

func TestMkcolListDelete(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)

	resp := ha.req(t, "MKCOL", "/dav/depo/klasor", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NotNil(t, ha.nodeByPath(t, st.ID, "klasor"))

	// MKCOL on an existing collection → 405 (RFC 4918).
	resp = ha.req(t, "MKCOL", "/dav/depo/klasor", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	// MKCOL with a missing intermediate → 409.
	resp = ha.req(t, "MKCOL", "/dav/depo/yok/alt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	resp = ha.req(t, http.MethodPut, "/dav/depo/klasor/a.txt", ha.adminEmail, ha.adminPass, "içerik", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, "PROPFIND", "/dav/depo/klasor/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "a.txt")

	// DELETE the folder — gone from driver and DB (soft-deleted subtree).
	fileNode := ha.nodeByPath(t, st.ID, "klasor/a.txt")
	require.NotNil(t, fileNode)
	resp = ha.req(t, http.MethodDelete, "/dav/depo/klasor", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp = ha.req(t, http.MethodGet, "/dav/depo/klasor/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Nil(t, ha.nodeByPath(t, st.ID, "klasor/a.txt"))
	require.Nil(t, ha.nodeByPath(t, st.ID, "klasor"))
}

// DAV DELETE lands in the filex trash (soft delete) instead of permanently
// destroying the bytes — and stays restorable via the trash service, with
// child paths mirrored correctly (GitHub issue #5).
func TestDeleteMovesToTrashAndRestore(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	ctx := context.Background()

	resp := ha.req(t, "MKCOL", "/dav/depo/klasor", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = ha.req(t, http.MethodPut, "/dav/depo/klasor/a.txt", ha.adminEmail, ha.adminPass, "çöp değil", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	dirNode := ha.nodeByPath(t, st.ID, "klasor")
	require.NotNil(t, dirNode)
	fileNode := ha.nodeByPath(t, st.ID, "klasor/a.txt")
	require.NotNil(t, fileNode)

	// DELETE: gone from DAV…
	resp = ha.req(t, http.MethodDelete, "/dav/depo/klasor", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp = ha.req(t, http.MethodGet, "/dav/depo/klasor/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Nil(t, ha.nodeByPath(t, st.ID, "klasor"))
	require.Nil(t, ha.nodeByPath(t, st.ID, "klasor/a.txt"))

	// …but the bytes are parked under .filex-trash on disk, NOT deleted.
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	root := cfg["path"].(string)
	matches, err := filepath.Glob(filepath.Join(root, ".filex-trash", "*__klasor", "a.txt"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "DAV DELETE dosyayı çöpe taşımalı, kalıcı silmemeli")

	// The trash bucket stays invisible over DAV.
	resp = ha.req(t, "PROPFIND", "/dav/depo/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.NotContains(t, bodyString(t, resp), ".filex-trash")

	// DB rows: soft-deleted + retagged; the child followed the folder and
	// the original paths are preserved in storage_key.
	gotDir, err := ha.store.GetNode(ctx, dirNode.ID)
	require.NoError(t, err)
	require.NotNil(t, gotDir.DeletedAt)
	require.Equal(t, "/klasor", gotDir.StorageKey)
	gotFile, err := ha.store.GetNode(ctx, fileNode.ID)
	require.NoError(t, err)
	require.NotNil(t, gotFile.DeletedAt)
	require.Equal(t, gotDir.Path+"/a.txt", gotFile.Path)
	require.Equal(t, "/klasor/a.txt", gotFile.StorageKey)

	// Restore via the trash service brings everything back.
	trashSvc := trash.New(ha.store, ha.resolver, nil)
	require.NoError(t, trashSvc.Restore(ctx, dirNode.ID))

	resp = ha.req(t, http.MethodGet, "/dav/depo/klasor/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "çöp değil", bodyString(t, resp))

	restored := ha.nodeByPath(t, st.ID, "klasor/a.txt")
	require.NotNil(t, restored)
	require.Equal(t, fileNode.ID, restored.ID)
	require.Nil(t, restored.DeletedAt)
}

// A second DELETE cycle at the SAME path must not conflict with the trashed
// rows of the first cycle (unique indexes are soft-delete-aware).
func TestDeleteRecreateDeleteAgain(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)

	for i := 0; i < 2; i++ {
		resp := ha.req(t, "MKCOL", "/dav/depo/tekrar", ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "cycle %d", i)
		resp = ha.req(t, http.MethodPut, "/dav/depo/tekrar/x.txt", ha.adminEmail, ha.adminPass, "v", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "cycle %d", i)
		require.NotNil(t, ha.nodeByPath(t, st.ID, "tekrar/x.txt"), "cycle %d", i)
		resp = ha.req(t, http.MethodDelete, "/dav/depo/tekrar", ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode, "cycle %d", i)
	}
	_ = st
}

func TestMoveRoundTrip(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)

	resp := ha.req(t, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "taşınacak", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, "MOVE", "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Destination": ha.srv.URL + "/dav/depo/b.txt", "Overwrite": "T"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, http.MethodGet, "/dav/depo/b.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "taşınacak", bodyString(t, resp))
	resp = ha.req(t, http.MethodGet, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// DB followed the move.
	require.Nil(t, ha.nodeByPath(t, st.ID, "a.txt"))
	moved := ha.nodeByPath(t, st.ID, "b.txt")
	require.NotNil(t, moved)
	require.Equal(t, "b.txt", moved.Name)

	// Cross-storage MOVE is refused.
	ha.addStorage(t, "ikinci", false, false)
	resp = ha.req(t, "MOVE", "/dav/depo/b.txt", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Destination": ha.srv.URL + "/dav/ikinci/b.txt"})
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

// ───────────────────────── authorization tests ────────────────────────────

func TestReadOnlyStorageWrites403(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "salt-oku", true, false)

	// Seed a file directly on disk so reads have something to hit.
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	require.NoError(t, os.WriteFile(filepath.Join(cfg["path"].(string), "ro.txt"), []byte("oku"), 0o644))

	resp := ha.req(t, http.MethodGet, "/dav/salt-oku/ro.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, m := range []string{http.MethodPut, "MKCOL", http.MethodDelete, "LOCK"} {
		resp := ha.req(t, m, "/dav/salt-oku/yeni.txt", ha.adminEmail, ha.adminPass, "x", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "method %s", m)
	}
}

func TestRBACStorageHiddenWithoutGrant(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "gizli", false, true)
	dbtest.SeedRegularUser(t, ha.store, "kullanici@test.local", "UserPass!1")

	// Root listing hides the storage; direct access 404s (no exists-oracle).
	resp := ha.req(t, "PROPFIND", "/dav/", "kullanici@test.local", "UserPass!1", "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.NotContains(t, bodyString(t, resp), "gizli")
	resp = ha.req(t, "PROPFIND", "/dav/gizli/", "kullanici@test.local", "UserPass!1", "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	// Writes are refused too (403 — write gate runs before visibility).
	resp = ha.req(t, http.MethodPut, "/dav/gizli/x.txt", "kullanici@test.local", "UserPass!1", "x", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Admin still sees it.
	resp = ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "gizli")
	_ = st
}

func TestRBACViewerGrantReadsButCannotWrite(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "proje", false, true)
	uid := dbtest.SeedUserWithRole(t, ha.store, "viewer@test.local", "ViewerPass!1", model.RoleUser)
	_, err := ha.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID:  st.ID,
		UserID:     uid,
		PathPrefix: "docs",
		Level:      model.GrantViewer,
	})
	require.NoError(t, err)

	// Admin seeds docs/rapor.txt through DAV.
	resp := ha.req(t, "MKCOL", "/dav/proje/docs", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = ha.req(t, http.MethodPut, "/dav/proje/docs/rapor.txt", ha.adminEmail, ha.adminPass, "rapor", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Viewer-granted user can read inside the grant…
	resp = ha.req(t, http.MethodGet, "/dav/proje/docs/rapor.txt", "viewer@test.local", "ViewerPass!1", "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "rapor", bodyString(t, resp))
	// …but cannot write there (viewer level)…
	resp = ha.req(t, http.MethodPut, "/dav/proje/docs/yeni.txt", "viewer@test.local", "ViewerPass!1", "x", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	// …and cannot read outside the grant.
	resp = ha.req(t, http.MethodPut, "/dav/proje/baska.txt", ha.adminEmail, ha.adminPass, "gizli veri", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp = ha.req(t, http.MethodGet, "/dav/proje/baska.txt", "viewer@test.local", "ViewerPass!1", "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ───────────────────────────── LOCK smoke test ────────────────────────────

func TestLockUnlockSmoke(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)

	resp := ha.req(t, http.MethodPut, "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "v1", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	lockBody := `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:">
  <D:lockscope><D:exclusive/></D:lockscope>
  <D:locktype><D:write/></D:locktype>
  <D:owner>dav-test</D:owner>
</D:lockinfo>`
	resp = ha.req(t, "LOCK", "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, lockBody,
		map[string]string{"Timeout": "Second-60", "Depth": "0"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	lockToken := resp.Header.Get("Lock-Token")
	require.NotEmpty(t, lockToken)

	// A write without the lock token is refused with 423 Locked.
	resp = ha.req(t, http.MethodPut, "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "hijack", nil)
	require.Equal(t, http.StatusLocked, resp.StatusCode)

	// With the token it succeeds.
	resp = ha.req(t, http.MethodPut, "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "v2",
		map[string]string{"If": "(" + strings.Trim(lockToken, "<>") + ")"})
	if resp.StatusCode != http.StatusCreated {
		// x/net/webdav expects the If token in <angle brackets> form.
		resp = ha.req(t, http.MethodPut, "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "v2",
			map[string]string{"If": "(" + lockToken + ")"})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	resp = ha.req(t, "UNLOCK", "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "",
		map[string]string{"Lock-Token": lockToken})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Unlocked again — plain PUT works.
	resp = ha.req(t, http.MethodPut, "/dav/depo/kilit.txt", ha.adminEmail, ha.adminPass, "v3", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

// ───────────────────────────── kill switch ────────────────────────────────

func TestKillSwitchDisabled(t *testing.T) {
	ha := newHarness(t)
	ha.h.cfg.Enabled = false
	resp := ha.req(t, "PROPFIND", "/dav/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ─────────────────────────── unit-level helpers ───────────────────────────

func TestSplitDavPath(t *testing.T) {
	cases := []struct {
		in        string
		name, rel string
		ok        bool
	}{
		{"/dav", "", "", true},
		{"/dav/", "", "", true},
		{"/dav/depo", "depo", "", true},
		{"/dav/depo/", "depo", "", true},
		{"/dav/depo/a/b.txt", "depo", "a/b.txt", true},
		{"/dav/depo/../etc", "depo", "etc", true}, // traversal collapses inside the storage
		{"/api/files", "", "", false},
	}
	for _, c := range cases {
		name, rel, ok := splitDavPath(c.in)
		require.Equal(t, c.ok, ok, c.in)
		require.Equal(t, c.name, name, c.in)
		require.Equal(t, c.rel, rel, c.in)
	}
}

func TestPathTraversalCannotEscapeStorage(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)

	// A crafted path that would escape the storage root resolves inside it
	// (path.Clean) — and the local driver double-guards. Nothing above the
	// storage root is reachable.
	resp := ha.req(t, http.MethodGet, "/dav/depo/..%2f..%2fetc%2fpasswd", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Regression: WebDAV extension verbs must survive the chi router — chi 405s
// unregistered methods before Mount handlers see them (caught live 2026-07-17;
// fixed by the RegisterMethod init in chimethods.go).
func TestChiMountPassesWebdavMethods(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "yerel", false, false)

	r := chi.NewRouter()
	r.Mount(Prefix, ha.h)
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest("PROPFIND", srv.URL+Prefix+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth(ha.adminEmail, ha.adminPass)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NotEqual(t, http.StatusMethodNotAllowed, resp.StatusCode,
		"chi swallowed PROPFIND with 405 — RegisterMethod init missing")
	require.Equal(t, 207, resp.StatusCode)
}

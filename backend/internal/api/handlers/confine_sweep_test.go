package handlers_test

// A token confined to one folder (`root:<adapter>://<rel>`) must not reach
// node data or file content OUTSIDE that folder — on ANY surface, not only the
// one whose report opened this audit (`/api/files/search`).
//
// confine.Middleware rewrites the `?path=`/body-path shapes, so the path-
// addressed reads and every mutation are already confined. The gap is the
// surfaces confine.Middleware cannot reach: the ones addressed by a numeric
// node/storage id (the id is not a path), and the listings/searches that walk a
// whole storage. thumb, versions, trash, shared-with-me and per-storage quota
// already close it with confine.Root.Within; these did not.
//
// Every probe binds the token to an ADMIN account on purpose: an admin's ACL
// clears every path, so nothing but the token's own `root:` ceiling can refuse
// the out-of-root request. That is the exact shape a host app hands an embed —
// one app token, minted by an operator, confined to the tenant's folder.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type confineFixture struct {
	srv      *httptest.Server
	store    db.Store
	token    string // confined to vault://sub
	subID    int64
	insideID int64
	outID    int64
	uid      int64
	insideC  string
	outC     string
}

func newConfineFixture(t *testing.T) *confineFixture {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	insideC := "INSIDE-SECRET-CONTENT-abc"
	outC := "OUTSIDE-SECRET-CONTENT-xyz"
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "inside.txt"), []byte(insideC), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte(outC), 0o644))

	var idx *search.Index
	srv, _, store := testutil.NewTestServerWith(t,
		func(c *config.Config) { c.PublicURL = "http://test.local" },
		func(d *api.Deps) {
			st := d.Store
			d.StorageResolver = func(id int64) (storage.Driver, error) {
				row, err := st.GetStorage(context.Background(), id)
				if err != nil || row == nil {
					return nil, fmt.Errorf("unknown storage %d", id)
				}
				var cfg map[string]any
				if err := json.Unmarshal(row.ConfigJSON, &cfg); err != nil {
					return nil, err
				}
				drv := &local.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			var oerr error
			idx, oerr = search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
			require.NoError(t, oerr)
			t.Cleanup(func() { _ = idx.Close() })
			d.Index = idx
		})
	useProductionAuthChain(t, store)

	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	vault, err := store.CreateStorage(ctx, &model.Storage{
		Name: "vault", Driver: "local", MountPath: "/vault",
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	sub, err := store.CreateNode(ctx, &model.Node{
		StorageID: vault.ID, Name: "sub", Path: "sub", Type: model.NodeTypeDirectory,
		PathHash: mutTestPathHash(vault.ID, "sub"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	inside, err := store.CreateNode(ctx, &model.Node{
		StorageID: vault.ID, ParentID: &sub.ID, Name: "inside.txt", Path: "sub/inside.txt",
		Type: model.NodeTypeFile, Size: int64(len(insideC)), Etag: "i", Mime: "text/plain",
		PathHash: mutTestPathHash(vault.ID, "sub/inside.txt"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	out, err := store.CreateNode(ctx, &model.Node{
		StorageID: vault.ID, Name: "outside.txt", Path: "outside.txt",
		Type: model.NodeTypeFile, Size: int64(len(outC)), Etag: "o", Mime: "text/plain",
		PathHash: mutTestPathHash(vault.ID, "outside.txt"), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	require.NoError(t, idx.IndexNode(ctx, inside))
	require.NoError(t, idx.IndexNodeContent(ctx, inside, insideC))
	require.NoError(t, idx.IndexNode(ctx, out))
	require.NoError(t, idx.IndexNodeContent(ctx, out, outC))

	uid, _ := testutil.SeedAdminUser(t, store)
	token := testutil.NewAPIToken(t, store, uid, "read,write,root:vault://sub")

	return &confineFixture{
		srv: srv, store: store, token: token, uid: uid,
		subID: sub.ID, insideID: inside.ID, outID: out.ID, insideC: insideC, outC: outC,
	}
}

func (f *confineFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	return f.do(t, http.MethodGet, path, "")
}

func (f *confineFixture) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", f.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestConfinement_NodeDataSurfaces — one confined token, every node-data door.
func TestConfinement_NodeDataSurfaces(t *testing.T) {
	f := newConfineFixture(t)
	inID := strconv.FormatInt(f.insideID, 10)
	outID := strconv.FormatInt(f.outID, 10)

	t.Run("search POST content: inside allowed, outside refused", func(t *testing.T) {
		st, body := f.do(t, http.MethodPost, "/api/files/search",
			`{"query":"INSIDE-SECRET","scope":"content"}`)
		require.Equal(t, http.StatusOK, st, body)
		assert.Contains(t, body, "inside.txt", "a confined token must still find its own files")

		st, body = f.do(t, http.MethodPost, "/api/files/search",
			`{"query":"OUTSIDE-SECRET","scope":"content"}`)
		require.Equal(t, http.StatusOK, st, body)
		assert.NotContains(t, body, "outside.txt", "an out-of-root name must not appear")
		assert.NotContains(t, body, f.outC, "an out-of-root content snippet must not appear")
	})

	t.Run("search GET name: outside refused", func(t *testing.T) {
		st, body := f.get(t, "/api/files/search?q=outside&scope=name&storage_id="+strconv.FormatInt(f.subID, 10))
		_ = st
		assert.NotContains(t, body, "outside.txt")
	})

	t.Run("toolbar search: outside refused, inside allowed", func(t *testing.T) {
		st, body := f.get(t, "/api/files/manager?action=search&path=vault://&filter=outside")
		require.Equal(t, http.StatusOK, st, body)
		assert.NotContains(t, body, "outside.txt", "toolbar search leaked an out-of-root file")

		st, body = f.get(t, "/api/files/manager?action=search&path=vault://&filter=inside")
		require.Equal(t, http.StatusOK, st, body)
		assert.Contains(t, body, "inside.txt", "toolbar search must still find in-root files")
	})

	t.Run("raw parent listing: outside refused, sub visible", func(t *testing.T) {
		st, body := f.get(t, "/api/files/manager?storage="+strconv.FormatInt(vaultOf(f), 10))
		require.Equal(t, http.StatusOK, st, body)
		assert.NotContains(t, body, "outside.txt", "raw parent listing leaked an out-of-root file")
	})

	t.Run("stat by id", func(t *testing.T) {
		st, _ := f.get(t, "/api/files/stat?id="+outID)
		assert.Equal(t, http.StatusNotFound, st, "stat of an out-of-root id must be refused")
		st, _ = f.get(t, "/api/files/stat?id="+inID)
		assert.Equal(t, http.StatusOK, st, "stat of an in-root id must still work")
	})

	t.Run("read bytes by id", func(t *testing.T) {
		st, body := f.get(t, "/api/files/read?id="+outID)
		assert.NotEqual(t, http.StatusOK, st, "read of an out-of-root id must be refused")
		assert.NotContains(t, body, f.outC, "out-of-root bytes must not be served")
		st, body = f.get(t, "/api/files/read?id="+inID)
		require.Equal(t, http.StatusOK, st)
		assert.Contains(t, body, f.insideC, "in-root bytes must still be served")
	})

	t.Run("share by id: outside refused", func(t *testing.T) {
		st, _ := f.do(t, http.MethodPost, "/api/files/share", `{"node_id":`+outID+`}`)
		assert.NotEqual(t, http.StatusOK, st, "minting a public link for an out-of-root file must be refused")
		_, body := f.get(t, "/api/files/share?node_id="+outID)
		assert.NotContains(t, body, "/s/", "listing an out-of-root file's links must not return a token")
	})

	t.Run("comments by id: outside refused", func(t *testing.T) {
		st, _ := f.get(t, "/api/files/comments?node_id="+outID)
		assert.Equal(t, http.StatusNotFound, st, "reading an out-of-root file's comments must be refused")
	})

	t.Run("meta write by id: outside refused, inside allowed", func(t *testing.T) {
		st, _ := f.do(t, http.MethodPost, "/api/files/manager/star",
			`{"node_id":`+outID+`,"starred":true}`)
		assert.Equal(t, http.StatusNotFound, st, "starring an out-of-root node must be refused")
		st, _ = f.do(t, http.MethodPost, "/api/files/manager/star",
			`{"node_id":`+inID+`,"starred":true}`)
		assert.Equal(t, http.StatusOK, st, "starring an in-root node must still work")

		st, _ = f.do(t, http.MethodPost, "/api/files/manager/tags",
			`{"node_id":`+outID+`,"tags":["x"]}`)
		assert.Equal(t, http.StatusNotFound, st, "tagging an out-of-root node must be refused")
	})

	t.Run("meta listings: outside filtered out", func(t *testing.T) {
		ctx := context.Background()
		// Plant the metadata directly, bypassing the (now-gated) write path, to
		// prove the READ listings filter what an older leak may have written.
		require.NoError(t, f.store.SetUserNodeMeta(ctx, f.uid, f.outID, "starred", "1"))
		require.NoError(t, f.store.SetUserNodeMeta(ctx, f.uid, f.outID, "opened",
			strconv.FormatInt(time.Now().Unix(), 10)))
		require.NoError(t, f.store.SetNodeTags(ctx, f.outID, []string{"secret"}))

		_, body := f.get(t, "/api/files/manager/star/list")
		assert.NotContains(t, body, "outside.txt", "starred listing leaked an out-of-root node")
		_, body = f.get(t, "/api/files/manager/recent")
		assert.NotContains(t, body, "outside.txt", "recent listing leaked an out-of-root node")
		_, body = f.get(t, "/api/files/manager/tagged?tag=secret")
		assert.NotContains(t, body, "outside.txt", "tagged listing leaked an out-of-root node")
	})
}

// vaultOf reports the storage id the fixture's nodes live in.
func vaultOf(f *confineFixture) int64 {
	n, err := f.store.GetNode(context.Background(), f.insideID)
	if err != nil || n == nil {
		return 0
	}
	return n.StorageID
}

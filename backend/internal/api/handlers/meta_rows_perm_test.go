package handlers_test

// The starred / recently-opened / tag listings carry `perm` and `read_only`
// on every row, like a folder listing does.
//
// The explorer renders ONE context menu (`selectionActionList`) and gates its
// write verbs — Rename, Delete, Move to, Share / Permissions — on the row's
// own `perm` and on the storage's `read_only`. A folder listing stamps both;
// these three listings stamped neither, so a row on Recent, Starred, a tag
// view or the Home cards had no level of its own and the menu fell back to
// whatever folder was open LAST — nothing on the landing page, so every write
// verb was missing (owner, 2026-09-19: "son kullanılanlar, ana sayfa gibi
// sayfalarda context menu eksik kalıyor. CONTEXT MENÜ HER YERDE AYNI OLMALI").
//
// Two storages, one writable and one read-only, so the assertion can tell
// "false" from "absent": `read_only` must be on the wire in BOTH cases.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestMetaLists_RowsCarryPermAndReadOnly(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	ctx := context.Background()
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	cfg, err := json.Marshal(map[string]any{"root": t.TempDir()})
	require.NoError(t, err)
	rw, err := store.CreateStorage(ctx, &model.Storage{
		Name: "rw-drive", Driver: "local", MountPath: "/rw",
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	ro, err := store.CreateStorage(ctx, &model.Storage{
		Name: "ro-drive", Driver: "local", MountPath: "/ro",
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
		ReadOnly: true,
	})
	require.NoError(t, err)

	mk := func(st *model.Storage, name string) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: name, Type: model.NodeTypeFile,
			Size: 3, Etag: name, Mime: "text/plain",
			PathHash: mutTestPathHash(st.ID, name), SeenAt: time.Now(),
		})
		require.NoError(t, err)
		return n
	}
	rwNode := mk(rw, "writable.txt")
	roNode := mk(ro, "frozen.txt")
	for _, n := range []*model.Node{rwNode, roNode} {
		require.NoError(t, store.SetUserNodeMeta(ctx, admin.ID, n.ID, "starred", "1"))
		require.NoError(t, store.SetUserNodeMeta(ctx, admin.ID, n.ID, "last_opened", "1758240000"))
		testutil.TagNode(t, store, n.ID, 0, "menu-everywhere")
	}

	type row struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Storage  string  `json:"storage"`
		Perm     *string `json:"perm"`
		ReadOnly *bool   `json:"read_only"`
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"starred", "/api/files/manager/star/list?limit=10"},
		{"recent", "/api/files/manager/recent?limit=10"},
		{"tagged", "/api/files/manager/tagged?tag=menu-everywhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.Get(srv.URL + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var body struct {
				Nodes []row `json:"nodes"`
			}
			require.NoError(t, json.Unmarshal(raw, &body), string(raw))
			require.Len(t, body.Nodes, 2, "%s: both seeded rows must be listed: %s", tc.path, string(raw))

			// The keys must be ON THE WIRE, not merely decodable: a missing
			// `read_only` decodes to nil and the explorer would treat an old
			// server's silence as "writable".
			var keys []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(json.RawMessage(rawField(t, raw, "nodes")), &keys))
			for _, k := range keys {
				assert.Contains(t, k, "perm", "%s: row lacks perm: %s", tc.path, string(raw))
				assert.Contains(t, k, "read_only", "%s: row lacks read_only: %s", tc.path, string(raw))
				assert.Contains(t, k, "storage", "%s: row lacks storage name: %s", tc.path, string(raw))
			}

			byName := map[string]row{}
			for _, r := range body.Nodes {
				byName[r.Name] = r
			}
			w, ok := byName["writable.txt"]
			require.True(t, ok, "writable row missing")
			require.NotNil(t, w.Perm)
			require.NotNil(t, w.ReadOnly)
			assert.Equal(t, "owner", *w.Perm, "an admin is owner everywhere")
			assert.False(t, *w.ReadOnly)
			assert.Equal(t, "rw-drive", w.Storage)

			f, ok := byName["frozen.txt"]
			require.True(t, ok, "read-only row missing")
			require.NotNil(t, f.Perm)
			require.NotNil(t, f.ReadOnly)
			assert.Equal(t, "owner", *f.Perm, "the level is left alone; read_only is the separate fact")
			assert.True(t, *f.ReadOnly, "a row on a read-only storage must say so")
			assert.Equal(t, "ro-drive", f.Storage)
		})
	}
}

// rawField returns the raw JSON of one top-level key.
func rawField(t *testing.T, body []byte, key string) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m))
	v, ok := m[key]
	require.True(t, ok, "missing %q in %s", key, string(body))
	return v
}

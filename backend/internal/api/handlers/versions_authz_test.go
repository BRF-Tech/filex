package handlers_test

// /api/files/versions had tenancy but no ownership or ACL check of any kind.
//
// ⚠ The Versions handler was the ONLY file surface in routes.go with no
// `AttachACL` call and no `acl.` reference anywhere in its file, and
// versioning.Service verifies only that the version belongs to the node it
// names before it overwrites the live bytes. So on a single-tenant install —
// the vast majority — there was no authorization at all:
//
//	POST /api/files/versions/restore  {"node_id":N,"version_id":V}
//
// answered 200 from a `viewer`-role account and the file's bytes on disk were
// replaced, and /snapshot answered 200 and wrote a new object into the storage.
// A read-only account could roll back, and force snapshots of, any file whose
// node id it could name.
//
// ⚠⚠ Restore is a destructive WRITE, so this file also pins that it goes
// through writehook — the pre-write guard (the bytes it is about to destroy are
// snapshotted first, and a snapshot that fails refuses the write) and the
// post-write gate (`file.updated`). It was the one write surface in filex that
// bypassed both.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	localdrv "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// ── harness ───────────────────────────────────────────────────────────────

const (
	vzLive   = "LIVE-CONTENT"
	vzOld    = "ROLLED-BACK-CONTENT"
	vzUserPW = "VersionsPass!1"
)

type vzFixture struct {
	srv     *httptest.Server
	store   db.Store
	storage *model.Storage
	root    string
	node    *model.Node
	version int64
}

// newVZFixture is a SINGLE-tenant install with a real local storage, a file
// with bytes on disk and one recorded version — so a restore is measurable as
// a byte change rather than inferred from a status code.
func newVZFixture(t *testing.T) *vzFixture {
	t.Helper()
	ctx := context.Background()

	srv, _, store := testutil.NewTestServerWith(t,
		func(c *config.Config) { c.MultiTenant = false },
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
				drv := &localdrv.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			d.Versions = versioning.New(d.Store, d.StorageResolver)
		})

	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data",
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	const live = "contract.txt"
	require.NoError(t, os.WriteFile(filepath.Join(root, live), []byte(vzLive), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".versions"), 0o755))
	oldKey := ".versions/" + live + ".v1"
	require.NoError(t, os.WriteFile(filepath.Join(root, oldKey), []byte(vzOld), 0o644))

	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: live, Path: live, Type: model.NodeTypeFile,
		Size: int64(len(vzLive)), Etag: "live", Mime: "text/plain",
		PathHash: mutTestPathHash(st.ID, live), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	v, err := store.CreateNodeVersion(ctx, &model.NodeVersion{
		NodeID: n.ID, VersionN: 1, Size: int64(len(vzOld)), Etag: "v1", StorageKey: oldKey,
	})
	require.NoError(t, err)

	return &vzFixture{srv: srv, store: store, storage: st, root: root, node: n, version: v.ID}
}

// asRole seeds an account with `role` and returns a logged-in client.
func (f *vzFixture) asRole(t *testing.T, role, email string) *http.Client {
	t.Helper()
	hash, err := local.HashPassword(vzUserPW)
	require.NoError(t, err)
	_, err = f.store.CreateUser(context.Background(), email, hash, role, "en", "UTC")
	require.NoError(t, err)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, client, email, vzUserPW)
	return client
}

func (f *vzFixture) liveBytes(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, f.node.Path))
	require.NoError(t, err)
	return string(b)
}

func (f *vzFixture) versionCount(t *testing.T) int {
	t.Helper()
	rows, err := f.store.ListNodeVersions(context.Background(), f.node.ID)
	require.NoError(t, err)
	return len(rows)
}

// ── the hole ──────────────────────────────────────────────────────────────

// TestVersions_ViewerCannotRestore.
//
// Red proof on the unfixed build, single-tenant, viewer account:
//
//	POST /api/files/versions/restore {"node_id":N,"version_id":V}  →  200 {"ok":true}
//
// and `contract.txt` on disk went from LIVE-CONTENT to ROLLED-BACK-CONTENT.
func TestVersions_ViewerCannotRestore(t *testing.T) {
	f := newVZFixture(t)
	viewer := f.asRole(t, model.RoleViewer, "readonly@test.local")

	status, body := doJSON(t, viewer, http.MethodPost,
		f.srv.URL+"/api/files/versions/restore",
		map[string]any{"node_id": f.node.ID, "version_id": f.version})
	require.Equal(t, http.StatusForbidden, status,
		"a read-only account must not be able to overwrite live bytes: %v", body)

	require.Equal(t, vzLive, f.liveBytes(t),
		"a 403 that still rewrote the file is not a fix")
}

// TestVersions_ViewerCannotSnapshot — the other write on this surface. It
// costs the owner storage and a retention slot on demand.
func TestVersions_ViewerCannotSnapshot(t *testing.T) {
	f := newVZFixture(t)
	viewer := f.asRole(t, model.RoleViewer, "readonly@test.local")
	before := f.versionCount(t)

	status, body := doJSON(t, viewer, http.MethodPost,
		f.srv.URL+"/api/files/versions/snapshot",
		map[string]any{"node_id": f.node.ID})
	require.Equal(t, http.StatusForbidden, status,
		"a read-only account must not be able to write a version: %v", body)
	require.Equal(t, before, f.versionCount(t), "no version row may have been written")
}

// TestVersions_ViewerMayStillReadTheTimeline — the deliberate asymmetry. A
// viewer can READ the file, so refusing them its history would be a
// regression dressed up as a fix; only the two WRITES are refused.
func TestVersions_ViewerMayStillReadTheTimeline(t *testing.T) {
	f := newVZFixture(t)
	viewer := f.asRole(t, model.RoleViewer, "readonly@test.local")

	status, body := doJSON(t, viewer, http.MethodGet,
		f.srv.URL+"/api/files/versions?node_id="+xtItoa(f.node.ID), nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	versions, _ := body["versions"].([]any)
	require.Len(t, versions, 1, "the viewer must still see the timeline: %v", body)
}

// ── single-tenant regression ──────────────────────────────────────────────

// TestVersions_EditorIsUnaffected is the honest half: it passes on `main` too.
// The change must cost an ordinary account nothing.
func TestVersions_EditorIsUnaffected(t *testing.T) {
	f := newVZFixture(t)
	user := f.asRole(t, model.RoleUser, "editor@test.local")

	t.Run("list", func(t *testing.T) {
		status, body := doJSON(t, user, http.MethodGet,
			f.srv.URL+"/api/files/versions?node_id="+xtItoa(f.node.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("snapshot", func(t *testing.T) {
		status, body := doJSON(t, user, http.MethodPost,
			f.srv.URL+"/api/files/versions/snapshot",
			map[string]any{"node_id": f.node.ID})
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("restore still rewrites the bytes", func(t *testing.T) {
		status, body := doJSON(t, user, http.MethodPost,
			f.srv.URL+"/api/files/versions/restore",
			map[string]any{"node_id": f.node.ID, "version_id": f.version})
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Equal(t, vzOld, f.liveBytes(t), "the restore must still restore")
	})
}

// TestVersions_UnknownNodeIsNotAnOracle — the refusal and the miss have to be
// the same answer, or the 403 tells an attacker which node ids exist.
func TestVersions_UnknownNodeIsNotAnOracle(t *testing.T) {
	f := newVZFixture(t)
	viewer := f.asRole(t, model.RoleViewer, "readonly@test.local")

	status, _ := doJSON(t, viewer, http.MethodGet,
		f.srv.URL+"/api/files/versions?node_id=999999", nil)
	require.Equal(t, http.StatusNotFound, status,
		"an id that does not exist must 404, not 200-with-an-empty-list")
}

// ── the pre-write guard ───────────────────────────────────────────────────

// TestVersions_RestoreSnapshotsTheBytesItReplaces.
//
// ⚠ Restore was the one write surface that never called
// writehook.BeforeOverwrite, so rolling back twice in a row destroyed whatever
// was live in between with nothing recorded. The router installs the guard by
// default (FILEX_VERSIONS_ON_OVERWRITE), so the assertion is simply that a
// restore leaves behind a version of the content it overwrote.
func TestVersions_RestoreSnapshotsTheBytesItReplaces(t *testing.T) {
	f := newVZFixture(t)
	user := f.asRole(t, model.RoleUser, "editor@test.local")
	before := f.versionCount(t)

	status, body := doJSON(t, user, http.MethodPost,
		f.srv.URL+"/api/files/versions/restore",
		map[string]any{"node_id": f.node.ID, "version_id": f.version})
	require.Equal(t, http.StatusOK, status, "%v", body)

	require.Equal(t, before+1, f.versionCount(t),
		"the pre-write guard must have snapshotted the bytes the restore replaced")

	// …and exactly one, not two: `snapshot_current` must not double up with
	// the guard that already did the same work.
	status, body = doJSON(t, user, http.MethodPost,
		f.srv.URL+"/api/files/versions/restore",
		map[string]any{"node_id": f.node.ID, "version_id": f.version, "snapshot_current": true})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, before+2, f.versionCount(t),
		"snapshot_current with the guard installed must not record the same bytes twice")
}

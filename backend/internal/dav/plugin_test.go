package dav

// WebDAV over a PLUGIN-backed storage.
//
// Why this file exists: the plugin driver was proven through the HTTP file
// manager, and /dav is a completely different consumer of storage.Driver —
// x/net/webdav drives it through a webdav.FileSystem, stats the storage root
// before anything else, writes through an os.File-shaped wrapper, and deletes
// by MOVING bytes into .filex-trash. None of those paths run in the file
// manager's tests. A plugin answers over a wire and returns its errors as
// JSON codes, so "the interface is satisfied" is not evidence that PROPFIND
// returns 207 or that DELETE keeps the bytes; only driving them is.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	"github.com/brf-tech/filex/backend/internal/quotastore"
)

// addPluginStorage registers a live plugin in the storage registry and
// creates a storage row on it. The row names the driver "plugin:<name>", so
// the harness resolver reaches it through storage.Get exactly as the server
// does in production — nothing here injects a driver behind the registry's
// back.
func (ha *harness) addPluginStorage(t *testing.T, p *testplugin.Plugin, name string) *model.Storage {
	t.Helper()
	driver := p.Register(t)
	cfg, _ := json.Marshal(map[string]any{"root": "/data"})
	st, err := ha.store.CreateStorage(context.Background(), &model.Storage{
		Name:       name,
		Driver:     driver,
		MountPath:  "/" + name,
		ConfigJSON: cfg,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st
}

// A plugin storage must answer the four verbs a WebDAV client actually uses
// on a first connect: PROPFIND to browse, GET to download, PUT to upload,
// DELETE to remove. The assertions deliberately look at the PLUGIN's own tree
// rather than at a second WebDAV response, so a surface that acknowledged a
// write without shipping the bytes cannot pass by reading back its own cache.
func TestPluginStorageServesWebDAV(t *testing.T) {
	ha := newHarness(t)
	p := testplugin.Start(t)
	ha.addPluginStorage(t, p, "eklenti")

	// Seeded straight into the plugin: the "somebody else put this in the
	// bucket" case, which is the only thing that proves the listing came from
	// the backend and not from filex's index.
	p.Seed("belgeler/rapor.txt", "plugin bytes")

	t.Run("propfind lists what the plugin holds", func(t *testing.T) {
		resp := ha.req(t, "PROPFIND", "/dav/eklenti/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
		require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
		body := bodyString(t, resp)
		require.Contains(t, body, "belgeler")

		resp = ha.req(t, "PROPFIND", "/dav/eklenti/belgeler/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
		require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
		body = bodyString(t, resp)
		require.Contains(t, body, "rapor.txt")
		// The size has to survive the protocol hop; a listing that reports
		// every file as 0 bytes still renders in a client, and still breaks
		// every sync tool that compares sizes.
		require.Contains(t, body, "<D:getcontentlength>12</D:getcontentlength>")
	})

	t.Run("get streams the plugin's bytes", func(t *testing.T) {
		resp := ha.req(t, http.MethodGet, "/dav/eklenti/belgeler/rapor.txt", ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "plugin bytes", bodyString(t, resp))
	})

	t.Run("put lands in the plugin", func(t *testing.T) {
		resp := ha.req(t, http.MethodPut, "/dav/eklenti/belgeler/yeni.txt", ha.adminEmail, ha.adminPass, "merhaba dünya", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		got, ok := p.Data("belgeler/yeni.txt")
		require.True(t, ok, "plugin tree after PUT: %v", p.Paths())
		require.Equal(t, "merhaba dünya", string(got))
	})

	t.Run("delete moves the bytes to trash inside the plugin", func(t *testing.T) {
		resp := ha.req(t, http.MethodDelete, "/dav/eklenti/belgeler/yeni.txt", ha.adminEmail, ha.adminPass, "", nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		require.False(t, p.Exists("belgeler/yeni.txt"), "plugin tree: %v", p.Paths())

		// DAV DELETE is a soft delete: trash.Put renames into .filex-trash on
		// the same driver. Over a plugin that rename is a protocol move, and
		// losing the bytes there would look identical to a successful delete
		// from the client's side — which is exactly why this asserts the
		// content is still reachable.
		var trashed []byte
		for _, pth := range p.Paths() {
			if strings.HasPrefix(pth, ".filex-trash/") {
				if b, ok := p.Data(pth); ok && string(b) == "merhaba dünya" {
					trashed = b
				}
			}
		}
		require.NotNil(t, trashed, "trashed copy not found in plugin tree: %v", p.Paths())
	})
}

// MKCOL then PUT then MOVE — the sequence every desktop WebDAV client runs
// when it creates a folder and drops a file in it. Move matters on its own
// because the plugin adapter can either forward it or emulate it with
// copy+delete, and both have to end with the bytes at the new path and
// nothing at the old one.
func TestPluginStorageMkcolAndMoveOverWebDAV(t *testing.T) {
	ha := newHarness(t)
	p := testplugin.Start(t)
	ha.addPluginStorage(t, p, "eklenti")

	resp := ha.req(t, "MKCOL", "/dav/eklenti/klasor/", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, http.MethodPut, "/dav/eklenti/klasor/a.txt", ha.adminEmail, ha.adminPass, "içerik", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, "MOVE", "/dav/eklenti/klasor/a.txt", ha.adminEmail, ha.adminPass, "", map[string]string{
		"Destination": ha.srv.URL + "/dav/eklenti/klasor/b.txt",
		"Overwrite":   "T",
	})
	require.Contains(t, []int{http.StatusCreated, http.StatusNoContent}, resp.StatusCode)

	require.False(t, p.Exists("klasor/a.txt"), "source still present: %v", p.Paths())
	got, ok := p.Data("klasor/b.txt")
	require.True(t, ok, "plugin tree after MOVE: %v", p.Paths())
	require.Equal(t, "içerik", string(got))
}

// A plugin that declares no write capability is handed to filex as a value
// WITHOUT a Write method, and /dav decides what a client may do by
// type-asserting exactly that. The guard here is a read-only plugin whose
// writes are refused at the gate with a 403 instead of reaching the backend
// and failing halfway — a partial write to a read-only store is the failure
// mode that leaves a zero-byte file behind.
func TestReadOnlyPluginRefusesWritesOverWebDAV(t *testing.T) {
	ha := newHarness(t)
	caps := testplugin.FullCaps()
	caps.Write, caps.Delete = false, false
	p := testplugin.Start(t, testplugin.WithCaps(caps))
	ha.addPluginStorage(t, p, "salt-okunur")
	p.Seed("var.txt", "okunabilir")

	resp := ha.req(t, http.MethodGet, "/dav/salt-okunur/var.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "okunabilir", bodyString(t, resp))

	resp = ha.req(t, http.MethodPut, "/dav/salt-okunur/yeni.txt", ha.adminEmail, ha.adminPass, "olmaz", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.False(t, p.Exists("yeni.txt"), "read-only plugin was written to: %v", p.Paths())
}

// A plugin process can die and come back; when it does it has forgotten its
// instance ids but not its data, and the adapter is supposed to re-create the
// instance and retry once. That recovery is invisible in the plugin package's
// own tests when a surface holds a driver for the lifetime of a connection —
// /dav caches the resolved driver per storage, so this is the case where a
// broken retry would strand a mounted drive until the server restarts.
func TestPluginRestartIsInvisibleToWebDAV(t *testing.T) {
	ha := newHarness(t)
	p := testplugin.Start(t)
	ha.addPluginStorage(t, p, "eklenti")
	p.Seed("a.txt", "önce")

	resp := ha.req(t, http.MethodGet, "/dav/eklenti/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "önce", bodyString(t, resp))

	p.Restart()

	resp = ha.req(t, http.MethodGet, "/dav/eklenti/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "önce", bodyString(t, resp))
	// Without this the test would still be green if Restart() quietly did
	// nothing: the retry path it means to exercise was never entered.
	require.Positive(t, p.NoInstanceCount(), "the plugin never reported a lost instance, so no retry was exercised")
}

// A plugin that is GONE — not restarted, gone — must not be reported as an
// empty folder. This is the failure that costs data: a WebDAV client that
// mirrors a directory and receives a successful 207 with no children in it
// concludes the remote tree was emptied and deletes its local copy to match.
// An error, whatever its number, makes it stop instead.
//
// ⚠ Measured while writing this (2026-08-19), and deliberately NOT asserted
// as desired behaviour: a GET for a single file while the backend is
// unavailable answers 404, because x/net/webdav's handleGetHeadPost maps ANY
// OpenFile error to StatusNotFound (webdav.go:202-205). That is not
// plugin-specific — an unreachable S3 or SFTP backend reads the same — so it
// belongs in a report, not in a test pinning it as intended.
func TestUnavailablePluginIsNotAnEmptyListingOverWebDAV(t *testing.T) {
	ha := newHarness(t)
	p := testplugin.Start(t)
	ha.addPluginStorage(t, p, "eklenti")
	p.Seed("belgeler/a.txt", "duruyor")

	resp := ha.req(t, "PROPFIND", "/dav/eklenti/belgeler/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "a.txt")

	p.Down()
	resp = ha.req(t, "PROPFIND", "/dav/eklenti/belgeler/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.NotEqual(t, http.StatusMultiStatus, resp.StatusCode,
		"an unavailable plugin answered a successful listing; a mirroring client would delete its local copy")

	// And it recovers on its own once the plugin answers again — no filex
	// restart, no re-saving the connection.
	p.Up()
	resp = ha.req(t, "PROPFIND", "/dav/eklenti/belgeler/", ha.adminEmail, ha.adminPass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), "a.txt")
}

// Quota accounting over a plugin storage.
//
// Why this needs a test at all: internal/quotastore never sees a driver — it
// decorates the STORE and counts the size on the node row, with the owner
// taken from the request context. That is exactly why a plugin can break it
// without touching it. A plugin write is streamed, and the size that ends up
// on the node row comes from whatever the surface knew at the time; if a
// plugin-backed write records 0 (or nothing at all), the user's usage stays
// flat, the ceiling never trips, and the first sign of trouble is a full disk.
func TestQuotaCountsBytesWrittenToAPluginStorage(t *testing.T) {
	ha := newHarnessStore(t, func(s db.Store) db.Store { return quotastore.New(s) })
	p := testplugin.Start(t)
	ha.addPluginStorage(t, p, "eklenti")

	ctx := context.Background()
	admin, err := ha.store.GetUserByEmail(ctx, ha.adminEmail)
	require.NoError(t, err)
	before, _, err := ha.store.GetUserUsage(ctx, admin.ID)
	require.NoError(t, err)

	body := "kotaya sayılacak baytlar"
	resp := ha.req(t, http.MethodPut, "/dav/eklenti/sayac.txt", ha.adminEmail, ha.adminPass, body, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// The bytes really are on the plugin — otherwise this would be measuring
	// bookkeeping for a write that never happened.
	got, ok := p.Data("sayac.txt")
	require.True(t, ok, "plugin tree: %v", p.Paths())
	require.Equal(t, body, string(got))

	after, _, err := ha.store.GetUserUsage(ctx, admin.ID)
	require.NoError(t, err)
	require.Equal(t, before+int64(len(body)), after,
		"usage moved by %d bytes, want %d", after-before, len(body))

	// And a delete gives them back. DAV's delete is a soft delete, so the
	// bytes stay in the trash and stay charged — releasing them here would
	// let a user park an unlimited amount of data in the trash for free.
	resp = ha.req(t, http.MethodDelete, "/dav/eklenti/sayac.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	trashed, _, err := ha.store.GetUserUsage(ctx, admin.ID)
	require.NoError(t, err)
	require.Equal(t, after, trashed, "trashing a file changed the usage; it should stay charged until it is purged")
}

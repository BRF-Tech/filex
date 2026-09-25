package handlers_test

// The explorer's listing of a folder the catalogue cannot vouch for
// (docs/LAZY-CATALOGUE.md): a storage whose first scan has not finished, and a
// lazily catalogued folder that is not watched. Both are listed from the
// storage with the catalogue overlaid; neither loses an entry, and neither
// loses what only the catalogue knows.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type lazyFx struct {
	srv    *httptest.Server
	store  db.Store
	st     *model.Storage
	root   string
	tok    string
	worker *syncpkg.Worker
}

// newListingFixture starts the router over one local storage. mode "" is an
// ordinary storage that has never been scanned; "lazy" runs the lazy engine.
func newListingFixture(t *testing.T, mode model.SyncMode, extra map[string]any) *lazyFx {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	dir := t.TempDir()
	cfg := map[string]any{"root": dir}
	for k, v := range extra {
		cfg[k] = v
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), cfg))
	if mode == "" {
		mode = model.SyncModeOnDemand
	}
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true, ConfigJSON: raw, SyncMode: mode,
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
	c := config.Default()
	c.PublicURL = "http://test.local"
	c.CORS.AllowedOrigins = []string{"*"}
	worker := syncpkg.New(store)
	deps := &api.Deps{
		Cfg: c, Store: store, Worker: worker, Caps: capability.New(store),
		Share: share.NewService(store), StorageResolver: resolver, LocalAuth: localDrv,
	}
	srv := httptest.NewServer(api.BuildRouter(deps))
	t.Cleanup(srv.Close)
	if mode == model.SyncModeLazy {
		require.NoError(t, worker.AddStorage(context.Background(), st))
		t.Cleanup(worker.Stop)
	}
	uid, _ := testutil.SeedAdminUser(t, store)
	tok := issueToken(t, store, uid, fullScopes, nil)
	return &lazyFx{srv: srv, store: store, st: st, root: dir, tok: tok, worker: worker}
}

func (f *lazyFx) write(t *testing.T, rel, body string) {
	t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

type listingEntry struct {
	ID          *int64 `json:"id"`
	Basename    string `json:"basename"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	SizePartial bool   `json:"size_partial"`
	Modified    int64  `json:"last_modified"`
}

type listingResp struct {
	Files       []listingEntry `json:"files"`
	StorageInfo []struct {
		Name     string                     `json:"name"`
		Coverage *syncpkg.CatalogueCoverage `json:"coverage"`
	} `json:"storage_info"`
}

func (f *lazyFx) index(t *testing.T, wire string) listingResp {
	t.Helper()
	status, body := fxReq(t, "GET", f.srv.URL+"/api/files/manager?action=index&path="+url.QueryEscape(wire), f.tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	var out listingResp
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	return out
}

func names(r listingResp) []string {
	out := make([]string, 0, len(r.Files))
	for _, e := range r.Files {
		out = append(out, e.Basename)
	}
	return out
}

func entry(r listingResp, name string) *listingEntry {
	for i := range r.Files {
		if r.Files[i].Basename == name {
			return &r.Files[i]
		}
	}
	return nil
}

// While a storage's first scan has not finished, a folder the scan has only
// partly reached is listed in full from the storage — the row it has keeps
// its id — and the listing says the catalogue is incomplete.
//
// Break: restore `len(nodes) == 0 &&` in front of the pre-sync hatch in
// vfIndex — the root lists the one catalogued entry.
func TestListing_FirstScanListsAPartlyCataloguedFolderInFull(t *testing.T) {
	f := newListingFixture(t, "", nil)
	for _, rel := range []string{"a.txt", "b.txt", "c.txt", "klasor/d.txt"} {
		f.write(t, rel, "x")
	}
	// The first scan has reached a.txt and nothing else.
	require.True(t, protocolsync.New(f.store, nil, nil, "test").Write(context.Background(), f.st, "a.txt", 1, "text/plain"))

	got := f.index(t, "main://")
	assert.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt", "klasor"}, names(got), "nothing looks missing mid-scan")
	a := entry(got, "a.txt")
	require.NotNil(t, a)
	require.NotNil(t, a.ID, "the entry the catalogue has keeps its id")
	assert.Nil(t, entry(got, "b.txt").ID)
	k := entry(got, "klasor")
	assert.True(t, k.SizePartial, "an uncatalogued folder's size is not known")
	assert.Zero(t, k.Size, "and it is never the directory entry's own few kilobytes")
	require.Len(t, got.StorageInfo, 1)
	require.NotNil(t, got.StorageInfo[0].Coverage)
	assert.Equal(t, syncpkg.CoverageFirstScan, got.StorageInfo[0].Coverage.Reason)
}

// Once the first scan has finished the catalogue is authoritative again: a
// row with no bytes behind it is listed (until the next scan), a file with no
// row is not, and no coverage note is sent.
func TestListing_AfterTheFirstScanTheCatalogueAnswers(t *testing.T) {
	f := newListingFixture(t, "", nil)
	f.write(t, "a.txt", "x")
	require.True(t, protocolsync.New(f.store, nil, nil, "test").Write(context.Background(), f.st, "a.txt", 1, "text/plain"))
	require.NoError(t, f.store.UpdateStorageSyncCursor(context.Background(), f.st.ID, time.Now(), ""))
	f.write(t, "sonradan.txt", "x")

	got := f.index(t, "main://")
	assert.Equal(t, []string{"a.txt"}, names(got))
	assert.Nil(t, got.StorageInfo[0].Coverage)
}

// A lazy storage: the first listing of a folder comes from the disk at once,
// the folder is catalogued behind it, and — once watched — the next listing
// comes from the catalogue with ids. A folder nobody opened is not catalogued.
func TestListing_LazyOpenCataloguesTheFolderBehindTheListing(t *testing.T) {
	f := newListingFixture(t, model.SyncModeLazy, map[string]any{"lazy_fill": "on_open"})
	f.write(t, "belgeler/rapor.pdf", "pdf")
	f.write(t, "belgeler/not.txt", "not")
	f.write(t, "arsiv/eski.txt", "eski")
	require.Eventually(t, func() bool { lazy, _ := f.worker.CatalogueCurrent(f.st.ID, "/"); return lazy }, 5*time.Second, 10*time.Millisecond)

	first := f.index(t, "main://belgeler")
	assert.ElementsMatch(t, []string{"not.txt", "rapor.pdf"}, names(first), "listed from the disk at once")
	require.Eventually(t, func() bool {
		_, cur := f.worker.CatalogueCurrent(f.st.ID, "/belgeler")
		return cur
	}, 10*time.Second, 20*time.Millisecond, "the opened folder is catalogued and watched")
	second := f.index(t, "main://belgeler")
	require.NotNil(t, entry(second, "rapor.pdf").ID, "now from the catalogue")

	n, _ := f.store.GetNodeByPath(context.Background(), f.st.ID, pathkey.Hash(f.st.ID, "/arsiv/eski.txt"))
	assert.Nil(t, n, "behaviour B: a folder nobody opened is not catalogued")
	require.NotNil(t, second.StorageInfo[0].Coverage)
	assert.Equal(t, syncpkg.CoverageVisitedOnly, second.StorageInfo[0].Coverage.Reason)
}

// The desktop sync engine plans from a listing and sends its signature back
// as the upload precondition. When that listing came from the disk (no row),
// the precondition is judged against the disk; `none` is refused when a file
// is already there.
//
// Break: drop the statOnStorage fallback in uploadExpectHolds — the first
// upload answers 412 and the second overwrites a file it did not know about.
func TestUploadPrecondition_JudgesAListingThatCameFromTheDisk(t *testing.T) {
	f := newListingFixture(t, "", nil)
	f.write(t, "diskte.txt", "eski icerik")
	got := f.index(t, "main://")
	e := entry(got, "diskte.txt")
	require.NotNil(t, e)
	require.Nil(t, e.ID, "no row: the listing came from the disk")

	upload := func(name, expect string) int {
		var b strings.Builder
		b.WriteString("--X\r\nContent-Disposition: form-data; name=\"path\"\r\n\r\nmain://\r\n")
		b.WriteString("--X\r\nContent-Disposition: form-data; name=\"expect\"\r\n\r\n" + expect + "\r\n")
		b.WriteString("--X\r\nContent-Disposition: form-data; name=\"file[]\"; filename=\"" + name + "\"\r\n\r\nyeni icerik\r\n--X--\r\n")
		status, _ := fxReq(t, "POST", f.srv.URL+"/api/files/manager?action=upload", f.tok, strings.NewReader(b.String()), "multipart/form-data; boundary=X")
		return status
	}
	assert.Equal(t, http.StatusOK, upload("diskte.txt", fmt.Sprintf("%d:%d", e.Size, e.Modified)),
		"the client saw exactly what is on the disk")

	f.write(t, "baskasi.txt", "birinin dosyasi")
	assert.Equal(t, http.StatusPreconditionFailed, upload("baskasi.txt", "none"),
		"`none` asked for an empty spot, and there is a file there")
	b, err := os.ReadFile(filepath.Join(f.root, "baskasi.txt"))
	require.NoError(t, err)
	assert.Equal(t, "birinin dosyasi", string(b))
}

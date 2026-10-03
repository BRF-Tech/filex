package handlers_test

// Admin → Tools → Thumbnail repair (POST/GET /api/admin/tools/thumbnails/
// repair). The walk is replaced by a fake: these hold the door — who may ask,
// for which storages, with what, and what they are told.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type recordingRepairer struct {
	mu   sync.Mutex
	jobs []ops.ThumbRepairJob
	hold chan struct{}
}

func (r *recordingRepairer) CountRepair(context.Context, ops.ThumbRepairJob) (int, error) {
	return 4, nil
}

func (r *recordingRepairer) RunRepair(ctx context.Context, job ops.ThumbRepairJob, progress func(ops.ThumbRepairCounts)) (ops.ThumbRepairCounts, error) {
	r.mu.Lock()
	r.jobs = append(r.jobs, job)
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
	}
	return ops.ThumbRepairCounts{Processed: 4, OK: 3, Skipped: 1}, nil
}

func (r *recordingRepairer) last() ops.ThumbRepairJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[len(r.jobs)-1]
}

func repairServer(t *testing.T, multiTenant bool, rep *recordingRepairer) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()
	return testutil.NewTestServerWith(t,
		func(c *config.Config) { c.MultiTenant = multiTenant },
		func(d *api.Deps) {
			opsDB, _ := testutil.NewTestDB(t)
			d.Ops = ops.New(opsDB, d.StorageResolver)
			d.Ops.SetThumbRepairer(rep)
			t.Cleanup(d.Ops.Stop)
		})
}

const repairURL = "/api/admin/tools/thumbnails/repair"

func TestThumbRepair_TenantAdminReachesOnlyTheirStorages(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, true, rep)
	mine := seedFullTenant(t, store, "globex")
	theirs := seedFullTenant(t, store, "initech")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+repairURL,
		map[string]any{"path": theirs.storage.Name + "://", "mode": "fix"})
	require.Equal(t, http.StatusNotFound, status, "another tenant's storage: %v", body)

	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL,
		map[string]any{"path": mine.storage.Name + "://", "mode": "rebuild"})
	require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, status, "%v", body)
	job := rep.last()
	assert.Equal(t, mine.storage.ID, job.StorageID)
	assert.Equal(t, ops.ThumbRepairRebuild, job.Mode)
	assert.NotContains(t, job.Reach, theirs.storage.ID)

	waitRepairIdle(t, client, srv.URL)
	// "Every storage" from a tenant administrator is the tenant's storages.
	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"mode": "fix"})
	require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, status, "%v", body)
	job = rep.last()
	assert.Zero(t, job.StorageID)
	require.NotNil(t, job.Reach, "an unscoped reach would be every storage on the instance")
	assert.Contains(t, job.Reach, mine.storage.ID)
	assert.NotContains(t, job.Reach, theirs.storage.ID)
}

func TestThumbRepair_ToldWhatIsWrongWithTheAsk(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	st := plainStorage(t, store, "arsiv")
	seedNodeIn(t, store, st.ID, "/kapak.png")

	status, body := doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://Yok", "mode": "fix"})
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "PATH_NOT_FOUND", body["code"])

	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://.versions", "mode": "fix"})
	assert.Equal(t, http.StatusNotFound, status, "filex's own folders are not a scope: %v", body)

	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://", "mode": "everything"})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "BAD_MODE", body["code"])

	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "yok://", "mode": "fix"})
	assert.Equal(t, http.StatusNotFound, status, "%v", body)

	// One file: the run is asked for exactly that path.
	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://kapak.png", "mode": "fix"})
	require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, status, "%v", body)
	assert.Equal(t, "/kapak.png", rep.last().Path)
	assert.Equal(t, "arsiv://kapak.png", body["path"])
}

func TestThumbRepair_OneRunAtATimeAndItsResult(t *testing.T) {
	rep := &recordingRepairer{hold: make(chan struct{})}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	plainStorage(t, store, "arsiv")

	status, body := doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://", "mode": "fix"})
	require.Equal(t, http.StatusAccepted, status, "a held run is still running after the watch: %v", body)
	assert.Equal(t, true, body["running"])

	status, body = doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"path": "arsiv://", "mode": "rebuild"})
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "BUSY", body["code"])
	require.NotNil(t, body["job"], "the busy answer carries the run to follow")

	close(rep.hold)
	st := waitRepairIdle(t, client, srv.URL)
	assert.Equal(t, float64(4), st["processed"])
	assert.Equal(t, float64(3), st["ok"])
	assert.Equal(t, float64(1), st["skipped"])
	assert.Equal(t, float64(0), st["failed"])
	assert.Equal(t, "ok", st["status"])
	assert.Equal(t, "fix", st["mode"])
	assert.Equal(t, "arsiv://", st["path"])

	// The ask is in the audit log, under its own name.
	entries, _, err := store.ListAuditFiltered(context.Background(), nil, "thumbnail.repair", nil, nil, 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the repair was not audited")
}

func TestThumbRepair_AdministratorsOnly(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	testutil.SeedRegularUser(t, store, "kisi@example.test", "Parola-123456")
	testutil.LoginAs(t, srv, client, "kisi@example.test", "Parola-123456")
	status, _ := doJSON(t, client, http.MethodPost, srv.URL+repairURL, map[string]any{"mode": "fix"})
	assert.Equal(t, http.StatusForbidden, status)
	status, _ = doJSON(t, client, http.MethodGet, srv.URL+repairURL, nil)
	assert.Equal(t, http.StatusForbidden, status)
}

func waitRepairIdle(t *testing.T, client *http.Client, base string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, body := doJSON(t, client, http.MethodGet, base+repairURL, nil)
		require.Equal(t, http.StatusOK, status)
		if body["running"] == false {
			return body
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the repair never ended")
	return nil
}

func plainStorage(t *testing.T, store db.Store, name string) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, ConfigJSON: json.RawMessage(`{"root":"/tmp/` + name + `"}`),
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	return st
}

// The SVG limits are the instance's: an administrator of a single-tenant
// install (or the supertenant) reads and changes them, a value out of range
// is refused, and a tenant administrator reads them and cannot change them.
func TestThumbRepair_SVGLimitsSettings(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, false, rep)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/settings", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(5), body["svg_max_mb"])
	assert.Equal(t, float64(10), body["svg_timeout_seconds"])
	assert.Equal(t, true, body["folder_previews"], "folders show their pictures by default")
	assert.Equal(t, true, body["editable"])

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"svg_max_mb": 12, "svg_timeout_seconds": 30})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, float64(12), body["svg_max_mb"])
	assert.Equal(t, float64(30), body["svg_timeout_seconds"])

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"svg_max_mb": 500})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "OUT_OF_RANGE", body["code"])
	status, body = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/settings", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(12), body["svg_max_mb"], "a refused value changes nothing")

	// The folder switch alone leaves the limits as they are.
	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"folder_previews": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["folder_previews"])
	assert.Equal(t, float64(12), body["svg_max_mb"])
	raw, err := store.GetSetting(context.Background(), "thumbs.folder_previews")
	require.NoError(t, err)
	assert.Equal(t, "false", raw)
}

func TestThumbRepair_TenantAdminCannotChangeTheLimits(t *testing.T) {
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, true, rep)
	mine := seedFullTenant(t, store, "globex")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)
	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/settings", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, body["editable"])
	status, _ = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"svg_max_mb": 20})
	assert.Equal(t, http.StatusForbidden, status)
	status, _ = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"folder_previews": false})
	assert.Equal(t, http.StatusForbidden, status, "the folder switch is the instance's too")
}

// The files without a thumbnail, and why: an SVG over a limit carries the
// limit; filex's own folders and encrypted files (nothing to repair) are not
// listed; another tenant's files are not either.
func TestThumbRepair_Problems(t *testing.T) {
	ctx := context.Background()
	rep := &recordingRepairer{}
	srv, client, store := repairServer(t, true, rep)
	mine := seedFullTenant(t, store, "globex")
	theirs := seedFullTenant(t, store, "initech")
	testutil.LoginAs(t, srv, client, mine.adminEmail, mine.adminPass)
	at := time.Now()
	mark := func(storageID int64, p, state, reason string) {
		n := seedNodeIn(t, store, storageID, p)
		require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{NodeID: n.ID, State: state, Error: reason, AttemptedAt: &at}))
	}
	mark(mine.storage.ID, "/harita.svg", "skipped", "svg_too_large:5242880")
	mark(mine.storage.ID, "/plan.svg", "skipped", "svg_timeout:10000")
	mark(mine.storage.ID, "/bozuk.png", "failed", "thumb: decode: image: unknown format")
	mark(mine.storage.ID, "/IMG_0001.heic", "skipped", "no_tool:heif")
	mark(mine.storage.ID, "/gizli.zip", "skipped", "archive_encrypted")
	mark(mine.storage.ID, "/dev.zip", "skipped", "archive_too_large")
	mark(mine.storage.ID, "/gizli.fxe", "skipped", "e2e-encrypted file")
	mark(mine.storage.ID, "/.versions/eski.png", "failed", "x")
	mark(theirs.storage.ID, "/onlarin.svg", "skipped", "svg_too_large:5242880")

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tools/thumbnails/problems", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	byPath := map[string]map[string]any{}
	for _, raw := range body["items"].([]any) {
		it := raw.(map[string]any)
		byPath[it["path"].(string)] = it
	}
	q := func(p string) map[string]any { return byPath[mine.storage.Name+"://"+p] }
	require.Len(t, byPath, 6, "%v", byPath)
	require.NotNil(t, q("harita.svg"), "%v", byPath)
	assert.Equal(t, "svg_too_large", q("harita.svg")["code"])
	assert.Equal(t, float64(5242880), q("harita.svg")["limit"])
	assert.Equal(t, "svg_timeout", q("plan.svg")["code"])
	assert.Equal(t, "failed", q("bozuk.png")["code"])
	assert.Contains(t, q("bozuk.png")["detail"], "unknown format")
	// A kind whose program is missing: skipped with the kind, not failed.
	assert.Equal(t, "no_tool", q("IMG_0001.heic")["code"])
	assert.Equal(t, "heif", q("IMG_0001.heic")["tool"])
	assert.Nil(t, q("IMG_0001.heic")["detail"])
	// An archive whose list is not shown says why, in its own code.
	assert.Equal(t, "archive_encrypted", q("gizli.zip")["code"])
	assert.Equal(t, "archive_too_large", q("dev.zip")["code"])
}

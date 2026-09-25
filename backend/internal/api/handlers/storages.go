package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/scanrule"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
)

// validateStorageRootPath decodes ConfigJSON and rejects storages that
// would mount at the backend root (empty or "/" prefix/path). A non-root
// sub-folder is required so that filemanager never shadows pre-existing
// files at the bucket / FS root. See storage.ValidateNonRootPath.
func validateStorageRootPath(st *model.Storage) error {
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return err
		}
	}
	return storage.ValidateNonRootPath(st.Driver, cfg)
}

// validateScanExclusions refuses a storage whose scan exclusions (issue #44)
// do not compile — a bad glob, a `!`, a `..`, or a pattern that would exclude
// everything. Refused here, where the operator can still fix it; a row that
// got past this some other way scans everything (sync.ruleFor).
func validateScanExclusions(st *model.Storage) error {
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return err
		}
	}
	_, err := scanrule.FromConfig(cfg)
	return err
}

// validateLazySettings refuses lazy catalogue settings that are out of range
// (storage.ValidateLazyConfig), for a storage that is catalogued lazily.
func validateLazySettings(st *model.Storage) error {
	if st.SyncMode != model.SyncModeLazy {
		return nil
	}
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return err
		}
	}
	return storage.ValidateLazyConfig(cfg)
}

// refuseStorageConfig answers a storage configuration the handler will not
// save: a machine code in `error` and, for the refusals a person can act on,
// the sentence in `message` — in the reader's language, from the server
// catalogue (`server.storage.*`). Anything else (a config that is not JSON)
// keeps the plain `error` it always had.
func refuseStorageConfig(w http.ResponseWriter, r *http.Request, err error) {
	var bad *scanrule.InvalidError
	switch {
	case errors.Is(err, storage.ErrRootPathForbidden):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "ROOT_PATH_FORBIDDEN",
			"message": srvtext.Text(langOf(r), "server.storage.root_path_forbidden", nil),
		})
	case errors.As(err, &bad):
		key := "server.storage.scan_exclude_syntax"
		switch bad.Problem {
		case scanrule.ProblemEverything:
			key = "server.storage.scan_exclude_everything"
		case scanrule.ProblemLimit:
			key = "server.storage.scan_exclude_limit"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "SCAN_EXCLUDE_INVALID",
			"message": srvtext.Text(langOf(r), key, srvtext.Vars{
				"pattern": bad.Pattern,
				"max":     strconv.Itoa(scanrule.MaxPatterns),
				"length":  strconv.Itoa(scanrule.MaxPatternLength),
			}),
		})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// Storages handles /api/admin/storages.
type Storages struct {
	Store  db.Store
	Worker *syncpkg.Worker
	// Plugins and StorageResolver are set when the plugin subsystem is on.
	// They exist for ONE reason: a storage saved on a plugin driver is probed
	// against its own configuration first (see plugin_gate.go). Nil means the
	// subsystem is off and nothing is probed.
	Plugins         *plugin.Manager
	StorageResolver func(int64) (storage.Driver, error)
	// ForgetStorage drops the cached driver the resolver built for a storage.
	// Set by the server; nil where nothing caches.
	ForgetStorage func(int64)
	// DemoMode marks a public playground, where "admin" is whoever read the
	// credentials off the landing page. See denyOnDemo.
	DemoMode bool
}

// denyOnDemo refuses new storage backends when this instance is a public demo.
//
// A demo publishes an admin login on purpose, so everything admin-only is
// public here — and adding a storage is the admin power that reaches OUTWARD
// from the server.
//
//   - `local` means "a path on the host". Measured on this project's demo:
//     storages rooted at /data, /etc and /proc/1 were all accepted, which is
//     the database, the configuration and the process environment of the
//     machine. The `/` guard that existed before stopped the laziest version of
//     that and nothing else.
//   - the remote drivers mean "connect from this server to an address I give
//     you", which is a request the visitor cannot otherwise make: loopback,
//     private ranges, a cloud metadata endpoint. Legitimate on any other
//     install — it is how you attach your own bucket — and not something a
//     public playground needs to offer.
//
// A demo ships with the storage it is meant to demonstrate; browsing, sharing,
// uploading and every other surface work unchanged.
//
// ⚠ Plugin drivers are not listed. On a demo the plugin subsystem is off by
// default anyway (config.PluginsDisabled), so a plugin storage can only exist
// where the operator deliberately turned it back on — and then it is their
// own program, deliberately installed.
func (h *Storages) denyOnDemo(driver string) error {
	if !h.DemoMode {
		return nil
	}
	switch driver {
	case "local":
		return errors.New("this is a public demo: a storage on the server's own filesystem cannot be added here")
	case "s3", "sftp", "webdav", "smb", "ftp", "ftps", "b2", "gcs", "azure":
		return errors.New("this is a public demo: connecting the server to another storage backend is disabled here — run your own filex to attach a bucket or a share")
	}
	return nil
}

// NewStorages constructs a Storages handler.
func NewStorages(store db.Store, worker *syncpkg.Worker) *Storages {
	return &Storages{Store: store, Worker: worker}
}

// List returns all configured storages. Each entry carries a `stats`
// blob with the file count + total byte sum so the admin Storages list
// page can render real "12 files, 4.2 MB" labels instead of static
// placeholders.
//
// Accepts `?role=primary` / `?role=replica` so the Depolar page can
// hide replica targets (operators never write to them directly) and
// the Replikasyon page can list replica candidates separately.
func (h *Storages) List(w http.ResponseWriter, r *http.Request) {
	roleFilter := r.URL.Query().Get("role")
	out, err := h.Store.ListStorages(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// ⚠ last_sync_state / last_sync_error are NOT columns on storages — they
	// are the last sync_runs row for it. The UI has always read them from this
	// endpoint, which never sent them, so the badge fell through to its "Never
	// ran" default no matter how many runs had succeeded (issue #16). They are
	// filled here rather than added to model.Storage because a run is a
	// separate record with its own lifecycle; the storage row only carries when
	// it last finished.
	type storageWithStats struct {
		*model.Storage
		Stats struct {
			FileCount int64 `json:"file_count"`
			TotalSize int64 `json:"total_size_bytes"`
		} `json:"stats"`
		LastSyncState string `json:"last_sync_state,omitempty"`
		LastSyncError string `json:"last_sync_error,omitempty"`
		// Catalogue is a lazily catalogued storage's state (lazyCatalogue).
		Catalogue *syncpkg.CatalogueCoverage `json:"catalogue,omitempty"`
		// Coverage is set while the stats count only part of the storage —
		// the same object drive usage carries (Worker.CatalogueCoverage), so
		// Home draws an operator's figure and everybody else's the same way.
		Coverage *syncpkg.CatalogueCoverage `json:"coverage,omitempty"`
	}
	enriched := make([]storageWithStats, 0, len(out))
	for _, st := range out {
		role := st.Role
		if role == "" {
			role = "primary"
		}
		if roleFilter != "" && role != roleFilter {
			continue
		}
		row := storageWithStats{Storage: st}
		if c, sz, err := h.Store.StorageStats(r.Context(), st.ID); err == nil {
			row.Stats.FileCount = c
			row.Stats.TotalSize = sz
		}
		if run, err := h.Store.GetLastSyncRun(r.Context(), st.ID); err == nil && run != nil {
			row.LastSyncState = run.Status
			row.LastSyncError = run.Error
		}
		row.Catalogue = h.lazyCatalogue(r.Context(), st.ID)
		if h.Worker != nil {
			row.Coverage = h.Worker.CatalogueCoverage(r.Context(), st)
		}
		enriched = append(enriched, row)
	}
	writeJSON(w, http.StatusOK, enriched)
}

// Get returns a single storage by id. The admin Storages list page
// fetches /api/admin/storages/{id} when the user clicks a row to
// open the edit view; without this route chi returned 405.
func (h *Storages) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !ownsStorage(w, r, id, "") {
		return
	}
	st, err := h.Store.GetStorage(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Mirror List's stats blob so the detail page header can show the
	// same "N files, M bytes" label without a second roundtrip.
	type storageWithStats struct {
		*model.Storage
		Stats struct {
			FileCount int64 `json:"file_count"`
			TotalSize int64 `json:"total_size_bytes"`
		} `json:"stats"`
		// Catalogue, Coverage: see List.
		Catalogue *syncpkg.CatalogueCoverage `json:"catalogue,omitempty"`
		Coverage  *syncpkg.CatalogueCoverage `json:"coverage,omitempty"`
	}
	out := storageWithStats{Storage: st}
	if c, sz, err := h.Store.StorageStats(r.Context(), st.ID); err == nil {
		out.Stats.FileCount = c
		out.Stats.TotalSize = sz
	}
	out.Catalogue = h.lazyCatalogue(r.Context(), st.ID)
	if h.Worker != nil {
		out.Coverage = h.Worker.CatalogueCoverage(r.Context(), st)
	}
	writeJSON(w, http.StatusOK, out)
}

// lazyCatalogue is the admin view of a lazily catalogued storage — folders
// catalogued, pending and watched, the watch budget, the filler's state and
// the deletions its guard held back (docs/LAZY-CATALOGUE.md). nil for every
// other storage.
func (h *Storages) lazyCatalogue(ctx context.Context, storageID int64) *syncpkg.CatalogueCoverage {
	if h.Worker == nil {
		return nil
	}
	cov, ok := h.Worker.CatalogueStatus(ctx, storageID)
	if !ok {
		return nil
	}
	return cov
}

// Create adds a new storage.
func (h *Storages) Create(w http.ResponseWriter, r *http.Request) {
	var st model.Storage
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if st.Name == "" || st.Driver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and driver required"})
		return
	}
	if err := validateStorageRootPath(&st); err != nil {
		refuseStorageConfig(w, r, err)
		return
	}
	if err := validateScanExclusions(&st); err != nil {
		refuseStorageConfig(w, r, err)
		return
	}
	if err := h.denyOnDemo(st.Driver); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if st.SyncMode == "" {
		st.SyncMode = model.SyncModePoll
	}
	// `lazy` is for local storages only (docs/LAZY-CATALOGUE.md); refused here
	// with the reason, rather than stored and quietly polled.
	if err := model.ValidateSyncModeFor(st.SyncMode, st.Driver); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := validateLazySettings(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if st.SyncIntervalS == 0 {
		st.SyncIntervalS = 900
	}
	// ⚠⚠ A storage on a PLUGIN is verified against this exact configuration
	// before the row exists. The plugin passed its own selftest at install,
	// which proves the code works; this proves it works with the credentials,
	// the bucket and the path somebody just typed. Finding out here costs one
	// error message — finding out later costs a user who uploads a file into
	// a storage that cannot hold it and blames filex for losing it.
	if msg, ok := verifyPluginStorage(r.Context(), h.Plugins, h.StorageResolver, &st); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	created, err := h.Store.CreateStorage(r.Context(), &st)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.Worker != nil && created.Enabled {
		// Use a detached context — the initial sync run kicks off
		// asynchronously and outlives this request. r.Context() is
		// cancelled the moment the HTTP response is flushed, which
		// would otherwise abort the in-flight worker.
		_ = h.Worker.AddStorage(context.Background(), created)
	}
	auth.SetAuditTarget(r.Context(), strconv.FormatInt(created.ID, 10), created.Name)
	writeJSON(w, http.StatusOK, created)
}

// Update modifies a storage row.
func (h *Storages) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !ownsStorage(w, r, id, "") {
		return
	}
	cur, err := h.Store.GetStorage(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := json.NewDecoder(r.Body).Decode(cur); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	cur.ID = id
	if err := h.denyOnDemo(cur.Driver); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	// A plugin storage is re-probed on every change: the operator may have
	// just pointed it at a different bucket, and a configuration that half
	// works fails the same way a half-working plugin does — in the user's
	// hands, looking like filex.
	if msg, ok := verifyPluginStorage(r.Context(), h.Plugins, h.StorageResolver, cur); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	if err := validateStorageRootPath(cur); err != nil {
		refuseStorageConfig(w, r, err)
		return
	}
	if err := validateScanExclusions(cur); err != nil {
		refuseStorageConfig(w, r, err)
		return
	}
	if err := model.ValidateSyncModeFor(cur.SyncMode, cur.Driver); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := validateLazySettings(cur); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.Store.UpdateStorage(r.Context(), cur); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.applyLive(cur)
	writeJSON(w, http.StatusOK, cur)
}

// applyLive makes an edited storage row the one the running process uses.
//
// Creating a storage starts a syncer; deleting one stops it. Editing one used
// to do neither, and every live consumer went on holding the copy it had taken
// when the process started: the syncer's snapshot — name, driver config, root
// path, schedule, enabled flag, and the driver it initialised for itself — and
// the resolver's cached driver behind every read, download and thumbnail.
//
// The operator was told the save succeeded, because it had; the database row
// was correct. Only the restart nobody knew to perform applied it. So a fixed
// bucket kept failing the old way, a disabled storage kept being walked, and a
// renamed one kept writing its old name into the log (issue #21).
//
// Rebuilding is deliberately blunt — forget the driver, stop the syncer, start
// a fresh one from the row just written. A syncer mid-walk is cancelled by the
// stop; that is correct, since it is walking a configuration the operator has
// just replaced, and the next run is a full pass anyway.
func (h *Storages) applyLive(st *model.Storage) {
	if h.ForgetStorage != nil {
		h.ForgetStorage(st.ID)
	}
	if h.Worker == nil {
		return
	}
	h.Worker.RemoveStorage(st.ID)
	if !st.Enabled {
		return
	}
	// Detached from the request: the caller's context dies with the response,
	// and this syncer has to outlive it. Worker.Stop still cancels it.
	if err := h.Worker.AddStorage(context.Background(), st); err != nil {
		slog.Warn("storages: restarting the syncer after an edit failed",
			slog.String("storage", st.Name), slog.String("err", err.Error()))
	}
}

// Delete removes a storage and its descendant nodes (cascade).
func (h *Storages) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !ownsStorage(w, r, id, "storage") {
		return
	}
	// Existence check before destructive work (DeleteStorage swallows
	// "no rows" for some drivers + RemoveStorage silently no-ops on
	// unknown ids). Without this, DELETE on a bogus id returns
	// {ok:true} which is misleading.
	gone, gerr := h.Store.GetStorage(r.Context(), id)
	if gerr != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}
	if h.Worker != nil {
		h.Worker.RemoveStorage(id)
	}
	if h.ForgetStorage != nil {
		h.ForgetStorage(id)
	}
	if err := h.Store.DeleteStorage(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The row is gone once the log is read: keep its name.
	auth.SetAuditTarget(r.Context(), "", gone.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ScopedRescanTimeout bounds a folder rescan (TriggerSync with ?path=), which
// answers inside the request.
var ScopedRescanTimeout = 10 * time.Minute

// TriggerSync forces an immediate sync run for a storage — or, with
// ?path=<folder>, a rescan of that one catalogued folder (rescanFolder).
func (h *Storages) TriggerSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !ownsStorage(w, r, id, "storage") {
		return
	}
	if h.Worker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "worker offline"})
		return
	}
	// ?path=<folder> rescans that one catalogued folder instead of the whole
	// storage. "", "/" and anything that cleans to the root are the full scan
	// below, exactly as before.
	if raw := r.URL.Query().Get("path"); raw != "" {
		dir, err := syncpkg.ScopePath(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if dir != "" {
			h.rescanFolder(w, r, id, dir)
			return
		}
	}
	// ⚠⚠ The run is DETACHED from the request, and answered immediately.
	//
	// It used to run inside the HTTP handler, which made a manual sync of a
	// large storage fail twice over: the browser gave up at its own 30-second
	// timeout and showed the operator "30000 milliseconds exceeded" for a sync
	// that was proceeding perfectly well — and, worse, the cancelled request
	// cancelled the CONTEXT the walk was using, so the pass stopped halfway,
	// leaving the catalogue half-updated and the tombstone pass with a partial
	// view. Reported on a Garage/S3 storage in issue #21.
	//
	// A second press while a run is in flight is answered, not queued: the
	// syncer admits one run per storage (sync.ErrRunInProgress) and the run the
	// operator wants is the one already walking. It is still a 202 — the
	// request was "scan this storage" and that is what is happening — with
	// `status: "running"` so a caller that cares can tell. A script that
	// creates a storage and asks for a scan straight away lands here every
	// time (the first poll starts with the syncer), and refusing it would turn
	// a correct sequence into an error. Progress is where it already was:
	// Storages → sync runs.
	if !h.Worker.Known(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}
	if h.Worker.Running(id) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":     true,
			"status": "running",
			"note":   "a scan is already running for this storage; no second one was started — watch its progress under sync runs",
		})
		return
	}
	go func() {
		// A background context with a generous ceiling: a walk of a large
		// bucket is minutes, not seconds, and an unbounded goroutine is how a
		// stuck driver becomes a leak.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 6*time.Hour)
		defer cancel()
		if err := h.Worker.Trigger(ctx, id); err != nil {
			slog.Warn("storages: manual sync failed",
				slog.Int64("storage", id), slog.String("err", err.Error()))
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":     true,
		"status": "started",
		"note":   "the sync runs in the background; watch its progress under sync runs",
	})
}

// rescanFolder is TriggerSync for one folder (sync.Worker.RescanFolder).
//
// Unlike the full scan it answers with its result, because a folder is small
// enough to wait for — and bounded by ScopedRescanTimeout, because some are
// not. It runs detached from the request like the full scan (a client that
// gives up must not cancel a walk half way); on the time limit it answers 504
// with the counts so far: the rows it reached are updated, and nothing was
// removed, since the tombstone pass never runs on a partial view. No sync_runs
// row is written and the storage's last-synced time does not move.
func (h *Storages) rescanFolder(w http.ResponseWriter, r *http.Request, id int64, dir string) {
	if !h.Worker.Known(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ScopedRescanTimeout)
	defer cancel()
	res, err := h.Worker.RescanFolder(ctx, id, dir)
	body := map[string]any{
		"path":       res.Path,
		"scanned":    res.Scanned,
		"added":      res.Added,
		"updated":    res.Updated,
		"removed":    res.Removed,
		"reconciled": res.Reconciled,
	}
	if res.RemovalSkipped != "" {
		body["removal_skipped"] = res.RemovalSkipped
	}
	switch {
	case err == nil:
		body["ok"] = true
		writeJSON(w, http.StatusOK, body)
	case errors.Is(err, syncpkg.ErrRunInProgress):
		// The same answer as a second full-scan press: not queued, not an
		// error — the storage is being walked right now.
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":     true,
			"status": "running",
			"note":   "a scan is already running for this storage; no folder rescan was started — ask again once it has finished",
		})
	case errors.Is(err, syncpkg.ErrFolderNotCatalogued):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": err.Error() + "; rescan the folder above it, or run a full scan",
		})
	case errors.Is(err, syncpkg.ErrNotAFolder), errors.Is(err, syncpkg.ErrScopeInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		body["error"] = fmt.Sprintf("the rescan did not finish within %s: the rows it reached are updated and nothing was removed; rescan a smaller folder, or run a full scan", ScopedRescanTimeout)
		writeJSON(w, http.StatusGatewayTimeout, body)
	default:
		slog.Warn("storages: folder rescan failed",
			slog.Int64("storage", id), slog.String("path", dir), slog.String("err", err.Error()))
		body["error"] = err.Error()
		writeJSON(w, http.StatusInternalServerError, body)
	}
}

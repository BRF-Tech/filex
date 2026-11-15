package handlers

// storages_advice.go - what the storage form is told about a storage's sync
// beyond its own row (issues #351 and #353):
//
//   - sync_suggestion: an existing storage that polls on a driver whose new
//     storages are catalogued with live watching (model.SyncModeSuggestion) -
//     the one-click switch the storage page and the storage list offer;
//   - filesystem: a local storage whose root is on a network filesystem or a
//     FUSE mount (internal/netfs), where a watch cannot be trusted and every
//     opened folder is read from the disk;
//   - the scan-interval advice (GET /api/admin/storages/{id}/interval-advice),
//     judged by internal/syncadvice from the storage's last measured scan.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netfs"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syncadvice"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

// probeStorageFS is how long an admin storage answer waits for a storage
// root's filesystem to be told (netfs.ProbeWithin remembers the answer, and a
// hung mount is asked once).
const probeStorageFS = 2 * time.Second

// storageFS says which filesystem a local storage's root is on, or nil: not a
// local storage, a local filesystem, or one that could not be told in time.
// Only a network one is worth a word on the storage page.
func storageFS(st *model.Storage) *netfs.Info {
	if st == nil || st.Driver != "local" {
		return nil
	}
	root := localRootOf(st)
	if root == "" {
		return nil
	}
	info, ok := netfs.ProbeWithin(root, probeStorageFS)
	if !ok || !info.Network {
		return nil
	}
	return &info
}

// localRootOf is the directory a local storage's driver opens - its config's
// path (or root), made absolute the way local.Driver.Init makes it.
func localRootOf(st *model.Storage) string {
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return ""
		}
	}
	root := storage.ConfigString(cfg, "path")
	if root == "" {
		root = storage.ConfigString(cfg, "root")
	}
	if strings.TrimSpace(root) == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	return abs
}

// suggestionOf is model.SyncModeSuggestion as the answers spell it ("" for
// none).
func suggestionOf(st *model.Storage) string { return string(model.SyncModeSuggestion(st)) }

// IntervalAdvice judges a scan interval for a storage, while a person types it:
//
//	GET /api/admin/storages/{id}/interval-advice?interval_s=300&mode=poll
//	GET /api/admin/storages/interval-advice?interval_s=300&mode=poll&driver=local
//
// interval_s and mode are what the form shows, saved or not (interval_s 0 or
// absent: the server's default; mode absent: the storage's, else the driver's
// default). The answer is syncadvice.Advice; the form words it. Without a
// storage (the new-storage form) nothing is measured yet, and the answer says
// so.
func (h *Storages) IntervalAdvice(w http.ResponseWriter, r *http.Request) {
	var st *model.Storage
	if raw := chi.URLParam(r, "id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		if !ownsStorage(w, r, id, "") {
			return
		}
		got, err := h.Store.GetStorage(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		st = got
	}
	q := r.URL.Query()
	mode := model.SyncMode(strings.TrimSpace(q.Get("mode")))
	if mode == "" && st != nil {
		mode = st.SyncMode
	}
	if mode == "" {
		driver := q.Get("driver")
		if st != nil {
			driver = st.Driver
		}
		mode = model.DefaultSyncModeFor(driver)
	}
	intervalS := 0
	if v := strings.TrimSpace(q.Get("interval_s")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "interval_s must be a whole number of seconds"})
			return
		}
		intervalS = n
	} else if st != nil {
		intervalS = st.SyncIntervalS
	}
	advice, err := h.adviceFor(r.Context(), st, mode, intervalS)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, advice)
}

// adviceFor gathers what syncadvice.Judge needs for st (nil: a storage not
// saved yet, nothing measured) at intervalS in mode, and judges it. The one
// way an interval is judged: the form's question (IntervalAdvice) and a save's
// answer (savedAnswer) both come here.
func (h *Storages) adviceFor(ctx context.Context, st *model.Storage, mode model.SyncMode, intervalS int) (syncadvice.Advice, error) {
	in := syncadvice.Input{
		Mode:            string(mode),
		Interval:        time.Duration(intervalS) * time.Second,
		DefaultInterval: h.Worker.DefaultInterval(),
	}
	if st != nil {
		run, err := h.Store.LastMeasuredSyncRun(ctx, st.ID)
		switch {
		case err == nil && run.FinishedAt != nil:
			in.Last = &syncadvice.Scan{
				Duration:   run.FinishedAt.Sub(run.StartedAt),
				Entries:    run.SeenCount,
				FinishedAt: *run.FinishedAt,
			}
			if run.PeakMemBytes > 0 {
				in.Last.PeakMemBytes = uint64(run.PeakMemBytes)
			}
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return syncadvice.Advice{}, err
		}
		// `fsnotify` on a network filesystem scans at the interval instead
		// of watching (sync.loopFSNotify, task #351): judged like poll.
		if mode == model.SyncModeFSNotify {
			in.WatchUntrusted = storageFS(st) != nil
		}
	}
	given := sysmem.Allocated()
	in.MemLimit = given.Bytes
	switch given.Source {
	case sysmem.SourceCgroupV1, sysmem.SourceCgroupV2:
		in.MemSource = syncadvice.MemContainer
	case sysmem.SourceHost:
		in.MemSource = syncadvice.MemHost
	}
	return syncadvice.Judge(in), nil
}

// storageSaved is a storage as a save answers it (POST and PATCH
// /api/admin/storages, and so the admin_storages_create / _update MCP tools,
// which run the same handlers): the row as storageShown shows it, and what its
// scan interval would cost (task #353), so a caller that never opens the form
// - an agent - hears it too. The advice is left out when it could not be read.
type storageSaved struct {
	*model.Storage
	IntervalAdvice *syncadvice.Advice `json:"interval_advice,omitempty"`
}

func (h *Storages) savedAnswer(ctx context.Context, st *model.Storage) storageSaved {
	out := storageSaved{Storage: storageShown(st)}
	if advice, err := h.adviceFor(ctx, st, st.SyncMode, st.SyncIntervalS); err == nil {
		out.IntervalAdvice = &advice
	}
	return out
}

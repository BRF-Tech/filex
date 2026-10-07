package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// ThumbRepair serves Admin → Tools → Thumbnail repair:
//
//	POST /api/admin/tools/thumbnails/repair  {path, mode}
//	GET  /api/admin/tools/thumbnails/repair  the tenant's latest run
//
// `path` is a qualified path: "photos://2024/summer" (a folder and everything
// in it), "photos://2024/cover.jpg" (one file), "photos://" (the storage), or
// "" (every storage the caller reaches). `mode` is "fix" (missing, failed,
// skipped, stale) or "rebuild" (everything).
//
// The run is an ops job (ops/thumb_repair.go): in the operations tray,
// cancellable through POST /api/files/ops/{id}/cancel, its tenant's alone,
// resumed after a restart. The walk it runs is the CLI's
// (`filex thumb backfill`, server.BackfillThumbs).
//
// ⚠ Administrators only, and a tenant administrator only over their tenant's
// storages: a named storage outside the scope is 404, and "every storage" is
// the tenant's reach (trash.Reach), never the instance.
type ThumbRepair struct {
	Store db.Store
	Ops   *ops.Service
	// Wait is how long POST watches a fresh run before answering; 0 = 3 s.
	// A small folder is done by then and the answer is its result.
	Wait time.Duration
	// Limits drops the pipeline's cached settings after a change. Nil: the
	// new ones apply within the cache's few seconds.
	Limits ThumbSettingsForgetter
}

// NewThumbRepair constructs the handler.
func NewThumbRepair(store db.Store, o *ops.Service) *ThumbRepair {
	return &ThumbRepair{Store: store, Ops: o}
}

// thumbRepairStatus is one run as POST and GET report it.
type thumbRepairStatus struct {
	OpID      int64  `json:"op_id,omitempty"`
	Running   bool   `json:"running"`
	Queued    bool   `json:"queued,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Status    string `json:"status,omitempty"`
	Mode      string `json:"mode,omitempty"`
	// Path is the qualified path it was asked for; "" was every storage.
	Path      string `json:"path"`
	StorageID int64  `json:"storage_id,omitempty"`
	// Total is how many files the run found to draw when it started.
	Total     int `json:"total"`
	Processed int `json:"processed"`
	OK        int `json:"ok"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	// Refused are storages the run would not touch: their catalogue cannot
	// hold their files yet (a sync is running, was interrupted, or never ran).
	Refused    []thumbRefusal `json:"refused,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type thumbRefusal struct {
	StorageID int64  `json:"storage_id"`
	Storage   string `json:"storage,omitempty"`
	// Code is server.GapSyncRunning / GapSyncAborted / GapNeverSynced.
	Code string `json:"code"`
}

func (h *ThumbRepair) statusOf(r *http.Request, op *ops.Op) thumbRepairStatus {
	job, refused, _, _ := op.ThumbRepairOf()
	st := thumbRepairStatus{
		OpID:       op.ID,
		Running:    op.Status == ops.StatusPending || op.Status == ops.StatusRunning,
		Queued:     op.Status == ops.StatusPending,
		Cancelled:  op.Status == ops.StatusCancelled,
		Status:     op.Status,
		Mode:       job.Mode,
		StorageID:  job.StorageID,
		Total:      op.Total,
		Processed:  op.Done,
		Failed:     op.Failed,
		Skipped:    op.Skipped,
		Error:      op.Error,
		FinishedAt: op.FinishedAt,
	}
	if op.StartedAt != nil {
		st.StartedAt = op.StartedAt
	} else {
		created := op.CreatedAt
		st.StartedAt = &created
	}
	if ok := st.Processed - st.Failed - st.Skipped; ok > 0 {
		st.OK = ok
	}
	names := map[int64]string{}
	name := func(id int64) string {
		if n, ok := names[id]; ok {
			return n
		}
		n := ""
		if s, err := h.Store.GetStorage(r.Context(), id); err == nil && s != nil {
			n = s.Name
		}
		names[id] = n
		return n
	}
	if job.StorageID != 0 {
		st.Path = joinAdapterPath(name(job.StorageID), job.Path)
	}
	for _, rf := range refused {
		st.Refused = append(st.Refused, thumbRefusal{StorageID: rf.StorageID, Storage: name(rf.StorageID), Code: rf.Code})
	}
	return st
}

// Status answers the tenant's latest repair: queued, running, or how it ended.
// `{"running": false}` alone when it has asked for none.
func (h *ThumbRepair) Status(w http.ResponseWriter, r *http.Request) {
	if h.Ops == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	op, err := h.Ops.LatestThumbRepair(r.Context(), ops.TenantKey(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if op == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	writeJSON(w, http.StatusOK, h.statusFor(r, op))
}

// statusFor is a run as this caller may be told about it. Unconfined, it is
// statusOf. A caller held to a folder (a `root:` token, a session narrowed by
// X-Filex-Root) is told about a run inside its folder in full, the storages it
// refused narrowed to the folder's; of any other run - another folder, the
// whole storage, every storage - only that one is going (`running`, `queued`),
// and of one that has ended nothing at all, as if none had been asked for: its
// path, storage, counts, op and refusals are not this caller's (filex #185).
func (h *ThumbRepair) statusFor(r *http.Request, op *ops.Op) any {
	st := h.statusOf(r, op)
	root, confined := confine.RootFrom(r.Context())
	if !confined {
		return st
	}
	job, _, _, _ := op.ThumbRepairOf()
	inside := false
	if job.StorageID != 0 {
		if s, err := h.Store.GetStorage(r.Context(), job.StorageID); err == nil && s != nil {
			inside = root.Within(s.Name, job.Path)
		}
	}
	if !inside {
		if !st.Running {
			return map[string]any{"running": false}
		}
		return map[string]any{"running": true, "queued": st.Queued}
	}
	kept := st.Refused[:0]
	for _, rf := range st.Refused {
		if rf.Storage == root.Adapter {
			kept = append(kept, rf)
		}
	}
	st.Refused = kept
	if len(st.Refused) == 0 {
		st.Refused = nil
	}
	return st
}

// Start queues a repair (see the type comment) and watches it for a moment:
// 200 with the result when it ended within Wait, 202 with its progress when it
// goes on. 409 BUSY while this tenant already has one going (with that run as
// `job`); 400 for an unreadable request; 404 for a storage or path the caller
// cannot see.
func (h *ThumbRepair) Start(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.Ops == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "ops queue unavailable"})
		return
	}
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body", "code": "BAD_REQUEST"})
			return
		}
	}
	if req.Mode == "" {
		req.Mode = ops.ThumbRepairFix
	}
	if req.Mode != ops.ThumbRepairFix && req.Mode != ops.ThumbRepairRebuild {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be fix or rebuild", "code": "BAD_MODE"})
		return
	}
	// A `root:`-confined token has had its path checked and rewritten by
	// confine.Middleware; "every storage" from such a token is its root.
	if req.Path == "" {
		if root, ok := confine.RootFrom(ctx); ok {
			req.Path = joinAdapterPath(root.Adapter, root.Rel)
		}
	}

	job := ops.ThumbRepairJob{Mode: req.Mode, Reach: trash.Reach(ctx)}
	target := "*"
	if req.Path != "" {
		adapter, rel := splitAdapterPath(req.Path)
		if adapter == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must name a storage: <storage>://<path>", "code": "BAD_PATH"})
			return
		}
		st, err := h.Store.GetStorageByName(ctx, adapter)
		if err != nil || st == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "storage not found", "code": "STORAGE_NOT_FOUND"})
			return
		}
		if !ownsStorage(w, r, st.ID, "storage") {
			return
		}
		job.StorageID = st.ID
		if rel != "" {
			clean := normalizeDBPath(rel)
			if syspath.Hidden(clean) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no file or folder at that path", "code": "PATH_NOT_FOUND"})
				return
			}
			n, err := h.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
			if err != nil || n == nil || n.DeletedAt != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no file or folder at that path", "code": "PATH_NOT_FOUND"})
				return
			}
			job.Path = path.Clean("/" + strings.Trim(clean, "/"))
		}
		target = joinAdapterPath(st.Name, job.Path)
		auth.SetAuditTarget(ctx, strconv.FormatInt(st.ID, 10), target)
	} else {
		auth.SetAuditTarget(ctx, "", target)
	}
	auth.AddAuditDetail(ctx, "mode", req.Mode)

	op, err := h.Ops.SubmitThumbRepair(ctx, ops.ThumbRepairRequest{Job: job, Tenant: ops.TenantKey(ctx)})
	if errors.Is(err, ops.ErrThumbRepairBusy) {
		// The run that is going is told about as Status tells it (statusFor):
		// a caller held to a folder learns another folder's run is going,
		// not where.
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "thumbnails are already being repaired", "code": "BUSY", "job": h.statusFor(r, op),
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.AddAuditDetail(ctx, "op_id", op.ID)
	wait := h.Wait
	if wait <= 0 {
		wait = 3 * time.Second
	}
	if done := h.Ops.ThumbRepairDone(op.ID); done != nil {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
			return // the caller has gone; the run has not
		}
	}
	if cur, err := h.Ops.Get(ctx, op.ID); err == nil {
		op = cur
	}
	st := h.statusOf(r, op)
	code := http.StatusOK
	if st.Running {
		code = http.StatusAccepted
	}
	writeJSON(w, code, st)
}

// ── the thumbnail settings (thumb/svglimits.go) ────────────────────────
//
// The folder switch and the two SVG limits: one card in Settings and on the
// repair tab, one GET and one PATCH.

// ThumbSettingsForgetter drops the pipeline's cached settings after a change
// (*thumb.Pipeline).
type ThumbSettingsForgetter interface{ ForgetSettings() }

type thumbSettingsBody struct {
	FolderPreviews    bool `json:"folder_previews"`
	SVGMaxMB          int  `json:"svg_max_mb"`
	SVGTimeoutSeconds int  `json:"svg_timeout_seconds"`
	// The office documents OnlyOffice draws (thumb/office.go): the largest
	// one sent, and how many at once per filex process.
	OfficeMaxMB int `json:"office_max_mb"`
	OfficeSlots int `json:"office_slots"`
	// Bounds and whether this caller may change them (the limits are the
	// instance's: a tenant administrator reads them and cannot set them).
	SVGMaxMBMin          int  `json:"svg_max_mb_min"`
	SVGMaxMBMax          int  `json:"svg_max_mb_max"`
	SVGTimeoutSecondsMin int  `json:"svg_timeout_seconds_min"`
	SVGTimeoutSecondsMax int  `json:"svg_timeout_seconds_max"`
	OfficeMaxMBMin       int  `json:"office_max_mb_min"`
	OfficeMaxMBMax       int  `json:"office_max_mb_max"`
	OfficeSlotsMin       int  `json:"office_slots_min"`
	OfficeSlotsMax       int  `json:"office_slots_max"`
	Editable             bool `json:"editable"`
}

func (h *ThumbRepair) settingsOf(r *http.Request) thumbSettingsBody {
	ctx := r.Context()
	editable := true
	if scope, ok := tenant.FromContext(ctx); ok && (scope == nil || !scope.IsSupertenant) {
		editable = false
	}
	return thumbSettingsBody{
		FolderPreviews:       thumb.FolderPreviewsSetting.Resolve(ctx, h.Store),
		SVGMaxMB:             thumb.SVGMaxMBSetting.Resolve(ctx, h.Store),
		SVGTimeoutSeconds:    thumb.SVGTimeoutSetting.Resolve(ctx, h.Store),
		SVGMaxMBMin:          thumb.SVGMaxMBSetting.Min,
		SVGMaxMBMax:          thumb.SVGMaxMBSetting.Max,
		SVGTimeoutSecondsMin: thumb.SVGTimeoutSetting.Min,
		SVGTimeoutSecondsMax: thumb.SVGTimeoutSetting.Max,
		OfficeMaxMB:          thumb.OfficeMaxMBSetting.Resolve(ctx, h.Store),
		OfficeSlots:          thumb.OfficeSlotsSetting.Resolve(ctx, h.Store),
		OfficeMaxMBMin:       thumb.OfficeMaxMBSetting.Min,
		OfficeMaxMBMax:       thumb.OfficeMaxMBSetting.Max,
		OfficeSlotsMin:       thumb.OfficeSlotsSetting.Min,
		OfficeSlotsMax:       thumb.OfficeSlotsSetting.Max,
		Editable:             editable,
	}
}

// Settings answers the thumbnail settings in force (GET .../thumbnails/settings).
func (h *ThumbRepair) Settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.settingsOf(r))
}

// UpdateSettings changes them (PATCH .../thumbnails/settings). The instance's
// setting: a tenant administrator is refused. A value out of range is 400,
// never clamped (dbsetting.IntSpec.Validate).
func (h *ThumbRepair) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "the thumbnail settings are the instance's") {
		return
	}
	var req struct {
		FolderPreviews    *bool `json:"folder_previews"`
		SVGMaxMB          *int  `json:"svg_max_mb"`
		SVGTimeoutSeconds *int  `json:"svg_timeout_seconds"`
		OfficeMaxMB       *int  `json:"office_max_mb"`
		OfficeSlots       *int  `json:"office_slots"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body", "code": "BAD_REQUEST"})
		return
	}
	ctx := r.Context()
	type change struct {
		spec dbsetting.IntSpec
		v    *int
	}
	changes := []change{
		{thumb.SVGMaxMBSetting, req.SVGMaxMB}, {thumb.SVGTimeoutSetting, req.SVGTimeoutSeconds},
		{thumb.OfficeMaxMBSetting, req.OfficeMaxMB}, {thumb.OfficeSlotsSetting, req.OfficeSlots},
	}
	for _, c := range changes {
		if c.v == nil {
			continue
		}
		if err := c.spec.Validate(*c.v); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": err.Error(), "code": "OUT_OF_RANGE", "field": c.spec.Key, "min": c.spec.Min, "max": c.spec.Max,
			})
			return
		}
	}
	for _, c := range changes {
		if c.v == nil {
			continue
		}
		if err := h.Store.UpsertSetting(ctx, c.spec.Key, strconv.Itoa(*c.v)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		auth.AddAuditDetail(ctx, c.spec.Key, *c.v)
	}
	if req.FolderPreviews != nil {
		if err := h.Store.UpsertSetting(ctx, thumb.FolderPreviewsSetting.Key, dbsetting.FormatBool(*req.FolderPreviews)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		auth.AddAuditDetail(ctx, thumb.FolderPreviewsSetting.Key, *req.FolderPreviews)
	}
	if h.Limits != nil {
		h.Limits.ForgetSettings()
	}
	writeJSON(w, http.StatusOK, h.settingsOf(r))
}

// ── the files without a thumbnail, and why ──────────────────────────────

type thumbProblemRow struct {
	NodeID    int64  `json:"node_id"`
	StorageID int64  `json:"storage_id"`
	Storage   string `json:"storage"`
	// Path is qualified: "<storage>://<path>", what a repair of the one file
	// is asked with.
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
	// Code is why, for the client to say in its own language:
	// svg_too_large / svg_timeout (Limit: bytes / milliseconds), no_engine,
	// no_tool (Tool: the kind whose program is missing: video, audio, pdf,
	// heif, heic_codec - ImageMagick is there and cannot decode a HEIC -
	// and office: OnlyOffice is not configured), archive_encrypted,
	// archive_too_large, the document server's answers (oo_corrupt, Detail:
	// what failed; oo_password; oo_too_large, Limit: bytes, 0 for the
	// document server's own limit; oo_retry, Tries: the tries so far, Detail:
	// what failed), or failed (Detail: the engine's own words, for an
	// administrator). Note is the marker the explorer shows on the file
	// (thumb.NoteOf): corrupt, encrypted, too_large, or absent.
	Code        string     `json:"code"`
	Limit       int64      `json:"limit,omitempty"`
	Tool        string     `json:"tool,omitempty"`
	Detail      string     `json:"detail,omitempty"`
	Tries       int        `json:"tries,omitempty"`
	Note        string     `json:"note,omitempty"`
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
	// App names the app a reason is about: app_failed, app_timeout (Limit:
	// milliseconds) and app_too_large (Limit: bytes).
	App string `json:"app,omitempty"`
	// Generator is who drew the picture, on a row drawn only after an earlier
	// handler failed (state ready, code fell_back).
	Generator string `json:"generator,omitempty"`
	// Attempts are the handlers asked, in order, and what each answered -
	// absent on rows from before 0.50.
	Attempts []thumbAttemptRow `json:"attempts,omitempty"`
}

// thumbAttemptRow is one handler asked about a file: `handler` is builtin or
// app:<name>, `version` the app's; ok, or the reason as the row's own fields
// spell it (code, limit, tool, detail).
type thumbAttemptRow struct {
	Handler string `json:"handler"`
	App     string `json:"app,omitempty"`
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Limit   int64  `json:"limit,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Tries   int    `json:"tries,omitempty"`
}

// thumbReason spells a recorded reason the way the repair tab reads it.
type thumbReason struct {
	Code, Tool, Detail, App string
	Limit                   int64
	Tries                   int
}

// reasonOf reads a thumbnail reason (the row's error, or an attempt's) into
// a code the client says in its own language.
func reasonOf(reason string) thumbReason {
	if code, limit := thumb.ParseSkip(reason); code != "" {
		return thumbReason{Code: code, Limit: limit}
	}
	if code, app, limit, ok := thumb.ParseAppReason(reason); ok {
		return thumbReason{Code: code, App: app, Limit: limit}
	}
	if code, what, tries, ok := thumb.ParseOfficeReason(reason); ok {
		return thumbReason{Code: code, Detail: what, Tries: tries}
	}
	switch {
	case reason == thumb.SkipNoHandler:
		return thumbReason{Code: thumb.SkipNoHandler}
	case thumb.IsNoEngineSkip(reason):
		return thumbReason{Code: "no_engine"}
	case isNoTool(reason):
		kind, _ := thumb.ParseNoTool(reason)
		return thumbReason{Code: "no_tool", Tool: kind}
	case reason == thumb.SkipArchiveEncrypted || reason == thumb.SkipArchiveTooLarge:
		return thumbReason{Code: reason}
	}
	return thumbReason{Code: "failed", Detail: reason}
}

// attemptRows reads a row's `attempts` for the repair tab.
func attemptRows(raw string) []thumbAttemptRow {
	list := thumb.ParseAttempts(raw)
	if len(list) == 0 {
		return nil
	}
	out := make([]thumbAttemptRow, 0, len(list))
	for _, a := range list {
		id, version, _ := strings.Cut(a.H, "@")
		row := thumbAttemptRow{Handler: id, Version: version, App: assoc.AppOf(id)}
		if a.R == "ok" {
			row.OK = true
		} else {
			rs := reasonOf(a.R)
			row.Code, row.Limit, row.Tool, row.Detail, row.Tries = rs.Code, rs.Limit, rs.Tool, rs.Detail, rs.Tries
		}
		out = append(out, row)
	}
	return out
}

// problemLimit is how many rows the list shows: the most recent problems
// are the ones an administrator is looking at, and a repair of the whole
// storage is one press away for the rest.
const problemLimit = 500

// problemScanRows is how many of the root's storage's rows are read for a
// caller held to a folder: the store narrows by storage, not by folder, so the
// rows outside the folder are read and passed over (problemReach). A folder
// whose problems lie past them, in an older part of a busy storage, is not
// told about those - nor that the list was cut, which would be a fact about
// the rows outside it.
const problemScanRows = 10 * problemLimit

// Problems lists the files whose thumbnail failed or was skipped, with the
// reason (GET .../thumbnails/problems), inside the caller's reach - and inside
// the folder of a caller held to one (a `root:` token, a session narrowed by
// X-Filex-Root; confine.Middleware is mounted on /tools).
func (h *ThumbRepair) Problems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	root, confined := confine.RootFrom(ctx)
	reach, limit := trash.Reach(ctx), problemLimit+1
	if confined {
		reach, limit = h.problemReach(r, root, reach), problemScanRows
	}
	rows, err := h.Store.ListThumbnailProblems(ctx, reach, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	names := map[int64]string{}
	storageName := func(id int64) string {
		name, ok := names[id]
		if !ok {
			if st, err := h.Store.GetStorage(ctx, id); err == nil && st != nil {
				name = st.Name
			}
			names[id] = name
		}
		return name
	}
	if confined {
		// ⚠⚠ Held to the folder BEFORE the list is cut: the rows outside
		// it are not this caller's, and neither is the fact that there are
		// more of them than the list shows (filex #185). An unreadable
		// storage name is outside every root.
		kept := rows[:0]
		for _, p := range rows {
			if root.Within(storageName(p.StorageID), p.Path) {
				kept = append(kept, p)
			}
		}
		rows = kept
	}
	truncated := len(rows) > problemLimit
	if truncated {
		rows = rows[:problemLimit]
	}
	out := make([]thumbProblemRow, 0, len(rows))
	for _, p := range rows {
		if syspath.Hidden(p.Path) {
			continue
		}
		name := storageName(p.StorageID)
		row := thumbProblemRow{
			NodeID: p.NodeID, StorageID: p.StorageID, Storage: name,
			Path: joinAdapterPath(name, p.Path), Name: p.Name, Size: p.Size, State: p.State,
			AttemptedAt: p.AttemptedAt, Attempts: attemptRows(p.Attempts),
		}
		if p.State == "ready" {
			// Drawn, but only after an earlier handler failed: who drew it is
			// the last attempt, and the row says so (code fell_back).
			row.Code = "fell_back"
			if n := len(row.Attempts); n > 0 {
				row.Generator = row.Attempts[n-1].Handler
			}
		} else {
			rs := reasonOf(p.Error)
			row.Code, row.Limit, row.Tool, row.Detail, row.App, row.Tries = rs.Code, rs.Limit, rs.Tool, rs.Detail, rs.App, rs.Tries
			row.Note = thumb.NoteForReason(p.Error)
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "truncated": truncated})
}

// problemReach is the storages a caller held to root may be told about: the
// root's own storage, when it lies inside the caller's reach (trash.Reach: nil
// is every storage); none otherwise - a root on a storage that does not
// resolve, or outside the tenant, lists nothing.
func (h *ThumbRepair) problemReach(r *http.Request, root confine.Root, reach []int64) []int64 {
	st, err := h.Store.GetStorageByName(r.Context(), root.Adapter)
	if err != nil || st == nil || (reach != nil && !slices.Contains(reach, st.ID)) {
		return []int64{}
	}
	return []int64{st.ID}
}

func isNoTool(reason string) bool {
	_, ok := thumb.ParseNoTool(reason)
	return ok
}

// thumbGeneratorRow is how many ready thumbnails one handler drew.
type thumbGeneratorRow struct {
	// Generator is builtin, app:<name>@<version>, or "" (drawn before 0.50,
	// or the placeholder card).
	Generator string `json:"generator"`
	App       string `json:"app,omitempty"`
	Version   string `json:"version,omitempty"`
	Count     int64  `json:"count"`
}

// Generators answers GET .../thumbnails/generators: the ready thumbnails in
// the caller's reach, counted by who drew them, the most first. A caller held
// to a folder is counted the root's own storage only (problemReach): the other
// storages' counts are not its (filex #185).
func (h *ThumbRepair) Generators(w http.ResponseWriter, r *http.Request) {
	reach := trash.Reach(r.Context())
	if root, confined := confine.RootFrom(r.Context()); confined {
		reach = h.problemReach(r, root, reach)
	}
	counts, err := h.Store.ThumbnailGenerators(r.Context(), reach)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]thumbGeneratorRow, 0, len(counts))
	for g, n := range counts {
		id, version, _ := strings.Cut(g, "@")
		out = append(out, thumbGeneratorRow{Generator: g, App: assoc.AppOf(id), Version: version, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Generator < out[j].Generator
	})
	writeJSON(w, http.StatusOK, map[string]any{"generators": out})
}

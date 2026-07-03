// Package handlers — dashboard.go
//
// GET /api/admin/dashboard returns aggregated stats for the admin landing
// page: per-storage summary, user/share/session counts, queue depth, recent
// activity and a short capability summary.
package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
)

// Dashboard handles /api/admin/dashboard.
type Dashboard struct {
	Store  db.Store
	Caps   *capability.Service
	Worker *syncpkg.Worker
}

// NewDashboard constructs the handler.
func NewDashboard(store db.Store, caps *capability.Service, worker *syncpkg.Worker) *Dashboard {
	return &Dashboard{Store: store, Caps: caps, Worker: worker}
}

// StorageSummary is a per-storage row in the dashboard response.
type StorageSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Driver         string `json:"driver"`
	MountPath      string `json:"mount_path"`
	Enabled        bool   `json:"enabled"`
	TotalFiles     int64  `json:"total_files"`
	TotalBytes     int64  `json:"total_bytes"`
	LastSyncAt     any    `json:"last_sync_at,omitempty"`
	LastSyncStatus string `json:"last_sync_status,omitempty"`
	State          string `json:"state"` // ok | stale | error | running
}

// CapabilitiesShort is the trimmed capability summary embedded in the
// dashboard payload (full capabilities are at /api/capabilities).
type CapabilitiesShort struct {
	FFmpeg              bool `json:"ffmpeg"`
	Ghostscript         bool `json:"ghostscript"`
	LibreOffice         bool `json:"libreoffice"`
	OnlyOfficeReachable bool `json:"onlyoffice_reachable"`
}

// Response is the shape returned to the admin UI.
type Response struct {
	Storages       []StorageSummary    `json:"storages"`
	TotalUsers     int64               `json:"total_users"`
	ActiveSessions int64               `json:"active_sessions"`
	QueueDepth     int                 `json:"queue_depth"`
	TotalFiles     int64               `json:"total_files"`
	TotalBytes     int64               `json:"total_bytes"`
	RecentActivity []*model.AuditEntry `json:"recent_activity"`
	Capabilities   CapabilitiesShort   `json:"capabilities"`
}

// Get renders the dashboard payload.
func (h *Dashboard) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	storages, err := h.Store.ListStorages(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows := make([]StorageSummary, 0, len(storages))
	var aggFiles, aggBytes int64
	for _, st := range storages {
		count, size, _ := h.Store.StorageStats(ctx, st.ID)
		aggFiles += count
		aggBytes += size
		row := StorageSummary{
			ID:         st.ID,
			Name:       st.Name,
			Driver:     st.Driver,
			MountPath:  st.MountPath,
			Enabled:    st.Enabled,
			TotalFiles: count,
			TotalBytes: size,
			State:      "ok",
		}
		if last, err := h.Store.GetLastSyncRun(ctx, st.ID); err == nil && last != nil {
			row.LastSyncAt = last.StartedAt
			row.LastSyncStatus = last.Status
			if last.Status == "error" {
				row.State = "error"
			} else if last.Status == "running" {
				row.State = "running"
			}
		} else {
			row.State = "stale"
		}
		rows = append(rows, row)
	}

	totalUsers, _ := h.Store.CountUsers(ctx)
	activeSessions, _ := h.Store.CountActiveSessions(ctx)

	queueDepth := 0
	if h.Worker != nil {
		queueDepth = h.Worker.QueueDepth()
	}

	recent, _ := h.Store.ListAuditRecent(ctx, 10)
	if recent == nil {
		recent = []*model.AuditEntry{}
	}

	capShort := CapabilitiesShort{}
	if h.Caps != nil {
		if cap, err := h.Caps.Get(ctx); err == nil && cap != nil {
			capShort.FFmpeg = cap.Thumbs.Video
			capShort.Ghostscript = cap.Thumbs.PDF
			capShort.LibreOffice = cap.Thumbs.Office
			if oo, ok := cap.External["onlyoffice"]; ok {
				capShort.OnlyOfficeReachable = oo.State == "ok"
			}
		}
	}

	writeJSON(w, http.StatusOK, Response{
		Storages:       rows,
		TotalUsers:     totalUsers,
		ActiveSessions: activeSessions,
		QueueDepth:     queueDepth,
		TotalFiles:     aggFiles,
		TotalBytes:     aggBytes,
		RecentActivity: recent,
		Capabilities:   capShort,
	})
}

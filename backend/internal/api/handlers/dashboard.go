// Package handlers — dashboard.go
//
// GET /api/admin/dashboard returns aggregated stats for the admin landing
// page: per-storage summary, user/share/session counts, queue depth, recent
// activity and a short capability summary.
package handlers

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
)

// QueueStats is the one question the dashboard asks the job queue: how many
// jobs are waiting or running. queue.Driver satisfies it.
type QueueStats interface {
	Stats(ctx context.Context) (queue.Stats, error)
}

// Dashboard handles /api/admin/dashboard.
type Dashboard struct {
	Store db.Store
	Caps  *capability.Service
	// Queue answers "Queue depth": the jobs waiting or running in the op queue
	// (the same counters the Queue page's Pending and Running cards show).
	// Nil reads as an empty queue.
	Queue QueueStats
	// DemoMode marks a public playground. The `recent_activity` block below
	// is the same audit rows the Audit page serves, client addresses and all,
	// on the FIRST page of the admin panel. See maskAuditRecent.
	DemoMode bool
}

// NewDashboard constructs the handler. q is the op queue ("Queue depth");
// nil reads as an empty queue.
func NewDashboard(store db.Store, caps *capability.Service, q queue.Driver) *Dashboard {
	h := &Dashboard{Store: store, Caps: caps}
	if q != nil {
		h.Queue = q
	}
	return h
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

// ActivityRow is one Recent activity line: the audit row, flat, plus the e-mail
// of the account that acted.
//
// ⚠ The page prints the actor under every action, and the payload never had
// one — the audit LIST joins users.email (db.AuditEntryWithUser), the dashboard
// read the bare rows — so each line said "— · Profile".
type ActivityRow struct {
	*model.AuditEntry
	UserEmail string `json:"user_email,omitempty"`
	// TargetName: which thing, in words (handlers/audit_targets.go).
	TargetName string `json:"target_name,omitempty"`
	// UserName is who acted, as every screen names a person (PersonLabel).
	UserName string `json:"user_name,omitempty"`
}

// Response is the shape returned to the admin UI.
type Response struct {
	Storages       []StorageSummary  `json:"storages"`
	TotalUsers     int64             `json:"total_users"`
	ActiveSessions int64             `json:"active_sessions"`
	QueueDepth     int               `json:"queue_depth"`
	TotalFiles     int64             `json:"total_files"`
	TotalBytes     int64             `json:"total_bytes"`
	RecentActivity []ActivityRow     `json:"recent_activity"`
	Capabilities   CapabilitiesShort `json:"capabilities"`
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
			// ⚠ The statuses the sync worker actually writes (poll.go). This
			// compared with "error", which it has never written, so a storage
			// whose last scan failed showed as "ok" here.
			switch last.Status {
			case "failed":
				row.State = "error"
			case "running":
				row.State = "running"
			case "aborted":
				// Stopped before it finished (closed when the server next
				// started): the catalogue is behind the backend until a run
				// completes.
				row.State = "stale"
			}
		} else {
			row.State = "stale"
		}
		rows = append(rows, row)
	}

	totalUsers, _ := h.Store.CountUsers(ctx)
	activeSessions, _ := h.Store.CountActiveSessions(ctx)
	// The storage rows above are already confined — they come from the scoped
	// ListStorages. These two counters are not: they are instance-wide SQL
	// aggregates, so a tenant admin's dashboard was quietly reporting the
	// platform's total headcount and live sessions.
	//
	// ⚠ The user count is recomputed from the scoped directory. The session
	// count has no per-tenant form (sessions carry a user, but there is no
	// count-by-provider query), so a confined tenant is shown its OWN users'
	// sessions only if that is cheap — it is not, so the field is zeroed
	// rather than reported wrong. A zero is honest; the platform total is not.
	if scope, confined := confinedScope(ctx); confined {
		activeSessions = 0
		if users, uerr := h.Store.ListUsersByProvider(ctx, scope.ProviderID); uerr == nil {
			totalUsers = int64(len(users))
		}
	}

	// ⚠⚠ Queue depth is the JOB QUEUE's backlog — pending plus running, the
	// same two numbers the Queue page shows. It used to be the number of
	// storage watchers the sync worker had started (one per enabled storage),
	// so the panel read "Queue depth 2" while the Queue page said 0 pending
	// and 0 running (release-candidate sweep, 2026-09-21). The queue is
	// instance-wide and its page is supertenant-only, so a confined tenant
	// admin is shown 0 rather than the platform's backlog.
	queueDepth := 0
	if h.Queue != nil {
		if _, confined := confinedScope(ctx); !confined {
			if qs, qerr := h.Queue.Stats(ctx); qerr == nil {
				queueDepth = int(qs.Pending + qs.Running)
			}
		}
	}

	recent, _ := h.Store.ListAuditRecent(ctx, 10)
	if h.DemoMode {
		maskAuditRecent(recent)
	}
	if recent == nil {
		recent = []*model.AuditEntry{}
	}
	// Same filter the audit list itself applies (handlers/audit.go): a tenant
	// admin sees its own users' activity, and system entries with no user stay
	// supertenant-only. Without this the dashboard's "recent activity" panel
	// was a live feed of other customers' file operations.
	if scope, confined := confinedScope(ctx); confined {
		allowed := map[int64]bool{}
		if users, uerr := h.Store.ListUsersByProvider(ctx, scope.ProviderID); uerr == nil {
			for _, u := range users {
				allowed[u.ID] = true
			}
		}
		kept := recent[:0]
		for _, e := range recent {
			if e != nil && e.UserID != nil && allowed[*e.UserID] {
				kept = append(kept, e)
			}
		}
		recent = kept
	}

	emails := map[int64]string{}
	names := map[int64]string{}
	if users, uerr := h.Store.ListUsers(ctx); uerr == nil {
		for _, u := range users {
			emails[u.ID] = u.Email
			names[u.ID] = u.Label()
		}
	}
	activity := make([]ActivityRow, 0, len(recent))
	namer := newAuditNamer(ctx, h.Store)
	for _, e := range recent {
		if e == nil {
			continue
		}
		row := ActivityRow{AuditEntry: e, TargetName: namer.name(e)}
		if e.UserID != nil {
			row.UserEmail = emails[*e.UserID]
			row.UserName = names[*e.UserID]
		}
		activity = append(activity, row)
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
		RecentActivity: activity,
		Capabilities:   capShort,
	})
}

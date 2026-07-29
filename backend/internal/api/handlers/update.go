package handlers

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/brf-tech/filex/backend/internal/update"
	"github.com/brf-tech/filex/backend/internal/version"
)

// Update is the admin-facing surface of the release checker:
//
//	GET  /api/admin/update        → what we know (cached, no network)
//	POST /api/admin/update/check  → check now
//	POST /api/admin/update/apply  → install the pending release (binary installs)
//
// The split matters: reading the status must never block on a slow or absent
// network, and applying must never be a side effect of looking.
type Update struct{ svc *update.Service }

// NewUpdate builds the handler. A nil service means the feature is compiled in
// but disabled by config; the endpoints then answer with a quiet "disabled"
// status rather than 404, so the UI can explain the state.
func NewUpdate(svc *update.Service) *Update { return &Update{svc: svc} }

type updateStatusResponse struct {
	Current         string          `json:"current"`
	Enabled         bool            `json:"enabled"`
	Policy          string          `json:"policy"`
	Mode            string          `json:"mode"`
	CanSelfApply    bool            `json:"can_self_apply"`
	RestartRequired bool            `json:"restart_required"`
	Checked         string          `json:"checked_at,omitempty"`
	CheckError      string          `json:"check_error,omitempty"`
	Action          string          `json:"action"`
	Step            string          `json:"step"`
	Reason          string          `json:"reason,omitempty"`
	Latest          *updateRelease  `json:"latest,omitempty"`
	Skipped         []updateRelease `json:"skipped,omitempty"`
	Instructions    []string        `json:"instructions,omitempty"`
	LastApplied     string          `json:"last_applied,omitempty"`
	LastApplyError  string          `json:"last_apply_error,omitempty"`
}

type updateRelease struct {
	Version    string `json:"version"`
	Date       string `json:"date,omitempty"`
	Notes      string `json:"notes,omitempty"`
	NotesURL   string `json:"notes_url,omitempty"`
	Security   bool   `json:"security,omitempty"`
	Migrations bool   `json:"migrations,omitempty"`
}

func toRelease(r update.Release) updateRelease {
	return updateRelease{
		Version:    r.Version,
		Date:       r.Date,
		Notes:      r.Notes,
		NotesURL:   r.NotesURL,
		Security:   r.IsSecurity(),
		Migrations: r.Migrations,
	}
}

// Status returns the cached view. Cheap by design — the admin shell polls it.
func (h *Update) Status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.status())
}

// Check forces a fetch. Used by the "check now" button; a failure is reported
// in the payload (check_error) rather than as an HTTP error, because "I could
// not reach the update server" is information, not a server fault.
func (h *Update) Check(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil || !h.svc.Enabled() {
		writeJSON(w, http.StatusOK, h.status())
		return
	}
	_, _ = h.svc.Check(r.Context())
	writeJSON(w, http.StatusOK, h.status())
}

// Apply installs the pending release. Refused with 409 when the install cannot
// replace itself (container) — a permanent condition the UI turns into
// instructions, and 409 keeps it distinct from a transient 5xx.
func (h *Update) Apply(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil || !h.svc.Enabled() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "update checking is disabled"})
		return
	}
	d, ok := h.svc.Decision()
	if !ok {
		if _, err := h.svc.Check(r.Context()); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "cannot reach the update server: " + err.Error()})
			return
		}
		d, _ = h.svc.Decision()
	}
	if d.Action == update.ActionNone {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "already up to date"})
		return
	}
	if !h.svc.Mode().CanSelfApply() {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        update.ErrNotSelfApplicable.Error(),
			"instructions": h.instructions(d),
		})
		return
	}
	if err := h.svc.Apply(r.Context(), d.Target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"applied":          d.Target.Version,
		"restart_required": h.svc.RestartRequired(),
	})
}

func (h *Update) status() updateStatusResponse {
	resp := updateStatusResponse{
		Current: version.Version,
		Action:  string(update.ActionNone),
		Step:    string(update.StepNone),
	}
	if h.svc == nil {
		resp.Reason = "update checking is disabled"
		resp.Policy = string(update.PolicyOff)
		resp.Mode = string(update.DetectInstallMode())
		return resp
	}
	st := h.svc.State()
	resp.Enabled = h.svc.Enabled()
	resp.Policy = string(h.svc.Policy())
	resp.Mode = string(h.svc.Mode())
	resp.CanSelfApply = h.svc.Mode().CanSelfApply()
	resp.RestartRequired = h.svc.RestartRequired()
	resp.CheckError = st.LastError
	resp.LastApplied = st.LastApplied
	resp.LastApplyError = st.LastApplyError
	if !st.LastCheck.IsZero() {
		resp.Checked = st.LastCheck.UTC().Format("2006-01-02T15:04:05Z")
	}
	if d, ok := h.svc.Decision(); ok {
		resp.Action = string(d.Action)
		resp.Step = string(d.Step)
		resp.Reason = d.Reason
		if d.Target.Version != "" {
			rel := toRelease(d.Target)
			resp.Latest = &rel
		}
		for _, s := range d.Skipped {
			resp.Skipped = append(resp.Skipped, toRelease(s))
		}
		if d.Action == update.ActionInstruct || !resp.CanSelfApply {
			resp.Instructions = h.instructions(d)
		}
	}
	return resp
}

// instructions renders the copy-paste upgrade steps for this install shape.
// Generated server-side because only the server knows how it was installed —
// the UI should render text, not guess a deployment model.
func (h *Update) instructions(d update.Decision) []string {
	ver := strings.TrimSpace(d.Target.Version)
	if ver == "" {
		ver = "<version>"
	}
	if h.svc != nil && h.svc.Mode() == update.ModeDocker {
		image := d.Target.Image
		if image == "" {
			image = "ghcr.io/brf-tech/filex:" + ver
		}
		steps := []string{
			"docker compose down",
			"# docker-compose.yml: image: " + image,
			"docker compose pull filex",
			"docker compose up -d",
		}
		if d.Step == update.StepMajor || d.Target.Migrations {
			steps = append([]string{"# back up your database and data directory first"}, steps...)
		}
		return steps
	}
	steps := []string{"filex self-update"}
	if d.Step == update.StepMajor {
		steps = []string{
			"# major release — read the notes first: " + d.Target.NotesURL,
			"filex self-update --to " + ver,
		}
	}
	steps = append(steps, "# or download manually for "+runtime.GOOS+"/"+runtime.GOARCH+" and restart the service")
	return steps
}

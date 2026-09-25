package handlers

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
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

	// PackageManager and PackageManagerName say who owns a mode=package
	// binary (homebrew / winget / snap, and the name a person writes);
	// UpgradeCommand is that manager's command for this install. All three
	// are empty for other modes, and the manager is empty when
	// FILEX_INSTALL_MODE=package was set without anything detectable.
	PackageManager     string `json:"package_manager,omitempty"`
	PackageManagerName string `json:"package_manager_name,omitempty"`
	UpgradeCommand     string `json:"upgrade_command,omitempty"`

	// Behavior is what this install does by itself about a new release —
	// Policy as far as the install can carry it out: off | announce | patch |
	// minor. PolicyLimit says why that is less than Policy asks for
	// (disabled | container | package | zero_major) and is absent when the
	// policy is in force; Policy stays what the operator saved. Worked out by
	// update.EffectiveOf: the page words these two and works nothing out from
	// mode + policy (#72: a Homebrew install set to "patch" read "install
	// patches", which it never does).
	Behavior    string `json:"behavior"`
	PolicyLimit string `json:"policy_limit,omitempty"`
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

// updateIsInstanceWide is the refusal a tenant admin reads. Applying a release
// replaces the binary every tenant is served by and requires a restart of the
// whole instance; the status read is gated with it because an admin who cannot
// apply an update has nothing to do with the answer.
const updateIsInstanceWide = "software updates apply to the whole instance and are managed by the platform operator"

// Status returns the cached view. Cheap by design — the admin shell polls it.
func (h *Update) Status(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, updateIsInstanceWide) {
		return
	}
	writeJSON(w, http.StatusOK, h.status(langOf(r)))
}

// Check forces a fetch. Used by the "check now" button; a failure is reported
// in the payload (check_error) rather than as an HTTP error, because "I could
// not reach the update server" is information, not a server fault.
func (h *Update) Check(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, updateIsInstanceWide) {
		return
	}
	if h.svc == nil || !h.svc.Enabled() {
		writeJSON(w, http.StatusOK, h.status(langOf(r)))
		return
	}
	_, _ = h.svc.Check(r.Context())
	writeJSON(w, http.StatusOK, h.status(langOf(r)))
}

// Apply installs the pending release. Refused with 409 when the install cannot
// replace itself (container, package manager) — a permanent condition the UI
// turns into instructions, and 409 keeps it distinct from a transient 5xx.
func (h *Update) Apply(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, updateIsInstanceWide) {
		return
	}
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
	if refusal := h.svc.Install().Refusal(); refusal != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        refusal.Error(),
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

func (h *Update) status(lang string) updateStatusResponse {
	resp := updateStatusResponse{
		Current: version.Version,
		Action:  string(update.ActionNone),
		Step:    string(update.StepNone),
	}
	if h.svc == nil {
		resp.Reason = srvtext.Text(lang, "server.update.reason.disabled", nil)
		resp.Policy = string(update.PolicyOff)
		inst := update.DetectInstall()
		describeInstall(&resp, inst)
		describeEffective(&resp, update.EffectiveOf(update.PolicyOff, false, inst.Mode, update.Version{}))
		resp.CanSelfApply = false // no updater, nothing to apply with
		return resp
	}
	st := h.svc.State()
	resp.Enabled = h.svc.Enabled()
	resp.Policy = string(h.svc.Policy())
	describeInstall(&resp, h.svc.Install())
	describeEffective(&resp, h.svc.Effective())
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
		if d.ReasonKey != "" {
			resp.Reason = srvtext.Text(lang, d.ReasonKey, reasonVars(lang, d.ReasonVars))
		}
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

// describeInstall fills what the page needs to know about how filex was
// installed. can_self_apply is only ever true for a plain binary.
func describeInstall(resp *updateStatusResponse, inst update.Install) {
	resp.Mode = string(inst.Mode)
	resp.CanSelfApply = inst.Mode.CanSelfApply()
	if inst.Mode == update.ModePackage {
		resp.PackageManager = string(inst.Manager)
		resp.PackageManagerName = inst.Manager.Label()
		resp.UpgradeCommand = inst.UpgradeCommand()
	}
}

// describeEffective fills what the policy badge says: what the install does,
// and why that is less than the saved policy when it is.
func describeEffective(resp *updateStatusResponse, e update.Effective) {
	resp.Behavior = string(e.Behavior)
	resp.PolicyLimit = string(e.Limit)
}

// instructions renders the copy-paste upgrade steps for this install shape.
// Generated server-side because only the server knows how it was installed —
// the UI should render text, not guess a deployment model.
func (h *Update) instructions(d update.Decision) []string {
	ver := strings.TrimSpace(d.Target.Version)
	if ver == "" {
		ver = "<version>"
	}
	backup := d.Step == update.StepMajor || d.Target.Migrations
	if h.svc != nil && h.svc.Mode() == update.ModePackage {
		return packageInstructions(h.svc.Install(), backup)
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
		if backup {
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

// packageInstructions are the steps for a binary a package manager owns: its
// own upgrade command, never `filex self-update`. The manager replaces the
// file, not the process, so a restart follows; and the snapshot a self-upgrade
// takes before a schema change does not happen here, so the backup is asked
// for in words.
func packageInstructions(inst update.Install, backup bool) []string {
	var steps []string
	if backup {
		steps = append(steps, "# back up your database and data directory first")
	}
	cmd := inst.UpgradeCommand()
	switch {
	case cmd == "":
		steps = append(steps, "# upgrade filex with the package manager that installed it")
	case inst.Manager == update.ManagerWinget:
		// Windows will not let a package manager delete or overwrite an
		// executable that is running.
		steps = append(steps, "# stop filex first: a running filex.exe cannot be replaced", cmd)
	default:
		steps = append(steps, cmd)
	}
	return append(steps, "# then restart filex, so the new version is the one running")
}

// reasonVars fills a decision's placeholders for lang: the policy is named by
// its word in that language (`server.update.policy.*`), not by the setting's
// value ("manual").
func reasonVars(lang string, in map[string]string) srvtext.Vars {
	out := srvtext.Vars{}
	for k, v := range in {
		out[k] = v
	}
	if p, ok := out["policy"]; ok && p != "" {
		out["policy"] = srvtext.Text(lang, "server.update.policy."+p, nil)
	}
	return out
}

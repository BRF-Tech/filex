package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/dbsetting"
	"github.com/brf-tech/filex/backend/internal/tenancy"
)

// TenancyAdmin is Admin → Multi-tenant mode (internal/tenancy):
//
//	GET /api/admin/tenancy   the mode in force, the one the next start will
//	                         run with, what pinned it, how many tenants
//	PUT /api/admin/tenancy   {"enabled": bool, "confirm": "<n>"}
//
// # Who
//
// The platform operator alone: the mode is the whole instance's. On a
// multi-tenant install a tenant's administrator is refused both (403
// supertenant_only, requireSupertenant); on a single-tenant install every
// administrator is the platform's. A change is a person's, signed in to the
// panel (sessionOnly): an API key reads the switch and cannot flip it.
//
// # What a change does
//
// It saves the `tenancy.multi_tenant` setting and nothing else. The mode in
// force changes at the next start (the reasons are in package tenancy), and
// until then both answers say `restart_required`. A mode the environment or
// the config file pins is refused with 409 `tenancy_locked`, naming which.
//
// Turning the mode OFF while the install has tenants besides the platform's
// own is maintenance mode: nothing is deleted, the tenants cannot sign in
// until the mode is back on. It is refused with 409 `confirm_required` and
// the number of tenants unless `confirm` is that number, so it is never one
// stray click. Both directions are recorded in the audit log
// (tenancy.enable / tenancy.disable, with the tenant count).
type TenancyAdmin struct {
	Store db.Store
	// Cfg is the configuration this server started with, after
	// tenancy.Resolve: MultiTenant is the mode in force, MultiTenantFrom what
	// pinned it.
	Cfg config.Config
}

// NewTenancyAdmin constructs the handler.
func NewTenancyAdmin(store db.Store, cfg config.Config) *TenancyAdmin {
	return &TenancyAdmin{Store: store, Cfg: cfg}
}

// refuseTenancySetting answers 400 to a write of the switch's row through the
// generic settings API (PUT /api/admin/settings/{key}, PATCH
// /api/admin/settings, and the admin_settings_* MCP tools behind them) and
// reports true; any other key is false and left alone. That door would save
// the mode with no lock, no confirmation and no tenancy.* audit row: one
// spelling of a key away from putting every tenant in maintenance mode.
func refuseTenancySetting(w http.ResponseWriter, key string) bool {
	if key != tenancy.SettingKey {
		return false
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error":   "tenancy_setting",
		"message": "multi-tenant mode is switched with PUT /api/admin/tenancy (the Multi-tenant mode page of the admin panel), which asks before it puts tenants in maintenance mode and records the change",
	})
	return true
}

// tenancyIsThePlatforms is the refusal a tenant's administrator reads.
const tenancyIsThePlatforms = "multi-tenant mode applies to the whole instance and is switched by the platform operator"

// tenancyView is the switch as the page reads it.
type tenancyView struct {
	// InForce is the mode this server runs with (the capabilities answer's
	// `multi_tenant`).
	InForce bool `json:"in_force"`
	// NextStart is the mode the next start will run with.
	NextStart bool `json:"next_start"`
	// Saved is the switch's stored value; null when it was never saved.
	Saved *bool `json:"saved"`
	// Locked: the environment or the config file pins the mode, and the
	// switch cannot change it.
	Locked bool `json:"locked"`
	// LockedBy is "environment" or "config_file" when Locked.
	LockedBy string `json:"locked_by,omitempty"`
	// Variable is what to change instead: FILEX_MULTI_TENANT, or the config
	// file's `multi_tenant`.
	Variable string `json:"variable,omitempty"`
	// RestartRequired: the next start runs another mode than this one.
	RestartRequired bool `json:"restart_required"`
	// Tenants is how many tenants the install has besides the platform's
	// own: the ones turning the mode off puts in maintenance.
	Tenants int `json:"tenants"`
}

func (h *TenancyAdmin) view(r *http.Request) tenancyView {
	ctx := r.Context()
	v := tenancyView{InForce: h.Cfg.MultiTenant, LockedBy: tenancy.LockedBy(h.Cfg)}
	v.Locked = v.LockedBy != ""
	switch v.LockedBy {
	case tenancy.LockedByEnvironment:
		v.Variable = tenancy.EnvVar
	case tenancy.LockedByConfigFile:
		v.Variable = "multi_tenant"
	}
	if on, ok := tenancy.Saved(ctx, h.Store); ok {
		v.Saved = &on
	}
	v.NextStart = tenancy.NextStart(ctx, h.Store, h.Cfg)
	v.RestartRequired = v.NextStart != v.InForce
	if n, err := tenancy.CountTenants(ctx, h.Store); err == nil {
		v.Tenants = n
	}
	return v
}

// Get answers the switch.
func (h *TenancyAdmin) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, tenancyIsThePlatforms) {
		return
	}
	writeJSON(w, http.StatusOK, h.view(r))
}

// Put saves the switch for the next start.
func (h *TenancyAdmin) Put(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, tenancyIsThePlatforms) {
		return
	}
	if !sessionOnly(w, r, "Switching multi-tenant mode needs an administrator signed in to the admin panel; an API key cannot do it.", nil) {
		return
	}
	var req struct {
		Enabled *bool  `json:"enabled"`
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled_required", "message": "say \"enabled\": true or false"})
		return
	}
	ctx := r.Context()
	if by := tenancy.LockedBy(h.Cfg); by != "" {
		what := tenancy.EnvVar
		if by == tenancy.LockedByConfigFile {
			what = "multi_tenant in the config file"
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     "tenancy_locked",
			"locked_by": by,
			"message":   what + " sets multi-tenant mode; change it there and restart filex",
		})
		return
	}
	n, err := tenancy.CountTenants(ctx, h.Store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tenants: " + err.Error()})
		return
	}
	want := *req.Enabled
	if want == tenancy.NextStart(ctx, h.Store, h.Cfg) {
		// Nothing changes: no row, and the answer is the switch as it stands.
		auth.SkipAuditRow(ctx)
		writeJSON(w, http.StatusOK, h.view(r))
		return
	}
	if !want && n > 0 && strings.TrimSpace(req.Confirm) != strconv.Itoa(n) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "confirm_required",
			"tenants": n,
			"message": "the install has " + strconv.Itoa(n) + " tenant(s) besides the platform's own; turning multi-tenant mode off puts them in maintenance mode (nothing is deleted). Send \"confirm\": \"" + strconv.Itoa(n) + "\" to go on",
		})
		return
	}
	if err := h.Store.UpsertSetting(ctx, tenancy.SettingKey, dbsetting.FormatBool(want)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	action := tenancy.ActionDisable
	if want {
		action = tenancy.ActionEnable
	}
	auth.SetAuditAction(ctx, action, tenancy.AuditTarget)
	auth.AddAuditDetail(ctx, "tenants", n)
	auth.AddAuditDetail(ctx, "in_force", h.Cfg.MultiTenant)
	auth.AddAuditDetail(ctx, "restart_required", want != h.Cfg.MultiTenant)
	writeJSON(w, http.StatusOK, h.view(r))
}

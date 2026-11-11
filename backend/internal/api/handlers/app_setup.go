package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Setting an app up, Reconfigure, removing it (filex 0.60.0) ─────────────
//
// docs/APP-PLUGINS-API.md → Setting an app up. The platform operator's, like
// every other app door (gate):
//
//	GET    /api/admin/app-plugins/{id}/setup                  the wizard's facts
//	GET    /api/admin/app-plugins/{id}/setup/view             the app's own step, opened
//	POST   /api/admin/app-plugins/{id}/setup/view             one event of it
//	POST   /api/admin/app-plugins/{id}/setup/routes/{route}/bases   a base for a route
//	PATCH  /api/admin/app-plugins/{id}/setup/bases/{base}     on/off, the default
//	DELETE /api/admin/app-plugins/{id}/setup/bases/{base}     while not finished
//	POST   /api/admin/app-plugins/{id}/setup/complete         finish: the app runs
//	GET    /api/admin/app-plugins/{id}/erase                  what Reconfigure / removal erase
//	POST   /api/admin/app-plugins/{id}/reconfigure            {"confirm": "<app name>"}

// Setup answers GET …/{id}/setup.
func (h *AppPluginsAdmin) Setup(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	a, err := h.Registry.SetupStatus(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type setupViewReq struct {
	Event    string         `json:"event"`
	ActionID string         `json:"action_id,omitempty"`
	State    map[string]any `json:"state,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// SetupView answers GET (the step opened) and POST (one event) of the app's
// own setup step.
func (h *AppPluginsAdmin) SetupView(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	req := setupViewReq{Event: "open"}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			writeError(w, r, http.StatusBadRequest, "bad_json", nil)
			return
		}
		if req.Event == "" {
			req.Event = "open"
		}
	}
	s, done, err := h.Registry.SetupView(r.Context(), p.Row.ID, auth.UserFrom(r.Context()), langOf(r),
		wire.ViewEventInput{Event: req.Event, ActionID: req.ActionID, State: req.State, Data: req.Data})
	if err != nil {
		if errors.Is(err, wasmplugin.ErrNoSetup) {
			writeError(w, r, http.StatusNotFound, "setup_not_needed", nil)
			return
		}
		writeAppCallFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"surface": s, "app_done": done})
}

type addBaseReq struct {
	Host     string `json:"host"`
	Prefix   string `json:"prefix"`
	TenantID int64  `json:"tenant_id"`
	Default  bool   `json:"default"`
}

// AddRouteBase answers POST …/setup/routes/{route}/bases.
func (h *AppPluginsAdmin) AddRouteBase(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "setting an app's addresses") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var req addBaseReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	b, err := h.Registry.AddRouteBase(r.Context(), p.Row.ID, chi.URLParam(r, "route"), req.Host, req.Prefix, req.TenantID, req.Default)
	if err != nil {
		h.setupFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"base": b})
}

// PatchRouteBase answers PATCH …/setup/bases/{base} {enabled?, default?}.
func (h *AppPluginsAdmin) PatchRouteBase(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "setting an app's addresses") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "base"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_id", nil)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
		Default *bool `json:"default"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	b, err := h.Registry.PatchRouteBase(r.Context(), p.Row.ID, id, req.Enabled, req.Default)
	if err != nil {
		h.setupFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"base": b})
}

// DeleteRouteBase answers DELETE …/setup/bases/{base}.
func (h *AppPluginsAdmin) DeleteRouteBase(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "setting an app's addresses") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "base"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_id", nil)
		return
	}
	if err := h.Registry.DeleteRouteBase(r.Context(), p.Row.ID, id); err != nil {
		h.setupFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CompleteSetup answers POST …/setup/complete.
func (h *AppPluginsAdmin) CompleteSetup(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "setting an app up") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var by int64
	if u := auth.UserFrom(r.Context()); u != nil {
		by = u.ID
	}
	if err := h.Registry.CompleteSetup(r.Context(), p.Row.ID, by); err != nil {
		h.setupFail(w, r, err)
		return
	}
	h.auditApp(r, "app_plugin.setup", p.Row.Name, nil)
	cur, _ := h.Registry.ByID(p.Row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"plugin": h.Registry.StatusOf(cur)})
}

// Erase answers GET …/{id}/erase: what Reconfigure and removal would erase.
func (h *AppPluginsAdmin) Erase(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	a, err := h.Registry.EraseFacts(r.Context(), p.Row.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

type confirmReq struct {
	Confirm string `json:"confirm"`
}

// Reconfigure answers POST …/{id}/reconfigure {"confirm": "<app name>"}.
func (h *AppPluginsAdmin) Reconfigure(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "reconfiguring an app") {
		return
	}
	p, ok := h.plugin(w, r)
	if !ok {
		return
	}
	var req confirmReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	facts, _ := h.Registry.EraseFacts(r.Context(), p.Row.ID)
	var by int64
	if u := auth.UserFrom(r.Context()); u != nil {
		by = u.ID
	}
	if err := h.Registry.Reconfigure(r.Context(), p.Row.ID, req.Confirm, by); err != nil {
		h.setupFail(w, r, err)
		return
	}
	var counts *wasmplugin.EraseCounts
	if facts != nil {
		counts = &facts.Counts
	}
	h.auditApp(r, "app_plugin.reconfigure", p.Row.Name, counts)
	cur, _ := h.Registry.ByID(p.Row.ID)
	writeJSON(w, http.StatusOK, map[string]any{"plugin": h.Registry.StatusOf(cur)})
}

// removeConfirm reads the typed name a removal carries: a JSON body
// {"confirm"} or ?confirm=.
func removeConfirm(r *http.Request) string {
	if c := r.URL.Query().Get("confirm"); c != "" {
		return c
	}
	if r.Body == nil {
		return ""
	}
	var req confirmReq
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
	return req.Confirm
}

// auditApp names what happened on the door's audit row, with the counts of
// what was erased.
func (h *AppPluginsAdmin) auditApp(r *http.Request, action, app string, counts *wasmplugin.EraseCounts) {
	ctx := r.Context()
	if auth.Audited(ctx) {
		auth.SetAuditAction(ctx, action, "app")
		auth.SetAuditTarget(ctx, app, "")
		if counts != nil {
			auth.AddAuditDetail(ctx, "data_keys", counts.DataKeys)
			auth.AddAuditDetail(ctx, "state_rows", counts.StateRows)
			auth.AddAuditDetail(ctx, "locks", counts.Locks)
			auth.AddAuditDetail(ctx, "links", counts.Links)
		}
		return
	}
	if h.Audit != nil {
		var uid *int64
		if u := auth.UserFrom(ctx); u != nil {
			id := u.ID
			uid = &id
		}
		_ = h.Audit(ctx, uid, action, 0, app, clientIP(r))
	}
}

// setupFail answers a setup door's refusal in the reader's language, with
// the field a base's refusal is about.
func (h *AppPluginsAdmin) setupFail(w http.ResponseWriter, r *http.Request, err error) {
	var be *wasmplugin.RouteBaseError
	if errors.As(err, &be) {
		writeError(w, r, http.StatusBadRequest, setupCode(be.Err), nil, "field", be.Field)
		return
	}
	switch code := setupCode(err); code {
	case "setup_app_step", "setup_no_base", "setup_done":
		writeError(w, r, http.StatusConflict, code, nil)
	case "confirm_mismatch":
		writeError(w, r, http.StatusBadRequest, code, nil)
	case "no_such_route", "no_such_base", "setup_not_needed":
		writeError(w, r, http.StatusNotFound, code, nil)
	default:
		h.fail(w, r, err)
	}
}

// setupCode is the wire code of a setup refusal.
func setupCode(err error) string {
	for _, e := range []error{
		shortlink.ErrHost, shortlink.ErrPrefix, shortlink.ErrPrefixInUse, shortlink.ErrPrefixNeeded,
		wasmplugin.ErrRouteBaseTaken, wasmplugin.ErrRouteHostTaken, wasmplugin.ErrRouteHostFilex,
		wasmplugin.ErrSetupAppStep, wasmplugin.ErrSetupNoBase, wasmplugin.ErrSetupDone,
		wasmplugin.ErrConfirm, wasmplugin.ErrNoSuchRoute, wasmplugin.ErrNoSuchBase, wasmplugin.ErrNoSetup,
		wasmplugin.ErrNoSuchTenant,
	} {
		if errors.Is(err, e) {
			return strings.TrimSpace(e.Error())
		}
	}
	return ""
}

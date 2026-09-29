// Package handlers — plugin_requests.go
//
// Plugin install requests (internal/pluginreq, docs/APP-PLUGINS.md →
// Install requests, docs/PLUGINS.md → Install requests):
//
//	POST /api/admin/plugin-requests                — leave a request (API key or session)
//	GET  /api/admin/plugin-requests[?status=…]     — list (pending by default; `all` for every state)
//	GET  /api/admin/plugin-requests/{id}           — one request, with its frozen manifest and review
//	POST /api/admin/plugin-requests/{id}/approve   — SESSION ONLY: install what the request froze
//	POST /api/admin/plugin-requests/{id}/reject    — SESSION ONLY: {"reason"?: "…"}
//
// ⚠⚠ Why this exists (owner, 2026-09-28): an API key may no longer install,
// upgrade, remove or re-permission a plugin (requireSession). It may leave a
// request; an administrator signed in to the panel decides. Approve and
// reject are therefore session-only here AND there is no MCP tool for either
// (ai_admin.go): a key that could approve its own request would be the old
// hole with an extra step.
//
// ⚠ Instance-wide like the plugins themselves: in multi-tenant mode only the
// supertenant may leave, read or decide a request (requireSupertenant). A
// tenant administrator gets 403.
//
// The service writes the audit rows itself (created, approved, rejected,
// expired, superseded), so these routes are skipped by the audit middleware
// (auth.ActionForPath) and one event is one row, whichever door it came in by.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/pluginreq"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// PluginRequests is the handler set.
type PluginRequests struct {
	Svc *pluginreq.Service
}

// NewPluginRequests constructs the handler.
func NewPluginRequests(svc *pluginreq.Service) *PluginRequests {
	return &PluginRequests{Svc: svc}
}

// waitingMessage is what the requester is told, in the answer they read
// (an agent's tool result, the CLI's output).
const waitingMessage = "The request is waiting for an administrator's approval in the admin panel (Plugins → Install requests). " +
	"An API key cannot approve or reject it; nothing is installed until an administrator does. " +
	"Follow it with GET " + PluginRequestsPath + "/{id}."

func (h *PluginRequests) gate(w http.ResponseWriter, r *http.Request) bool {
	return requireSupertenant(w, r, "plugins are managed by the platform operator")
}

// actorOf is who is asking or deciding: the account, and the key it came
// through when it came through one.
func actorOf(r *http.Request) pluginreq.Actor {
	ctx := r.Context()
	a := pluginreq.Actor{IP: clientIP(r)}
	if u := auth.UserFrom(ctx); u != nil {
		if u.ID > 0 {
			id := u.ID
			a.UserID = &id
		}
		a.Name = u.Label()
	}
	if tok := auth.TokenFrom(ctx); tok != nil {
		id := tok.ID
		a.TokenID = &id
		a.TokenLabel = tok.Label
		if tu := auth.TokenUserFrom(ctx); tu != "" && tu != tok.Label {
			a.TokenLabel = tok.Label + " (" + tu + ")"
		}
	}
	return a
}

// pluginRequestWire is a request on the wire.
type pluginRequestWire struct {
	ID         int64            `json:"id"`
	Kind       string           `json:"kind"`
	Op         string           `json:"op"`
	Name       string           `json:"name"`
	Label      wire.Text        `json:"label,omitempty"`
	PluginID   *int64           `json:"plugin_id,omitempty"`
	SourceKind string           `json:"source_kind"`
	Source     pluginreq.Source `json:"source"`
	Version    string           `json:"version"`
	// FromVersion: the version an upgrade replaces.
	FromVersion    string `json:"from_version,omitempty"`
	SHA256         string `json:"sha256"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	// Permissions are the ids the request asks to grant; PermissionRows the
	// same, each with its label in the reader's language and the app's own
	// reason ({lang: text}, permission_reasons) — the install review's rows.
	Permissions    []string                   `json:"permissions"`
	PermissionRows []wasmplugin.PermissionRow `json:"permission_rows"`
	RequestedBy    *int64                     `json:"requested_by,omitempty"`
	Requester      string                     `json:"requester"`
	TokenLabel     string                     `json:"token_label,omitempty"`
	Reason         string                     `json:"reason"`
	Status         string                     `json:"status"`
	DecidedBy      *int64                     `json:"decided_by,omitempty"`
	Decider        string                     `json:"decider,omitempty"`
	DecidedAt      *time.Time                 `json:"decided_at,omitempty"`
	DecisionNote   string                     `json:"decision_note,omitempty"`
	Result         json.RawMessage            `json:"result,omitempty"`
	ExpiresAt      time.Time                  `json:"expires_at"`
	CreatedAt      time.Time                  `json:"created_at"`
	// Manifest (the frozen filex-app.json, or a storage plugin's feed) and
	// Review (the dry run) are in a single request's answer, not the list.
	Manifest json.RawMessage `json:"manifest,omitempty"`
	Review   json.RawMessage `json:"review,omitempty"`
}

// requestReview is the part of a stored review the wire reads back.
type requestReview struct {
	Manifest *struct {
		Label wire.Text `json:"label"`
	} `json:"manifest"`
	Permissions []wasmplugin.PermissionRow `json:"permissions"`
}

// pluginRequestView renders one request for lang; full adds the manifest and
// the review.
func pluginRequestView(r *model.PluginRequest, lang string, full bool) pluginRequestWire {
	out := pluginRequestWire{
		ID: r.ID, Kind: r.Kind, Op: r.Op, Name: r.Name, PluginID: r.PluginID, SourceKind: r.SourceKind,
		Source: pluginreq.SourceOf(r), Version: r.Version, FromVersion: r.FromVersion,
		SHA256: r.SHA256, ManifestSHA256: r.ManifestSHA256,
		RequestedBy: r.RequestedBy, Requester: r.Requester, TokenLabel: r.TokenLabel, Reason: r.Reason,
		Status: r.Status, DecidedBy: r.DecidedBy, Decider: r.Decider, DecidedAt: r.DecidedAt,
		DecisionNote: r.DecisionNote, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		Permissions: []string{}, PermissionRows: []wasmplugin.PermissionRow{},
	}
	_ = json.Unmarshal([]byte(r.PermissionsJSON), &out.Permissions)
	if out.Permissions == nil {
		out.Permissions = []string{}
	}
	var rv requestReview
	_ = json.Unmarshal([]byte(r.ReviewJSON), &rv)
	if rv.Manifest != nil {
		out.Label = rv.Manifest.Label
	}
	reasons := map[string]wire.Text{}
	for _, row := range rv.Permissions {
		reasons[row.ID] = row.Reason
	}
	for _, id := range out.Permissions {
		label := id
		if p, err := wasmplugin.ParsePermission(id); err == nil {
			label = p.Label(lang)
		}
		out.PermissionRows = append(out.PermissionRows, wasmplugin.PermissionRow{ID: id, Label: label, Reason: reasons[id]})
	}
	if strings.TrimSpace(r.ResultJSON) != "" && json.Valid([]byte(r.ResultJSON)) {
		out.Result = json.RawMessage(r.ResultJSON)
	}
	if full {
		if strings.TrimSpace(r.ManifestJSON) != "" && json.Valid([]byte(r.ManifestJSON)) {
			out.Manifest = json.RawMessage(r.ManifestJSON)
		}
		if strings.TrimSpace(r.ReviewJSON) != "" && json.Valid([]byte(r.ReviewJSON)) {
			out.Review = json.RawMessage(r.ReviewJSON)
		}
	}
	return out
}

// fail answers a refusal from the request service or from the plugin
// layers it drove, with the code a client switches on.
func (h *PluginRequests) fail(w http.ResponseWriter, err error, req *model.PluginRequest, lang string) {
	var pe *pluginreq.Error
	var sup *pluginreq.Superseded
	var ie *wasmplugin.InstallError
	var rej plugin.RejectedError
	body := map[string]any{}
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &sup):
		status = http.StatusConflict
		body["error"] = "superseded"
		body["message"] = "the source no longer serves what this request froze; nothing was installed: " + sup.Why
	case errors.As(err, &pe):
		status = pe.Status
		body["error"] = pe.Code
		body["message"] = pe.Message
	case errors.As(err, &ie):
		// The install review's refusal, in the shape the wizard reads.
		(&AppPluginsAdmin{}).fail(w, err)
		return
	case errors.As(err, &rej):
		status = http.StatusBadRequest
		body["error"] = "refused"
		body["message"] = err.Error()
	default:
		if errors.Is(err, plugin.ErrBadName) {
			status = http.StatusBadRequest
			body["error"] = "bad_request"
		} else {
			status = installStatus(err)
			body["error"] = "failed"
		}
		body["message"] = err.Error()
	}
	if req != nil {
		body["request"] = pluginRequestView(req, lang, false)
	}
	writeJSON(w, status, body)
}

// Create leaves a request: POST /api/admin/plugin-requests. 201 with the new
// request, or 200 with the pending one for the same source.
func (h *PluginRequests) Create(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var in pluginreq.CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad json"})
		return
	}
	req, created, err := h.Svc.Create(r.Context(), in, actorOf(r))
	if err != nil {
		h.fail(w, err, nil, langOf(r))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"request": pluginRequestView(req, langOf(r), false),
		"created": created,
		"message": waitingMessage,
	})
}

// List answers the requests: GET /api/admin/plugin-requests[?status=pending|
// approved|rejected|expired|superseded|all]. Pending by default.
func (h *PluginRequests) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = model.PluginRequestPending
	}
	rows, err := h.Svc.List(r.Context(), status)
	if err != nil {
		h.fail(w, err, nil, langOf(r))
		return
	}
	out := make([]pluginRequestWire, 0, len(rows))
	for _, row := range rows {
		out = append(out, pluginRequestView(row, langOf(r), false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out, "ttl_days": int(h.Svc.TTL() / (24 * time.Hour))})
}

func requestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad id"})
		return 0, false
	}
	return id, true
}

// Get answers one request with its frozen manifest and review.
func (h *PluginRequests) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	req, err := h.Svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err, nil, langOf(r))
		return
	}
	body := map[string]any{"request": pluginRequestView(req, langOf(r), true)}
	if req.Status == model.PluginRequestPending {
		body["message"] = waitingMessage
	}
	writeJSON(w, http.StatusOK, body)
}

// Approve installs what the request froze: session only.
func (h *PluginRequests) Approve(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "approving a plugin request") {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	req, result, err := h.Svc.Approve(r.Context(), id, actorOf(r), langOf(r))
	if err != nil {
		h.fail(w, err, req, langOf(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": pluginRequestView(req, langOf(r), false), "plugin": result})
}

// Reject closes a request without installing anything: session only.
func (h *PluginRequests) Reject(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) || !requireSession(w, r, "rejecting a plugin request") {
		return
	}
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "bad json"})
			return
		}
	}
	req, err := h.Svc.Reject(r.Context(), id, actorOf(r), body.Reason)
	if err != nil {
		h.fail(w, err, req, langOf(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": pluginRequestView(req, langOf(r), false)})
}

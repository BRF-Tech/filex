package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// WebhooksAdmin is the admin CRUD surface for webhook v2 targets
// (multi-destination, event-filtered, HMAC-signed deliveries). The
// legacy single global webhook keeps its own endpoints under
// /api/admin/notifications/webhook-config.
type WebhooksAdmin struct {
	Store  db.Store
	Notify notify.Service
}

// NewWebhooksAdmin constructs the handler.
func NewWebhooksAdmin(store db.Store, svc notify.Service) *WebhooksAdmin {
	return &WebhooksAdmin{Store: store, Notify: svc}
}

// webhookTargetResp is the API projection of a target. The secret is
// NEVER echoed back — only a set/unset flag (write-only credential).
//
// Last-delivery info comes in two shapes: the persisted columns
// (last_http_status / last_error / last_delivery_at, migration 00019 —
// survive restarts, the admin UI's source of truth) plus the legacy
// in-memory last_status object kept for older UI builds.
type webhookTargetResp struct {
	ID         int64                        `json:"id"`
	Name       string                       `json:"name"`
	URL        string                       `json:"url"`
	SecretSet  bool                         `json:"secret_set"`
	Events     []string                     `json:"events"`
	Enabled    bool                         `json:"enabled"`
	CreatedAt  string                       `json:"created_at"`
	LastStatus *notify.TargetDeliveryStatus `json:"last_status,omitempty"`

	LastHTTPStatus *int    `json:"last_http_status,omitempty"`
	LastError      *string `json:"last_error,omitempty"`
	LastDeliveryAt *string `json:"last_delivery_at,omitempty"`
}

func toWebhookTargetResp(t *model.WebhookTarget, statuses map[int64]notify.TargetDeliveryStatus) webhookTargetResp {
	events := t.EventList()
	if events == nil {
		events = []string{}
	}
	resp := webhookTargetResp{
		ID:        t.ID,
		Name:      t.Name,
		URL:       t.URL,
		SecretSet: t.Secret != "",
		Events:    events,
		Enabled:   t.Enabled,
		CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if t.LastStatus != nil {
		v := *t.LastStatus
		resp.LastHTTPStatus = &v
	}
	if t.LastError != nil {
		v := *t.LastError
		resp.LastError = &v
	}
	if t.LastDeliveryAt != nil {
		v := t.LastDeliveryAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		resp.LastDeliveryAt = &v
	}
	if statuses != nil {
		if st, ok := statuses[t.ID]; ok {
			s := st
			resp.LastStatus = &s
		}
	}
	return resp
}

// sanitizeWebhookEvents trims entries, drops empties, and joins back to
// the CSV form the DB stores.
func sanitizeWebhookEvents(events []string) string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return strings.Join(out, ",")
}

func validWebhookURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// List returns every target (enabled or not), secrets masked, plus the
// in-memory last-delivery status per target.
//
//	GET /api/admin/webhooks
func (h *WebhooksAdmin) List(w http.ResponseWriter, r *http.Request) {
	targets, err := h.Store.ListWebhookTargets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var statuses map[int64]notify.TargetDeliveryStatus
	if h.Notify != nil {
		statuses = h.Notify.TargetStatuses()
	}
	items := make([]webhookTargetResp, 0, len(targets))
	for _, t := range targets {
		items = append(items, toWebhookTargetResp(t, statuses))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// webhookTargetCreateReq is the POST body. enabled defaults to true
// when omitted; events empty (or absent) means "all events".
type webhookTargetCreateReq struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
}

// Create adds a new target.
//
//	POST /api/admin/webhooks
//	body: {name, url, secret?, events?: ["file.uploaded",...], enabled?}
func (h *WebhooksAdmin) Create(w http.ResponseWriter, r *http.Request) {
	var req webhookTargetCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if !validWebhookURL(req.URL) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url must start with http:// or https://"})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	created, err := h.Store.CreateWebhookTarget(r.Context(), &model.WebhookTarget{
		Name:    req.Name,
		URL:     req.URL,
		Secret:  req.Secret,
		Events:  sanitizeWebhookEvents(req.Events),
		Enabled: enabled,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toWebhookTargetResp(created, nil))
}

// webhookTargetPatchReq carries partial updates — nil fields keep the
// stored value. Secret semantics: absent → keep; "" → clear; value →
// replace (write-only, never read back).
type webhookTargetPatchReq struct {
	Name    *string   `json:"name"`
	URL     *string   `json:"url"`
	Secret  *string   `json:"secret"`
	Events  *[]string `json:"events"`
	Enabled *bool     `json:"enabled"`
}

// Update patches a target.
//
//	PATCH /api/admin/webhooks/{id}
func (h *WebhooksAdmin) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var req webhookTargetPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	target, err := h.Store.GetWebhookTarget(r.Context(), id)
	if err != nil || target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if req.Name != nil {
		if n := strings.TrimSpace(*req.Name); n != "" {
			target.Name = n
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
			return
		}
	}
	if req.URL != nil {
		u := strings.TrimSpace(*req.URL)
		if !validWebhookURL(u) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url must start with http:// or https://"})
			return
		}
		target.URL = u
	}
	if req.Secret != nil {
		target.Secret = *req.Secret
	}
	if req.Events != nil {
		target.Events = sanitizeWebhookEvents(*req.Events)
	}
	if req.Enabled != nil {
		target.Enabled = *req.Enabled
	}
	if err := h.Store.UpdateWebhookTarget(r.Context(), target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toWebhookTargetResp(target, nil))
}

// Delete removes a target.
//
//	DELETE /api/admin/webhooks/{id}
func (h *WebhooksAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.DeleteWebhookTarget(r.Context(), id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test fires a sample payload at one target synchronously (single
// attempt) and returns the outcome so the admin sees pass/fail with
// the concrete error inline.
//
//	POST /api/admin/webhooks/{id}/test
func (h *WebhooksAdmin) Test(w http.ResponseWriter, r *http.Request) {
	if h.Notify == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	target, err := h.Store.GetWebhookTarget(r.Context(), id)
	if err != nil || target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	result := h.Notify.TestTarget(r.Context(), target)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     result.Status == "sent",
		"result": result,
	})
}

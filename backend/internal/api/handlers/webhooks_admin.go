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
	"github.com/brf-tech/filex/backend/internal/srvtext"
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
	Lang       string                       `json:"lang"`
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
		Lang:      t.Lang,
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

// validWebhookURL: an http(s) address. ⚠ The scheme in any case
// (`HTTPS://hooks.example`): a URL's scheme is case-insensitive, and the
// form said yes to one this said no to (0.54 audit, B10).
func validWebhookURL(u string) bool {
	return hasPrefixFold(u, "http://") || hasPrefixFold(u, "https://")
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// webhookFormRefusal is a refused target form: `error` the English a script
// reads, `field` the box it is about (name | url | lang), `message` the sentence in
// the reader's language. The page shows the sentence under that box and has
// no rule of its own about either.
func webhookFormRefusal(w http.ResponseWriter, r *http.Request, field, url string) {
	code, english := "name_required", "name required"
	switch {
	case field == "url" && url == "":
		code, english = "url_required", "url required"
	case field == "url":
		code, english = "url_scheme", "url must start with http:// or https://"
	case field == "lang":
		code, english = "invalid_lang", "invalid_lang"
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{
		"error":   english,
		"field":   field,
		"message": srvtext.Text(requestLang(r), "server.webhooks."+code, nil),
	})
}

// webhookLang is the language a target is told in, as stored: "" (the
// instance's, FILEX_DEFAULT_LOCALE) or a language the server speaks - one it
// ships or one a running language pack adds, as it serves the tag
// (srvtext.Resolve: `tr-TR` is stored as `tr`). ok=false for anything else.
func webhookLang(in string) (string, bool) {
	if strings.TrimSpace(in) == "" {
		return "", true
	}
	if v := srvtext.Resolve(in); v != "" {
		return v, true
	}
	return "", false
}

// List returns every target (enabled or not), secrets masked, plus the
// in-memory last-delivery status per target.
//
//	GET /api/admin/webhooks
func (h *WebhooksAdmin) List(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "webhook targets receive every tenant's event stream") {
		return
	}
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
	// Lang is the language the target's title and body are said in; "" is
	// the instance's (notify say.go).
	Lang string `json:"lang"`
}

// Create adds a new target.
//
//	POST /api/admin/webhooks
//	body: {name, url, secret?, events?: ["file.uploaded",...], enabled?, lang?}
func (h *WebhooksAdmin) Create(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "webhook targets receive every tenant's event stream") {
		return
	}
	var req webhookTargetCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		webhookFormRefusal(w, r, "name", "")
		return
	}
	if !validWebhookURL(req.URL) {
		webhookFormRefusal(w, r, "url", req.URL)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	lang, ok := webhookLang(req.Lang)
	if !ok {
		webhookFormRefusal(w, r, "lang", "")
		return
	}
	created, err := h.Store.CreateWebhookTarget(r.Context(), &model.WebhookTarget{
		Name:    req.Name,
		URL:     req.URL,
		Secret:  req.Secret,
		Events:  sanitizeWebhookEvents(req.Events),
		Enabled: enabled,
		Lang:    lang,
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
	Lang    *string   `json:"lang"`
}

// Update patches a target.
//
//	PATCH /api/admin/webhooks/{id}
func (h *WebhooksAdmin) Update(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "webhook targets receive every tenant's event stream") {
		return
	}
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
			webhookFormRefusal(w, r, "name", "")
			return
		}
	}
	if req.URL != nil {
		u := strings.TrimSpace(*req.URL)
		if !validWebhookURL(u) {
			webhookFormRefusal(w, r, "url", u)
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
	if req.Lang != nil {
		lang, ok := webhookLang(*req.Lang)
		if !ok {
			webhookFormRefusal(w, r, "lang", "")
			return
		}
		target.Lang = lang
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
	if !requireSupertenant(w, r, "webhook targets receive every tenant's event stream") {
		return
	}
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
	if !requireSupertenant(w, r, "webhook targets receive every tenant's event stream") {
		return
	}
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

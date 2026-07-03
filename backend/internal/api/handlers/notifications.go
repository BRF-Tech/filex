package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// Notifications wraps the notify.Service for the HTTP layer.
type Notifications struct {
	Service notify.Service
}

// NewNotifications constructs the handler.
func NewNotifications(svc notify.Service) *Notifications {
	return &Notifications{Service: svc}
}

// List paginates the current user's bell history (broadcasts +
// user-scoped). Admin-global view is exposed via /admin/api/notifications
// (passing nil userID to the service).
//
//	GET /api/notifications?unread=true&limit=50&offset=0
func (h *Notifications) List(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	uid := user.ID
	onlyUnread := r.URL.Query().Get("unread") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.Service.List(r.Context(), &uid, onlyUnread, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// UnreadCount returns the bell badge number for the current user.
//
//	GET /api/notifications/unread-count
func (h *Notifications) UnreadCount(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	uid := user.ID
	n, err := h.Service.UnreadCount(r.Context(), &uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// MarkRead marks a single notification read.
//
//	POST /api/notifications/{id}/read
func (h *Notifications) MarkRead(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	uid := user.ID
	if err := h.Service.MarkRead(r.Context(), id, &uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MarkAllRead clears the user's unread queue.
//
//	POST /api/notifications/read-all
func (h *Notifications) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	uid := user.ID
	if err := h.Service.MarkAllRead(r.Context(), &uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetSettings returns the user's preferences.
//
//	GET /api/notifications/settings
func (h *Notifications) GetSettings(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	st, err := h.Service.GetSettings(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// UpdateSettings replaces the user's preferences.
//
//	PATCH /api/notifications/settings
//	body: {in_app_enabled: bool, muted_events: ["replica_fail", ...]}
func (h *Notifications) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		InAppEnabled bool     `json:"in_app_enabled"`
		MutedEvents  []string `json:"muted_events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	muted := body.MutedEvents
	if muted == nil {
		muted = []string{}
	}
	mutedJSON, err := json.Marshal(muted)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "marshal muted_events: " + err.Error()})
		return
	}
	st := &model.NotificationSettings{
		UserID:         user.ID,
		InAppEnabled:   body.InAppEnabled,
		MutedEventsRaw: mutedJSON,
	}
	if err := h.Service.UpsertSettings(r.Context(), st); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// AdminList exposes the global view (broadcasts + every user's
// notifications) to admins.
//
//	GET /admin/api/notifications?unread=true&limit=50&offset=0
func (h *Notifications) AdminList(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	onlyUnread := r.URL.Query().Get("unread") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.Service.List(r.Context(), nil, onlyUnread, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// AdminTest emits a manual event so the admin can verify the webhook
// URL works.
//
//	POST /admin/api/notifications/test
func (h *Notifications) AdminTest(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	id, err := h.Service.Send(r.Context(), notify.Event{
		Event:    "admin_test",
		Severity: notify.SeverityInfo,
		Title:    "filex test notification",
		Body:     "If you're reading this, both the in-app bell and the webhook plumbing are wired correctly.",
		Meta:     map[string]any{"source": "admin_test"},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// AdminWebhookConfig returns the current webhook URL + a token-set
// flag (the token itself is never exposed back to the UI).
//
//	GET /admin/api/notifications/webhook-config
func (h *Notifications) AdminWebhookConfig(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	url, tokenSet := h.Service.WebhookConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       url,
		"token_set": tokenSet,
	})
}

// AdminUpdateWebhookConfig overrides the URL/token at runtime. Pass
// empty url to disable webhook delivery without taking the in-app
// channel down. token may be left empty to keep the existing one
// (sentinel: empty in body means clear; supply "__keep__" to retain).
//
//	PATCH /admin/api/notifications/webhook-config
//	body: {url: "...", token: "..." or "__keep__" or ""}
func (h *Notifications) AdminUpdateWebhookConfig(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	var body struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	// We deliberately do not implement a "keep existing token" sentinel
	// — the Service does not expose the current token (security:
	// secrets shouldn't round-trip through the admin UI), so the only
	// safe semantic is "the body is the new full state". Empty token
	// clears; non-empty replaces; clients that want to preserve it
	// must supply the same value they originally configured.
	if body.Token == "__keep__" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "use empty token to clear, or supply the token explicitly; '__keep__' is not supported",
		})
		return
	}
	h.Service.SetWebhook(body.URL, body.Token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

package handlers

import (
	"context"
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
	if kept, confined := confineNotifications(r.Context(), rows); confined {
		// `total` is recomputed rather than carried through, for the same
		// reason the trash listing recomputes it: a count that still describes
		// the rows we just removed tells the caller how much is happening
		// elsewhere on the platform.
		rows, total = kept, int64(len(kept))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// confineNotifications drops rows a confined tenant may not see, and reports
// whether it did anything (false = unscoped or supertenant, list untouched).
//
// # Why the fix is here and not on the write side
//
// Everything a WORKER emits carries `user_id = NULL`, because a worker has no
// request and therefore no user — the antivirus scanner, the replica failure
// recorder, the replica reconciler. The store's per-user predicate is
// `(user_id IS NULL OR user_id = ?)`, so every such row was visible to every
// user of every tenant, carrying infected-file paths and `failed_paths` dumps.
//
// The row already carries what is needed to place MOST of them: `marshalMeta`
// folds the event's NodeRef into `meta_json` as `node.storage_id`, so an
// antivirus alert can be attributed to a storage and therefore to a tenant. No
// migration, no new column, and — the part that matters operationally — no
// change to what the workers write, which is what would have had to be
// backfilled for the rows already in the table.
//
// ⚠ The replica events genuinely cannot be placed: they carry a path and
// nothing else (`internal/replica/recorder.go`, `reconcile.go`), and a bare
// path does not name a storage. Rather than guess, an unattributable broadcast
// is treated as what it is — an instance-wide OPERATOR event — and hidden from
// confined tenants while staying visible to the supertenant and to every
// single-tenant install. Guessing would be worse in both directions: matching
// on path prefix would show one tenant another's alert whenever two storages
// share a folder name, and showing it unconditionally is the leak we are here
// to close.
//
// A row addressed to a specific user is left alone: it is already scoped to
// that user, and the caller is asking about themselves.
func confineNotifications(ctx context.Context, rows []*model.Notification) ([]*model.Notification, bool) {
	scope, confined := confinedScope(ctx)
	if !confined {
		return rows, false
	}
	kept := make([]*model.Notification, 0, len(rows))
	for _, n := range rows {
		if n == nil {
			continue
		}
		if n.UserID != nil {
			kept = append(kept, n)
			continue
		}
		if sid, ok := notificationStorageID(n); ok && scope.CanAccessStorage(sid) {
			kept = append(kept, n)
		}
	}
	return kept, true
}

// notificationStorageID pulls the storage a broadcast row is about out of its
// meta blob. `meta.node.storage_id` is the shape notify.marshalMeta writes for
// every event carrying a NodeRef; `meta.storage_id` is accepted too so an
// emitter that only has the id (no full node) can place its row without going
// through a NodeRef it would have to invent.
func notificationStorageID(n *model.Notification) (int64, bool) {
	if len(n.MetaJSON) == 0 {
		return 0, false
	}
	var meta struct {
		Node *struct {
			StorageID int64 `json:"storage_id"`
		} `json:"node"`
		StorageID *int64 `json:"storage_id"`
	}
	if err := json.Unmarshal(n.MetaJSON, &meta); err != nil {
		return 0, false
	}
	if meta.Node != nil && meta.Node.StorageID != 0 {
		return meta.Node.StorageID, true
	}
	if meta.StorageID != nil && *meta.StorageID != 0 {
		return *meta.StorageID, true
	}
	return 0, false
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
	if _, confined := confinedScope(r.Context()); confined {
		// The badge has to agree with the list, and the SQL COUNT cannot: it
		// has no way to look inside meta_json. Counting the rows the list would
		// actually show is the only answer that is not a lie — a badge saying 3
		// over a list of 1 is both a hint at how much is happening elsewhere on
		// the platform and a number the user can never clear, because they
		// cannot mark read what they cannot see.
		//
		// ⚠ Bounded on purpose. notificationBadgeMax rows are enough for a
		// badge (every UI that draws one caps the display long before this),
		// and an unbounded walk of the notifications table on every poll of the
		// bell is not a trade worth making for a number nobody reads past two
		// digits.
		rows, _, err := h.Service.List(r.Context(), &uid, true, notificationBadgeMax, 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		kept, _ := confineNotifications(r.Context(), rows)
		writeJSON(w, http.StatusOK, map[string]any{"count": int64(len(kept))})
		return
	}
	n, err := h.Service.UnreadCount(r.Context(), &uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// notificationBadgeMax caps the multi-tenant badge walk. It is the store's own
// per-page ceiling (ListNotifications clamps limit to 500), so asking for more
// would silently come back as 50.
const notificationBadgeMax = 500

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
	// ⚠ BEFORE the "notifications offline" 503 below, so the refusal does not
	// disclose whether the operator has the notify service wired — the same
	// ordering handlers/plugins.go adopted for the plugins_disabled 503.
	//
	// One global URL + token receives EVERY tenant's event stream, so a tenant
	// admin repointing it would have piped other customers' file paths at a
	// host they control. Per-tenant webhook targets are a feature (the rows
	// would have to carry a provider and the emitter filter by it), not
	// something a gate can approximate.
	if !requireSupertenant(w, r, "the legacy webhook receives every tenant's event stream") {
		return
	}
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
	// ⚠ BEFORE the "notifications offline" 503 below, so the refusal does not
	// disclose whether the operator has the notify service wired — the same
	// ordering handlers/plugins.go adopted for the plugins_disabled 503.
	//
	// One global URL + token receives EVERY tenant's event stream, so a tenant
	// admin repointing it would have piped other customers' file paths at a
	// host they control. Per-tenant webhook targets are a feature (the rows
	// would have to carry a provider and the emitter filter by it), not
	// something a gate can approximate.
	if !requireSupertenant(w, r, "the legacy webhook receives every tenant's event stream") {
		return
	}
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

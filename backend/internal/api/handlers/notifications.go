package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
)

// Notifications wraps the notify.Service for the HTTP layer.
type Notifications struct {
	Service notify.Service
	// Store and ACL answer "may this member see the file a broadcast names" —
	// with the same grants the explorer filters its listings by. With either
	// one nil there is nothing to prove it with, and no such row reaches a
	// member.
	Store db.Store
	ACL   *acl.Resolver
}

// NewNotifications constructs the handler.
func NewNotifications(svc notify.Service, store db.Store, r *acl.Resolver) *Notifications {
	return &Notifications{Service: svc, Store: store, ACL: r}
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
	bell := bellFor(r.Context(), user)
	rows, total, err := h.Service.List(r.Context(), &uid, bell, onlyUnread, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if kept, filtered := h.visibleTo(r.Context(), user, bell, rows); filtered {
		// `total` is recomputed rather than carried through, for the same
		// reason the trash listing recomputes it: a count that still describes
		// the rows we just removed tells the caller how much is happening
		// where they cannot look.
		rows, total = kept, int64(len(kept))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// bellFor picks which broadcasts the caller's bell takes from the store
// (notify.Bell): every kind for an admin nobody confines — the admin of a
// single-tenant install, or the supertenant's — the kinds that name a file for
// a tenant admin, and the member kinds for everybody else. Only the first reads
// the broadcasts as stored; the others get the per-row pass in visibleTo.
func bellFor(ctx context.Context, user *model.User) notify.Bell {
	_, confined := confinedScope(ctx)
	switch {
	case !user.IsAdmin():
		return notify.MemberBell
	case confined:
		return notify.TenantAdminBell
	default:
		return notify.AdminBell
	}
}

// visibleTo drops the broadcast rows the caller may not see, and reports
// whether it looked at all (false = the caller reads every row as stored).
//
// A row addressed to a user is left alone: the store returns no one else's,
// so it is the caller's own. The question is only ever about BROADCASTS
// (`user_id = NULL`), which the store hands to every bell, and it is asked in
// two layers.
//
// # The tenant
//
// Everything a worker emits without a person behind it carries `user_id =
// NULL` — the antivirus scanner, the replica failure recorder, the replica
// reconciler — so every such row was visible to every user of every tenant,
// carrying infected-file paths and `failed_paths` dumps. The row carries what
// is needed to place most of them: `marshalMeta` folds the event's NodeRef into
// `meta_json` as `node.storage_id`, so an alert can be attributed to a storage
// and therefore to a tenant. No migration, no new column, and no change to
// what the workers write — which is what would have had to be backfilled for
// the rows already in the table.
//
// # The person
//
// A tenant is not a person. Placing the row in the caller's tenant said
// nothing about whether the caller may open the FOLDER it names, and on an
// RBAC storage most members may not open most folders: members were reading
// the names of files deleted from folders they have no grant on. So a member
// gets a broadcast only when it is a kind a member may receive at all
// (notify.MemberMayReceive — never a link's bearer token, never an alarm meant
// for the operator) and the explorer would list what it names:
// acl.Set.CanSee, the predicate the listing itself uses, including the ancestor
// folders a member walks through to reach a grant. An admin is not asked:
// admins bypass RBAC in the listing too.
//
// # A row that cannot be placed
//
// The replica events carry a bare path and nothing else
// (`internal/replica/recorder.go`, `reconcile.go`), and a bare path does not
// name a storage. Rather than guess, such a row is treated as what it is — an
// instance-wide OPERATOR event, "user_id IS NULL → admin-visible" in the
// schema's own words — and reaches only an AdminBell. Guessing would be worse
// in both directions: matching on path prefix would show one storage's alert
// to the readers of another whenever two storages share a folder name, and
// showing it unconditionally is the leak.
//
// The SQL read (notify.Bell) already leaves out the broadcast kinds a bell
// never shows, so what arrives here is mostly the caller's own rows; the checks
// below still make this function a complete answer on its own.
func (h *Notifications) visibleTo(ctx context.Context, user *model.User, bell notify.Bell, rows []*model.Notification) ([]*model.Notification, bool) {
	if bell == notify.AdminBell {
		return rows, false
	}
	scope, confined := confinedScope(ctx)
	sets := map[int64]*acl.Set{}
	kept := make([]*model.Notification, 0, len(rows))
	for _, n := range rows {
		if n == nil {
			continue
		}
		if n.UserID != nil {
			kept = append(kept, n)
			continue
		}
		if !user.IsAdmin() && !notify.MemberMayReceive(n.Event) {
			continue
		}
		sid, rel, ok := notificationPlace(n)
		if !ok {
			continue
		}
		if confined && !scope.CanAccessStorage(sid) {
			continue
		}
		if user.IsAdmin() || h.memberCanSee(ctx, user, sid, rel, sets) {
			kept = append(kept, n)
		}
	}
	return kept, true
}

// memberCanSee answers acl.Set.CanSee for one storage, loading the caller's
// grants for it once per request (sets caches them, a failed load as nil).
//
// ⚠ Fails CLOSED: an unwired resolver, a storage that no longer resolves, a
// disabled storage (it is in nobody's drive list) or a grant query that errors
// all hide the row. Showing it would be answering "may they see this" with "we
// could not check".
func (h *Notifications) memberCanSee(ctx context.Context, user *model.User, storageID int64, rel string, sets map[int64]*acl.Set) bool {
	set, loaded := sets[storageID]
	if !loaded {
		set = h.loadACLSet(ctx, user, storageID)
		sets[storageID] = set
	}
	return set != nil && set.CanSee(rel)
}

func (h *Notifications) loadACLSet(ctx context.Context, user *model.User, storageID int64) *acl.Set {
	if h.Store == nil || h.ACL == nil {
		return nil
	}
	st, err := h.Store.GetStorage(ctx, storageID)
	if err != nil || st == nil || !st.Enabled {
		return nil
	}
	set, err := h.ACL.LoadSet(ctx, user, st)
	if err != nil {
		return nil
	}
	return set
}

// notificationPlace pulls the storage and the storage-relative path a
// broadcast row is about out of its meta blob. `meta.node` is the shape
// notify.marshalMeta writes for every event carrying a NodeRef;
// `meta.storage_id` is accepted too so an emitter that only has the id (no
// full node) can place its row without going through a NodeRef it would have
// to invent. Such a row names no path, and "" asks about the storage root —
// which a member can see exactly when the storage is in their drive list.
func notificationPlace(n *model.Notification) (storageID int64, rel string, ok bool) {
	if len(n.MetaJSON) == 0 {
		return 0, "", false
	}
	var meta struct {
		Node *struct {
			StorageID int64  `json:"storage_id"`
			Path      string `json:"path"`
		} `json:"node"`
		StorageID *int64 `json:"storage_id"`
	}
	if err := json.Unmarshal(n.MetaJSON, &meta); err != nil {
		return 0, "", false
	}
	if meta.Node != nil && meta.Node.StorageID != 0 {
		return meta.Node.StorageID, meta.Node.Path, true
	}
	if meta.StorageID != nil && *meta.StorageID != 0 {
		return *meta.StorageID, "", true
	}
	return 0, "", false
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
	bell := bellFor(r.Context(), user)
	if bell != notify.AdminBell {
		// The badge has to agree with the list, and the SQL COUNT cannot: it
		// has no way to look inside meta_json, let alone at the caller's
		// grants. Counting the rows the list would actually show is the only
		// answer that is not a lie — a badge saying 3 over a list of 1 is both
		// a hint at how much is happening where the caller cannot look and a
		// number they can never clear, because they cannot mark read what they
		// cannot see.
		//
		// ⚠ Bounded on purpose. notificationBadgeMax rows are enough for a
		// badge (every UI that draws one caps the display long before this),
		// and an unbounded walk of the notifications table on every poll of the
		// bell is not a trade worth making for a number nobody reads past two
		// digits. The rows walked are the caller's own and the few broadcast
		// kinds their bell takes at all (notify.Bell).
		rows, _, err := h.Service.List(r.Context(), &uid, bell, true, notificationBadgeMax, 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		kept, _ := h.visibleTo(r.Context(), user, bell, rows)
		writeJSON(w, http.StatusOK, map[string]any{"count": int64(len(kept))})
		return
	}
	n, err := h.Service.UnreadCount(r.Context(), &uid, bell)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// notificationBadgeMax caps the filtered badge walk. It is the store's own
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
	// ⚠ BEFORE the "notifications offline" 503, same ordering as the webhook
	// config below.
	//
	// This is every event the instance recorded — every tenant's file paths,
	// every user's bell — and a tenant admin read all of it: the route only
	// asks for an admin, and in multi-tenant mode that means the admin of ANY
	// tenant. Filtering it is not a gate's job: a row names its tenant only
	// inside meta_json (and the replica events not at all), so a scoped page
	// would come back short with a count that describes rows it removed. A
	// tenant's own events already reach its admins through the bell, which is
	// scoped (visibleTo). A per-tenant audit is a feature, with a tenant column
	// behind it.
	if !requireSupertenant(w, r, "the notification history holds every tenant's events") {
		return
	}
	if h.Service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications offline"})
		return
	}
	onlyUnread := r.URL.Query().Get("unread") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.Service.List(r.Context(), nil, notify.AdminBell, onlyUnread, limit, offset)
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
	// ⚠ BEFORE the 503, same ordering as its siblings. The test event goes to
	// the instance's global webhook and to every webhook target — the
	// operator's receivers, not a tenant's — and lands as a row only the
	// supertenant's bell can show, so a tenant admin could only ever use it to
	// fire deliveries at somebody else's endpoint.
	if !requireSupertenant(w, r, "the test event goes to every webhook destination of the instance") {
		return
	}
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

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/tenant"
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
//
// It is also what the browser notification and the desktop toast read (the
// web store diffs this list; the desktop polls `unread=true`), so what a
// reader may learn of a broadcast is decided here once for all three.
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
	onlyUnread := r.URL.Query().Get("unread") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, total, err := h.bellRead(r.Context(), user, onlyUnread, limit, offset)
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

// bellRead is one page of the caller's bell, and the total of what they may
// see. An admin nobody confines reads the rows as stored; every other bell's
// broadcasts go through the per-row pass (bellJudge.keep), with the total and
// the page counted in what the reader sees (notify.Service.ListVisible).
//
// ⚠ PR #42 set the total of a filtered page to the page's own length — the
// rule the multi-tenant path already had. Once every member's bell is
// filtered that turned "12 of 30" into "12 of 12": the full-list screen showed
// no pager and rows 26-30 could not be reached (e2e 109). Counting the
// reader's own rows in SQL and walking only the broadcasts keeps the total
// exact without telling anybody how much happens where they cannot look.
//
// A caller confined to a folder (a token's `root:` scope, bellJudge.inRoot)
// has every row judged, its own included: a notice about a file outside the
// root is not theirs to read, whoever it is addressed to.
func (h *Notifications) bellRead(ctx context.Context, user *model.User, onlyUnread bool, limit, offset int) ([]*model.Notification, int64, error) {
	bell := bellFor(ctx, user)
	j := h.judge(ctx, user, bell)
	if bell == notify.AdminBell && !j.rooted {
		uid := user.ID
		return h.Service.List(ctx, &uid, bell, onlyUnread, limit, offset)
	}
	var keepOwn func(*model.Notification) bool
	if j.rooted {
		keepOwn = j.keep
	}
	return h.Service.ListVisible(ctx, user.ID, bell, onlyUnread, limit, offset, j.keep, keepOwn)
}

// bellFor picks which broadcasts the caller's bell takes from the store
// (notify.Bell): every kind for an admin nobody confines — the admin of a
// single-tenant install, or the supertenant's — the kinds that name a file for
// a tenant admin, and the member kinds for everybody else. Only the first reads
// the broadcasts as stored; the others get the per-row pass (bellJudge).
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

// bellJudge decides, one BROADCAST at a time, whether the caller may be shown
// it. A row addressed to a user is never asked about: the store returns no one
// else's, so it is the caller's own.
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
// the names of files deleted from folders they have no grant on (PR #42,
// Berk Başarır). So a member gets a broadcast only when it is a kind a member
// may receive at all (notify.Bell — never a link's bearer token, never an
// alarm meant for the operator) and the explorer would list what it names:
// acl.Set.CanSee, the predicate the listing itself uses, including the
// ancestor folders a member walks through to reach a grant. An admin is not
// asked: admins bypass RBAC in the listing too.
//
// # A row that cannot be placed
//
// The replica events carry a bare path and nothing else
// (`internal/replica/recorder.go`, `reconcile.go`), and a bare path does not
// name a storage. Rather than guess, such a row is treated as what it is — an
// instance-wide OPERATOR event — and reaches only an AdminBell. Guessing would
// be worse in both directions: matching on path prefix would show one
// storage's alert to the readers of another whenever two storages share a
// folder name, and showing it unconditionally is the leak. The one exception
// is a notice meant for everybody that names nothing (notify.ForEveryone): it
// reaches every reader nobody confines to a tenant.
//
// ⚠ A row that names a file by NAME but not by path cannot be checked against
// anybody's grants either — v0.43.0's person view strips the path of an
// "open with filex" working copy (notify/personview.go) and keeps its name.
// Such a row reaches no member (fail closed); the antivirus scanner addresses
// its own to the person whose copy it is.
//
// # A folder-confined caller
//
// A token confined to one folder (`root:<storage>://<folder>`, narrowed by an
// X-Filex-Root header — confine.Middleware, the files routes' own helper) is
// exactly as narrow as it says: it reads no notice about a file outside its
// folder — its owner's own rows included, the admin's bypasses included — and
// marks none read. A notice that names no file at all (the admin page's test,
// an app's notice about a list) is not about the folder and stays readable.
// See inRoot.
type bellJudge struct {
	h        *Notifications
	ctx      context.Context
	user     *model.User
	bell     notify.Bell
	scope    *tenant.Scope
	confined bool
	sets     map[int64]*acl.Set

	root   confine.Root
	rooted bool
	names  map[int64]string
}

func (h *Notifications) judge(ctx context.Context, user *model.User, bell notify.Bell) *bellJudge {
	scope, confined := confinedScope(ctx)
	root, rooted := callerRoot(ctx)
	return &bellJudge{h: h, ctx: ctx, user: user, bell: bell, scope: scope, confined: confined,
		sets: map[int64]*acl.Set{}, root: root, rooted: rooted, names: map[int64]string{}}
}

func (j *bellJudge) keep(n *model.Notification) bool {
	if n == nil {
		return false
	}
	if !j.inRoot(n) {
		return false
	}
	if n.UserID != nil || j.bell == notify.AdminBell {
		return true
	}
	if !j.bell.Admits(n.Event) {
		return false
	}
	p, ok := notificationPlace(n)
	if !ok {
		return notify.ForEveryone(n.Event) && !j.confined && !notify.NamesAFile(n.MetaJSON)
	}
	if j.confined && !j.scope.CanAccessStorage(p.storageID) {
		return false
	}
	if j.user.IsAdmin() {
		return true
	}
	if p.nameOnly {
		return false
	}
	return j.h.memberCanSee(j.ctx, j.user, p.storageID, p.rel, j.sets)
}

// inRoot reports whether a row is readable under the caller's folder
// confinement: true for an unconfined caller, and for a row that names no file;
// otherwise every file the row names must lie inside the root.
//
// What a row names a file WITH is the stored shape notify.marshalMeta writes:
// `node` (storage id + path; with only a name it is placed at its storage), a
// bare `storage_id`, the click `target` (storage name + path), the `share`
// path, a move's `from`/`to`, and the bare paths an operator alarm carries
// (`path`, `failed_paths`, `trash_path`, `folder`). ⚠ Fails CLOSED: a reference the confinement cannot
// place — a path with no storage, a storage that does not resolve — hides the
// row. "We could not tell where it is" is not "inside".
func (j *bellJudge) inRoot(n *model.Notification) bool {
	if !j.rooted {
		return true
	}
	if len(n.MetaJSON) == 0 {
		return true
	}
	var meta rowRefs
	if json.Unmarshal(n.MetaJSON, &meta) != nil {
		return false
	}
	// The storage the row's relative paths belong to: the node's.
	storage, placed := "", false
	if meta.Node != nil {
		if meta.Node.StorageID == 0 {
			return false
		}
		storage, placed = j.storageName(meta.Node.StorageID), true
		// A node with no path — only a name, as an "open with filex" working
		// copy keeps — is placed at its storage: inside only a root that is
		// the whole storage.
		if !j.root.Within(storage, meta.Node.Path) {
			return false
		}
	} else if meta.StorageID != nil && *meta.StorageID != 0 {
		storage, placed = j.storageName(*meta.StorageID), true
		if !j.root.Within(storage, "") {
			return false
		}
	}
	if t := meta.Target; t != nil && t.Path != "" {
		switch t.Kind {
		case "file", "dir", "trash":
			if t.Storage == "" || !j.root.Within(t.Storage, t.Path) {
				return false
			}
		}
	}
	share := ""
	if meta.Share != nil {
		share = meta.Share.Path
	}
	for _, rel := range []string{meta.From, meta.To, share} {
		if rel == "" {
			continue
		}
		if !placed || !j.root.Within(storage, rel) {
			return false
		}
	}
	// Bare paths with no storage of their own (the replica alarms). Beside a
	// placed node they are that node's (a quarantine's trash key, a drop's
	// folder name); alone, nothing says where they are.
	if !placed && (meta.Path != nil || meta.FailedPaths != nil || meta.TrashPath != nil || meta.Folder != nil) {
		return false
	}
	return true
}

// rowRefs is every field of a stored row's meta that can name a file.
type rowRefs struct {
	Node *struct {
		StorageID int64  `json:"storage_id"`
		Path      string `json:"path"`
		Name      string `json:"name"`
	} `json:"node"`
	StorageID *int64 `json:"storage_id"`
	Target    *struct {
		Kind    string `json:"kind"`
		Storage string `json:"storage"`
		Path    string `json:"path"`
	} `json:"target"`
	Share *struct {
		Path string `json:"path"`
	} `json:"share"`
	From        string `json:"from"`
	To          string `json:"to"`
	Path        any    `json:"path"`
	FailedPaths any    `json:"failed_paths"`
	TrashPath   any    `json:"trash_path"`
	Folder      any    `json:"folder"`
}

// storageName is the adapter name a storage id is confined under, cached for
// the request ("" when it does not resolve, which Within refuses).
func (j *bellJudge) storageName(id int64) string {
	if name, ok := j.names[id]; ok {
		return name
	}
	name := rootStorageName(j.ctx, j.h.Store, id)
	j.names[id] = name
	return name
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

// rowPlace is where a broadcast row is about: its storage, the path inside it,
// and whether it names a file it gives no path for (nameOnly).
type rowPlace struct {
	storageID int64
	rel       string
	nameOnly  bool
}

// notificationPlace pulls the storage and the storage-relative path a
// broadcast row is about out of its meta blob. `meta.node` is the shape
// notify.marshalMeta writes for every event carrying a NodeRef;
// `meta.storage_id` is accepted too so an emitter that only has the id (no
// full node) can place its row without going through a NodeRef it would have
// to invent. Such a row names no path and no file, and "" asks about the
// storage root — which a member can see exactly when the storage is in their
// drive list.
func notificationPlace(n *model.Notification) (rowPlace, bool) {
	if len(n.MetaJSON) == 0 {
		return rowPlace{}, false
	}
	var meta struct {
		Node *struct {
			StorageID int64  `json:"storage_id"`
			Path      string `json:"path"`
			Name      string `json:"name"`
		} `json:"node"`
		StorageID *int64 `json:"storage_id"`
	}
	if err := json.Unmarshal(n.MetaJSON, &meta); err != nil {
		return rowPlace{}, false
	}
	if meta.Node != nil && meta.Node.StorageID != 0 {
		return rowPlace{storageID: meta.Node.StorageID, rel: meta.Node.Path,
			nameOnly: meta.Node.Path == "" && meta.Node.Name != ""}, true
	}
	if meta.StorageID != nil && *meta.StorageID != 0 {
		return rowPlace{storageID: *meta.StorageID}, true
	}
	return rowPlace{}, false
}

// UnreadCount returns the bell badge number for the current user.
//
//	GET /api/notifications/unread-count
//
// The badge has to agree with the list, and a bare SQL COUNT cannot where the
// broadcasts get a per-row pass: it has no way to look inside meta_json, let
// alone at the caller's grants. So it is the list's own total (bellRead) —
// a badge saying 3 over a list of 1 is both a hint at how much is happening
// where the caller cannot look and a number they can never clear, because
// they cannot mark read what they cannot see.
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
	bell := bellFor(r.Context(), user)
	if _, rooted := callerRoot(r.Context()); bell != notify.AdminBell || rooted {
		_, n, err := h.bellRead(r.Context(), user, true, 1, 0)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": n})
		return
	}
	uid := user.ID
	n, err := h.Service.UnreadCount(r.Context(), &uid, bell)
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
	// A folder-confined caller marks only what it can read: a row about a file
	// outside its root is left as it is — the same 204, so the answer says
	// nothing about the row.
	if _, rooted := callerRoot(r.Context()); rooted {
		if n, err := h.readable(r.Context(), user, id); err != nil || n == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	// A row addressed to the caller: its own column, which the store stamps for
	// its addressee and nobody else.
	if err := h.Service.MarkRead(r.Context(), id, &uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// A broadcast: the caller's own mark, and only on one the caller's bell
	// shows. Anything else — another user's row, a kind the bell does not read,
	// a file the caller may not see, an id nobody has — answers the same 204,
	// so the endpoint says nothing about which ids exist.
	if n := h.readableBroadcast(r.Context(), user, id); n != nil {
		if err := h.Service.MarkBroadcastsRead(r.Context(), uid, []int64{n.ID}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// readableBroadcast returns the broadcast with this id when the caller may
// mark it read, nil otherwise.
//
// An admin nobody confines reads every broadcast — in the admin history too,
// including routine rows no bell shows — so every one is theirs to mark. For
// everybody else it is the list's own answer: a kind their bell reads
// (notify.Bell.Admits) that survives the per-row pass.
func (h *Notifications) readableBroadcast(ctx context.Context, user *model.User, id int64) *model.Notification {
	n, err := h.readable(ctx, user, id)
	if err != nil || n == nil || n.UserID != nil {
		return nil
	}
	return n
}

// readable returns the row with this id when the caller's bell may show it
// (bellJudge.keep: the kinds, the tenant, the grants, the folder confinement),
// nil otherwise. A row addressed to somebody passes the judge as the caller's
// own; the store's stamp checks the addressee.
func (h *Notifications) readable(ctx context.Context, user *model.User, id int64) (*model.Notification, error) {
	if h.Store == nil {
		return nil, nil
	}
	n, err := h.Store.GetNotification(ctx, id)
	if err != nil || n == nil {
		return nil, err
	}
	if !h.judge(ctx, user, bellFor(ctx, user)).keep(n) {
		return nil, nil
	}
	return n, nil
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
	if _, rooted := callerRoot(r.Context()); rooted {
		if err := h.markVisibleRead(r.Context(), user); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.Service.MarkAllRead(r.Context(), &uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Every broadcast up to now, for the caller alone. Rows their bell does not
	// show are read for them too, which changes nothing anybody can see.
	if err := h.Service.MarkAllBroadcastsRead(r.Context(), uid); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markVisibleRead is "mark all read" for a folder-confined caller: only the
// unread rows its bell shows, its own and broadcasts alike — never the owner's
// notices about the rest of the account, which the owner has not seen.
// Bounded like the walk behind the list (Service.ListVisible).
func (h *Notifications) markVisibleRead(ctx context.Context, user *model.User) error {
	uid := user.ID
	for round := 0; round < 10; round++ {
		rows, _, err := h.bellRead(ctx, user, true, 500, 0)
		if err != nil {
			return err
		}
		var broadcasts []int64
		for _, n := range rows {
			if n.UserID != nil {
				if err := h.Service.MarkRead(ctx, n.ID, &uid); err != nil {
					return err
				}
			} else {
				broadcasts = append(broadcasts, n.ID)
			}
		}
		if len(broadcasts) > 0 {
			if err := h.Service.MarkBroadcastsRead(ctx, uid, broadcasts); err != nil {
				return err
			}
		}
		if len(rows) < 500 {
			return nil
		}
	}
	return nil
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
	// A broadcast's read state is per reader: the history shows the caller's.
	var reader int64
	if u := auth.UserFrom(r.Context()); u != nil {
		reader = u.ID
	}
	rows, total, err := h.Service.History(r.Context(), reader, onlyUnread, limit, offset)
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
		Event:    notify.EventAdminTest,
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

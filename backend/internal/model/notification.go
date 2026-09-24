package model

import (
	"encoding/json"
	"strings"
	"time"
)

// Notification is one row in the notifications table — either a
// broadcast (UserID nil) or scoped to a single user.
type Notification struct {
	ID            int64           `json:"id"`
	Event         string          `json:"event"`
	Severity      string          `json:"severity"`
	Title         string          `json:"title"`
	Body          string          `json:"body"`
	MetaJSON      json.RawMessage `json:"meta"`
	UserID        *int64          `json:"user_id,omitempty"`
	ReadAt        *time.Time      `json:"read_at,omitempty"`
	WebhookStatus string          `json:"webhook_status"`
	WebhookError  string          `json:"webhook_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`

	// Target is derived from MetaJSON at read time (HydrateTarget), never
	// stored in a column of its own. Absent on rows that have nothing to
	// open — the client reads a missing `target` as kind "none".
	Target *NotificationTarget `json:"target,omitempty"`

	// UserName is the display name of the person a user-scoped row belongs
	// to, filled on the admin list only (notify.Service.List with no user).
	// The audit page's Scope column printed `user #2` — a database id is not
	// who somebody is (release-candidate sweep, 2026-09-21).
	UserName string `json:"user_name,omitempty"`

	// AdminsOnly marks, on the admin list, a broadcast only administrators'
	// bells show (an operator alarm such as update_available): its Scope is
	// "Administrators", not "Everyone". Kept beside Audience for clients that
	// read only this flag; it is exactly Audience == "admins".
	AdminsOnly bool `json:"admins_only,omitempty"`

	// Audience says, on the admin list, who a BROADCAST reaches — the same
	// rule the bells apply (notify.BroadcastAudience): "everyone", "viewers"
	// (administrators and the members who can see the file it names),
	// "admins", or "nobody" (routine file activity recorded without the
	// person who did it — kept in the history, shown in no bell). Empty on a
	// row addressed to somebody, and on every row a bell hands out.
	Audience string `json:"audience,omitempty"`
}

// NotificationTargetKind says WHAT a notification is about, so a click on it
// can go somewhere. It is a closed set on purpose: every click surface (bell
// row, browser notification, desktop notification) switches on these five and
// nothing else, which is what keeps the three from disagreeing.
type NotificationTargetKind string

// The five target kinds. "none" is a real answer, not a missing one — an
// event about the whole instance (a replica failure, an available update)
// has nothing to open, and saying so is better than inventing a path.
const (
	TargetNone  NotificationTargetKind = "none"
	TargetFile  NotificationTargetKind = "file"
	TargetDir   NotificationTargetKind = "dir"
	TargetShare NotificationTargetKind = "share"
	// TargetTrash is the Trash view, with the item that came from Path
	// (its ORIGINAL path in Storage) selected. What a soft delete — or an
	// antivirus quarantine — addresses.
	//
	// ⚠⚠ It exists because the only address a trashed file used to have was
	// its trash KEY: `file.trashed` shipped `{kind: file, path:
	// ".filex-trash/<unix>-<rand>__<name>"}`, and a click opened the bin's
	// raw folder — breadcrumb `docs › .filex-trash`, an empty listing, no way
	// to restore (the owner's report, 2026-09-21). The Trash view lists items
	// by where they CAME FROM, so the original path is the one address in it
	// that means anything; `.filex-trash/…` is never a target of any kind.
	TargetTrash NotificationTargetKind = "trash"
	// TargetApp opens one of an app plugin's HOME pages (Open.Plugin +
	// Open.View, optionally at Open.Section) — a notice about a list rather
	// than about a file. No storage, no path. A click surface that has no
	// such page (the desktop app) brings its window forward and stops.
	TargetApp NotificationTargetKind = "app"
)

// NotificationTarget is the typed "where does this go" of one notification.
//
// ⚠ Storage is the storage NAME, not its numeric id: the explorer addresses
// storages by name (`<storage>://<path>`), and the id is meaningless to a
// caller who cannot read /api/admin/storages — which is every non-admin. It is
// resolved once, centrally, when the event is sent (notify.Service.Send), so
// no emitter has to look it up and no two emitters can resolve it differently.
//
// ⚠ Path is relative to that storage's root and never carries the
// `<storage>://` prefix; Kind decides how it is read — the file itself for
// TargetFile, the folder for TargetDir, the path the item was deleted FROM for
// TargetTrash.
type NotificationTarget struct {
	Kind    NotificationTargetKind `json:"kind"`
	Storage string                 `json:"storage,omitempty"`
	Path    string                 `json:"path,omitempty"`
	// ID is the opaque id of a target that is not a path — the share token
	// for TargetShare.
	ID string `json:"id,omitempty"`
	// Open, when set, says what to do once the file is reached: start an
	// app-plugin action or view on it (a "sign this" notification lands the
	// reader in the signing screen, not merely on the file).
	Open *NotificationOpen `json:"open,omitempty"`
}

// NotificationOpen is the app-plugin deep link carried by a target: the
// plugin and either an action id or a view id.
type NotificationOpen struct {
	Plugin string `json:"plugin"`
	Action string `json:"action,omitempty"`
	View   string `json:"view,omitempty"`
	// Section is the part of a home page to land on (TargetApp only).
	Section string `json:"section,omitempty"`
}

// TargetFromMeta pulls the target back out of a stored meta blob.
//
// The target rides inside meta_json (the same fold notify.marshalMeta does for
// node/share/actor) rather than in a column of its own, so reading it back is
// a parse and not a migration. A blob that has no target — every row written
// before this field existed — reads back nil, which every client treats as
// TargetNone.
func TargetFromMeta(meta json.RawMessage) *NotificationTarget {
	if len(meta) == 0 {
		return nil
	}
	var wrap struct {
		Target *NotificationTarget `json:"target"`
	}
	if err := json.Unmarshal(meta, &wrap); err != nil {
		return nil
	}
	if wrap.Target == nil || wrap.Target.Kind == "" || wrap.Target.Kind == TargetNone {
		return nil
	}
	return wrap.Target
}

// HydrateTarget fills the read-only Target field from MetaJSON. Called on
// every row the bell hands out (notify.Service.List), so the API item carries
// `target` as a top-level field instead of making each client dig through
// `meta`.
func (n *Notification) HydrateTarget() {
	if n == nil {
		return
	}
	n.Target = TargetFromMeta(n.MetaJSON)
}

// NotificationInput is the new-row payload — DB drivers turn this into
// an INSERT. ID + WebhookStatus default ("pending") are filled in by
// the store.
type NotificationInput struct {
	Event    string
	Severity string
	Title    string
	Body     string
	MetaJSON json.RawMessage
	UserID   *int64
}

// BroadcastFilter decides how a notification read treats broadcasts — rows
// with no user_id. The zero value reads every broadcast, with the read state
// stored on the row.
//
// It is applied IN SQL, like the mute list, so that a bell's page is filled
// with rows its reader can be shown, not with rows a filter throws away after
// the page was cut.
type BroadcastFilter struct {
	// Only admits just the broadcasts of these events. Wins over Except.
	// Only and Except narrow a per-user read and are ignored by the
	// admin-global one; ReaderID applies to both.
	Only []string
	// Except leaves out the broadcasts of these events.
	Except []string
	// ReaderID is whose read state a broadcast carries in this read
	// (migration 00056). For that reader a broadcast is read when it is at or
	// below their "mark all read" point, when they marked it on its own, or
	// when the row's own read_at was stamped before per-reader state existed.
	// 0 reads the row's own column only. A row addressed to a user always
	// carries that column.
	ReaderID int64
	// OwnOnly takes no broadcast at all, BroadcastsOnly none of the reader's
	// own rows — the two halves of a per-user read, read apart. A bell whose
	// broadcasts get a per-row pass after the store (a member's: the grants)
	// counts its own rows in SQL and walks only the broadcasts, so its total
	// is exact and its pages are cut where the reader sees them
	// (notify.Service.ListVisible). Ignored by the admin-global read.
	OwnOnly        bool
	BroadcastsOnly bool
}

// NotificationSettings captures per-user notification preferences. Stored
// in the notification_settings table. Default-on for a fresh user — a
// missing row is treated as InAppEnabled=true with no muted events.
type NotificationSettings struct {
	UserID         int64           `json:"user_id"`
	InAppEnabled   bool            `json:"in_app_enabled"`
	MutedEventsRaw json.RawMessage `json:"muted_events"`
}

// MutedList decodes MutedEventsRaw into trimmed, non-empty event names.
//
// It is the read-side twin of WebhookTarget.EventList below: one place that
// turns the stored form into a list, so every consumer agrees on what "muted"
// means.
//
// ⚠ Malformed JSON resolves to "nothing muted" rather than to an error. The
// column is written by the API as a marshalled []string and can only be
// corrupt if somebody edited the row by hand. Failing open there shows a user
// one notification they did not want; failing closed would silently hide every
// notification they have — the wrong way round for a display preference.
func (s *NotificationSettings) MutedList() []string {
	if s == nil || len(s.MutedEventsRaw) == 0 {
		return nil
	}
	var raw []string
	if err := json.Unmarshal(s.MutedEventsRaw, &raw); err != nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsMuted reports whether this user has muted the given event id. An empty
// list mutes nothing — the inverse of MatchesEvent's empty-means-everything.
func (s *NotificationSettings) IsMuted(event string) bool {
	for _, e := range s.MutedList() {
		if e == event {
			return true
		}
	}
	return false
}

// WebhookTarget is one row of webhook_targets (webhook v2, migration
// 00017) — an additional POST destination next to the legacy single
// global webhook. Events is a comma-separated allow-list of event names
// (mirrors APIToken.Usernames' CSV precedent); empty means "all events".
//
// Secret is never serialized — admin API responses expose only a
// secret_set flag. It signs each delivery body as
// X-Filex-Signature: sha256=<hex hmac-sha256(body)>.
type WebhookTarget struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	Events    string    `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	// Last-delivery persistence (migration 00019). All nil until the
	// first delivery attempt (real dispatch or admin test-fire).
	// LastStatus is the HTTP status code of the final attempt — 0 means
	// the request never got a response (DNS/connect/timeout). LastError
	// is nil after a success, the aggregated error message otherwise.
	LastStatus     *int       `json:"last_status,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
}

// EventList splits the CSV allow-list into trimmed, non-empty names.
func (t *WebhookTarget) EventList() []string {
	if t == nil || strings.TrimSpace(t.Events) == "" {
		return nil
	}
	parts := strings.Split(t.Events, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// MatchesEvent reports whether the target wants deliveries for the
// given event name. An empty allow-list matches everything.
func (t *WebhookTarget) MatchesEvent(event string) bool {
	list := t.EventList()
	if len(list) == 0 {
		return true
	}
	for _, e := range list {
		if e == event {
			return true
		}
	}
	return false
}

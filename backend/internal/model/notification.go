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
}

// NotificationTargetKind says WHAT a notification is about, so a click on it
// can go somewhere. It is a closed set on purpose: every click surface (bell
// row, browser notification, desktop notification) switches on these four and
// nothing else, which is what keeps the three from disagreeing.
type NotificationTargetKind string

// The four target kinds. "none" is a real answer, not a missing one — an
// event about the whole instance (a replica failure, an available update)
// has nothing to open, and saying so is better than inventing a path.
const (
	TargetNone  NotificationTargetKind = "none"
	TargetFile  NotificationTargetKind = "file"
	TargetDir   NotificationTargetKind = "dir"
	TargetShare NotificationTargetKind = "share"
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
// TargetFile, the folder for TargetDir.
type NotificationTarget struct {
	Kind    NotificationTargetKind `json:"kind"`
	Storage string                 `json:"storage,omitempty"`
	Path    string                 `json:"path,omitempty"`
	// ID is the opaque id of a target that is not a path — the share token
	// for TargetShare.
	ID string `json:"id,omitempty"`
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

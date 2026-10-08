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

	// WebhookReason is, at read time, the webhook cell's second half in the
	// reader's language (notify.WebhookReason): why a delivery was skipped,
	// in the server's words - WebhookError keeps the code (no_destination,
	// digest_unnamed, sibling, stopped) - or a failed delivery's error as the
	// receiver gave it. Never stored.
	WebhookReason string `json:"webhook_reason,omitempty"`

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

	// E2E is set, at read time, on a row whose sentence names an item inside
	// an end-to-end encrypted folder (notify.SayRows): Title and Body then say
	// "🔒 Encrypted item" there, and E2E says where those words stand, so a
	// browser that has the folder unlocked puts the item's real name in their
	// place. Never stored.
	E2E *NotificationE2E `json:"e2e,omitempty"`

	// Opens says, at read time, whether a click on the row goes somewhere
	// (notify.Opens - the one rule the bell's rows, the page's pop-up and a
	// push follow). Never stored.
	Opens *bool `json:"opens,omitempty"`
}

// NotificationE2E is the one part of a notification's text the server cannot
// say: the name of an item inside an end-to-end encrypted folder, which it
// only has scrambled. The sentence is still the server's (internal/notify
// say.go); Title and Body here are that sentence cut at the names, and Names
// is what each name is. A reader with the folder's key fills a name in; a
// reader without it uses the name's Locked text, which is exactly what the
// row's own title and body already say.
type NotificationE2E struct {
	Title []NotificationTextPart `json:"title"`
	Body  []NotificationTextPart `json:"body"`
	Names []NotificationE2EName  `json:"names"`
}

// NotificationTextPart is one piece of a sentence: either words (Text) or the
// name at Names[Name].
type NotificationTextPart struct {
	Text string `json:"text,omitempty"`
	Name *int   `json:"name,omitempty"`
}

// NotificationE2EName is one name inside an encrypted folder.
type NotificationE2EName struct {
	// Wire is the item as listings address it, "<storage>://<path>", its
	// encrypted segments as stored.
	Wire string `json:"wire"`
	// Root is the encrypted folder it is in, "<storage>://<root>".
	Root string `json:"root"`
	// Part is "name" (the item's own name) or "path" (where it is, from the
	// encrypted folder down).
	Part string `json:"part"`
	// Locked is what the name reads as without the folder's key: the lock
	// word, or "<root>/…/<lock word>" for a path.
	Locked string `json:"locked"`
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
	// Digest is the reader's notification digest (internal/notify digest.go,
	// migration 00087): which of their rows are "quiet" — held for the digest
	// rather than told one by one. Nil reads every row as it always was. Like
	// Only and Except it narrows a per-user read and is ignored by the
	// admin-global one, which is the audit.
	Digest *DigestFilter
}

// DigestFilter says which rows of a reader's bell are QUIET: a row above the
// reader's digest point (After) of one of the kinds they hold for the digest
// (Quiet). A quiet row is in the reader's list, as read, and counted by
// neither the unread list nor the badge — the digest that carries it is what
// is unread. Applied in SQL, so the badge and the list agree.
type DigestFilter struct {
	// After is the reader's digest point (notify_digest_state.through_id):
	// every row at or below it has been told, on its own or in a digest.
	After int64
	// Quiet is the event kinds this reader holds for the digest. Empty: no row
	// is quiet.
	Quiet []string
	// Only reads the quiet rows alone, whatever their read state — what a
	// digest is made of — and UpTo, when above zero, stops at that id.
	Only bool
	UpTo int64
}

// IsQuiet reports whether a row read through f is quiet for its reader — the
// Go twin of the SQL predicate, for a row already in hand.
func (f *DigestFilter) IsQuiet(n *Notification) bool {
	if f == nil || n == nil || n.ID <= f.After {
		return false
	}
	for _, e := range f.Quiet {
		if e == n.Event {
			return true
		}
	}
	return false
}

// DigestPolicy is an administrator's defaults for the notification digest,
// for the people of one scope: the instance (Scope 0, a single-tenant
// install) or one tenant (its provider id). Table notify_digest_policy.
type DigestPolicy struct {
	Scope int64 `json:"-"`
	// WindowMinutes is how long the non-urgent notifications are held before
	// the digest that carries them is sent: 1-15.
	WindowMinutes int `json:"window_minutes"`
	// Urgent is the kinds that are told at once for everybody who did not
	// choose otherwise. Nil: the built-in list (notify.DefaultUrgentEvents).
	Urgent    []string  `json:"urgent_events"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NotificationSettings captures per-user notification preferences. Stored
// in the notification_settings table. Default-on for a fresh user — a
// missing row is treated as InAppEnabled=true with no muted events.
type NotificationSettings struct {
	UserID         int64           `json:"user_id"`
	InAppEnabled   bool            `json:"in_app_enabled"`
	MutedEventsRaw json.RawMessage `json:"muted_events"`
	// UrgentOverridesRaw is the person's own urgent choices (migration
	// 00087): a JSON object {"<event>": true|false}; a kind it does not name
	// follows the administrator's default. ⚠ Nil on a write keeps what is
	// stored: a client that knows nothing of the digest (an older desktop
	// app) resends the two fields above and must not wipe these.
	UrgentOverridesRaw json.RawMessage `json:"urgent_overrides,omitempty"`
}

// UrgentOverrides decodes UrgentOverridesRaw. Malformed JSON reads as "no
// choice made" — the administrator's defaults then apply, which is the
// fail-open direction for a preference that only ever delays a notice.
func (s *NotificationSettings) UrgentOverrides() map[string]bool {
	if s == nil || len(s.UrgentOverridesRaw) == 0 {
		return nil
	}
	var raw map[string]bool
	if err := json.Unmarshal(s.UrgentOverridesRaw, &raw); err != nil {
		return nil
	}
	out := make(map[string]bool, len(raw))
	for k, v := range raw {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = v
		}
	}
	return out
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

	// Lang is the language the target's `title` and `body` are said in
	// (migration 00105): a webhook is a receiver no person stands behind, so
	// it has the language chosen for it here, and "" is the instance's
	// (FILEX_DEFAULT_LOCALE, else English). notify say.go.
	Lang string `json:"lang"`

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

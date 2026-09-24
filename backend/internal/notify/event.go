// Package notify is filex' notification subsystem.
//
// Two delivery channels:
//   - Webhook: a single configurable POST endpoint that always receives
//     a generic JSON body. Retry 3× exponential backoff.
//   - In-app:  per-user bell + history + read/unread, surfaced via the
//     /api/notifications/... endpoints. Persisted in the notifications
//     table so they survive a server restart.
//
// Both channels fan out from one Service.Send call. The webhook URL +
// optional bearer token come from FILEX_WEBHOOK_URL / FILEX_WEBHOOK_TOKEN
// (.env bootstrap) or DB-stored override (notification_settings tabular
// override is per-user; the webhook URL itself is global, stored in the
// settings table). v0.1 reads them from config; the admin panel exposes
// PATCH /admin/api/notifications/webhook-config to change them at runtime.
package notify

import (
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Severity classifies an event's urgency. Used by the UI for color
// coding and webhook receivers for filtering.
type Severity string

// Severity values. Free-form strings are accepted by the store but the
// UI only knows these four.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// EventType is a stable string id for a kind of event. Kept as a typed
// alias rather than an enum so consumers can introduce new types without
// touching this package.
type EventType string

// Canonical events fired by filex itself. Subsystems may emit other
// types; the webhook payload echoes whatever is given.
//
// ⚠ DECLARED IS NOT EMITTED. Seven of the operational alerts below have no
// producer anywhere in the tree: replica_fail_spike, quota_near_full,
// quota_full, queue_stuck, auth_fail_spike, disk_full, update_applied. A
// webhook target may still name one — MatchesEvent accepts it — so the
// subscription saves and then waits forever, which reads exactly like a
// subsystem with no problems. They are kept (rather than deleted) because the
// alarms are wanted; docs/NOTIFICATIONS.md carries an "Emitted" column so an
// operator is not misled meanwhile. Wire the producer before removing a name
// from that list.
const (
	EventReplicaFail          EventType = "replica_fail"
	EventReplicaFailSpike     EventType = "replica_fail_spike"
	EventReplicaReconcileDone EventType = "replica_reconcile_done"
	EventReplicaStatusReport  EventType = "replica_status_report"
	EventPrimaryReadFail      EventType = "primary_read_fail"
	EventQuotaNearFull        EventType = "quota_near_full"
	EventQuotaFull            EventType = "quota_full"
	EventQueueStuck           EventType = "queue_stuck"
	EventAuthFailSpike        EventType = "auth_fail_spike"
	EventDiskFull             EventType = "disk_full"
	// EventUpdateAvailable fires ONCE per newly published release — the
	// "already announced" mark is persisted, so a restart loop cannot turn it
	// into a stream.
	EventUpdateAvailable EventType = "update_available"
	// EventUpdateApplied fires after a self-upgrade replaced the binary. The
	// version moving is exactly the kind of change an operator must find in
	// the log even if nobody was watching when it happened.
	EventUpdateApplied EventType = "update_applied"
)

// operatorEvents are the alarms above: things only an administrator can act on
// (upgrade the server, fix a replica, free a disk). They are recorded as
// broadcasts — one row, no user — and the bell used to hand every broadcast to
// every signed-in person, so a plain user read "filex v0.42.2 yayınlandı — Bu
// sunucu 0.1.0-dev sürümünde çalışıyor" about a server they cannot touch
// (release-candidate sweep, 2026-09-21). The read path keeps them out of a
// non-administrator's bell and badge (Bell.never, applied by Service.List /
// UnreadCount); the row, the admin audit list and the webhook are unchanged.
//
// ⚠ The rule is by EVENT, not by address: a row of one of these kinds that is
// addressed to a plain user is kept out of their bell too. No emitter does
// that today (all of them broadcast); one that wants to tell a person about
// their own quota must use a person-facing event, not borrow an alarm.
//
// ⚠ A broadcast is NOT operator-only by itself: `admin_test` from the
// notifications page and an app's instance-wide `plugin.notice` are meant for
// everybody, and e2e 109 reads the former from a non-admin's bell. Who reads
// which broadcast is decided in ONE place — bell.go.
var operatorEvents = []EventType{
	EventReplicaFail, EventReplicaFailSpike, EventReplicaReconcileDone, EventReplicaStatusReport,
	EventPrimaryReadFail, EventQuotaNearFull, EventQuotaFull, EventQueueStuck, EventAuthFailSpike,
	EventDiskFull, EventUpdateAvailable, EventUpdateApplied,
}

// Canonical file/share events (webhook v2 — "Bağlan" (Connect) wave). Emitted
// asynchronously from the API mutation handlers; webhook targets filter
// on these names via their per-target events allow-list.
//
// ⚠ This const block is the SINGLE SOURCE OF TRUTH for the subscribable
// event catalogue. The rule is mechanical: an EventType whose value contains
// a dot is a webhook-v2 event an operator can tick in the admin UI, and the
// admin UI's list must match this block exactly. Two tests hold that:
//
//   - catalog_test.go here refuses an inline notify.EventType("x.y")
//     anywhere in the backend, so a new event cannot be born outside this
//     block (that is how "e2e.escrow_used" existed for a release without
//     ever reaching the UI);
//   - web/tests/webhooks/eventCatalog.test.ts parses this file and fails
//     when web/src/lib/webhookEvents.ts drifts from it, or when an event is
//     missing an operator-readable label in en.json or tr.json.
//
// The older underscore-named alerts above (replica_fail, disk_full, …) are
// operational alarms from the pre-v2 global webhook; MatchesEvent will still
// filter on them if a target names one, but they are not part of the catalogue
// the UI offers.
const (
	EventFileUploaded EventType = "file.uploaded"
	// EventFileUpdated fires when a write REPLACED the bytes of a file that
	// already existed, where file.uploaded means the write created it. Both
	// come from the same post-write gate (internal/writehook), so every write
	// surface — editor save, browser re-upload, WebDAV PUT, S3 PutObject,
	// SFTP/FTPS/NFS, the AI/MCP surface — splits them the same way.
	//
	// ⚠ Adding it NARROWS file.uploaded: a subscriber that watched
	// file.uploaded to see edits stops hearing them and has to subscribe to
	// file.updated as well. That is deliberate — an event called "uploaded"
	// that also fires for an in-place edit cannot be filtered by anyone.
	EventFileUpdated EventType = "file.updated"
	// EventFileUploadFailed fires when bytes filex already acknowledged could
	// not be written to the storage driver — the staged upload path answers the
	// client before it transfers, so a failure after that point is invisible
	// unless it is announced (issue #16).
	EventFileUploadFailed EventType = "file.upload_failed"
	EventFileDeleted      EventType = "file.deleted"
	EventFileMoved        EventType = "file.moved"
	EventFileTrashed      EventType = "file.trashed"
	EventShareCreated     EventType = "share.created"
	EventDropReceived     EventType = "drop.received"
	/* koru:k2 av */
	// EventFileInfected fires when the async ClamAV scan flags an
	// uploaded file; the payload carries the node plus a `signature`
	// meta field, and the file is quarantined into trash.
	EventFileInfected EventType = "file.infected"
	/* calisma:d3 comments */
	// EventCommentAdded fires when a user comments on a file/folder
	// node (v0.6 "Çalışma" (Work)). The payload carries the node (path/name),
	// the actor, and meta {comment_id, body (first 200 chars)}.
	EventCommentAdded EventType = "comment.added"
	// Archive events describe the completed user operation rather than each
	// low-level file write it performed. Extraction can create hundreds of
	// files; one completion event is useful, hundreds of "new file" alerts are
	// not.
	EventArchiveCreated   EventType = "archive.created"
	EventArchiveExtracted EventType = "archive.extracted"
	// EventE2EEscrowUsed fires when an encrypted folder was opened with the
	// operator's ESCROW key rather than its owner's passphrase. Not the
	// recovery key — that one the owner holds; escrow means somebody else's
	// key was used on their folder, which is why the owner is told. The payload
	// carries the node plus meta {escrow_kid, storage, folder} and, when the
	// caller was authenticated, actor_email.
	//
	// It was written as an inline notify.EventType("e2e.escrow_used") in the
	// handler, which is why it never appeared in the admin UI's list: an
	// event that is not a constant here is invisible to everything that reads
	// this block. catalog_test.go now refuses that shape.
	EventE2EEscrowUsed EventType = "e2e.escrow_used"
	// EventPluginNotice is an app plugin (internal/wasmplugin) speaking to
	// people through the notify_send host function: a signature request, a
	// finished conversion, anything the plugin's author phrased. The row's
	// Title/Body are the plugin's English text; meta carries `plugin` (its
	// name), `title_en`/`title_tr`/`body_en`/`body_tr` for the reader's
	// language, and `job` when sent from a queued action.
	EventPluginNotice EventType = "plugin.notice"
	// EventAdminTest is the admin notifications page's "Send test
	// notification": one broadcast through the bell and every webhook, so an
	// operator can see the plumbing work. It names nothing and is meant for
	// every bell (bell.go → everyoneEvents); e2e 109 seeds a non-admin's bell
	// with it.
	EventAdminTest EventType = "admin_test"
)

// Target is the typed "where does a click on this notification go" —
// re-exported from model so subsystems only ever import notify.
//
// ⚠ There is exactly ONE of these per notification and exactly one resolver
// per surface reading it. `node`/`share` above stay what they always were:
// descriptive context for a webhook receiver. The target is the ADDRESS, and
// it is a separate field because the two are not the same thing — a
// `file.trashed` event describes the file at its original path and has to open
// the copy in the trash, and `share.created` carries both a node and a share
// while only one of them is the thing to open.
type Target = model.NotificationTarget

// TargetKind re-exports model.NotificationTargetKind.
type TargetKind = model.NotificationTargetKind

// The six target kinds, re-exported so an emitter never imports model just
// to name one.
const (
	TargetNone  = model.TargetNone
	TargetFile  = model.TargetFile
	TargetDir   = model.TargetDir
	TargetShare = model.TargetShare
	TargetTrash = model.TargetTrash
	TargetApp   = model.TargetApp
)

// TrashTarget addresses the Trash view with the item that was deleted FROM
// origPath selected — the target of a soft delete and of an antivirus
// quarantine. origPath is where the file lived, never its `.filex-trash/…`
// key: the Trash view lists items by where they came from, and the key is a
// path no surface serves (syspath.Sealed).
func TrashTarget(origPath string) *Target {
	return &Target{Kind: TargetTrash, Path: cleanTargetPath(origPath)}
}

// AppTarget addresses one of an app plugin's home pages, optionally at a
// section of it. The plugin and view are checked by the caller (the host's
// notify_send answers only for the app's own `home` views).
func AppTarget(plugin, view, section string) *Target {
	return &Target{Kind: TargetApp, Open: &model.NotificationOpen{Plugin: plugin, View: view, Section: section}}
}

// FileTarget addresses one file by its path inside its storage. The click
// opens the file's FOLDER with the file selected — the folder is derived by
// the client, never stored, so a rename of the parent cannot leave a target
// pointing at a folder and a file that disagree.
func FileTarget(p string) *Target { return &Target{Kind: TargetFile, Path: cleanTargetPath(p)} }

// DirTarget addresses one folder by its path inside its storage.
func DirTarget(p string) *Target { return &Target{Kind: TargetDir, Path: cleanTargetPath(p)} }

// ShareTarget addresses a public share link by its token.
func ShareTarget(token string) *Target {
	token = strings.TrimSpace(token)
	if token == "" {
		return &Target{Kind: TargetNone}
	}
	return &Target{Kind: TargetShare, ID: token}
}

// ParentDirTarget addresses the FOLDER a path sits in — the honest target for
// an event about something that is no longer there (a permanent delete).
func ParentDirTarget(p string) *Target {
	p = cleanTargetPath(p)
	if p == "" {
		return &Target{Kind: TargetDir}
	}
	d := path.Dir(p)
	if d == "." || d == "/" {
		d = ""
	}
	return &Target{Kind: TargetDir, Path: d}
}

// cleanTargetPath normalises a storage-relative path: backslashes to slashes,
// no leading slash, no `<storage>://` prefix.
//
// ⚠ The prefix strip is not cosmetic. Emitters hand over `node.Path`, which is
// storage-relative, but a caller that passes a qualified path would otherwise
// produce `<storage>://<storage>://…` at the client — a target that resolves
// to a folder that does not exist, which looks exactly like a deleted file.
func cleanTargetPath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+3:]
	}
	return strings.TrimPrefix(p, "/")
}

// NodeRef identifies the file/folder an event is about (webhook v2
// payload `node` object).
type NodeRef struct {
	StorageID int64  `json:"storage_id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size,omitempty"`
}

// ShareRef identifies the share link an event is about (webhook v2
// payload `share` object).
type ShareRef struct {
	Token string `json:"token"`
	Path  string `json:"path,omitempty"`
}

// ActorRef identifies who triggered the event, best-effort (webhook v2
// payload `actor` object). Anonymous surfaces (public drop) omit it.
type ActorRef struct {
	ID    int64  `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
}

// Event is the in-memory shape that subsystems hand to Service.Send.
//
// On the wire (webhook payload) and in the DB it is encoded as JSON
// — the field tags below match the documented public contract.
type Event struct {
	Event    EventType      `json:"event"`
	Severity Severity       `json:"severity"`
	Title    string         `json:"title"`
	Body     string         `json:"body"`
	Meta     map[string]any `json:"meta,omitempty"`
	TS       time.Time      `json:"ts"`

	// At mirrors TS under the webhook-v2 documented field name; Send
	// fills it from TS so the wire payload always carries `at`.
	At time.Time `json:"at"`

	// Structured webhook-v2 payload objects (all optional). Node points
	// at the file/folder the event concerns, Share at the public link,
	// Actor at the triggering user. Send also folds them into the
	// persisted meta_json so the in-app history keeps the context.
	Node  *NodeRef  `json:"node,omitempty"`
	Share *ShareRef `json:"share,omitempty"`
	Actor *ActorRef `json:"actor,omitempty"`

	// Target is where a click on this notification goes. Emitters set it
	// with FileTarget/DirTarget/ShareTarget/ParentDirTarget; Send fills in
	// the storage NAME from Node.StorageID and, failing that, downgrades the
	// target to TargetNone rather than shipping half an address. It is
	// always non-nil on the wire, so a receiver can switch on `target.kind`
	// without a nil check.
	Target *Target `json:"target,omitempty"`

	// UserID, when non-nil, scopes the in-app notification to a single
	// user. Otherwise the row is broadcast, and who reads it is bell.go's
	// rule: by kind, then — for a member — only when the file it names is one
	// the member can see (handlers/notifications.go). The webhook delivery is
	// unaffected.
	UserID *int64 `json:"-"`
}

// WebhookStatus enumerates the lifecycle of a single webhook attempt
// chain. Persisted to notifications.webhook_status and surfaced in the
// admin UI.
type WebhookStatus string

const (
	// WebhookStatusPending — the webhook attempt has not yet been made.
	WebhookStatusPending WebhookStatus = "pending"
	// WebhookStatusSent — the upstream returned 2xx within the budget.
	WebhookStatusSent WebhookStatus = "sent"
	// WebhookStatusFailed — exhausted retries; the operator should
	// investigate.
	WebhookStatusFailed WebhookStatus = "failed"
	// WebhookStatusSkipped — no webhook URL configured. The in-app
	// row still exists.
	WebhookStatusSkipped WebhookStatus = "skipped"
)

// (A second, unused `notify.Settings` struct declaring the same three fields
// as model.NotificationSettings lived here. Nothing constructed it and nothing
// converted to it — the store, the API and the bell filter all use
// model.NotificationSettings — so it was a decoy for anyone grepping for where
// muting is decided. Deleted rather than kept in sync.)

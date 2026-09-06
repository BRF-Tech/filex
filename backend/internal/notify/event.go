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

import "time"

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
	// EventE2EEscrowUsed fires when an encrypted folder was opened with the
	// recovery (escrow) key rather than its owner's passphrase. The payload
	// carries the node plus meta {escrow_kid, storage, folder} and, when the
	// caller was authenticated, actor_email.
	//
	// It was written as an inline notify.EventType("e2e.escrow_used") in the
	// handler, which is why it never appeared in the admin UI's list: an
	// event that is not a constant here is invisible to everything that reads
	// this block. catalog_test.go now refuses that shape.
	EventE2EEscrowUsed EventType = "e2e.escrow_used"
)

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

	// UserID, when non-nil, scopes the in-app notification to a single
	// user. Otherwise the row is broadcast (admin-visible to everyone
	// with role=admin). The webhook delivery is unaffected.
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

// Settings is the per-user opt-in toggle.
type Settings struct {
	UserID       int64       `json:"user_id"`
	InAppEnabled bool        `json:"in_app_enabled"`
	MutedEvents  []EventType `json:"muted_events"`
}

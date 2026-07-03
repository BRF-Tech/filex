package model

import (
	"encoding/json"
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

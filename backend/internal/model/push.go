package model

import "time"

// PushSubscription is one device a person receives Web Push on (task #191,
// internal/notify push.go): a browser profile on a phone or a computer, or the
// web app added to an iPhone's Home Screen.
//
// ⚠ Endpoint, P256dh and Auth never go out in an answer. The endpoint is an
// address anybody holding it (and the instance's VAPID key) may push to; the
// client finds itself in the list by EndpointHash (webpush.EndpointHash, the
// hex SHA-256 it computes the same way).
type PushSubscription struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	Endpoint     string `json:"-"`
	EndpointHash string `json:"endpoint_hash"`
	P256dh       string `json:"-"`
	Auth         string `json:"-"`
	// Label is what the device was called when it subscribed ("Chrome on
	// Android"), for the person's list.
	Label string `json:"label"`
	// Service is the push service's host (fcm.googleapis.com,
	// web.push.apple.com ...), filled when answered.
	Service string `json:"service,omitempty"`
	// ThroughID is the device's mark: every notification at or below it has
	// been pushed to it or passed over. A compare-and-set moves it, so two
	// servers never push one row twice.
	ThroughID int64 `json:"-"`
	// Failures counts the push service's refusals since the last push it
	// took; past a few the device is dropped.
	Failures  int        `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	LastOKAt  *time.Time `json:"last_ok_at,omitempty"`
}

// PushVAPIDKey is the instance's application server key (RFC 8292): made at
// the first start, replaced only by a rotation.
//
// ⚠ PrivateSealed is the private key sealed with FILEX_SECRET_KEY
// (internal/secretbox, bound to its row); it never leaves the server.
type PushVAPIDKey struct {
	PublicKey     string
	PrivateSealed string
	CreatedAt     time.Time
}

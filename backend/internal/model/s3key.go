package model

import "time"

// S3AccessKey is the credential an S3 client signs requests with (migration
// 00026).
//
// # Why it is not an APIToken with a different label
//
// An API token is sha256-hashed because a bearer protocol sends the secret and
// the server only has to compare. SigV4 sends no secret — it sends an HMAC
// chain derived from one — so the server must be able to recompute that chain,
// which means holding the secret in recoverable form (SecretEnc, sealed by
// internal/secretbox). Different storage contract, different table.
//
// # What it may and may not do
//
// A key minted FROM an API token (APITokenID non-nil) is that token projected
// into a protocol that cannot carry it. Its effective permission is the
// intersection of everything upstream — token scopes ∩ ACL grants ∩
// confinement ∩ tenant ∩ storage read_only ∩ role ceiling. It may narrow; it
// may never widen. A key with no token carries the account's own permissions.
type S3AccessKey struct {
	ID int64 `json:"id"`
	// AccessKeyID is the public half — what the client puts in the
	// Authorization header, and what an incoming signature is looked up by.
	AccessKeyID string `json:"access_key_id"`
	// SecretEnc is the sealed secret. It is `json:"-"` for the same reason a
	// password hash is: the plaintext is shown exactly once, at creation, and
	// no listing endpoint may ever hand it back.
	SecretEnc string `json:"-"`
	UserID    int64  `json:"user_id"`
	// APITokenID ties the key to the token it inherits from, or nil when it was
	// minted straight from the account. The FK cascades, so revoking the token
	// revokes this key too — a credential must not outlive its parent.
	APITokenID *int64 `json:"api_token_id,omitempty"`
	Label      string `json:"label"`
	// Bucket/Prefix are the optional confinement, in the shape S3 clients
	// already understand (and IAM already models). Empty Bucket means every
	// storage the caller can see.
	Bucket string `json:"bucket,omitempty"`
	Prefix string `json:"prefix,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// DisabledAt turns the key off without deleting it, so an operator can stop
	// a leaked credential and still see it in the audit trail.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Usable reports whether the key may authenticate right now. Expiry and the
// disabled flag are checked in one place so a caller cannot honour one and
// forget the other.
func (k *S3AccessKey) Usable(now time.Time) bool {
	if k == nil || k.DisabledAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return false
	}
	return true
}

package model

import "time"

// SSHPublicKey is a key an account registered for the SFTP endpoint
// (migration 00027).
//
// # What it decides, and what it does not
//
// It does NOT decide whether the client holds the private key — x/crypto/ssh
// has already verified the signature by the time filex is consulted. It decides
// WHICH ACCOUNT that key belongs to, which is why the fingerprint is unique
// across the install: two accounts sharing a key would make the login
// ambiguous, and resolving it by taking the first row is how a key ends up
// authenticating as somebody else.
type SSHPublicKey struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
	// Name is the human label — the comment from the pasted key by default,
	// which is usually `user@machine` and is exactly what somebody needs to
	// decide which row to revoke.
	Name string `json:"name"`
	// Fingerprint is the SHA256 fingerprint OpenSSH prints, base64, without the
	// `SHA256:` prefix. It is what a login is looked up by.
	Fingerprint string `json:"fingerprint"`
	// PublicKey is the normalised wire form (`<type> <base64>`), so the key can
	// be shown back and exported.
	PublicKey string `json:"public_key"`

	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// DisabledAt switches a key off without deleting it, so a machine that is
	// away for a while can be stopped and restored rather than re-registered.
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Usable reports whether this key may authenticate right now.
func (k *SSHPublicKey) Usable() bool { return k != nil && k.DisabledAt == nil }

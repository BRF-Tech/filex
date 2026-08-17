package model

import "time"

// NFSExport is one NFSv3 mount point, bound to one account (migration 00028).
//
// # The path is the credential
//
// NFSv3 cannot authenticate a caller in any way filex can use: real identity
// means RPCSEC_GSS and therefore Kerberos, and AUTH_SYS is the client asserting
// its own uid with nothing to check it against. So the binding moves: the mount
// handshake names an export path, and that path carries 32 bytes of entropy.
// Whoever knows it may mount, and the mount then runs as this account — the
// same shape as a share link.
//
// ⚠ Consequences worth saying out loud rather than discovering:
//   - the path is a SECRET, so it belongs in a config file with the same care
//     as a password, and a mount table is a place secrets end up readable;
//   - NFSv3 is unencrypted, so this is a LAN or VPN protocol. `filex mount`
//     (FUSE over HTTPS) is the answer for anything else;
//   - revoking is deleting the row, and it takes effect on the next request
//     rather than tearing down a live mount.
type NFSExport struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
	// APITokenID ties this export to the token it inherits from, or nil when
	// minted from the account. The FK cascades, so revoking the token revokes
	// the mount with it.
	APITokenID *int64 `json:"api_token_id,omitempty"`
	Label      string `json:"label"`
	// TokenHash is sha256 of the path secret. The plaintext is shown once, at
	// creation, and never stored — this is a bearer credential the server only
	// has to compare, unlike an S3 secret which SigV4 must recompute from.
	TokenHash string `json:"-"`
	// StorageName/Prefix confine the mount, in the same shape the S3 keys use.
	// Empty StorageName means every storage the account can see.
	StorageName string `json:"storage_name,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	// ReadOnly makes this export refuse every write regardless of the
	// account's own permissions. An NFS mount is usually consumed by a MACHINE,
	// and "this one may only read" is the commonest thing to want to say.
	ReadOnly bool `json:"read_only"`
	// AllowCIDRs is a comma-separated allow-list. Empty means any address the
	// listener itself accepts.
	AllowCIDRs string `json:"allow_cidrs,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Usable reports whether this export may be mounted right now.
func (e *NFSExport) Usable(now time.Time) bool {
	if e == nil || e.DisabledAt != nil {
		return false
	}
	if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
		return false
	}
	return true
}

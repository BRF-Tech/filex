package model

import "time"

// Grant levels for a per-file/per-folder ACL entry (file_grants table).
// Ordered by privilege: viewer < editor < owner.
//
//   - GrantViewer — view + download only (no mutation, no convert/edit).
//   - GrantEditor — read + write + rename/move/delete + convert.
//   - GrantOwner  — everything an editor can do PLUS manage the item's
//     permission set (grant/revoke to other users on that item & its subtree).
const (
	GrantViewer = "viewer"
	GrantEditor = "editor"
	GrantOwner  = "owner"
)

// ValidGrantLevel reports whether s is a known grant level.
func ValidGrantLevel(s string) bool {
	switch s {
	case GrantViewer, GrantEditor, GrantOwner:
		return true
	default:
		return false
	}
}

// FileGrant is one row of the per-file/per-folder ACL. It grants a single
// user a level on a path within one storage. PathPrefix is stored in
// confine-form (cleaned, no leading/trailing slash; "" == storage root); for
// a directory grant the level cascades to every descendant, for a file grant
// it applies to that exact path only. Grants are only consulted on storages
// with RBACEnabled=true. See internal/acl for resolution.
type FileGrant struct {
	ID         int64     `json:"id"`
	StorageID  int64     `json:"storage_id"`
	PathPrefix string    `json:"path_prefix"`
	IsDir      bool      `json:"is_dir"`
	UserID     int64     `json:"user_id"`
	Level      string    `json:"level"`
	CreatedBy  *int64    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`

	// Enrichment fields (not persisted; filled by the permissions handler for
	// the UI panel). Left empty by the store scanners.
	UserEmail       string `json:"user_email,omitempty"`
	UserDisplayName string `json:"user_display_name,omitempty"`
	Inherited       bool   `json:"inherited,omitempty"`
}

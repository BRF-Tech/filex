package model

import "time"

/* calisma:d3 comments */

// NodeComment is one comment on a file/folder node (v0.6 "Çalışma" wave,
// migration 00020). Comments form a flat chronological thread per node,
// surfaced in the inspector panel.
//
// AuthorName / CanDelete are API-layer projections (joined display name,
// caller-specific delete right) — never persisted.
type NodeComment struct {
	ID        int64      `json:"id"`
	NodeID    int64      `json:"node_id"`
	UserID    int64      `json:"user_id"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// AuthorName is the joined users.display_name (falling back to email)
	// filled by ListNodeComments. Not persisted.
	AuthorName string `json:"author_name,omitempty"`
	// CanDelete marks whether the CURRENT caller may delete this comment
	// (author or admin). Computed by the API layer. Not persisted.
	CanDelete bool `json:"can_delete,omitempty"`
}

package model

import (
	"time"
)

// NodeType enumerates node kinds.
type NodeType string

const (
	NodeTypeFile      NodeType = "file"
	NodeTypeDirectory NodeType = "dir"
	NodeTypeSymlink   NodeType = "symlink"
)

// SyncState describes per-node sync lifecycle.
type SyncState string

const (
	SyncStateSynced  SyncState = "synced"
	SyncStateDirty   SyncState = "dirty"
	SyncStatePending SyncState = "pending"
	SyncStateError   SyncState = "error"
)

// Node is the canonical representation of a file or directory in DB cache.
type Node struct {
	ID           int64      `json:"id"`
	StorageID    int64      `json:"storage_id"`
	ParentID     *int64     `json:"parent_id,omitempty"`
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	PathHash     string     `json:"path_hash"`
	StorageKey   string     `json:"storage_key,omitempty"`
	Type         NodeType   `json:"type"`
	Size         int64      `json:"size"`
	Mime         string     `json:"mime,omitempty"`
	Etag         string     `json:"etag,omitempty"`
	BackendMtime *time.Time `json:"backend_mtime,omitempty"`
	DBMtime      time.Time  `json:"db_mtime"`
	SyncState    SyncState  `json:"sync_state"`
	// TransferState is "stored" (the bytes are on the driver) or "staged" (the
	// row exists and is listed, but the bytes are still in filex's staging area
	// while a background upload-commit op transfers them). Everything written
	// before staged uploads existed reads back "stored".
	TransferState string     `json:"transfer_state,omitempty"`
	SeenAt        time.Time  `json:"seen_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	// Optional joined data — populated by API layer, never persisted.
	Thumb *Thumbnail        `json:"thumb,omitempty"`
	Meta  map[string]string `json:"meta,omitempty"`
	// Storage is the NAME of the storage holding this node, filled by the
	// handlers that return nodes outside a folder listing (starred, recently
	// opened). Those rows carry only a numeric storage_id, and a client in
	// multi-storage mode cannot build the `name://path` it needs to open one —
	// so the recently-opened tray listed files that did nothing when clicked.
	Storage string `json:"storage,omitempty"`
}

// Thumbnail references a generated thumbnail asset.
type Thumbnail struct {
	NodeID      int64      `json:"node_id"`
	State       string     `json:"state"` // pending, ready, failed, skipped
	StorageKey  string     `json:"storage_key,omitempty"`
	Width       int        `json:"width,omitempty"`
	Height      int        `json:"height,omitempty"`
	Error       string     `json:"error,omitempty"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
}

// NodeVersion is a historical snapshot of a node's content.
type NodeVersion struct {
	ID         int64     `json:"id"`
	NodeID     int64     `json:"node_id"`
	VersionN   int       `json:"version_n"`
	StorageKey string    `json:"storage_key,omitempty"`
	Size       int64     `json:"size"`
	Etag       string    `json:"etag,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

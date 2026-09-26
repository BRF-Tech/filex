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

	// ─── Ownership (migration 00004 + 00038) ───
	//
	// OwnerID is who PUT THE THING HERE. nil means SYSTEM: nobody put it here
	// through filex — the storage scanner found it, it was written straight
	// into the bucket, or the row predates the column. "System" is the honest
	// word for ownerless, and no user is invented to stand in for it.
	//
	// It is set by the acting identity on every write surface (browser upload,
	// new folder, save, WebDAV/FTPS/SFTP/S3/CLI/desktop under the account whose
	// token was used) and it does NOT move afterwards: a move or a rename is
	// the same file, and an overwrite changes the bytes, not whose file it is.
	// The one exception is adoption — a system row (nil) that a user writes
	// becomes that user's, because "nobody's" is not somebody else's.
	OwnerID *int64 `json:"owner_id,omitempty"`
	// LastActorID is who touched it LAST. nil means system for the same reason
	// OwnerID does: a change that arrived from outside filex (the bucket side
	// changed, a sync found new bytes) has no actor to name.
	LastActorID *int64 `json:"last_actor_id,omitempty"`
	// ExternalUpload marks a thing that arrived through an anonymous drop link
	// / file request. The owner is the person who CREATED the link — they asked
	// for the file, it lands in their storage and it is billed to their quota —
	// but the row still has to be able to say the bytes were handed over by
	// somebody else. That somebody is anonymous by design and gets no identity.
	ExternalUpload bool `json:"external_upload,omitempty"`
	// DeletedBy is who put it in the TRASH (migration 00061): the person whose
	// delete it was, named on the row and on every row trashed with it, since
	// the trash lists a folder's contents as rows of their own. nil when
	// nobody in filex did it (the scanner found the object gone, the virus
	// scan quarantined it) or the row was trashed before filex kept this. A
	// restore clears it; so does every soft delete, before the person is
	// named, so a name never outlives the trip through the trash it was for.
	DeletedBy *int64 `json:"deleted_by,omitempty"`

	// OwnerName is the owner's display name, resolved in one batched lookup by
	// the API layer for the rows it is about to return. Never persisted, and
	// empty for a system row — the client decides what to call "nobody".
	OwnerName string `json:"owner_name,omitempty"`
	// LastActorName is the same for LastActorID.
	LastActorName string `json:"last_actor_name,omitempty"`

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

// NameMatch is what a search without the index asks the database for: the
// live nodes of one storage whose NAME holds every word (search.PlanFallback
// builds it; db.Store.SearchNodes answers it). Every field is literal text,
// never LIKE grammar: the store escapes what its dialect needs.
type NameMatch struct {
	// Words must ALL be in the name, compared the way internal/namefold
	// compares — composed, the four i's one letter, case folded — on both
	// sides. Each word is already folded that way. Checked after Runs.
	Words []string
	// Runs are ASCII text every stored spelling of a matching name holds as
	// it is (see namefold.Plain), so the database can reject most rows with
	// its own case-insensitive LIKE before the costlier comparison above.
	// They narrow nothing Words would not; they only make it cheap.
	Runs []string
	// Prefer decides which rows survive the LIMIT: names equal to it, or to
	// it plus an extension, first; then names starting with it; then the
	// rest — shorter names first within a tier, then by name. Folded like
	// Words. "" orders by name.
	Prefer string
}

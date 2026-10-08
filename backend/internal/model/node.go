package model

import (
	"fmt"
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

	// UnavailableReason is what the storage answered when the sync last asked
	// whether this row's object still exists and got neither "yes" nor "not
	// found" (migration 00078, issue #104): a plugin that does not speak Stat
	// for it, a permission it lacks, a backend error. Empty for an ordinary
	// row. A row that carries one is listed with a warning, and every
	// operation on it - and below it, for a folder - is refused with 409
	// ENTRY_UNAVAILABLE until the storage answers for it again.
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	// UnavailableAt is when the storage last gave that answer.
	UnavailableAt *time.Time `json:"unavailable_at,omitempty"`
	// Unavailable is UnavailableReason != "", set by the store when it reads
	// the row, so a JSON reader (the AI/MCP answers) has a flag to test.
	Unavailable bool `json:"unavailable,omitempty"`

	// LinkState is why a symlink row (NodeTypeSymlink) is a link filex will
	// not follow: the driver's storage.MetaLinkState - outside_root, broken,
	// unresolved - as the sync last listed it (migration 00098). Empty when no
	// reason is known: a row catalogued before 0.54 until the next sync of its
	// folder, or a driver that gives none.
	//
	// ⚠ The common node reads (GetNode, ListNodesByParent, ...) leave it
	// empty. A listing asks Store.NodeLinkStates for its link rows
	// (handlers hydrateLinkStates); db.NodeLinkStateSQL says why.
	LinkState string `json:"link_state,omitempty"`

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
	// SourceSig is the ContentFingerprint of the bytes the latest render
	// read (migration 00075). A row whose signature is not the node's
	// current one was drawn from other content: thumb.Assess re-renders it.
	// Empty on rows drawn before 0.50.
	SourceSig string `json:"source_sig,omitempty"`
	// AttemptedAt is when the latest render started, whatever it ended in.
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
	// Generator is who drew the picture: "builtin", or "app:<name>@<version>"
	// (migration 00077). Empty when nobody did, and on rows from before 0.50.
	Generator string `json:"generator,omitempty"`
	// Attempts is who was asked, in order, and what each answered: a JSON
	// list of {"h": handler, "r": "ok" | reason} (thumb.Attempt). Empty on
	// rows drawn before 0.50; "[]" when every handler was switched off.
	Attempts string `json:"attempts,omitempty"`
}

// ThumbnailProblem is one file whose thumbnail failed or was skipped, as the
// repair tool lists them (Store.ListThumbnailProblems).
type ThumbnailProblem struct {
	NodeID      int64      `json:"node_id"`
	StorageID   int64      `json:"storage_id"`
	Path        string     `json:"path"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	AttemptedAt *time.Time `json:"attempted_at,omitempty"`
	// Attempts: see Thumbnail.Attempts.
	Attempts string `json:"attempts,omitempty"`
}

// ContentFingerprint identifies the version of a node's content without
// reading it: the backend's etag when it reports one, otherwise size and
// modification time (to the millisecond).
//
// ⚠⚠ The ONE definition of "the content changed". The search index decides
// whether to extract a file's text again with it, and the thumbnail pipeline
// whether to draw a file again. A second, slightly different rule would have
// one of them re-running on every pass while the other never noticed.
// What each driver gives it, and what slips through, is in
// docs/thumbnails.md (Design notes) and sync/etag.go objectDrift.
func (n *Node) ContentFingerprint() string {
	if n == nil {
		return ""
	}
	if n.Etag != "" {
		return n.Etag
	}
	var mt int64
	if n.BackendMtime != nil {
		mt = n.BackendMtime.UnixMilli()
	}
	return fmt.Sprintf("%d:%d", n.Size, mt)
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

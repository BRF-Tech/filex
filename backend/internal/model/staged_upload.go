package model

import "time"

// Staged upload states.
//
// A successful commit DELETES the row (the bytes are on the driver and the
// staging directory is gone), so there is deliberately no "stored" state here —
// the node's TransferState carries that fact instead.
const (
	// StagedUploadStaging — chunks are being received.
	StagedUploadStaging = "staging"
	// StagedUploadCommitting — the client committed; the background op is
	// streaming staging → driver.
	StagedUploadCommitting = "committing"
	// StagedUploadFailed — the transfer failed. The staging directory is KEPT
	// so the transfer can be retried without re-uploading a byte.
	StagedUploadFailed = "failed"
)

// Node transfer states (nodes.transfer_state).
const (
	// TransferStateStaged — the node exists and is listed, but its bytes are
	// still in filex's staging area.
	TransferStateStaged = "staged"
	// TransferStateStored — the bytes are on the storage driver. Every node
	// written before staged uploads existed is, by definition, stored.
	TransferStateStored = "stored"
	// TransferStateFailed — the transfer to the storage driver failed. The
	// bytes are still in staging (the transfer is retryable) but they are NOT
	// on the backend, and the node must say so: a node left at "staged"
	// forever is indistinguishable from one still in flight, which is how a
	// dead upload passed for a healthy one in issue #16.
	TransferStateFailed = "failed"
)

// TransferLandedSkew is how far the storage's clock may lag filex's when
// TransferLanded compares an object's modification time with the commit.
const TransferLandedSkew = 2 * time.Second

// TransferLanded reports whether an object found at an unstored node's key —
// transfer_state "staged" or "failed" — can be taken as the bytes that node's
// staged upload committed, given the object's size and modification time.
//
// It exists for ONE situation: the upload's staging session is gone, so
// nothing will ever transfer the bytes again, and the only possible copy is
// the one on the storage. A caller must have checked that no session remains:
// while one does, the session is the truth, and this is not asked.
//
// The evidence is two facts, both required:
//
//   - the size is the committed size (n.Size is what the client committed);
//   - the object is not older than the commit. An overwrite stamps
//     backend_mtime with the commit time when it publishes the row, and a new
//     file's row is created at the commit (db_mtime). The bytes a transfer
//     writes land after that; the version an overwrite was REPLACING, still at
//     the key because the new bytes never arrived, is older.
//
// ⚠ This is not a read-path fallback, and filebody must not become one: while
// a node is unstored its reads come from staging, and "the driver has an
// object of the right size" alone is exactly the silent wrong answer
// filebody refuses to give. This rule only settles the catalogue's state when
// the evidence is complete, and refuses when a time cannot be compared.
func TransferLanded(n *Node, size int64, mtime time.Time) bool {
	if n == nil || size != n.Size || mtime.IsZero() {
		return false
	}
	committedAt := n.DBMtime
	if n.BackendMtime != nil {
		committedAt = *n.BackendMtime
	}
	return !mtime.Before(committedAt.Add(-TransferLandedSkew))
}

// StagedUpload is one in-flight staged (resumable, driver-agnostic) upload.
//
// It is the session record; the authority for what is physically staged is the
// manifest inside the staging directory (internal/staging). ReceivedBytes here
// mirrors the manifest's contiguous offset so listings and the sweeper do not
// have to open every manifest.
type StagedUpload struct {
	ID            string    `json:"id"`
	StorageID     int64     `json:"storage_id"`
	StorageKey    string    `json:"storage_key"`
	UserID        int64     `json:"user_id"`
	TotalSize     int64     `json:"total_size"`
	ChunkSize     int64     `json:"chunk_size"`
	Mime          string    `json:"mime,omitempty"`
	Hash          string    `json:"hash,omitempty"`
	ReceivedBytes int64     `json:"received_bytes"`
	State         string    `json:"state"`
	Error         string    `json:"error,omitempty"`
	NodeID        *int64    `json:"node_id,omitempty"`
	OpID          *int64    `json:"op_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

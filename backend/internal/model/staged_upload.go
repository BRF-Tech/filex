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
)

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

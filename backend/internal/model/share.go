package model

import "time"

// Share kinds. A "download" share grants outbound read access to a node
// (the classic /s/{token} link). A "drop" share is the inverse: a public
// upload link that lets anonymous visitors write files INTO a folder
// without ever seeing its contents (the /d/{token} file-drop link).
const (
	ShareKindDownload = "download"
	ShareKindDrop     = "drop"
)

// Share is a public token granting limited access to a node. For a
// download share this is read (indir); for a drop share it is blind
// upload into the node (a directory) — see Kind.
type Share struct {
	ID            int64      `json:"id"`
	NodeID        int64      `json:"node_id"`
	Token         string     `json:"token"`
	PinHash       string     `json:"-"`       // never serialized
	HasPin        bool       `json:"has_pin"` // computed
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	MaxDownloads  *int       `json:"max_downloads,omitempty"`
	DownloadCount int        `json:"download_count"`
	CreatedBy     *int64     `json:"created_by,omitempty"`
	// CreatedVia is the token username the creating API call acted under
	// ("work", "fishapp"…). Empty for browser-session shares. Display-only —
	// ownership/authorization stay on CreatedBy.
	CreatedVia string    `json:"created_via,omitempty"`
	CreatedAt  time.Time `json:"created_at"`

	// Drop-link fields (Kind == ShareKindDrop). Empty/zero for a normal
	// download share.
	Kind         string  `json:"kind"`                    // "download" | "drop"
	MaxUploads   *int    `json:"max_uploads,omitempty"`   // cap on total files received
	UploadCount  int     `json:"upload_count"`            // files received so far
	DropSettings *string `json:"drop_settings,omitempty"` // JSON limits blob (max_files, max_file_size_mb, allowed_ext, ask_name)
}

// IsDrop reports whether this is a public upload (file-drop) share.
func (s *Share) IsDrop() bool { return s != nil && s.Kind == ShareKindDrop }

// IsExpired reports whether the share has lapsed. Covers time expiry, the
// download cap (download shares) and the upload cap (drop shares).
func (s *Share) IsExpired(now time.Time) bool {
	if s == nil {
		return true
	}
	if s.ExpiresAt != nil && now.After(*s.ExpiresAt) {
		return true
	}
	if s.MaxDownloads != nil && s.DownloadCount >= *s.MaxDownloads {
		return true
	}
	if s.MaxUploads != nil && s.UploadCount >= *s.MaxUploads {
		return true
	}
	return false
}

// ChunkedUpload tracks an in-flight multipart upload.
type ChunkedUpload struct {
	ID         string       `json:"id"`
	StorageID  int64        `json:"storage_id"`
	StorageKey string       `json:"storage_key"`
	UploadID   string       `json:"upload_id"`
	TotalSize  int64        `json:"total_size"`
	Parts      []UploadPart `json:"parts"`
	ExpiresAt  time.Time    `json:"expires_at"`
}

// UploadPart represents one chunk of a multipart upload.
type UploadPart struct {
	PartNumber int    `json:"part_number"`
	Etag       string `json:"etag"`
	Size       int64  `json:"size"`
	URL        string `json:"url,omitempty"` // only on init response
}

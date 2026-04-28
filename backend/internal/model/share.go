package model

import "time"

// Share is a public token granting limited download access to a node.
type Share struct {
	ID            int64      `json:"id"`
	NodeID        int64      `json:"node_id"`
	Token         string     `json:"token"`
	PinHash       string     `json:"-"`              // never serialized
	HasPin        bool       `json:"has_pin"`        // computed
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	MaxDownloads  *int       `json:"max_downloads,omitempty"`
	DownloadCount int        `json:"download_count"`
	CreatedBy     *int64     `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// IsExpired reports whether the share has lapsed.
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
	return false
}

// ChunkedUpload tracks an in-flight multipart upload.
type ChunkedUpload struct {
	ID         string    `json:"id"`
	StorageID  int64     `json:"storage_id"`
	StorageKey string    `json:"storage_key"`
	UploadID   string    `json:"upload_id"`
	TotalSize  int64     `json:"total_size"`
	Parts      []UploadPart `json:"parts"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// UploadPart represents one chunk of a multipart upload.
type UploadPart struct {
	PartNumber int    `json:"part_number"`
	Etag       string `json:"etag"`
	Size       int64  `json:"size"`
	URL        string `json:"url,omitempty"` // only on init response
}

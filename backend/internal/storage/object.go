package storage

import "time"

// ObjectKind enumerates Object types.
type ObjectKind string

const (
	KindFile      ObjectKind = "file"
	KindDirectory ObjectKind = "dir"
	KindSymlink   ObjectKind = "symlink"
)

// Object is a backend-agnostic representation of a single FS entry.
type Object struct {
	Path     string            `json:"path"` // logical path within storage (POSIX-style)
	Name     string            `json:"name"` // basename
	Size     int64             `json:"size"`
	Kind     ObjectKind        `json:"kind"`
	Mime     string            `json:"mime,omitempty"`
	Etag     string            `json:"etag,omitempty"`
	Mtime    time.Time         `json:"mtime,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Event is emitted by Watcher-capable drivers.
type Event struct {
	Op   string // "create", "modify", "delete", "move"
	Path string
	From string // populated for move events only
}

// PresignedUpload holds the URL/method/headers a browser uses for direct uploads.
type PresignedUpload struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	UploadID  string            `json:"upload_id,omitempty"` // for multipart
	PartSize  int64             `json:"part_size,omitempty"`
	PartCount int               `json:"part_count,omitempty"`
	PartURLs  []string          `json:"part_urls,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// Capabilities advertises the Storage Driver's operation set.
type Capabilities struct {
	Read bool `json:"read"`
	// Range reports ranged reads (storage.RangeReader). Callers that serve
	// HTTP bodies use it to decide between http.ServeContent (seekable,
	// 206/Content-Range) and a whole-object io.Copy.
	Range   bool `json:"range"`
	Write   bool `json:"write"`
	Move    bool `json:"move"`
	Copy    bool `json:"copy"`
	Delete  bool `json:"delete"`
	Mkdir   bool `json:"mkdir"`
	Presign bool `json:"presign"`
	Watch   bool `json:"watch"`
}

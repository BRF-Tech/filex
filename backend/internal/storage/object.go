package storage

import "time"

// ObjectKind enumerates Object types.
type ObjectKind string

const (
	KindFile      ObjectKind = "file"
	KindDirectory ObjectKind = "dir"
	KindSymlink   ObjectKind = "symlink"
)

// Metadata key and values a driver uses to say WHY an entry came back as
// KindSymlink instead of as the thing it points at.
//
// A driver that can follow a link reports the TARGET's kind, so an entry left
// as KindSymlink is always one the caller cannot open. Without a reason
// attached, the UI has a row it must refuse to act on and nothing to tell
// anyone — which is exactly how issue #34 was experienced: a 0-byte file that
// would not open and gave no explanation.
const (
	MetaLinkState = "link_state"
	// MetaLinkTarget carries the driver's own resolved identity for a FOLLOWED
	// directory link — the thing CycleGuard keys on. Only directory links need
	// it, because only a directory link can send a walk round in a circle, and
	// resolving one is the expensive call (measured 1.1 ms on Windows), so it
	// is not paid for file links or for links that were not followed.
	MetaLinkTarget = "link_target"

	// LinkFollowed — the link resolved inside the root and the Object
	// describes its TARGET. Carried so a surface can still badge the row as a
	// link; the Kind, size and mtime are the target's.
	LinkFollowed = "followed"
	// LinkOutsideRoot — the target is outside the storage root and the
	// storage's follow_symlinks option is off.
	LinkOutsideRoot = "outside_root"
	// LinkBroken — the link resolves to nothing at all.
	LinkBroken = "broken"
	// LinkUnresolved — the driver knows this is a link and deliberately did
	// not follow it, because it has no way to tell an in-root target from an
	// out-of-root one. Remote backends say this: filex's boundary there is the
	// account's own permissions, so following a link would leave the
	// configured root with nothing to stop the walk.
	LinkUnresolved = "unresolved"
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

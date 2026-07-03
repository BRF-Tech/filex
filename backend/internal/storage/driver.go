// Package storage provides the abstract Storage Driver interface used to
// front any byte-stream backend (local FS, S3, SFTP, WebDAV, …).
//
// The base Driver interface is intentionally tiny — additional capabilities
// are advertised through optional sub-interfaces (Writer, Mover, Copier,
// Deleter, Mkdirer, Presigner, Watcher), AList-style. The Capabilities
// helper computes the runtime feature set by checking which sub-interfaces
// the concrete driver satisfies.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by drivers when a path does not exist.
var ErrNotFound = errors.New("storage: not found")

// ErrReadOnly is returned when a write op hits a read-only mount.
var ErrReadOnly = errors.New("storage: read-only")

// ErrUnsupported is returned when an op is not supported by this driver.
var ErrUnsupported = errors.New("storage: unsupported")

// Driver is the minimum surface a backend must implement. All paths use
// POSIX-style forward slashes and are relative to the storage root.
type Driver interface {
	Init(ctx context.Context, cfg map[string]any) error
	Name() string
	List(ctx context.Context, path string) ([]Object, error)
	Stat(ctx context.Context, path string) (Object, error)
	Read(ctx context.Context, path string) (io.ReadCloser, error)
	Capabilities() Capabilities
}

// Writer adds upload support.
type Writer interface {
	Write(ctx context.Context, path string, r io.Reader, size int64) error
}

// Mover renames/moves objects.
type Mover interface {
	Move(ctx context.Context, src, dst string) error
}

// Copier copies objects (server-side if possible).
type Copier interface {
	Copy(ctx context.Context, src, dst string) error
}

// Deleter removes objects (idempotent — no error on missing).
type Deleter interface {
	Delete(ctx context.Context, path string) error
}

// Mkdirer creates directories — drivers without a directory concept may
// no-op or persist a placeholder.
type Mkdirer interface {
	Mkdir(ctx context.Context, path string) error
}

// Presigner returns URLs for browser-direct upload/download.
type Presigner interface {
	PresignUpload(ctx context.Context, path string, size int64) (PresignedUpload, error)
	PresignDownload(ctx context.Context, path string, ttl time.Duration) (string, error)
}

// MultipartUploader is implemented by drivers (S3, GCS, Azure) that support
// resumable multipart uploads via presigned URLs.
type MultipartUploader interface {
	InitMultipart(ctx context.Context, path string, totalSize int64, partCount int) (uploadID string, partURLs []string, err error)
	CompleteMultipart(ctx context.Context, path string, uploadID string, parts []PartCompletion) error
	AbortMultipart(ctx context.Context, path string, uploadID string) error
}

// PartCompletion is a finished multipart segment (stored in DB during upload).
type PartCompletion struct {
	PartNumber int
	Etag       string
}

// Watcher streams change events from the backend.
type Watcher interface {
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// ComputeCapabilities introspects a Driver to figure out which optional
// interfaces it implements. Drivers that lie in their static Capabilities()
// are still trumped by reality here.
func ComputeCapabilities(d Driver) Capabilities {
	c := d.Capabilities()
	if _, ok := d.(Writer); ok {
		c.Write = true
	}
	if _, ok := d.(Mover); ok {
		c.Move = true
	}
	if _, ok := d.(Copier); ok {
		c.Copy = true
	}
	if _, ok := d.(Deleter); ok {
		c.Delete = true
	}
	if _, ok := d.(Mkdirer); ok {
		c.Mkdir = true
	}
	if _, ok := d.(Presigner); ok {
		c.Presign = true
	}
	if _, ok := d.(Watcher); ok {
		c.Watch = true
	}
	return c
}

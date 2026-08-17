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

// RangeReader adds ranged (partial) reads, so a caller can fetch a byte
// window without pulling the whole object. Implemented only by backends
// that can genuinely start a transfer at an offset — a driver that
// silently returns the object from byte 0 would hand corrupt data to
// http.ServeContent, so leaving this unimplemented is always correct and
// callers must be able to fall back to Read.
//
// Contract:
//
//   - off is an absolute offset from the start of the object and must be
//     >= 0; a negative off is an error (suffix ranges are resolved by the
//     caller, which knows the size).
//   - length < 0 means "to the end of the object". length == 0 yields a
//     reader that is immediately at EOF, without touching the backend.
//   - off at or past EOF is NOT an error: the returned ReadCloser reports
//     io.EOF on the first Read. (S3 answers 416 for that case and the
//     driver maps it back; local/SFTP get it for free.) The caller cannot
//     always know the current size — it may have shrunk since Stat — so a
//     short read is the honest answer, not a failure.
//   - length beyond EOF is clamped by the backend: the reader ends early
//     rather than erroring.
//   - A missing path returns ErrNotFound, exactly like Read.
//
// The returned ReadCloser must be closed by the caller.
type RangeReader interface {
	ReadRange(ctx context.Context, path string, off, length int64) (io.ReadCloser, error)
}

// LimitReadCloser caps rc at n bytes while still closing the underlying
// stream — the shape every RangeReader needs when the backend can start at
// an offset but not stop at one (local seek, SFTP seek, FTP REST). n < 0
// returns rc untouched ("to the end of the object").
func LimitReadCloser(rc io.ReadCloser, n int64) io.ReadCloser {
	if n < 0 {
		return rc
	}
	return limitedReadCloser{Reader: io.LimitReader(rc, n), c: rc}
}

type limitedReadCloser struct {
	io.Reader
	c io.Closer
}

func (l limitedReadCloser) Close() error { return l.c.Close() }

// emptyReadCloser is at EOF immediately — the answer for a zero-length
// window and for an offset at or past EOF.
type emptyReadCloser struct{}

func (emptyReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (emptyReadCloser) Close() error             { return nil }

// EmptyReadCloser returns a ReadCloser that is immediately at EOF.
func EmptyReadCloser() io.ReadCloser { return emptyReadCloser{} }

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

// Toucher sets an object's modification time.
//
// This is what carries a file's own timestamp through a transfer instead of
// stamping it with the moment it arrived. rclone, s3fs and the desktop sync all
// send the source mtime alongside the bytes (`x-amz-meta-mtime` over S3); a
// server that drops it reports every synced file as modified-now, and the next
// run copies the lot again.
//
// ⚠ Optional on purpose. An object store has no settable modification time, and
// a driver that accepted one and quietly ignored it would be worse than one that
// does not implement this at all: callers can see the difference between "not
// supported" and "supported and applied", and cannot see the difference between
// "applied" and "pretended".
type Toucher interface {
	SetMtime(ctx context.Context, path string, mtime time.Time) error
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

// PartUploader is MultipartUploader plus a SERVER-SIDE part upload.
//
// MultipartUploader on its own is presign-only: it hands part URLs to the
// browser, which means filex never touches the bytes. The staged upload path
// (internal/staging, docs/UPLOADS.md) holds the bytes itself and must push
// them, so it needs a way to send one part from an io.Reader and get its ETag
// back.
//
// ⚠ The part boundaries here are the DRIVER's, not the client's. A client that
// staged 1 MiB chunks must not break an S3 backend, where every non-final part
// has to be at least MinBackendPartSize — so the commit step re-chunks.
type PartUploader interface {
	MultipartUploader
	UploadPart(ctx context.Context, path, uploadID string, partNumber int, r io.Reader, size int64) (etag string, err error)
}

// MinBackendPartSize is the smallest non-final multipart part S3 accepts.
// Anything smaller is rejected with EntityTooSmall at CompleteMultipartUpload
// time — that is, after every byte has already been sent.
const MinBackendPartSize = 5 * 1024 * 1024

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
	if _, ok := d.(RangeReader); ok {
		c.Range = true
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

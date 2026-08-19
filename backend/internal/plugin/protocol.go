// Package plugin lets a storage driver live OUTSIDE the filex binary.
//
// # Why this exists
//
// Every storage filex speaks — local disk, S3, SFTP, FTP, WebDAV, SMB — is a Go
// package compiled into the binary and registered from an init(). That is the
// right shape for the drivers filex ships, and the wrong shape for the driver
// somebody writes for their own system: they would have to fork filex, learn
// its build, and rebuild on every release. A plugin is that same driver as a
// separate program that filex starts (or connects to) and talks to over a
// small HTTP/JSON protocol. Any language that can serve HTTP can be a filex
// storage driver.
//
// # What a plugin is
//
// One of two things, and the protocol is the same for both:
//
//   - a BINARY filex launches itself: it lives under <data-dir>/plugins/<name>/,
//     is started with FILEX_PLUGIN_TOKEN and FILEX_PLUGIN_SOCKET_DIR in its
//     environment, listens on a unix socket in that directory (or on a loopback
//     TCP port), and prints ONE handshake line to stdout —
//     `FILEX-PLUGIN/1 unix:/path/to.sock` or `FILEX-PLUGIN/1 tcp:127.0.0.1:PORT`.
//     filex supervises it: health-checks it, restarts it with backoff when it
//     dies, kills it on shutdown.
//   - a REMOTE service the operator runs (a sidecar container, a daemon on
//     another host): the admin registers its base URL and a bearer token and
//     filex just connects. Nothing is spawned.
//
// # The protocol, in one screen
//
// Every request carries `Authorization: Bearer <token>`. Every error body is
// {"error": <code>, "message": <text>}. Codes filex maps back to storage
// errors: not_found, read_only, unsupported, no_instance; anything else is a
// plain failure with the message attached.
//
//	GET    /v1/describe                     → DescribeResponse (name, version, fields, capabilities)
//	POST   /v1/instances       {config}     → {"instance": id}     one per storage row; Init
//	DELETE /v1/instances/{id}                                       release it
//	GET    /v1/instances/{id}/list?path=    → {"objects": [Object]}
//	GET    /v1/instances/{id}/stat?path=    → Object
//	GET    /v1/instances/{id}/read?path=    → body stream (Range: bytes=a-b honoured when capabilities.range)
//	PUT    /v1/instances/{id}/write?path=   ← body stream (X-Filex-Size: n, or -1 when unknown)
//	POST   /v1/instances/{id}/move   {src,dst}
//	POST   /v1/instances/{id}/copy   {src,dst}
//	POST   /v1/instances/{id}/delete {path}     idempotent
//	POST   /v1/instances/{id}/mkdir  {path}
//	POST   /v1/instances/{id}/set-mtime {path,mtime}   RFC 3339
//	GET    /v1/instances/{id}/watch          → text/event-stream of Event, when capabilities.watch
//
// The driver a plugin implements is registered in filex as `plugin:<name>`,
// so a plugin can never shadow a built-in driver, and its config form on the
// Connections page renders from the fields it describes — exactly as the
// built-in drivers' forms do (storage.Descriptor). Full specification with the
// reasoning behind each choice: docs/PLUGINS.md.
package plugin

import (
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// ProtocolVersion is the only version this host speaks. A plugin whose
// describe reports another one is refused with a clear message rather than
// half-working.
const ProtocolVersion = 1

// HandshakePrefix is the first token of the single line a launched plugin
// prints to stdout, followed by a space and the address it listens on.
const HandshakePrefix = "FILEX-PLUGIN/1"

// DriverPrefix namespaces plugin drivers in the storage registry.
const DriverPrefix = "plugin:"

// Capabilities is what the plugin says it can do. It is deliberately NOT
// storage.Capabilities: presigned URLs and multipart are out of scope for the
// protocol's first version, and set_mtime is a capability here where the host
// expresses it as an optional interface (storage.Toucher).
//
// The rules the host applies (documented, enforced by the SDK's own checks):
//
//   - read is implied — a driver that cannot list/stat/read is not a driver.
//   - range: `Range` on /read is honoured. When false, the host emulates ranged
//     reads by discarding the prefix — correct bytes, just not cheap.
//   - write + delete together make the driver writable in filex's eyes; write
//     without delete is refused at describe time (filex could create files it
//     can never remove, which trash and versioning both need).
//   - move / copy: emulated by the host (copy = read→write, move = copy→delete)
//     when a writable plugin lacks them, so a plugin author only writes them
//     when the backend can do better than a byte copy.
//   - mkdir: no-op when a writable plugin lacks it (object stores have no
//     directories; the local driver's semantics do not apply).
//   - set_mtime: only offered to filex when true — a mtime that is accepted and
//     silently dropped is worse than one that is refused (storage.Toucher).
//   - watch: /watch is an SSE stream of Events.
type Capabilities struct {
	Range    bool `json:"range"`
	Write    bool `json:"write"`
	Delete   bool `json:"delete"`
	Move     bool `json:"move"`
	Copy     bool `json:"copy"`
	Mkdir    bool `json:"mkdir"`
	SetMtime bool `json:"set_mtime"`
	Watch    bool `json:"watch"`
}

// Writable reports whether the host will offer write, delete (and emulated
// move/copy/mkdir) on this plugin.
func (c Capabilities) Writable() bool { return c.Write && c.Delete }

// DescribeResponse is GET /v1/describe. Everything the host needs to
// register the driver: its identity, its config form and what it can do.
type DescribeResponse struct {
	// Protocol must equal ProtocolVersion.
	Protocol int `json:"protocol"`
	// Name is the driver's short identifier: [a-z0-9][a-z0-9_-]{0,31}. The
	// storage registry knows it as DriverPrefix+Name.
	Name string `json:"name"`
	// Version is the plugin's own version string, shown in the admin list.
	Version string `json:"version,omitempty"`
	// Label is the human name for the driver picker (English; the plugin may
	// also ship an I18nKey the host's catalogue does not know — the label is
	// the fallback surfaces already apply).
	Label string `json:"label"`
	// Fields is the config form, in exactly the shape storage.Descriptor uses
	// so the Connections page can render it without knowing the driver.
	Fields       []storage.Field `json:"fields"`
	Capabilities Capabilities    `json:"capabilities"`
}

// InstanceRequest is POST /v1/instances: the config the admin saved for one
// storage row, keyed by the field keys the plugin described.
type InstanceRequest struct {
	Config map[string]any `json:"config"`
}

// InstanceResponse names the instance every later call addresses.
type InstanceResponse struct {
	Instance string `json:"instance"`
}

// ListResponse is GET /v1/instances/{id}/list.
type ListResponse struct {
	Objects []Object `json:"objects"`
}

// Object is one entry of a listing or a stat. Same fields as storage.Object,
// spelled out here so the wire format is owned by the protocol and does not
// move when the internal type does.
type Object struct {
	Path     string            `json:"path"`
	Name     string            `json:"name"`
	Size     int64             `json:"size"`
	Kind     string            `json:"kind"` // "file" | "dir" | "symlink"
	Mime     string            `json:"mime,omitempty"`
	Etag     string            `json:"etag,omitempty"`
	Mtime    time.Time         `json:"mtime,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (o Object) toStorage() storage.Object {
	kind := storage.ObjectKind(o.Kind)
	switch kind {
	case storage.KindFile, storage.KindDirectory, storage.KindSymlink:
	default:
		kind = storage.KindFile
	}
	return storage.Object{
		Path: o.Path, Name: o.Name, Size: o.Size, Kind: kind,
		Mime: o.Mime, Etag: o.Etag, Mtime: o.Mtime, Metadata: o.Metadata,
	}
}

// PathRequest is the body of delete / mkdir.
type PathRequest struct {
	Path string `json:"path"`
}

// MoveRequest is the body of move / copy.
type MoveRequest struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// MtimeRequest is the body of set-mtime.
type MtimeRequest struct {
	Path  string    `json:"path"`
	Mtime time.Time `json:"mtime"`
}

// Event is one SSE `data:` payload of /watch.
type Event struct {
	Op   string `json:"op"` // create | modify | delete | move
	Path string `json:"path"`
	From string `json:"from,omitempty"`
}

// ErrorResponse is every non-2xx body.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// Error codes the host understands. Anything else is reported verbatim.
const (
	ErrCodeNotFound    = "not_found"
	ErrCodeReadOnly    = "read_only"
	ErrCodeUnsupported = "unsupported"
	// ErrCodeNoInstance says the instance id is unknown — the plugin was
	// restarted and lost its state. The host re-creates the instance from the
	// saved config and retries once, so a plugin restart is invisible.
	ErrCodeNoInstance = "no_instance"
	ErrCodeInvalid    = "invalid"
)

// SizeHeader carries the write size on PUT /write, because a streamed body
// may be chunked and Content-Length absent. -1 means unknown.
const SizeHeader = "X-Filex-Size"

// Package pluginsdk is the Go half of writing a filex storage plugin.
//
// A plugin is a program. filex starts it (or connects to it), asks what it
// can do, and then speaks a small HTTP/JSON protocol to it — so a plugin can
// be written in any language. This package exists so that a plugin written in
// Go is a handful of methods rather than a web server: implement Backend,
// call Serve, and the handshake, routing, streaming, error codes and instance
// bookkeeping are done for you.
//
// The whole of a working plugin:
//
//	package main
//
//	import (
//		"context"
//		"io"
//
//		"github.com/brf-tech/filex/backend/pkg/pluginsdk"
//	)
//
//	type myFS struct{ root string }
//
//	func (m *myFS) List(ctx context.Context, path string) ([]pluginsdk.Object, error) { … }
//	func (m *myFS) Stat(ctx context.Context, path string) (pluginsdk.Object, error)   { … }
//	func (m *myFS) Read(ctx context.Context, path string) (io.ReadCloser, error)      { … }
//
//	func main() {
//		pluginsdk.Serve(pluginsdk.Plugin[*myFS]{
//			Name:  "myfs",
//			Label: "My storage",
//			Fields: []pluginsdk.Field{
//				{Key: "root", Type: pluginsdk.FieldString, Label: "Root", Required: true, Root: true},
//			},
//			Open: func(ctx context.Context, cfg map[string]any) (*myFS, error) {
//				return &myFS{root: pluginsdk.String(cfg, "root")}, nil
//			},
//		})
//	}
//
// Write support is added by ALSO implementing Writer and Deleter on the same
// value; the SDK reports the capability only when the interface is there, so
// the capability and the code cannot disagree. The same applies to Mover,
// Copier, Mkdirer, RangeReader, Toucher and Watcher.
//
// ⚠ Run it under filex, not by hand: the process reads FILEX_PLUGIN_TOKEN and
// FILEX_PLUGIN_SOCKET_DIR from its environment and prints its handshake line
// on stdout. `FILEX_PLUGIN_LISTEN=127.0.0.1:PORT` runs it standalone for
// development (then register it in filex as a REMOTE plugin with that token).
package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Protocol is the version this SDK speaks.
const Protocol = 1

// ── the types a plugin author touches ───────────────────────────────────────

// FieldType picks the widget filex renders for a config field.
type FieldType string

// Field types.
const (
	FieldString   FieldType = "string"
	FieldInt      FieldType = "int"
	FieldBool     FieldType = "bool"
	FieldPassword FieldType = "password"
	FieldSelect   FieldType = "select"
)

// SelectOption is one choice of a FieldSelect field.
type SelectOption struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	I18nKey string `json:"i18n_key,omitempty"`
}

// Field describes one key of the config form filex shows when somebody adds a
// storage on this plugin. Same shape filex's built-in drivers use.
type Field struct {
	Key         string         `json:"key"`
	Type        FieldType      `json:"type"`
	Label       string         `json:"label"`
	Help        string         `json:"help,omitempty"`
	I18nKey     string         `json:"i18n_key,omitempty"`
	HelpI18nKey string         `json:"help_i18n_key,omitempty"`
	Required    bool           `json:"required"`
	Secret      bool           `json:"secret"`
	Default     any            `json:"default,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
	Min         *int           `json:"min,omitempty"`
	Max         *int           `json:"max,omitempty"`
	Monospace   bool           `json:"monospace,omitempty"`
	Multiline   bool           `json:"multiline,omitempty"`
	Advanced    bool           `json:"advanced,omitempty"`
	// Root marks the field that scopes this storage inside the backend (a
	// path, a prefix, a bucket). filex refuses to mount a backend's root, so
	// a plugin with no Root field can never be scoped — declare one.
	Root bool `json:"root,omitempty"`
}

// ObjectKind values.
const (
	KindFile = "file"
	KindDir  = "dir"
	KindLink = "symlink"
)

// Object is one entry of a listing or a stat.
type Object struct {
	Path     string            `json:"path"`
	Name     string            `json:"name"`
	Size     int64             `json:"size"`
	Kind     string            `json:"kind"`
	Mime     string            `json:"mime,omitempty"`
	Etag     string            `json:"etag,omitempty"`
	Mtime    time.Time         `json:"mtime,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Event is one change notification (Watcher).
type Event struct {
	Op   string `json:"op"` // create | modify | delete | move
	Path string `json:"path"`
	From string `json:"from,omitempty"`
}

// Errors a Backend returns. Anything else becomes a plain failure with its
// message forwarded to filex (and into the admin's error line).
var (
	// ErrNotFound — the path does not exist.
	ErrNotFound = errors.New("not found")
	// ErrReadOnly — this backend, or this path, refuses writes.
	ErrReadOnly = errors.New("read only")
	// ErrUnsupported — the operation is not available here.
	ErrUnsupported = errors.New("unsupported")
)

// Backend is one opened storage: what Open returned for one config. The three
// methods here are the minimum; everything else is an optional interface.
type Backend interface {
	List(ctx context.Context, path string) ([]Object, error)
	Stat(ctx context.Context, path string) (Object, error)
	Read(ctx context.Context, path string) (io.ReadCloser, error)
}

// RangeReader lets filex fetch a byte window without pulling the whole
// object. length < 0 means "to the end". An offset at or past EOF is NOT an
// error — return a reader that is immediately at EOF.
//
// ⚠ Implement this only if the backend can genuinely start a transfer at an
// offset. filex emulates ranges for plugins that do not, which is slower but
// always correct; a driver that returned the object from byte 0 while
// claiming a range would hand corrupt data to a video player.
type RangeReader interface {
	ReadRange(ctx context.Context, path string, off, length int64) (io.ReadCloser, error)
}

// Writer accepts uploads. size is -1 when the length is not known up front.
type Writer interface {
	Write(ctx context.Context, path string, r io.Reader, size int64) error
}

// Deleter removes an object. Deleting something that is not there is not an
// error. ⚠ filex requires Writer and Deleter TOGETHER: it will not offer a
// backend that can create files it cannot remove (trash and versioning both
// need to).
type Deleter interface {
	Delete(ctx context.Context, path string) error
}

// Mover renames server-side. Optional: filex falls back to copy+delete.
type Mover interface {
	Move(ctx context.Context, src, dst string) error
}

// Copier copies server-side. Optional: filex falls back to read+write.
type Copier interface {
	Copy(ctx context.Context, src, dst string) error
}

// Mkdirer creates a directory. Optional: object stores have none, and filex
// treats a missing Mkdir as a no-op rather than an error.
type Mkdirer interface {
	Mkdir(ctx context.Context, path string) error
}

// Toucher sets an object's modification time — what carries a file's own
// timestamp through a sync instead of stamping it "now".
//
// ⚠ Implement it only if the backend really stores it. filex can tell "not
// supported" from "supported and applied"; it cannot tell "applied" from
// "pretended", and a plugin that accepts an mtime and drops it makes every
// sync run copy everything again.
type Toucher interface {
	SetMtime(ctx context.Context, path string, mtime time.Time) error
}

// Watcher streams change events until ctx is cancelled. Close the channel
// when the stream ends.
//
// ⚠⚠ Nothing in filex subscribes to this yet. The protocol carries the
// stream and this SDK serves it, but the only event-driven sync mode today is
// fsnotify, which works on the local driver alone; no built-in driver
// implements storage.Watcher either. Implementing it is cheap and forward
// compatible — just do not expect it to change how often filex notices your
// changes until a watch-driven sync mode exists.
type Watcher interface {
	Watch(ctx context.Context) (<-chan Event, error)
}

// Presigner hands out URLs the CLIENT uses directly, so bytes never pass
// through filex.
//
// ⚠ Only implement this when the URL is reachable from where the client is
// — a browser, not the filex host. A plugin that returns a loopback URL
// passes conformance (which only checks that a URL comes back and parses) and
// then fails in every browser.
type Presigner interface {
	PresignUpload(ctx context.Context, path string, size int64) (Presigned, error)
	PresignDownload(ctx context.Context, path string, ttl time.Duration) (Presigned, error)
}

// Presigned is one signed URL.
type Presigned struct {
	URL       string            `json:"url"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// Multipart is a resumable upload in parts. Implementing it requires Writer
// and Deleter too — filex refuses the combination otherwise, because a
// resumable upload is still an upload.
//
// PartURLs may be empty: filex then pushes each part itself through
// UploadPart, which is what the staged-upload path does (it holds the bytes,
// so it cannot hand a browser a URL).
type Multipart interface {
	InitMultipart(ctx context.Context, path string, totalSize int64, partCount int) (uploadID string, partURLs []string, err error)
	UploadPart(ctx context.Context, path, uploadID string, partNumber int, r io.Reader, size int64) (etag string, err error)
	CompleteMultipart(ctx context.Context, path, uploadID string, parts []Part) error
	AbortMultipart(ctx context.Context, path, uploadID string) error
}

// Part is one finished piece of a multipart upload.
type Part struct {
	PartNumber int    `json:"part_number"`
	Etag       string `json:"etag"`
}

// Closer is called when filex releases an instance (storage edited, plugin
// stopping). Optional.
type Closer interface {
	Close() error
}

// Plugin is what Serve needs: identity, the config form, and how to open a
// backend for one config.
//
// ⚠ The type parameter is not ceremony — it is how the capabilities are
// derived. The SDK reports what T's METHOD SET implements (Writer, Toucher,
// Watcher …), computed from the type itself, so a plugin can neither claim a
// capability it did not write nor hide one it did. Nothing is opened to find
// out, which also means describing a plugin never touches the backend.
type Plugin[T Backend] struct {
	Name    string // [a-z0-9][a-z0-9_-]{0,31}; filex registers it as plugin:<name>
	Version string
	Label   string // human name in the driver picker
	Fields  []Field
	Open    func(ctx context.Context, config map[string]any) (T, error)
	// SelfTest opens a THROWAWAY backend for filex's conformance probes.
	//
	// ⚠⚠ Provide it. filex probes every capability a plugin declares before
	// it will let anybody build a storage on it — because a plugin that claims
	// write and cannot write produces failures the user reads as filex being
	// broken. Without a self-test area the plugin is registered UNVERIFIED and
	// the probes run against somebody's first real storage instead.
	//
	// Return a backend over scratch space you are happy to have written to and
	// deleted (a temp directory, a throwaway prefix, an in-memory area). filex
	// releases it with DELETE /v1/instances/{id} when it is done.
	SelfTest func(ctx context.Context) (T, error)
}

// ── config helpers, so a plugin does not re-implement them ─────────────────

// String reads a string config value (empty when absent or another type).
func String(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return s
}

// Int reads an integer config value. JSON numbers arrive as float64, and a
// form may send "8080" as a string — both are accepted, so a plugin does not
// have to care which surface filled the form.
func Int(cfg map[string]any, key string, def int) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// Bool reads a boolean config value, accepting "true"/"1" as well.
func Bool(cfg map[string]any, key string, def bool) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	}
	return def
}

// ── serving ─────────────────────────────────────────────────────────────────

// Serve runs the plugin until the process is asked to stop. It never
// returns normally; a fatal error exits non-zero with a message on stderr,
// which filex relays into its own log.
func Serve[T Backend](p Plugin[T]) {
	if err := Run(context.Background(), p); err != nil {
		fmt.Fprintln(os.Stderr, "filex plugin:", err)
		os.Exit(1)
	}
}

// Run is Serve with a context you own — for tests and for embedding a plugin
// in a larger program.
func Run[T Backend](ctx context.Context, p Plugin[T]) error {
	if p.Name == "" || p.Label == "" || p.Open == nil {
		return errors.New("plugin needs a Name, a Label and an Open function")
	}
	token := os.Getenv("FILEX_PLUGIN_TOKEN")
	if token == "" {
		return errors.New("FILEX_PLUGIN_TOKEN is not set — a plugin is started by filex, not by hand (set FILEX_PLUGIN_LISTEN and a token to run it standalone)")
	}
	s := &server{
		name: p.Name, version: p.Version, label: p.Label, fields: p.Fields,
		open: func(ctx context.Context, cfg map[string]any) (Backend, error) {
			b, err := p.Open(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return b, nil
		},
		caps:     capsOfType[T](),
		token:    token,
		backends: map[string]Backend{},
	}
	if p.SelfTest != nil {
		s.selfTest = func(ctx context.Context) (Backend, error) {
			b, err := p.SelfTest(ctx)
			if err != nil {
				return nil, err
			}
			return b, nil
		}
	}

	ln, addr, err := listen()
	if err != nil {
		return err
	}
	defer ln.Close()

	// The handshake: ONE line, on stdout, before anything else is printed.
	fmt.Printf("FILEX-PLUGIN/%d %s\n", Protocol, addr)
	_ = os.Stdout.Sync()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := &http.Server{Handler: s, ReadHeaderTimeout: 30 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
		s.closeAll()
		return nil
	}
}

// listen picks the transport filex asked for: a unix socket in the directory
// it provided (the default, and the only one with no port to collide), or a
// loopback TCP port when FILEX_PLUGIN_LISTEN is set — which is also how a
// plugin runs standalone during development.
func listen() (net.Listener, string, error) {
	if v := os.Getenv("FILEX_PLUGIN_LISTEN"); v != "" {
		ln, err := net.Listen("tcp", v)
		if err != nil {
			return nil, "", err
		}
		return ln, "tcp:" + ln.Addr().String(), nil
	}
	dir := os.Getenv("FILEX_PLUGIN_SOCKET_DIR")
	// Windows has no unix sockets in the form filex dials, so a plugin there
	// always takes a loopback port.
	if dir == "" || runtime.GOOS == "windows" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", err
		}
		return ln, "tcp:" + ln.Addr().String(), nil
	}
	sock := filepath.Join(dir, fmt.Sprintf("p%d.sock", os.Getpid()))
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, "", err
	}
	// The socket is the door to every storage credential filex hands this
	// plugin: nobody but the filex user gets to knock.
	_ = os.Chmod(sock, 0o600)
	return ln, "unix:" + sock, nil
}

type server struct {
	name    string
	version string
	label   string
	fields  []Field
	open    func(ctx context.Context, cfg map[string]any) (Backend, error)
	// selfTest is nil when the plugin provides no scratch area.
	selfTest func(ctx context.Context) (Backend, error)
	caps     map[string]bool
	token    string

	mu       sync.Mutex
	backends map[string]Backend
	next     int
}

func (s *server) closeAll() {
	s.mu.Lock()
	list := make([]Backend, 0, len(s.backends))
	for _, b := range s.backends {
		list = append(list, b)
	}
	s.backends = map[string]Backend{}
	s.mu.Unlock()
	for _, b := range list {
		if c, ok := b.(Closer); ok {
			_ = c.Close()
		}
	}
}

func (s *server) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}

// failErr maps a Backend error onto the protocol's codes.
func (s *server) failErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, os.ErrNotExist):
		s.fail(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrReadOnly):
		s.fail(w, http.StatusForbidden, "read_only", err.Error())
	case errors.Is(err, ErrUnsupported):
		s.fail(w, http.StatusBadRequest, "unsupported", err.Error())
	default:
		s.fail(w, http.StatusInternalServerError, "error", err.Error())
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		s.fail(w, http.StatusUnauthorized, "unauthorized", "bad or missing token")
		return
	}
	switch {
	case r.URL.Path == "/v1/describe" && r.Method == http.MethodGet:
		s.describe(w)
	case r.URL.Path == "/v1/instances" && r.Method == http.MethodPost:
		s.createInstance(w, r)
	case r.URL.Path == "/v1/selftest" && r.Method == http.MethodPost:
		s.handleSelfTest(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/instances/"):
		s.instance(w, r)
	default:
		s.fail(w, http.StatusNotFound, "not_found", "no such route")
	}
}

// describe answers with the capabilities computed from the backend TYPE — no
// backend is opened, and no plugin author has to keep a list in step with
// their own methods.
func (s *server) describe(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"protocol":     Protocol,
		"name":         s.name,
		"version":      s.version,
		"label":        s.label,
		"fields":       s.fields,
		"capabilities": s.caps,
	})
}

// capsOfType reads the method set of T.
func capsOfType[T Backend]() map[string]bool {
	t := reflect.TypeOf((*T)(nil)).Elem()
	has := func(iface any) bool {
		return t.Implements(reflect.TypeOf(iface).Elem())
	}
	write := has((*Writer)(nil))
	del := has((*Deleter)(nil))
	// ⚠ write and delete travel together: filex refuses a driver that can
	// create files it cannot remove (trash and versioning both need to), and
	// reporting the pair honestly here turns that into a plain "this plugin
	// is read-only" instead of a registration refused at start-up.
	rw := write && del
	return map[string]bool{
		"range":     has((*RangeReader)(nil)),
		"write":     rw,
		"delete":    rw,
		"move":      has((*Mover)(nil)),
		"copy":      has((*Copier)(nil)),
		"mkdir":     has((*Mkdirer)(nil)),
		"set_mtime": has((*Toucher)(nil)),
		"watch":     has((*Watcher)(nil)),
		"presign":   has((*Presigner)(nil)),
		// Multipart is only offered on a writable backend, for the same
		// reason filex refuses the pair split.
		"multipart": rw && has((*Multipart)(nil)),
	}
}

func (s *server) createInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Config map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	b, err := s.open(r.Context(), req.Config)
	if err != nil {
		s.failErr(w, err)
		return
	}
	if b == nil {
		s.fail(w, http.StatusInternalServerError, "error", "Open returned no backend and no error")
		return
	}
	s.mu.Lock()
	s.next++
	id := "i" + strconv.Itoa(s.next)
	s.backends[id] = b
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"instance": id})
}

func (s *server) instance(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	op := ""
	if len(parts) > 1 {
		op = parts[1]
	}
	s.mu.Lock()
	b, ok := s.backends[id]
	s.mu.Unlock()

	if op == "" && r.Method == http.MethodDelete {
		if ok {
			s.mu.Lock()
			delete(s.backends, id)
			s.mu.Unlock()
			if c, isC := b.(Closer); isC {
				_ = c.Close()
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !ok {
		// filex re-creates the instance and retries once, so this is what a
		// plugin restart looks like from the other side — not an error.
		s.fail(w, http.StatusConflict, "no_instance", "unknown instance "+id)
		return
	}

	ctx := r.Context()
	q := r.URL.Query()
	switch op {
	case "list":
		objs, err := b.List(ctx, q.Get("path"))
		if err != nil {
			s.failErr(w, err)
			return
		}
		if objs == nil {
			objs = []Object{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"objects": objs})
	case "stat":
		o, err := b.Stat(ctx, q.Get("path"))
		if err != nil {
			s.failErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(o)
	case "read":
		s.read(w, r, b, q.Get("path"))
	case "write":
		wr, okW := b.(Writer)
		if !okW {
			s.fail(w, http.StatusBadRequest, "unsupported", "this plugin is read-only")
			return
		}
		size := int64(-1)
		if v := r.Header.Get("X-Filex-Size"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				size = n
			}
		}
		if err := wr.Write(ctx, q.Get("path"), r.Body, size); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "delete":
		d, okD := b.(Deleter)
		if !okD {
			s.fail(w, http.StatusBadRequest, "unsupported", "this plugin is read-only")
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := d.Delete(ctx, req.Path); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "mkdir":
		m, okM := b.(Mkdirer)
		if !okM {
			// Not an error: filex treats a plugin without directories the way
			// it treats an object store.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := m.Mkdir(ctx, req.Path); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "move", "copy":
		var req struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var err error
		if op == "move" {
			m, okM := b.(Mover)
			if !okM {
				s.fail(w, http.StatusBadRequest, "unsupported", "move")
				return
			}
			err = m.Move(ctx, req.Src, req.Dst)
		} else {
			c, okC := b.(Copier)
			if !okC {
				s.fail(w, http.StatusBadRequest, "unsupported", "copy")
				return
			}
			err = c.Copy(ctx, req.Src, req.Dst)
		}
		if err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "set-mtime":
		t, okT := b.(Toucher)
		if !okT {
			s.fail(w, http.StatusBadRequest, "unsupported", "set_mtime")
			return
		}
		var req struct {
			Path  string    `json:"path"`
			Mtime time.Time `json:"mtime"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := t.SetMtime(ctx, req.Path, req.Mtime); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "watch":
		s.watch(w, r, b)
	case "presign-upload", "presign-download":
		s.presign(w, r, b, op)
	case "multipart/init", "multipart/complete", "multipart/abort":
		s.multipart(w, r, b, op)
	case "multipart/part":
		s.multipartPart(w, r, b, q)
	default:
		s.fail(w, http.StatusNotFound, "not_found", op)
	}
}

// handleSelfTest opens a throwaway backend for filex's conformance probes.
func (s *server) handleSelfTest(w http.ResponseWriter, r *http.Request) {
	if s.selfTest == nil {
		s.fail(w, http.StatusNotFound, "unsupported", "this plugin offers no selftest area")
		return
	}
	b, err := s.selfTest(r.Context())
	if err != nil {
		s.failErr(w, err)
		return
	}
	if b == nil {
		s.fail(w, http.StatusInternalServerError, "error", "SelfTest returned no backend and no error")
		return
	}
	s.mu.Lock()
	s.next++
	id := "i" + strconv.Itoa(s.next)
	s.backends[id] = b
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"instance": id})
}

func (s *server) presign(w http.ResponseWriter, r *http.Request, b Backend, op string) {
	p, ok := b.(Presigner)
	if !ok {
		s.fail(w, http.StatusBadRequest, "unsupported", "presign")
		return
	}
	var req struct {
		Path       string `json:"path"`
		Size       int64  `json:"size"`
		TTLSeconds int64  `json:"ttl_s"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var (
		out Presigned
		err error
	)
	if op == "presign-upload" {
		out, err = p.PresignUpload(r.Context(), req.Path, req.Size)
	} else {
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		out, err = p.PresignDownload(r.Context(), req.Path, ttl)
	}
	if err != nil {
		s.failErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *server) multipart(w http.ResponseWriter, r *http.Request, b Backend, op string) {
	m, ok := b.(Multipart)
	if !ok {
		s.fail(w, http.StatusBadRequest, "unsupported", "multipart")
		return
	}
	var req struct {
		Path      string `json:"path"`
		UploadID  string `json:"upload_id"`
		TotalSize int64  `json:"total_size"`
		PartCount int    `json:"part_count"`
		Parts     []Part `json:"parts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	switch op {
	case "multipart/init":
		id, urls, err := m.InitMultipart(r.Context(), req.Path, req.TotalSize, req.PartCount)
		if err != nil {
			s.failErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": id, "part_urls": urls})
	case "multipart/complete":
		if err := m.CompleteMultipart(r.Context(), req.Path, req.UploadID, req.Parts); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default: // abort
		if err := m.AbortMultipart(r.Context(), req.Path, req.UploadID); err != nil {
			s.failErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) multipartPart(w http.ResponseWriter, r *http.Request, b Backend, q url.Values) {
	m, ok := b.(Multipart)
	if !ok {
		s.fail(w, http.StatusBadRequest, "unsupported", "multipart")
		return
	}
	part, _ := strconv.Atoi(q.Get("part"))
	size := int64(-1)
	if v := r.Header.Get("X-Filex-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			size = n
		}
	}
	etag, err := m.UploadPart(r.Context(), q.Get("path"), q.Get("upload_id"), part, r.Body, size)
	if err != nil {
		s.failErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"etag": etag})
}

func (s *server) read(w http.ResponseWriter, r *http.Request, b Backend, path string) {
	off, length, ranged := parseRange(r.Header.Get("Range"))
	if ranged {
		if rr, ok := b.(RangeReader); ok {
			rc, err := rr.ReadRange(r.Context(), path, off, length)
			if err != nil {
				s.failErr(w, err)
				return
			}
			defer rc.Close()
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.Copy(w, rc)
			return
		}
	}
	rc, err := b.Read(r.Context(), path)
	if err != nil {
		s.failErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, rc)
}

// parseRange understands the two forms filex sends: bytes=N- and bytes=N-M.
func parseRange(h string) (off, length int64, ok bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, -1, false
	}
	spec := strings.TrimPrefix(h, "bytes=")
	bits := strings.SplitN(spec, "-", 2)
	start, err := strconv.ParseInt(strings.TrimSpace(bits[0]), 10, 64)
	if err != nil || start < 0 {
		return 0, -1, false
	}
	if len(bits) < 2 || strings.TrimSpace(bits[1]) == "" {
		return start, -1, true
	}
	end, err := strconv.ParseInt(strings.TrimSpace(bits[1]), 10, 64)
	if err != nil || end < start {
		return start, -1, true
	}
	return start, end - start + 1, true
}

func (s *server) watch(w http.ResponseWriter, r *http.Request, b Backend) {
	wt, ok := b.(Watcher)
	if !ok {
		s.fail(w, http.StatusBadRequest, "unsupported", "watch")
		return
	}
	ch, err := wt.Watch(r.Context())
	if err != nil {
		s.failErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	// ⚠ Flush the headers immediately. Without this the response header sits
	// in the server's buffer until the first event, and filex waits for a
	// header that was written but never sent — a watch that appears to hang
	// on an idle backend.
	if flusher != nil {
		flusher.Flush()
	}
	for {
		select {
		case ev, more := <-ch:
			if !more {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

// Package testplugin runs a real filex storage plugin — the HTTP/JSON
// protocol of internal/plugin, served over httptest — so any package that
// consumes a storage.Driver can be exercised against a PLUGIN-backed storage
// without spawning a second process.
//
// # Why this package exists
//
// The plugin adapter was proven through the HTTP file manager only. Every
// other surface (sync, WebDAV, S3, SFTP, FTP, NFS, quota, thumbnails) reaches
// storage through the same storage.Driver interface, but each one leans on a
// different corner of it: sync stats and lists, WebDAV wants a directory at
// the root, SFTP writes with an unknown size, NFS asks for the same path
// twice. A plugin driver is NOT a local driver with a longer code path — it
// answers over a wire, its errors arrive as JSON codes, its instance can
// vanish under a restart. Those differences only show up when a surface is
// driven end to end, and a shell script that proves it once on a laptop
// proves nothing on the next release. Hence: a helper the tests share.
//
// The fake here is deliberately written the way a third-party plugin author
// would write one — flat map, no filex internals — because if the protocol is
// awkward to implement it shows up here first.
package testplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// FullCaps is everything the protocol offers. Tests that care about a
// specific capability being ABSENT should start here and switch one off, so
// the difference under test is the only difference.
func FullCaps() plugin.Capabilities {
	return plugin.Capabilities{
		Range: true, Write: true, Delete: true, Move: true,
		Copy: true, Mkdir: true, SetMtime: true, Watch: true,
	}
}

// SeedTime is the mtime every seeded file carries. Fixed, because a sync test
// that compares "changed since last pass" against time.Now() passes for the
// wrong reason on a fast machine.
var SeedTime = time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)

type file struct {
	data  []byte
	mtime time.Time
	dir   bool
}

func (f *file) size() int64 {
	if f == nil || f.dir {
		return 0
	}
	return int64(len(f.data))
}

// Plugin is a running fake plugin: an httptest server speaking the protocol
// over an in-memory tree.
type Plugin struct {
	mu   sync.Mutex
	srv  *httptest.Server
	name string
	caps plugin.Capabilities

	// files is the plugin's "disk": shared by every instance, exactly like a
	// real backend. Forgetting instances (a restart) must never wipe it —
	// otherwise the adapter's retry looks like it worked when it had in fact
	// lost everything.
	files     map[string]*file
	instances map[string]map[string]any
	next      int

	forgetAll  bool
	down       bool
	noInstance int
	events     chan plugin.Event

	reads  int
	ranges int
	writes int
	lists  int
}

// Option tunes a plugin before it starts.
type Option func(*Plugin)

// WithName sets the driver name; the registry sees "plugin:<name>".
func WithName(name string) Option { return func(p *Plugin) { p.name = name } }

// WithCaps replaces the declared capabilities.
func WithCaps(c plugin.Capabilities) Option { return func(p *Plugin) { p.caps = c } }

// Start brings a plugin up and shuts it down with the test.
func Start(t *testing.T, opts ...Option) *Plugin {
	t.Helper()
	p := &Plugin{
		name:      "fake",
		caps:      FullCaps(),
		files:     map[string]*file{},
		instances: map[string]map[string]any{},
		events:    make(chan plugin.Event, 8),
	}
	for _, o := range opts {
		o(p)
	}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(p.srv.Close)
	return p
}

// URL is the plugin's base address.
func (p *Plugin) URL() string { return p.srv.URL }

// DriverName is what the storage registry knows this plugin as.
func (p *Plugin) DriverName() string { return plugin.DriverPrefix + p.name }

const token = "test-token"

type handle struct {
	c    *plugin.Client
	name string
}

func (h handle) Client() (*plugin.Client, error) { return h.c, nil }
func (h handle) DriverName() string              { return plugin.DriverPrefix + h.name }

// Driver returns a storage.Driver already Init'ed against the plugin — the
// exact value the manager hands the rest of filex.
func (p *Plugin) Driver(t *testing.T) storage.Driver {
	t.Helper()
	return p.DriverWithConfig(t, map[string]any{"root": "/data"})
}

// DriverWithConfig is Driver with the config the admin would have saved.
func (p *Plugin) DriverWithConfig(t *testing.T, cfg map[string]any) storage.Driver {
	t.Helper()
	addr, err := plugin.ParseAddress(p.srv.URL)
	if err != nil {
		t.Fatalf("testplugin: parse address: %v", err)
	}
	cl := plugin.NewClient(addr, token)
	t.Cleanup(cl.Close)
	d := plugin.NewDriver(handle{c: cl, name: p.name}, p.caps)
	if err := d.Init(t.Context(), cfg); err != nil {
		t.Fatalf("testplugin: init driver: %v", err)
	}
	return d
}

// Register puts the plugin's factory in the global storage registry under
// "plugin:<name>" and removes it again after the test, so surfaces that look
// a driver up by name (storage.Get) meet a plugin exactly as they would in
// production. Returns the driver name to put in the storage row.
func (p *Plugin) Register(t *testing.T) string {
	t.Helper()
	addr, err := plugin.ParseAddress(p.srv.URL)
	if err != nil {
		t.Fatalf("testplugin: parse address: %v", err)
	}
	name := p.DriverName()
	// Unregister first: a previous test in the same binary may have left the
	// name behind if it failed hard, and Register panics on a duplicate.
	storage.Unregister(name)
	storage.Register(name, func() storage.Driver {
		cl := plugin.NewClient(addr, token)
		return plugin.NewDriver(handle{c: cl, name: p.name}, p.caps)
	})
	t.Cleanup(func() { storage.Unregister(name) })
	return name
}

// ── the plugin's own tree, for arrange/assert ───────────────────────────────

// Seed writes a file directly into the plugin's tree, bypassing the driver —
// the "somebody else put this in the bucket" case every sync exists for.
func (p *Plugin) Seed(pth, data string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[clean(pth)] = &file{data: []byte(data), mtime: SeedTime}
}

// SeedBytes is Seed for content that is not a string (an image, say).
func (p *Plugin) SeedBytes(pth string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[clean(pth)] = &file{data: data, mtime: SeedTime}
}

// SeedDir creates an explicit empty directory.
func (p *Plugin) SeedDir(pth string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[clean(pth)] = &file{dir: true, mtime: SeedTime}
}

// Delete removes a path from the plugin's tree directly, bypassing the driver
// — the other half of "somebody else changed the bucket", and the only way to
// test that a sync notices a disappearance.
func (p *Plugin) Delete(pth string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, clean(pth))
}

// Data returns what the plugin actually holds at pth. Assertions go through
// this rather than through the driver, so a surface that silently swallowed a
// write cannot pass by reading back its own cache.
func (p *Plugin) Data(pth string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.files[clean(pth)]
	if !ok || f.dir {
		return nil, ok && !f.dir
	}
	return append([]byte(nil), f.data...), true
}

// Exists reports whether anything lives at pth.
func (p *Plugin) Exists(pth string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.files[clean(pth)]
	return ok
}

// Paths lists everything the plugin holds, sorted — useful in a failure
// message, where "not found" is far less informative than the actual tree.
func (p *Plugin) Paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.files))
	for k := range p.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Counts reports how many list/read/range/write calls reached the plugin. A
// surface that "works" by never touching the backend is a surface that is
// serving something else.
func (p *Plugin) Counts() (lists, reads, ranges, writes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lists, p.reads, p.ranges, p.writes
}

// Restart makes the plugin forget its instance ids exactly once, which is
// what a crashed-and-respawned plugin looks like from the host. The files
// survive, as they would on a real backend.
func (p *Plugin) Restart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forgetAll = true
}

// Down makes every instance call answer no_instance for good, which is what a
// plugin whose process is gone looks like from the host: the adapter can keep
// re-creating instances and keep being told they do not exist. Surfaces must
// report that as a failure — a "gone" plugin that renders as an empty folder
// or a 404 tells the user their files were deleted.
func (p *Plugin) Down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = true
}

// Up ends what Down started.
func (p *Plugin) Up() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = false
}

// NoInstanceCount is how many times the plugin has answered no_instance. A
// restart test that never reaches a non-zero count proved nothing: the retry
// path it means to exercise was never entered.
func (p *Plugin) NoInstanceCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.noInstance
}

// Emit pushes a watch event to whoever is subscribed.
func (p *Plugin) Emit(ev plugin.Event) { p.events <- ev }

func clean(s string) string { return strings.Trim(strings.TrimSpace(s), "/") }

// ── protocol ────────────────────────────────────────────────────────────────

func (p *Plugin) writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(plugin.ErrorResponse{Error: code, Message: msg})
}

func (p *Plugin) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+token {
		p.writeErr(w, http.StatusUnauthorized, "unauthorized", "bad token")
		return
	}
	switch {
	case r.URL.Path == "/v1/describe":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.DescribeResponse{
			Protocol: plugin.ProtocolVersion,
			Name:     p.name,
			Version:  "1.2.3",
			Label:    "Fake storage",
			Fields: []storage.Field{
				{Key: "root", Type: storage.FieldString, Label: "Root", Required: true, Root: true},
			},
			Capabilities: p.caps,
		})
	case r.URL.Path == "/v1/instances" && r.Method == http.MethodPost:
		var req plugin.InstanceRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p.mu.Lock()
		p.next++
		id := "i" + strconv.Itoa(p.next)
		p.instances[id] = req.Config
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.InstanceResponse{Instance: id})
	case strings.HasPrefix(r.URL.Path, "/v1/instances/"):
		p.instanceOp(w, r)
	default:
		p.writeErr(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (p *Plugin) instanceOp(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	op := ""
	if len(parts) > 1 {
		op = parts[1]
	}
	p.mu.Lock()
	if p.forgetAll || p.down {
		p.forgetAll = false
		p.instances = map[string]map[string]any{}
	}
	_, known := p.instances[id]
	if !known {
		p.noInstance++
	}
	p.mu.Unlock()

	if r.Method == http.MethodDelete && op == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !known {
		p.writeErr(w, http.StatusConflict, plugin.ErrCodeNoInstance, "unknown instance "+id)
		return
	}

	q := r.URL.Query()
	switch op {
	case "list":
		p.opList(w, clean(q.Get("path")))
	case "stat":
		p.opStat(w, clean(q.Get("path")))
	case "read":
		p.opRead(w, r, clean(q.Get("path")))
	case "write":
		p.opWrite(w, r, clean(q.Get("path")))
	case "delete":
		var req plugin.PathRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p.mu.Lock()
		delete(p.files, clean(req.Path))
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "mkdir":
		if !p.caps.Mkdir {
			p.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, "mkdir")
			return
		}
		var req plugin.PathRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p.mu.Lock()
		p.files[clean(req.Path)] = &file{dir: true, mtime: time.Now().UTC()}
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "move", "copy":
		p.opMoveCopy(w, r, op)
	case "set-mtime":
		if !p.caps.SetMtime {
			p.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, "set-mtime")
			return
		}
		var req plugin.MtimeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		p.mu.Lock()
		if f, ok := p.files[clean(req.Path)]; ok {
			f.mtime = req.Mtime
		}
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "watch":
		p.opWatch(w, r)
	default:
		p.writeErr(w, http.StatusNotFound, "not_found", op)
	}
}

func (p *Plugin) opList(w http.ResponseWriter, prefix string) {
	p.mu.Lock()
	p.lists++
	seen := map[string]plugin.Object{}
	for pth, f := range p.files {
		if prefix != "" && !strings.HasPrefix(pth, prefix+"/") {
			continue
		}
		rel := strings.TrimPrefix(pth, prefix)
		rel = strings.TrimPrefix(rel, "/")
		name, isDir := rel, f.dir
		if i := strings.Index(rel, "/"); i >= 0 {
			name, isDir = rel[:i], true
		}
		if name == "" {
			continue
		}
		kind, size, mtime := string(storage.KindFile), f.size(), f.mtime
		if isDir {
			kind, size = string(storage.KindDirectory), int64(0)
		}
		// A directory implied by a child must not inherit the child's mtime
		// as if it were its own; whichever entry wins the dedup, a real dir
		// entry wins over an implied one.
		if prev, dup := seen[name]; dup && prev.Kind == string(storage.KindDirectory) {
			continue
		}
		seen[name] = plugin.Object{
			Path: path.Join(prefix, name), Name: name,
			Size: size, Kind: kind, Mtime: mtime,
		}
	}
	p.mu.Unlock()

	out := make([]plugin.Object, 0, len(seen))
	for _, o := range seen {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugin.ListResponse{Objects: out})
}

func (p *Plugin) opStat(w http.ResponseWriter, pth string) {
	// The root always exists and is a directory. WebDAV PROPFIND, the NFS
	// mount and every "browse from the top" surface stat it first; a plugin
	// that 404s its own root looks broken to all of them at once.
	if pth == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.Object{
			Path: "", Name: "", Kind: string(storage.KindDirectory), Mtime: SeedTime,
		})
		return
	}
	p.mu.Lock()
	f, ok := p.files[pth]
	if !ok {
		for k := range p.files {
			if strings.HasPrefix(k, pth+"/") {
				f, ok = &file{dir: true, mtime: SeedTime}, true
				break
			}
		}
	}
	p.mu.Unlock()
	if !ok {
		p.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, pth)
		return
	}
	kind := string(storage.KindFile)
	if f.dir {
		kind = string(storage.KindDirectory)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plugin.Object{
		Path: pth, Name: path.Base(pth), Size: f.size(), Kind: kind, Mtime: f.mtime,
	})
}

func (p *Plugin) opRead(w http.ResponseWriter, r *http.Request, pth string) {
	p.mu.Lock()
	f, ok := p.files[pth]
	p.reads++
	p.mu.Unlock()
	if !ok || f.dir {
		p.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, pth)
		return
	}
	data := f.data
	rng := r.Header.Get("Range")
	if rng == "" || !p.caps.Range {
		_, _ = w.Write(data)
		return
	}
	p.mu.Lock()
	p.ranges++
	p.mu.Unlock()
	var start, end int64 = 0, int64(len(data)) - 1
	bits := strings.SplitN(strings.TrimPrefix(rng, "bytes="), "-", 2)
	start, _ = strconv.ParseInt(bits[0], 10, 64)
	if len(bits) > 1 && bits[1] != "" {
		end, _ = strconv.ParseInt(bits[1], 10, 64)
	}
	if start >= int64(len(data)) {
		// Past EOF is a short answer, not an error — storage.RangeReader says so.
		w.WriteHeader(http.StatusPartialContent)
		return
	}
	if end >= int64(len(data)) {
		end = int64(len(data)) - 1
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(data[start : end+1])
}

func (p *Plugin) opWrite(w http.ResponseWriter, r *http.Request, pth string) {
	if !p.caps.Write {
		p.writeErr(w, http.StatusBadRequest, plugin.ErrCodeReadOnly, "read-only plugin")
		return
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		p.writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	p.mu.Lock()
	p.writes++
	p.files[pth] = &file{data: b, mtime: time.Now().UTC()}
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plugin) opMoveCopy(w http.ResponseWriter, r *http.Request, op string) {
	if (op == "move" && !p.caps.Move) || (op == "copy" && !p.caps.Copy) {
		p.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, op)
		return
	}
	var req plugin.MoveRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	src, dst := clean(req.Src), clean(req.Dst)
	p.mu.Lock()
	f, ok := p.files[src]
	if ok {
		cp := *f
		p.files[dst] = &cp
		if op == "move" {
			delete(p.files, src)
		}
	}
	p.mu.Unlock()
	if !ok {
		p.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, src)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plugin) opWatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	// ⚠ FLUSH the headers. WriteHeader alone leaves them in the server's
	// buffer and the client waits for a header that was written but never
	// sent — a 60-second hang that looks like a broken subscription.
	if fl != nil {
		fl.Flush()
	}
	for {
		select {
		case ev, ok := <-p.events:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

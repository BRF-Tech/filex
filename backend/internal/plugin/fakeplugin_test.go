package plugin_test

// A plugin the tests can drive: an in-memory storage behind the real HTTP
// protocol. Everything below is what a third-party plugin author would write,
// which is exactly why the tests use it — if the protocol is awkward to
// implement, it shows up here first.

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
	"time"

	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/storage"
)

type fakeFile struct {
	data  []byte
	mtime time.Time
	dir   bool
}

// fakePlugin serves the protocol over httptest. Knobs let a test make it
// misbehave in the ways a real one would: forget its instances (restart),
// answer an error code, or lack a capability.
type fakePlugin struct {
	mu    sync.Mutex
	srv   *httptest.Server
	token string

	name string
	caps plugin.Capabilities
	// files is the plugin's "disk": shared by every instance, exactly like a
	// real backend. A restart forgets the INSTANCE ids, never the data — a
	// fake that wiped the files too would have made the retry look like it
	// worked when it had actually lost everything.
	files     map[string]*fakeFile
	instances map[string]map[string]any // instance id → its config
	next      int

	// counters/knobs
	describes  int
	creates    int
	forgetAll  bool // answer no_instance to every instance call once
	rangeCalls int
	readCalls  int
	events     chan plugin.Event
}

func newFakePlugin(name string, caps plugin.Capabilities) *fakePlugin {
	f := &fakePlugin{
		name:      name,
		caps:      caps,
		token:     "test-token",
		files:     map[string]*fakeFile{},
		instances: map[string]map[string]any{},
		events:    make(chan plugin.Event, 8),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakePlugin) Close()      { f.srv.Close() }
func (f *fakePlugin) URL() string { return f.srv.URL }

func (f *fakePlugin) client() *plugin.Client {
	addr, err := plugin.ParseAddress(f.srv.URL)
	if err != nil {
		panic(err)
	}
	return plugin.NewClient(addr, f.token)
}

func (f *fakePlugin) seed(p string, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p] = &fakeFile{data: []byte(data), mtime: time.Unix(1700000000, 0).UTC()}
}

func (f *fakePlugin) get(p string) *fakeFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[p]
}

func (f *fakePlugin) writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(plugin.ErrorResponse{Error: code, Message: msg})
}

func (f *fakePlugin) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.writeErr(w, http.StatusUnauthorized, "unauthorized", "bad token")
		return
	}
	switch {
	case r.URL.Path == "/v1/describe":
		f.mu.Lock()
		f.describes++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.DescribeResponse{
			Protocol: plugin.ProtocolVersion,
			Name:     f.name,
			Version:  "1.2.3",
			Label:    "Fake storage",
			Fields: []storage.Field{
				{Key: "root", Type: storage.FieldString, Label: "Root", I18nKey: "fake.root", Required: true, Root: true},
				{Key: "secret", Type: storage.FieldPassword, Label: "Secret", I18nKey: "fake.secret", Secret: true},
			},
			Capabilities: f.caps,
		})
	case r.URL.Path == "/v1/instances" && r.Method == http.MethodPost:
		var req plugin.InstanceRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if v, _ := req.Config["root"].(string); v == "boom" {
			f.writeErr(w, http.StatusBadRequest, plugin.ErrCodeInvalid, "root says boom")
			return
		}
		f.mu.Lock()
		f.next++
		f.creates++
		id := "i" + strconv.Itoa(f.next)
		f.instances[id] = req.Config
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.InstanceResponse{Instance: id})
	case strings.HasPrefix(r.URL.Path, "/v1/instances/"):
		f.instanceOp(w, r)
	default:
		f.writeErr(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (f *fakePlugin) instanceOp(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	op := ""
	if len(parts) > 1 {
		op = parts[1]
	}
	f.mu.Lock()
	if f.forgetAll {
		f.forgetAll = false
		f.instances = map[string]map[string]any{}
	}
	_, known := f.instances[id]
	files := f.files
	f.mu.Unlock()
	if r.Method == http.MethodDelete && op == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !known {
		f.writeErr(w, http.StatusConflict, plugin.ErrCodeNoInstance, "unknown instance "+id)
		return
	}
	q := r.URL.Query()
	switch op {
	case "list":
		prefix := strings.Trim(q.Get("path"), "/")
		seen := map[string]*plugin.Object{}
		f.mu.Lock()
		for p, fl := range files {
			if prefix != "" && !strings.HasPrefix(p, prefix+"/") {
				continue
			}
			rel := strings.TrimPrefix(p, strings.TrimSuffix(prefix+"/", "/"))
			rel = strings.TrimPrefix(rel, "/")
			name := rel
			isDir := fl.dir
			if i := strings.Index(rel, "/"); i >= 0 {
				name, isDir = rel[:i], true
			}
			if name == "" {
				continue
			}
			full := path.Join(prefix, name)
			kind := string(storage.KindFile)
			size := fl.size()
			if isDir {
				kind, size = string(storage.KindDirectory), 0
			}
			seen[name] = &plugin.Object{Path: full, Name: name, Size: size, Kind: kind, Mtime: fl.mtime}
		}
		f.mu.Unlock()
		out := make([]plugin.Object, 0, len(seen))
		for _, o := range seen {
			out = append(out, *o)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.ListResponse{Objects: out})
	case "stat":
		p := strings.Trim(q.Get("path"), "/")
		f.mu.Lock()
		fl, ok := files[p]
		if !ok {
			// A directory exists when something lives under it.
			for k := range files {
				if strings.HasPrefix(k, p+"/") {
					fl, ok = &fakeFile{dir: true}, true
					break
				}
			}
		}
		f.mu.Unlock()
		if !ok {
			f.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, p)
			return
		}
		kind := string(storage.KindFile)
		if fl.dir {
			kind = string(storage.KindDirectory)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(plugin.Object{
			Path: p, Name: path.Base(p), Size: fl.size(), Kind: kind, Mtime: fl.mtime,
		})
	case "read":
		p := strings.Trim(q.Get("path"), "/")
		f.mu.Lock()
		fl, ok := files[p]
		f.readCalls++
		f.mu.Unlock()
		if !ok {
			f.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, p)
			return
		}
		data := fl.data
		if rng := r.Header.Get("Range"); rng != "" && f.caps.Range {
			f.mu.Lock()
			f.rangeCalls++
			f.mu.Unlock()
			var start, end int64 = 0, int64(len(data)) - 1
			spec := strings.TrimPrefix(rng, "bytes=")
			bits := strings.SplitN(spec, "-", 2)
			start, _ = strconv.ParseInt(bits[0], 10, 64)
			if len(bits) > 1 && bits[1] != "" {
				end, _ = strconv.ParseInt(bits[1], 10, 64)
			}
			if start >= int64(len(data)) {
				w.WriteHeader(http.StatusPartialContent)
				return
			}
			if end >= int64(len(data)) {
				end = int64(len(data)) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[start : end+1])
			return
		}
		_, _ = w.Write(data)
	case "write":
		if !f.caps.Write {
			f.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, "read-only plugin")
			return
		}
		p := strings.Trim(q.Get("path"), "/")
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		files[p] = &fakeFile{data: b, mtime: time.Now().UTC()}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "delete":
		var req plugin.PathRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		delete(files, strings.Trim(req.Path, "/"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "mkdir":
		var req plugin.PathRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		files[strings.Trim(req.Path, "/")] = &fakeFile{dir: true}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "move", "copy":
		if (op == "move" && !f.caps.Move) || (op == "copy" && !f.caps.Copy) {
			f.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, op)
			return
		}
		var req plugin.MoveRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		src, dst := strings.Trim(req.Src, "/"), strings.Trim(req.Dst, "/")
		f.mu.Lock()
		fl, ok := files[src]
		if ok {
			cp := *fl
			files[dst] = &cp
			if op == "move" {
				delete(files, src)
			}
		}
		f.mu.Unlock()
		if !ok {
			f.writeErr(w, http.StatusNotFound, plugin.ErrCodeNotFound, src)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "set-mtime":
		if !f.caps.SetMtime {
			f.writeErr(w, http.StatusBadRequest, plugin.ErrCodeUnsupported, "no mtime")
			return
		}
		var req plugin.MtimeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		if fl, ok := files[strings.Trim(req.Path, "/")]; ok {
			fl.mtime = req.Mtime
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "watch":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		// ⚠ FLUSH the headers. WriteHeader alone leaves them in the server's
		// buffer, so the client sits waiting for a response header that has
		// been written but not sent — which is exactly the 60-second hang
		// this fake produced the first time. Any real plugin serving SSE has
		// to do the same, and the SDK does it for you.
		if fl != nil {
			fl.Flush()
		}
		for {
			select {
			case ev, ok := <-f.events:
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
	default:
		f.writeErr(w, http.StatusNotFound, "not_found", op)
	}
}

func (fl *fakeFile) size() int64 {
	if fl == nil || fl.dir {
		return 0
	}
	return int64(len(fl.data))
}

// handle implements Handle for the tests that drive a driver directly.
type staticHandle struct {
	c    *plugin.Client
	name string
	err  error
}

func (h staticHandle) Client() (*plugin.Client, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.c, nil
}
func (h staticHandle) DriverName() string { return plugin.DriverPrefix + h.name }

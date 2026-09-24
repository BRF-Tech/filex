package wasmplugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Call scope: the ticket a host function checks ─────────────────────
//
// A plugin never names a storage path. Every call (action_run, view_event)
// is handed a Scope that lists the files it may read as opaque refs (`in:0`,
// `in:1`, …) and, for a job, a place to create outputs (`out:N`). The host
// functions resolve refs against THIS scope and nothing else, so a plugin
// that guesses `in:7` gets not_found, not the seventh file of somebody
// else's job. The scope lives on the context of the call (WithScope) and is
// closed — spool removed, handles shut — when the call ends.

type ctxKey struct{}

// WithScope attaches a call scope to ctx.
func WithScope(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func scopeFrom(ctx context.Context) *Scope {
	s, _ := ctx.Value(ctxKey{}).(*Scope)
	return s
}

// scopeFile is one file the scope knows: an input on a storage, an output
// being written in the spool, or an engine artefact.
type scopeFile struct {
	Ref  string
	Name string
	Size int64
	Mime string
	// Rel is the storage-relative path of an input; "" for spool files.
	Rel string
	// Path is the spool path once the file has bytes on local disk.
	Path string
	// Output marks a file the plugin created (file_create) or an engine
	// produced; only these may be named in ActionRunOutput.Outputs.
	Output bool
	// Asset marks a file asset_fetch put in the app's cache: readable by the
	// app, never an input, never an output (assets.go).
	Asset bool
}

type handle struct {
	f     *scopeFile
	r     io.ReadCloser
	w     io.WriteCloser
	wrote int64
}

// Scope is one call's ticket. Build with newScope; close with Close.
type Scope struct {
	plugin    *Installed
	reg       *Registry
	jobID     string
	storageID int64
	drv       storage.Driver
	actor     *model.User
	locale    string
	// writable is true for action_run only: views and pages may read inputs
	// but never create outputs or touch state.
	writable bool
	dir      string
	// storageName qualifies input paths for the guest (`name://rel`).
	storageName string
	// readOnly: the storage takes no writes. Handed to the guest on every
	// input (wire.FileRef.ReadOnly) so an app can refuse a flow that ends in
	// a write before it starts one.
	readOnly bool

	mu         sync.Mutex
	files      map[string]*scopeFile
	order      []string
	handles    map[uint64]*handle
	nextHandle uint64
	nextOut    int
	nextRun    int
	progress   func(done, total int64, msg string)
	cancelled  bool
	// page is set on a page_event call: the public link being answered (a
	// share carrying plugin_id/page_id — migration 00046).
	page *model.Share
	// promises are the links this job asked for against its OWN outputs,
	// which have no catalogue node until runJob commits them. The token and
	// the PIN were handed to the plugin at the ask; the ROW is written when
	// the bytes land. See public_promised.go.
	promises []*promisedShare
	// promisedLocks are the locks this job asked for on its own outputs,
	// keyed by output ref, taken when runJob commits them (hfFileLock).
	promisedLocks map[string]*model.AppPluginLock
	// outputMode is the action's effective output mode ("sibling" | "version"
	// | "none"), so share_create can tell a plugin that asked to share an
	// output of an action that writes none — a link that could never open.
	outputMode string
	// system marks a call that belongs to NOBODY because the HOST started it
	// on its own clock: the hourly wake-up (schedule.go Tick). It is not the
	// same as "actor is nil" and must never be inferred from that — a PUBLIC
	// PAGE call is actor-less too, and that one is a stranger on the internet.
	// Only what the host itself begins is the system.
	//
	// ⚠ It buys exactly one thing, in hfStateList: an app's listing of its OWN
	// state rows is not narrowed by a person's ACL, because there is no person
	// to narrow it by. Nothing else in a scope reads it, and a system scope is
	// read-only (writable is false), so it can neither write files nor state,
	// take locks, sign, nor open links.
	system bool
}

func newScope(plugin *Installed, reg *Registry, jobID string, storageID int64, drv storage.Driver, actor *model.User, locale string, writable bool) (*Scope, error) {
	dir, err := os.MkdirTemp(reg.spoolRoot(), "call-")
	if err != nil {
		return nil, fmt.Errorf("wasmplugin: spool: %w", err)
	}
	return &Scope{
		plugin: plugin, reg: reg, jobID: jobID, storageID: storageID, drv: drv,
		actor: actor, locale: locale, writable: writable, dir: dir,
		files: map[string]*scopeFile{}, handles: map[uint64]*handle{},
	}, nil
}

// AddInput registers a storage file the call may read.
func (s *Scope) AddInput(rel string, size int64, mime string) wire.FileRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := "in:" + strconv.Itoa(len(s.order))
	f := &scopeFile{Ref: ref, Name: path.Base(rel), Size: size, Mime: mime, Rel: rel}
	s.files[ref] = f
	s.order = append(s.order, ref)
	return wire.FileRef{Ref: ref, Name: f.Name, Size: size, Mime: mime, PathRel: rel, Path: s.qualified(rel), ReadOnly: s.readOnly}
}

// qualified is the adapter-qualified spelling of a storage-relative path,
// or "" when the scope has no storage name (pages, tests).
func (s *Scope) qualified(rel string) string {
	if s.storageName == "" || rel == "" {
		return ""
	}
	return s.storageName + "://" + strings.TrimPrefix(rel, "/")
}

// Inputs lists the registered inputs in order.
func (s *Scope) Inputs() []wire.FileRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]wire.FileRef, 0, len(s.order))
	for _, ref := range s.order {
		f := s.files[ref]
		if f == nil || f.Output || f.Asset {
			continue
		}
		out = append(out, wire.FileRef{Ref: f.Ref, Name: f.Name, Size: f.Size, Mime: f.Mime, PathRel: f.Rel, Path: s.qualified(f.Rel), ReadOnly: s.readOnly && f.Rel != ""})
	}
	return out
}

// Close shuts every open handle and removes the spool.
//
// ⚠ It also abandons any share this call PROMISED and did not keep (a job
// that failed, an output the plugin never named). Nothing has to be undone in
// the database — a promise is the absence of a row — but the copies staged
// for it are removed here, on every exit path, rather than left for the
// sweeper.
func (s *Scope) Close() {
	s.dropPromises()
	s.mu.Lock()
	for id, h := range s.handles {
		if h.r != nil {
			_ = h.r.Close()
		}
		if h.w != nil {
			_ = h.w.Close()
		}
		delete(s.handles, id)
	}
	dir := s.dir
	s.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (s *Scope) file(ref string) (*scopeFile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[ref]
	return f, ok
}

// Output returns a plugin-created file by ref, for committing.
func (s *Scope) Output(ref string) (*scopeFile, bool) {
	f, ok := s.file(ref)
	if !ok || !f.Output {
		return nil, false
	}
	return f, true
}

// openRead opens an input or spool file for reading.
func (s *Scope) openRead(ctx context.Context, ref string) (uint64, int64, error) {
	f, ok := s.file(ref)
	if !ok {
		return 0, 0, hostErr(wire.ErrNotFound, "no such file ref")
	}
	var rc io.ReadCloser
	var err error
	if f.Path != "" {
		rc, err = os.Open(f.Path)
	} else {
		if s.drv == nil {
			return 0, 0, hostErr(wire.ErrUnavailable, "no storage for this call")
		}
		rc, err = s.drv.Read(ctx, f.Rel)
	}
	if err != nil {
		return 0, 0, hostErr(wire.ErrUnavailable, "open: "+err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextHandle++
	s.handles[s.nextHandle] = &handle{f: f, r: rc}
	return s.nextHandle, f.Size, nil
}

// read reads up to n bytes from an open read handle; (nil, true) at EOF.
func (s *Scope) read(id uint64, n int) ([]byte, bool, error) {
	s.mu.Lock()
	h := s.handles[id]
	s.mu.Unlock()
	if h == nil || h.r == nil {
		return nil, false, hostErr(wire.ErrInvalid, "not an open read handle")
	}
	if n <= 0 || n > maxChunk {
		n = maxChunk
	}
	buf := make([]byte, n)
	got, err := io.ReadFull(h.r, buf)
	if got > 0 {
		return buf[:got], false, nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, hostErr(wire.ErrUnavailable, "read: "+err.Error())
	}
	return nil, true, nil
}

// create opens a new output file in the spool.
func (s *Scope) create(name string) (uint64, string, error) {
	if !s.writable {
		return 0, "", hostErr(wire.ErrPermissionDenied, "this call may not create files")
	}
	name = safeName(name)
	if name == "" {
		return 0, "", hostErr(wire.ErrInvalid, "empty output name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.files) >= maxScopeFiles {
		return 0, "", hostErr(wire.ErrTooLarge, "too many files in one call")
	}
	ref := "out:" + strconv.Itoa(s.nextOut)
	s.nextOut++
	p := filepath.Join(s.dir, "out-"+strconv.Itoa(s.nextOut)+"-"+name)
	w, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", hostErr(wire.ErrInternal, "create: "+err.Error())
	}
	f := &scopeFile{Ref: ref, Name: name, Path: p, Output: true}
	s.files[ref] = f
	s.order = append(s.order, ref)
	s.nextHandle++
	s.handles[s.nextHandle] = &handle{f: f, w: w}
	return s.nextHandle, ref, nil
}

func (s *Scope) write(id uint64, b []byte) error {
	s.mu.Lock()
	h := s.handles[id]
	limit := s.reg.opts.MaxOutputBytes
	s.mu.Unlock()
	if h == nil || h.w == nil {
		return hostErr(wire.ErrInvalid, "not an open write handle")
	}
	if limit > 0 && h.wrote+int64(len(b)) > limit {
		return hostErr(wire.ErrTooLarge, "output exceeds the per-file limit")
	}
	n, err := h.w.Write(b)
	h.wrote += int64(n)
	if err != nil {
		return hostErr(wire.ErrInternal, "write: "+err.Error())
	}
	return nil
}

func (s *Scope) closeHandle(id uint64) error {
	s.mu.Lock()
	h := s.handles[id]
	delete(s.handles, id)
	s.mu.Unlock()
	if h == nil {
		return hostErr(wire.ErrInvalid, "not an open handle")
	}
	if h.r != nil {
		_ = h.r.Close()
	}
	if h.w != nil {
		if err := h.w.Close(); err != nil {
			return hostErr(wire.ErrInternal, "close: "+err.Error())
		}
		h.f.Size = h.wrote
	}
	return nil
}

// spool makes sure an input has bytes on local disk (engines need a path)
// and returns that path. Inputs are streamed from the storage once; a spool
// file already made is reused.
func (s *Scope) spool(ctx context.Context, ref string) (string, error) {
	f, ok := s.file(ref)
	if !ok {
		return "", hostErr(wire.ErrNotFound, "no such file ref")
	}
	if f.Path != "" {
		return f.Path, nil
	}
	if s.drv == nil {
		return "", hostErr(wire.ErrUnavailable, "no storage for this call")
	}
	rc, err := s.drv.Read(ctx, f.Rel)
	if err != nil {
		return "", hostErr(wire.ErrUnavailable, "open: "+err.Error())
	}
	defer rc.Close()
	p := filepath.Join(s.dir, "in-"+strings.TrimPrefix(ref, "in:")+"-"+safeName(f.Name))
	w, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", hostErr(wire.ErrInternal, "spool: "+err.Error())
	}
	limit := s.reg.opts.MaxInputBytes
	var r io.Reader = rc
	if limit > 0 {
		r = io.LimitReader(rc, limit+1)
	}
	n, err := io.Copy(w, r)
	cerr := w.Close()
	if err != nil || cerr != nil {
		_ = os.Remove(p)
		return "", hostErr(wire.ErrUnavailable, "spool: copy failed")
	}
	if limit > 0 && n > limit {
		_ = os.Remove(p)
		return "", hostErr(wire.ErrTooLarge, "input exceeds the per-file limit")
	}
	s.mu.Lock()
	f.Path = p
	f.Size = n
	s.mu.Unlock()
	return p, nil
}

// addArtefact registers a file an engine produced.
func (s *Scope) addArtefact(name, p string, size int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := "eng:" + strconv.Itoa(len(s.order))
	s.files[ref] = &scopeFile{Ref: ref, Name: name, Size: size, Path: p, Output: true}
	s.order = append(s.order, ref)
	return ref
}

const (
	maxChunk      = 1 << 20
	maxScopeFiles = 256
)

// safeName reduces a plugin-supplied file name to a single path element.
func safeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
}

// HostError is what a host function hands back on failure; the guest sees
// it as {"error": {code, message}}.
type hostError struct {
	Code    string
	Message string
}

func (e *hostError) Error() string { return e.Code + ": " + e.Message }

func hostErr(code, msg string) error { return &hostError{Code: code, Message: msg} }

func asHostError(err error) *hostError {
	var he *hostError
	if errors.As(err, &he) {
		return he
	}
	return &hostError{Code: wire.ErrInternal, Message: err.Error()}
}

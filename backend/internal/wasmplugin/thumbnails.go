package wasmplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Thumbnails an app draws (docs/thumbnails.md → Thumbnails drawn by apps)
//
// A manifest's `thumbnails` block names the kinds of file the app draws; its
// module answers the `thumbnail` export. Each kind is a DERIVED permission
// (`thumbnail:.<ext>`, `thumbnail:<type>`), listed at the install review like
// the interface's: drawing a kind means being handed the bytes of every file
// of that kind filex draws, on every storage, whoever owns it. An upgrade
// that draws one more kind is a new grant.
//
// The call is the narrowest one the platform has. It is handed ONE file's
// bytes (spooled, so the app reads them with file_open like any input) and
// its name, size and type - no path, no storage, no person. Every host
// function but reading that file, the app's own settings and assets, and a
// granted http host answers permission_denied (thumbHostFns). It cannot
// write, keep state, lock, sign, notify, mail, look anybody up or run an
// engine, so the only way a file can leave is an `http:` grant the
// administrator approved - and each call that used one is audited
// (app_plugin.thumbnail_sent).

// permPrefixThumbnail is a kind an app draws thumbnails of:
// `thumbnail:.jar`, `thumbnail:application/java-archive`.
const permPrefixThumbnail = "thumbnail:"

// isThumbPerm reports whether p is derived from the `thumbnails` block.
func isThumbPerm(p Permission) bool { return strings.HasPrefix(string(p), permPrefixThumbnail) }

// thumbHostFns are the host functions a thumbnail call may reach. Everything
// else answers permission_denied whatever the app was granted: a thumbnail
// call runs for every file of its kinds, with nobody asking for it.
var thumbHostFns = map[string]bool{
	"file_open": true, "file_read": true, "file_close": true,
	"settings_get": true, "asset_fetch": true, "http_request": true,
	"engine_available": true,
}

// thumbDenied is the one sentence a thumbnail call is refused with.
const thumbDenied = "a thumbnail call reads the file it was given and nothing else"

// checkThumbnails validates the `thumbnails` block and adds the kinds it
// names to the grant, one permission each.
func (m *Manifest) checkThumbnails() error {
	if m.Thumbnails == nil {
		return nil
	}
	a := &m.Thumbnails.Applies
	if err := validateApplies(a); err != nil {
		return fmt.Errorf("manifest: thumbnails: %w", err)
	}
	if a.Kind != "file" {
		return fmt.Errorf("manifest: thumbnails: a thumbnail is drawn of a file: applies.kind must be file, not %q", a.Kind)
	}
	if len(a.Ext) == 0 && len(a.Mime) == 0 {
		return fmt.Errorf("manifest: thumbnails: name the kinds the app draws - give applies.ext or applies.mime (an app may not draw every file)")
	}
	if a.Multi || a.Min > 0 || a.Max > 0 || len(a.State) > 0 || len(a.NoState) > 0 || a.Writable || len(a.EngineExt) > 0 {
		return fmt.Errorf("manifest: thumbnails: the rule names kinds of file (ext, mime) and nothing else")
	}
	perms := []Permission{}
	for _, e := range a.Ext {
		if !viewerKindOK("." + e) {
			return fmt.Errorf("manifest: thumbnails: applies.ext %q is not a file extension", e)
		}
		perms = append(perms, Permission(permPrefixThumbnail+"."+e))
	}
	for _, mt := range a.Mime {
		if !viewerKindOK(mt) {
			return fmt.Errorf("manifest: thumbnails: applies.mime %q is not a media type (a family like image/* is; */* is not)", mt)
		}
		perms = append(perms, Permission(permPrefixThumbnail+mt))
	}
	seen := map[Permission]bool{}
	for _, p := range m.Perms {
		seen[p] = true
	}
	for _, p := range perms {
		if !seen[p] {
			seen[p] = true
			m.Perms = append(m.Perms, p)
		}
	}
	return nil
}

// checkThumbnailExport refuses a module whose manifest draws thumbnails but
// which exports no `thumbnail` - found at install, not at the first file.
func checkThumbnailExport(ctx context.Context, m *Manifest, c exportProbe) error {
	if m.Thumbnails == nil {
		return nil
	}
	has, err := c.HasExport(ctx, "thumbnail")
	if err != nil {
		return err
	}
	if !has {
		return &CallError{Code: CodeRefused, Export: "thumbnail",
			Message: "the manifest has a thumbnails block, but the module has no thumbnail export"}
	}
	return nil
}

// thumbKinds are the kinds the app draws AND was granted: the extensions and
// the media types.
func (p *Installed) thumbKinds() (exts, mimes []string) {
	if p.Manifest == nil || p.Manifest.Thumbnails == nil {
		return nil, nil
	}
	a := p.Manifest.Thumbnails.Applies
	for _, e := range a.Ext {
		if p.Grants.Has(Permission(permPrefixThumbnail + "." + e)) {
			exts = append(exts, e)
		}
	}
	for _, mt := range a.Mime {
		if p.Grants.Has(Permission(permPrefixThumbnail + mt)) {
			mimes = append(mimes, mt)
		}
	}
	return exts, mimes
}

// drawsThumbnails reports whether p is an app that draws thumbnails now.
func (p *Installed) drawsThumbnails() bool {
	if state, _ := p.State(); state != StateRunning || !p.HasModule() {
		return false
	}
	exts, mimes := p.thumbKinds()
	return len(exts)+len(mimes) > 0
}

// ThumbnailHandlers lists the running apps that draw thumbnails, by name -
// the thumbnail half of assoc.Source.
func (r *Registry) ThumbnailHandlers() []assoc.AppHandler {
	if r == nil {
		return nil
	}
	var out []assoc.AppHandler
	for _, p := range r.All() {
		if !p.drawsThumbnails() {
			continue
		}
		exts, mimes := p.thumbKinds()
		out = append(out, assoc.AppHandler{
			Handler: assoc.Handler{ID: assoc.ThumbID(p.Row.Name), App: p.Row.Name, Version: p.Row.Version, Label: p.Manifest.Label},
			Ext:     exts, Mime: mimes,
		})
	}
	return out
}

// OpenHandlers lists the running apps' interfaces that open files (`viewer`
// views), by app name and then in the manifest's order - the open half of
// assoc.Source, and the order the explorer's list has always had.
func (r *Registry) OpenHandlers() []assoc.AppHandler {
	if r == nil {
		return nil
	}
	var out []assoc.AppHandler
	for _, p := range r.All() {
		if state, _ := p.State(); state != StateRunning || p.UI() == nil {
			continue
		}
		for _, v := range p.Manifest.Views {
			if v.Placement != "viewer" || v.UI == "" {
				continue
			}
			out = append(out, assoc.AppHandler{
				Handler: assoc.Handler{ID: assoc.OpenID(p.Row.Name, v.ID), App: p.Row.Name, View: v.ID, Version: p.Row.Version, Label: v.Label},
				Ext:     append([]string(nil), v.Applies.Ext...), Mime: append([]string(nil), v.Applies.Mime...),
			})
		}
	}
	return out
}

// ── limits ─────────────────────────────────────────────────────────────

// ThumbLimits are an app's thumbnail limits as the administrator sees them.
type ThumbLimits struct {
	// MaxInputMB is the largest file the app is sent (MiB).
	MaxInputMB int `json:"max_input_mb"`
	// TimeoutS is the time it has per file.
	TimeoutS int `json:"timeout_s"`
	// MemoryMB is its memory in a thumbnail call (MiB).
	MemoryMB int `json:"memory_mb"`
	// Concurrency is how many files it draws at once.
	Concurrency int `json:"concurrency"`
}

// The defaults and the ranges (docs/thumbnails.md → Thumbnails drawn by apps).
const (
	defaultThumbInputMB     = 32
	defaultThumbTimeoutS    = 10
	defaultThumbConcurrency = 2
	minThumbMemoryMB        = 16
	maxThumbTimeoutS        = 60
	maxThumbConcurrency     = 8
)

// ThumbLimitsAnswer is the answer of GET/PUT …/thumbnails: what is in force,
// what the administrator stored (0 = the default), the defaults and the
// bounds, and the kinds the app draws.
type ThumbLimitsAnswer struct {
	Values   ThumbLimits `json:"values"`
	Stored   ThumbLimits `json:"stored"`
	Defaults ThumbLimits `json:"defaults"`
	Min      ThumbLimits `json:"min"`
	Max      ThumbLimits `json:"max"`
	Ext      []string    `json:"ext"`
	Mime     []string    `json:"mime"`
}

// thumbDefaults are the defaults for p: the memory is the manifest's.
func (r *Registry) thumbDefaults(p *Installed) ThumbLimits {
	return ThumbLimits{
		MaxInputMB:  min(defaultThumbInputMB, r.maxThumbInputMB()),
		TimeoutS:    defaultThumbTimeoutS,
		MemoryMB:    p.Manifest.MemoryPages() / 16,
		Concurrency: defaultThumbConcurrency,
	}
}

func (r *Registry) maxThumbInputMB() int {
	mb := int(r.opts.MaxInputBytes >> 20)
	if mb < 1 {
		mb = 1
	}
	return mb
}

func (r *Registry) thumbBounds() (lo, hi ThumbLimits) {
	return ThumbLimits{MaxInputMB: 1, TimeoutS: 1, MemoryMB: minThumbMemoryMB, Concurrency: 1},
		ThumbLimits{MaxInputMB: r.maxThumbInputMB(), TimeoutS: maxThumbTimeoutS, MemoryMB: MaxMemoryPages / 16, Concurrency: maxThumbConcurrency}
}

// effective fills every zero of stored with the default.
func effectiveLimits(stored, def ThumbLimits) ThumbLimits {
	pick := func(v, d int) int {
		if v <= 0 {
			return d
		}
		return v
	}
	return ThumbLimits{
		MaxInputMB: pick(stored.MaxInputMB, def.MaxInputMB), TimeoutS: pick(stored.TimeoutS, def.TimeoutS),
		MemoryMB: pick(stored.MemoryMB, def.MemoryMB), Concurrency: pick(stored.Concurrency, def.Concurrency),
	}
}

// thumbState is an app's thumbnail machinery: its limits as last read, the
// slots it draws in, and the module compiled with its thumbnail memory.
type thumbState struct {
	mu       sync.Mutex
	stored   *ThumbLimits
	readAt   time.Time
	slots    chan struct{}
	compiled *Compiled
	pages    int
}

// thumbLimitsTTL is how long the stored limits are trusted (a change made
// here drops them at once).
const thumbLimitsTTL = 10 * time.Second

func (r *Registry) storedThumbLimits(ctx context.Context, p *Installed) ThumbLimits {
	ts := &p.thumb
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.stored != nil && time.Since(ts.readAt) < thumbLimitsTTL {
		return *ts.stored
	}
	var got ThumbLimits
	if row, err := r.opts.Store.GetAppThumbLimits(ctx, p.Row.ID); err == nil && row != nil {
		got = ThumbLimits{MaxInputMB: row.MaxInputMB, TimeoutS: row.TimeoutS, MemoryMB: row.MemoryMB, Concurrency: row.Concurrency}
	}
	ts.stored, ts.readAt = &got, time.Now()
	return got
}

// thumbLimitsOf is what is in force for p.
func (r *Registry) thumbLimitsOf(ctx context.Context, p *Installed) ThumbLimits {
	return effectiveLimits(r.storedThumbLimits(ctx, p), r.thumbDefaults(p))
}

// ThumbLimits answers an app's thumbnail limits.
func (r *Registry) ThumbLimits(ctx context.Context, id int64) (*ThumbLimitsAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if p.Manifest.Thumbnails == nil {
		return nil, installErr(ErrCodeNotFound, "this app draws no thumbnails")
	}
	stored := r.storedThumbLimits(ctx, p)
	lo, hi := r.thumbBounds()
	exts, mimes := p.thumbKinds()
	return &ThumbLimitsAnswer{
		Values: effectiveLimits(stored, r.thumbDefaults(p)), Stored: stored, Defaults: r.thumbDefaults(p),
		Min: lo, Max: hi, Ext: nonNilStrings(exts), Mime: nonNilStrings(mimes),
	}, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PutThumbLimits stores an app's thumbnail limits (0 = the default). A value
// outside its range is refused with the field named, not clamped: what the
// administrator typed is what is stored or nothing is.
func (r *Registry) PutThumbLimits(ctx context.Context, id int64, l ThumbLimits) (*ThumbLimitsAnswer, error) {
	p, ok := r.ByID(id)
	if !ok {
		return nil, installErr(ErrCodeNotFound, "no such plugin")
	}
	if r.opts.Demo {
		return nil, installErr(ErrCodeDemo, "app plugins cannot be changed on the demo instance")
	}
	if p.Manifest.Thumbnails == nil {
		return nil, installErr(ErrCodeNotFound, "this app draws no thumbnails")
	}
	lo, hi := r.thumbBounds()
	for _, f := range []struct {
		name      string
		v, lo, hi int
	}{
		{"max_input_mb", l.MaxInputMB, lo.MaxInputMB, hi.MaxInputMB},
		{"timeout_s", l.TimeoutS, lo.TimeoutS, hi.TimeoutS},
		{"memory_mb", l.MemoryMB, lo.MemoryMB, hi.MemoryMB},
		{"concurrency", l.Concurrency, lo.Concurrency, hi.Concurrency},
	} {
		if f.v != 0 && (f.v < f.lo || f.v > f.hi) {
			return nil, &InstallError{Code: ErrCodeOutOfRange, Message: fmt.Sprintf("%s must be %d..%d (0 = the default)", f.name, f.lo, f.hi), Where: f.name,
				Requires: fmt.Sprintf("%d..%d", f.lo, f.hi)}
		}
	}
	if err := r.opts.Store.PutAppThumbLimits(ctx, &model.AppThumbLimits{
		PluginID: p.Row.ID, MaxInputMB: l.MaxInputMB, TimeoutS: l.TimeoutS, MemoryMB: l.MemoryMB, Concurrency: l.Concurrency,
	}); err != nil {
		return nil, err
	}
	p.thumb.mu.Lock()
	p.thumb.stored = nil
	p.thumb.mu.Unlock()
	p.log("info", fmt.Sprintf("thumbnail limits: %d MB, %d s, %d MB of memory, %d at once (0 = the default)", l.MaxInputMB, l.TimeoutS, l.MemoryMB, l.Concurrency))
	return r.ThumbLimits(ctx, id)
}

// ErrCodeOutOfRange: a limit outside its range (InstallError.Where names it).
const ErrCodeOutOfRange = "out_of_range"

// AppLimits are an app's limits in force, for the pipeline's freshness rule
// (thumb.AppThumbs). ok is false for an app that draws nothing now.
func (r *Registry) AppLimits(app string) (int64, time.Duration, bool) {
	p, ok := r.ByName(app)
	if !ok || !p.drawsThumbnails() {
		return 0, 0, false
	}
	l := r.thumbLimitsOf(context.Background(), p)
	return int64(l.MaxInputMB) << 20, time.Duration(l.TimeoutS) * time.Second, true
}

// slot takes one of the app's thumbnail slots (sized by its concurrency
// limit), waiting while all are taken. A change of the limit swaps the
// channel: calls already running finish on the old one.
func (p *Installed) thumbSlot(ctx context.Context, n int) (func(), error) {
	p.thumb.mu.Lock()
	if p.thumb.slots == nil || cap(p.thumb.slots) != n {
		p.thumb.slots = make(chan struct{}, n)
	}
	slots := p.thumb.slots
	p.thumb.mu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// thumbCompiled is the module a thumbnail call runs on: the app's own when
// its thumbnail memory is the manifest's, else a copy compiled with that
// memory (the ceiling is fixed when a module is compiled), kept until the
// limit changes or the app is unloaded.
func (r *Registry) thumbCompiled(ctx context.Context, p *Installed, memoryMB int) (*Compiled, error) {
	pages := memoryMB * 16
	base, err := p.running()
	if err != nil {
		return nil, err
	}
	if pages == p.Manifest.MemoryPages() {
		return base, nil
	}
	p.thumb.mu.Lock()
	defer p.thumb.mu.Unlock()
	if p.thumb.compiled != nil && p.thumb.pages == pages {
		return p.thumb.compiled, nil
	}
	m := *p.Manifest
	m.Limits.MemoryPages = pages
	c, err := r.rt.Compile(ctx, filepath.Join(r.opts.Dir, p.Row.Name, p.Row.WasmPath), p.Row.SHA256, &m, nil, p.log)
	if err != nil {
		return nil, err
	}
	if p.thumb.compiled != nil {
		_ = p.thumb.compiled.Close(ctx)
	}
	p.thumb.compiled, p.thumb.pages = c, pages
	return c, nil
}

// dropThumbModule frees the thumbnail copy of an app's module.
func (p *Installed) dropThumbModule(ctx context.Context) {
	p.thumb.mu.Lock()
	if p.thumb.compiled != nil {
		_ = p.thumb.compiled.Close(ctx)
		p.thumb.compiled = nil
	}
	p.thumb.mu.Unlock()
}

// ── the call ───────────────────────────────────────────────────────────

func drawErr(app, code string, limit int64, err error) error {
	return &assoc.DrawError{App: app, Code: code, Limit: limit, Err: err}
}

// DrawThumbnail asks the app to draw one file: the bytes in req.Body, which
// must not be more than its size limit. It answers the image the app drew (a
// PNG or a JPEG, at most wire.ThumbnailMaxOutputBytes - the pipeline checks
// the rest), or an *assoc.DrawError.
func (r *Registry) DrawThumbnail(ctx context.Context, app string, req assoc.DrawRequest) ([]byte, error) {
	p, ok := r.ByName(app)
	if !ok || !p.drawsThumbnails() {
		return nil, drawErr(app, assoc.DrawFailed, 0, errors.New("the app draws no thumbnails now"))
	}
	ext := assoc.ExtOf(req.Name)
	exts, mimes := p.thumbKinds()
	if !(assoc.AppHandler{Ext: exts, Mime: mimes}).Matches(ext, req.Mime) {
		return nil, drawErr(app, assoc.DrawFailed, 0, errors.New("the app does not draw this kind of file"))
	}
	lim := r.thumbLimitsOf(ctx, p)
	maxBytes := int64(lim.MaxInputMB) << 20
	if req.Size > maxBytes {
		return nil, drawErr(app, assoc.DrawTooLarge, maxBytes, nil)
	}
	release, err := p.thumbSlot(ctx, lim.Concurrency)
	if err != nil {
		return nil, drawErr(app, assoc.DrawFailed, 0, err)
	}
	defer release()
	c, err := r.thumbCompiled(ctx, p, lim.MemoryMB)
	if err != nil {
		return nil, drawErr(app, assoc.DrawFailed, 0, err)
	}
	s, err := newScope(p, r, "", 0, nil, nil, "", false)
	if err != nil {
		return nil, drawErr(app, assoc.DrawFailed, 0, err)
	}
	s.thumb = true
	defer s.Close()
	ref, size, err := s.spoolThumbInput(req.Body, req.Name, req.Mime, maxBytes)
	if err != nil {
		var over *thumbOver
		if errors.As(err, &over) {
			return nil, drawErr(app, assoc.DrawTooLarge, maxBytes, nil)
		}
		return nil, drawErr(app, assoc.DrawFailed, 0, err)
	}
	in, _ := json.Marshal(wire.ThumbnailInput{
		File: ref, Ext: ext, MaxWidth: wire.ThumbnailSize, MaxHeight: wire.ThumbnailSize,
		MaxOutputBytes: wire.ThumbnailMaxOutputBytes, Locale: srvtext.Pick(), Settings: r.publicSettings(ctx, p),
	})
	timeout := time.Duration(lim.TimeoutS) * time.Second
	raw, err := c.Call(WithScope(ctx, s), "thumbnail", in, timeout)
	r.auditThumbSent(p, s, req, size)
	if err != nil {
		if IsCode(err, CodeTimeout) {
			p.log("warn", fmt.Sprintf("thumbnail of %s: did not finish within %s", req.Name, timeout))
			return nil, drawErr(app, assoc.DrawTimeout, timeout.Milliseconds(), err)
		}
		p.log("warn", "thumbnail of "+req.Name+": "+err.Error())
		return nil, drawErr(app, assoc.DrawFailed, 0, err)
	}
	if len(raw) > wire.ThumbnailMaxOutputBytes*2 {
		p.log("warn", "thumbnail of "+req.Name+": the answer is too large")
		return nil, drawErr(app, assoc.DrawFailed, 0, errors.New("the answer is too large"))
	}
	var out wire.ThumbnailOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		p.log("warn", "thumbnail of "+req.Name+": the answer is not a thumbnail")
		return nil, drawErr(app, assoc.DrawFailed, 0, errors.New("the answer is not a thumbnail"))
	}
	if len(out.Image) == 0 || len(out.Image) > wire.ThumbnailMaxOutputBytes {
		p.log("warn", fmt.Sprintf("thumbnail of %s: an image of %d bytes (1..%d)", req.Name, len(out.Image), wire.ThumbnailMaxOutputBytes))
		return nil, drawErr(app, assoc.DrawFailed, 0, errors.New("no image, or one over the size limit"))
	}
	return out.Image, nil
}

// thumbOver: the file turned out larger than the app's limit while it was
// being spooled (the catalogue's size was behind the file).
type thumbOver struct{}

func (*thumbOver) Error() string { return "over the size limit" }

// spoolThumbInput copies the file into the call's spool, at most max bytes,
// and registers it as the call's one input. The app is told its name, size
// and type - never where it lives.
func (s *Scope) spoolThumbInput(body io.Reader, name, mime string, max int64) (wire.FileRef, int64, error) {
	p := filepath.Join(s.dir, "in-0-"+safeName(filepath.Base(name)))
	if safeName(filepath.Base(name)) == "" {
		p = filepath.Join(s.dir, "in-0")
	}
	w, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return wire.FileRef{}, 0, err
	}
	n, err := io.Copy(w, io.LimitReader(body, max+1))
	cerr := w.Close()
	if err != nil || cerr != nil {
		return wire.FileRef{}, 0, fmt.Errorf("spool: %v %v", err, cerr)
	}
	if n > max {
		return wire.FileRef{}, n, &thumbOver{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := "in:0"
	f := &scopeFile{Ref: ref, Name: safeName(filepath.Base(name)), Size: n, Mime: mime, Path: p}
	s.files[ref] = f
	s.order = append(s.order, ref)
	return wire.FileRef{Ref: ref, Name: f.Name, Size: n, Mime: mime}, n, nil
}

// noteSent records a host a thumbnail call reached (http_request, or an
// asset downloaded rather than served from the cache).
func (s *Scope) noteSent(host string) {
	if s == nil || !s.thumb {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.sent {
		if h == host {
			return
		}
	}
	s.sent = append(s.sent, host)
}

// auditThumbSent writes the one audit row of a thumbnail call that reached
// the network: which app, which hosts, which file. Nothing when it did not.
func (r *Registry) auditThumbSent(p *Installed, s *Scope, req assoc.DrawRequest, size int64) {
	s.mu.Lock()
	hosts := append([]string(nil), s.sent...)
	s.mu.Unlock()
	if len(hosts) == 0 {
		return
	}
	sort.Strings(hosts)
	p.log("info", "thumbnail of "+req.Name+": sent to "+strings.Join(hosts, ", "))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.opts.Store.InsertAuditEntry(ctx, &model.AuditEntry{
		Action: AuditActionThumbnailSent, TargetType: "file", TargetID: req.Path,
		Metadata: map[string]any{
			"plugin": p.Row.Name, "version": p.Row.Version, "hosts": hosts,
			"storage_id": req.StorageID, "path": req.Path, "node_id": req.NodeID, "size": size,
		},
		CreatedAt: time.Now(),
	}); err != nil {
		r.log.Warn("app-plugins: thumbnail audit row not written", slog.String("plugin", p.Row.Name), slog.Any("err", err))
	}
}

// AuditActionThumbnailSent is the audit action of a thumbnail call that
// reached the network.
const AuditActionThumbnailSent = "app_plugin.thumbnail_sent"

// ManifestHandlers are the handlers an app with this manifest would add: one
// per `viewer` view of its interface (open) and its `thumbnails` block
// (thumbnail) - what the install review's File types group lists before the
// app exists.
func ManifestHandlers(m *Manifest) (open, thumb []assoc.AppHandler) {
	if m == nil {
		return nil, nil
	}
	if m.UI != nil {
		for _, v := range m.Views {
			if v.Placement != "viewer" || v.UI == "" {
				continue
			}
			open = append(open, assoc.AppHandler{
				Handler: assoc.Handler{ID: assoc.OpenID(m.Name, v.ID), App: m.Name, View: v.ID, Version: m.Version, Label: v.Label},
				Ext:     append([]string(nil), v.Applies.Ext...), Mime: append([]string(nil), v.Applies.Mime...),
			})
		}
	}
	if m.Thumbnails != nil {
		thumb = append(thumb, assoc.AppHandler{
			Handler: assoc.Handler{ID: assoc.ThumbID(m.Name), App: m.Name, Version: m.Version, Label: m.Label},
			Ext:     append([]string(nil), m.Thumbnails.Applies.Ext...), Mime: append([]string(nil), m.Thumbnails.Applies.Mime...),
		})
	}
	return open, thumb
}

// ParseManifestForReview is ParseManifest for a caller outside the package
// that holds a dry run's manifest (wire) and wants the handlers it adds.
func ParseManifestForReview(m *wire.Manifest) *Manifest {
	if m == nil {
		return nil
	}
	return &Manifest{Manifest: *m}
}

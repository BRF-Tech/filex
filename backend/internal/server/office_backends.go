package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/officeengine"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// officeBackends is the office engine's back ends and the choice between
// them (filex 0.57, task #268):
//
//   - the office engine an app brings - the Office editor's x2t and page
//     layout in WebAssembly, run in a process of its own
//     (internal/officeengine, the app's `engines.office`);
//   - the connected ONLYOFFICE Document Server (internal/onlyoffice), which
//     stays optional.
//
// The apps' office engine (`engines:office`, the Convert app, MCP's
// file_convert) and the office thumbnails both ask it, so the two always
// agree on who works. When both are there the administrator chooses (Default
// apps → the `engine` capability, internal/assoc); by default an app's
// engine goes first, and the Document Server takes the jobs while it is not
// running - being prepared after an install, say. The contracts do not move:
// wasmplugin.OfficeConverter and thumb.OfficeDrawer, the error classes, the
// output names.
type officeBackends struct {
	ds      *onlyoffice.Service
	dsConv  officeEngine
	dsThumb *officeThumbs
	// runner runs the app's engine (an *officeengine.Runner); nil when apps
	// are off on this instance.
	runner appEngine
	// assoc holds the administrator's order; nil when apps are off (the
	// Document Server alone, then).
	assoc *assoc.Service
	// apps lists the running apps' engines (wasmplugin.OfficeEngines).
	apps func() []wasmplugin.OfficeEnginePackage
	// hostVersion is this filex's: a new filex prepares the engine again.
	hostVersion string
	// workDir is where a thumbnail's document is put for the engine.
	workDir string
}

// appEngine is what the back ends need of the engine runner
// (*officeengine.Runner; a fake in tests).
type appEngine interface {
	Status() officeengine.Status
	Convert(ctx context.Context, c officeengine.Conversion) error
	DrawThumbnail(ctx context.Context, t officeengine.Thumbnail) ([]byte, error)
	SetPackage(p *officeengine.Package)
	Release(app string)
}

var (
	_ appEngine                  = (*officeengine.Runner)(nil)
	_ wasmplugin.OfficeConverter = (*officeBackends)(nil)
	_ wasmplugin.OfficePreparer  = (*officeBackends)(nil)
	_ wasmplugin.EngineHost      = (*officeBackends)(nil)
	_ thumb.OfficeDrawer         = (*officeBackends)(nil)
)

// newOfficeBackends builds the back ends. runner is nil (the interface, not a
// typed nil) when apps are off.
func newOfficeBackends(ds *onlyoffice.Service, runner appEngine, as *assoc.Service, apps func() []wasmplugin.OfficeEnginePackage, hostVersion string) *officeBackends {
	return &officeBackends{ds: ds, dsConv: officeEngine{svc: ds}, dsThumb: &officeThumbs{svc: ds}, runner: runner, assoc: as, apps: apps, hostVersion: hostVersion}
}

// dsReady: the Document Server is configured now.
func (b *officeBackends) dsReady(ctx context.Context) bool {
	return b.ds != nil && b.ds.EnabledCtx(ctx)
}

// chain is the back ends that are on, in the order they are asked.
func (b *officeBackends) chain(ctx context.Context) []assoc.Handler {
	if b.assoc == nil {
		if b.dsReady(ctx) {
			return []assoc.Handler{{ID: assoc.OnlyOffice}}
		}
		return nil
	}
	return b.assoc.EngineChain(ctx, assoc.EngineOffice).On
}

// pick is the back end that takes a job now: the first one on the chain that
// runs. preparing reports that an app's engine on the chain before it (or
// with nothing after it) is still being compiled.
func (b *officeBackends) pick(ctx context.Context) (h assoc.Handler, ok, preparing bool) {
	var st officeengine.Status
	if b.runner != nil {
		st = b.runner.Status()
	}
	for _, c := range b.chain(ctx) {
		switch {
		case c.IsOnlyOffice():
			if b.dsReady(ctx) {
				return c, true, preparing
			}
		case c.IsApp() && b.runner != nil && st.App == c.App:
			switch st.State {
			case officeengine.StateReady:
				return c, true, preparing
			case officeengine.StatePreparing:
				preparing = true
			}
		}
	}
	return assoc.Handler{}, false, preparing
}

// Ready reports whether a back end runs now (both contracts).
func (b *officeBackends) Ready(ctx context.Context) bool {
	_, ok, _ := b.pick(ctx)
	return ok
}

// OfficePreparing reports that nothing runs now because an app's engine is
// being compiled.
func (b *officeBackends) OfficePreparing(ctx context.Context) bool {
	_, ok, preparing := b.pick(ctx)
	return !ok && preparing
}

// Convert is the apps' office engine (wasmplugin.OfficeConverter).
func (b *officeBackends) Convert(ctx context.Context, req wasmplugin.OfficeRequest) error {
	h, ok, preparing := b.pick(ctx)
	switch {
	case !ok && preparing:
		return wasmplugin.ErrOfficePreparing
	case !ok:
		return wasmplugin.ErrOfficeUnconfigured
	case h.IsOnlyOffice():
		return b.dsConv.Convert(ctx, req)
	}
	err := b.runner.Convert(ctx, officeengine.Conversion{
		Src: req.Src, From: req.From, To: req.To, Delimiter: req.Delimiter, CodePage: req.CodePage,
		Dst: req.Dst, MaxBytes: req.MaxBytes,
	})
	return engineConvertError(ctx, err)
}

// engineConvertError says the app's engine's failure in the words the
// office engine's callers know (wasmplugin/office.go): the conversion API's
// numbers it already carries, the result's ceiling, the job's clock.
func engineConvertError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, officeengine.ErrPreparing):
		return wasmplugin.ErrOfficePreparing
	case errors.Is(err, officeengine.ErrNoEngine), errors.Is(err, officeengine.ErrClosed), errors.Is(err, officeengine.ErrFailed):
		return wasmplugin.ErrOfficeUnconfigured
	}
	if e, ok := officeengine.AsError(err); ok {
		if e.Over {
			return wasmplugin.ErrOfficeTooLarge
		}
		reason := e.What
		if e.Err != nil {
			reason += ": " + e.Err.Error()
		}
		return &wasmplugin.OfficeError{Code: e.Code, Reason: reason}
	}
	return &wasmplugin.OfficeError{Reason: err.Error()}
}

// DrawPage is the office thumbnail (thumb.OfficeDrawer): the Document
// Server's own job, or the same job - 320 x 320, the first page, a sheet laid
// out as DrawThumbnail lays it out - on the app's engine.
func (b *officeBackends) DrawPage(ctx context.Context, pg thumb.OfficePage) ([]byte, error) {
	h, ok, preparing := b.pick(ctx)
	switch {
	case !ok:
		what := "none"
		if preparing {
			what = "preparing"
		}
		return nil, &thumb.OfficeError{Class: thumb.OfficeUnconfigured, What: what}
	case h.IsOnlyOffice():
		return b.dsThumb.DrawPage(ctx, pg)
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(pg.Name)), ".")
	if ext == "" {
		return nil, &thumb.OfficeError{Class: thumb.OfficeCorrupt, What: "type"}
	}
	if pg.Source == nil {
		return nil, &thumb.OfficeError{Class: thumb.OfficeTransient, What: "source"}
	}
	src, cleanup, err := b.stage(ctx, pg, ext)
	if err != nil {
		return nil, &thumb.OfficeError{Class: thumb.OfficeTransient, What: "source", Err: err}
	}
	defer cleanup()
	png, err := b.runner.DrawThumbnail(ctx, officeengine.Thumbnail{
		Src: src, From: ext, Size: onlyoffice.ThumbSize, Params: onlyoffice.ThumbLayoutParams(ext), MaxBytes: pg.MaxBytes,
	})
	if err != nil {
		return nil, engineThumbError(ctx, err)
	}
	return png, nil
}

// stage puts the document where the engine process reads it: a file of its
// own, removed when the picture is drawn.
func (b *officeBackends) stage(ctx context.Context, pg thumb.OfficePage, ext string) (string, func(), error) {
	rc, err := pg.Source(ctx)
	if err != nil {
		return "", nil, err
	}
	defer rc.Close()
	dir, err := os.MkdirTemp(b.workDir, "filex-office-src-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "document."+ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// engineThumbError is the app's engine's failure in the pipeline's words
// (thumb/office.go, the table): classed as the Document Server's code would
// be (onlyoffice.ClassOf), so a document the engine cannot read is marked as
// one the Document Server could not.
func engineThumbError(ctx context.Context, err error) *thumb.OfficeError {
	if ctx.Err() != nil {
		return &thumb.OfficeError{Class: thumb.OfficeTransient, What: "timeout", Err: err}
	}
	switch {
	case errors.Is(err, officeengine.ErrPreparing):
		return &thumb.OfficeError{Class: thumb.OfficeUnconfigured, What: "preparing", Err: err}
	case errors.Is(err, officeengine.ErrNoEngine), errors.Is(err, officeengine.ErrClosed), errors.Is(err, officeengine.ErrFailed):
		return &thumb.OfficeError{Class: thumb.OfficeUnconfigured, What: "none", Err: err}
	}
	e, ok := officeengine.AsError(err)
	if !ok {
		return &thumb.OfficeError{Class: thumb.OfficeTransient, What: "failed", Err: err}
	}
	if e.Over {
		// A picture over the answer's ceiling: another try draws the same.
		return &thumb.OfficeError{Class: thumb.OfficeCorrupt, What: "large-image", Err: err}
	}
	class := thumb.OfficeTransient
	switch onlyoffice.ClassOf(e.Code) {
	case onlyoffice.ClassCorrupt:
		class = thumb.OfficeCorrupt
	case onlyoffice.ClassPassword:
		class = thumb.OfficePassword
	case onlyoffice.ClassTooLarge:
		class = thumb.OfficeTooLarge
	}
	return &thumb.OfficeError{Class: class, What: e.What, Err: err}
}

// ── The apps' engines ──────────────────────────────────────────────────

// EnginesChanged runs the engine of the first app on the chain that brings
// one (wasmplugin.EngineHost): an app the administrator switched off there
// runs nothing; when the Document Server goes first, the app's engine is
// still prepared, so moving it up later needs no wait.
func (b *officeBackends) EnginesChanged() {
	if b.runner == nil {
		return
	}
	pkgs := map[string]wasmplugin.OfficeEnginePackage{}
	for _, p := range b.apps() {
		pkgs[p.App] = p
	}
	var pick *officeengine.Package
	if len(pkgs) > 0 && b.assoc != nil {
		for _, h := range b.assoc.EngineChain(context.Background(), assoc.EngineOffice).On {
			p, ok := pkgs[h.App]
			if !h.IsApp() || !ok {
				continue
			}
			pick = &officeengine.Package{
				App: p.App, Version: p.Version, Engine: p.Engine, EngineSHA256: p.EngineSHA256,
				UI: p.UI, UISHA256: p.UISHA256, EditorDir: p.EditorDir, HostVersion: b.hostVersion,
			}
			break
		}
	}
	b.runner.SetPackage(pick)
}

// ReleaseEngine lets go of an app's engine files (wasmplugin.EngineHost).
func (b *officeBackends) ReleaseEngine(app string) {
	if b.runner != nil {
		b.runner.Release(app)
	}
}

// States is every back end of the office engine as Default apps shows it:
// the Document Server (ready, or not connected) and each app's engine
// (preparing, ready, failed - with why - or idle: not the one running).
func (b *officeBackends) States(ctx context.Context) map[string]handlers.EngineBackendState {
	out := map[string]handlers.EngineBackendState{}
	if b.dsReady(ctx) {
		out[assoc.OnlyOffice] = handlers.EngineBackendState{State: string(officeengine.StateReady)}
	} else {
		out[assoc.OnlyOffice] = handlers.EngineBackendState{State: "off"}
	}
	var st officeengine.Status
	if b.runner != nil {
		st = b.runner.Status()
	}
	if b.apps == nil {
		return out
	}
	for _, p := range b.apps() {
		id := assoc.EngineID(p.App)
		if b.runner != nil && st.App == p.App && st.State != officeengine.StateAbsent {
			since := st.Since
			out[id] = handlers.EngineBackendState{State: string(st.State), Error: st.Error, Since: &since}
			continue
		}
		out[id] = handlers.EngineBackendState{State: "idle"}
	}
	return out
}

// officeEngineCacheDir is where the engine's compiled modules are kept.
func officeEngineCacheDir(dataDir string) string { return filepath.Join(dataDir, "office-engine") }

// officeEngineIdle is how long an engine process nobody uses is kept.
const officeEngineIdle = 3 * time.Minute

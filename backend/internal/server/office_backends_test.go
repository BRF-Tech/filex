package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/officeengine"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// The office engine's two back ends (office_backends.go): the engine the
// Office editor brings and the Document Server, and who takes a job. The
// engine runner is a fake here (its own tests are internal/officeengine's);
// what is measured is the choice, the words a failure is said in, and that
// the thumbnails do not care which back end drew them.
//
// RED before 0.57: the office engine was the Document Server alone
// (officeEngine{svc}, officeThumbs{svc}); there was no app engine to choose.

// fakeAppEngine is the engine runner: its state, and what it was asked.
type fakeAppEngine struct {
	mu       sync.Mutex
	status   officeengine.Status
	convErr  error
	conv     []officeengine.Conversion
	thumbs   []officeengine.Thumbnail
	sources  [][]byte
	png      []byte
	thumbErr error
	pkgs     []*officeengine.Package
	released []string
}

func (f *fakeAppEngine) Status() officeengine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeAppEngine) set(state officeengine.State) {
	f.mu.Lock()
	f.status = officeengine.Status{State: state, App: "office-editor", Version: "0.4.0"}
	f.mu.Unlock()
}

func (f *fakeAppEngine) Convert(_ context.Context, c officeengine.Conversion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conv = append(f.conv, c)
	if f.convErr != nil {
		return f.convErr
	}
	return os.WriteFile(c.Dst, []byte("engine:"+c.From+"->"+c.To), 0o600)
}

func (f *fakeAppEngine) DrawThumbnail(_ context.Context, t officeengine.Thumbnail) ([]byte, error) {
	src, err := os.ReadFile(t.Src)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thumbs = append(f.thumbs, t)
	f.sources = append(f.sources, src)
	if err != nil {
		return nil, err
	}
	if f.thumbErr != nil {
		return nil, f.thumbErr
	}
	return f.png, nil
}

func (f *fakeAppEngine) SetPackage(p *officeengine.Package) {
	f.mu.Lock()
	f.pkgs = append(f.pkgs, p)
	f.mu.Unlock()
}

func (f *fakeAppEngine) Release(app string) {
	f.mu.Lock()
	f.released = append(f.released, app)
	f.mu.Unlock()
}

// engineApps is the registry as Default apps reads it: one app that brings
// an office engine.
type engineApps struct{}

func (engineApps) OpenHandlers() []assoc.AppHandler      { return nil }
func (engineApps) ThumbnailHandlers() []assoc.AppHandler { return nil }
func (engineApps) EngineHandlers() []assoc.AppHandler {
	return []assoc.AppHandler{{Handler: assoc.Handler{ID: assoc.EngineID("office-editor"), App: "office-editor", Version: "0.4.0",
		Label: wire.Text{"en": "Office editor"}}, Ext: []string{assoc.EngineOffice}}}
}

func engineAppsPackages() []wasmplugin.OfficeEnginePackage {
	return []wasmplugin.OfficeEnginePackage{{App: "office-editor", Version: "0.4.0", Engine: "engine.zip", EngineSHA256: "e1",
		UI: "ui.zip", UISHA256: "u1", EditorDir: "editor"}}
}

// backendsWorld is the back ends as the server wires them, over a real rules
// store: ds may be nil (no Document Server connected).
func backendsWorld(t *testing.T, ds *onlyoffice.Service) (*officeBackends, *fakeAppEngine, *assoc.Service, *model.Storage, *local.Driver, string, db.Store) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	if ds == nil {
		ds = onlyoffice.New(nil, nil, "", "", "https://filex.test", 0)
	}
	as := assoc.New(store)
	as.SetSource(engineApps{})
	as.SetOnlyOfficeEngine(func() bool { return ds.EnabledCtx(context.Background()) })
	app := &fakeAppEngine{status: officeengine.Status{State: officeengine.StateAbsent}}
	b := newOfficeBackends(ds, app, as, engineAppsPackages, "test")
	as.SetOnlyOfficeThumb(func(name, mime string) bool {
		return thumb.OnlyOfficeDraws(name, mime) && b.Ready(context.Background())
	})
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{Name: "belgeler", Driver: "local", MountPath: "/belgeler", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	require.NoError(t, err)
	return b, app, as, st, drv, root, store
}

func backendRequest(t *testing.T) wasmplugin.OfficeRequest {
	t.Helper()
	dir := t.TempDir()
	return engineRequest(t, dir, "letter.docx", "DOCX", "pdf")
}

// By default the app's engine goes first; while it is being prepared the
// Document Server takes the jobs; the administrator may put the Document
// Server first, or switch the app's engine off.
func TestOfficeBackends_WhoTakesAJob(t *testing.T) {
	ctx := context.Background()
	ds := newOfficeDS(t)
	b, app, as, _, _, _, _ := backendsWorld(t, ds.svc)

	// The handlers' ids - what a rule stores, so an upgrade keeps the choice;
	// Keys() is how a thumbnail row records them (id@version).
	var on []string
	for _, h := range as.EngineChain(ctx, assoc.EngineOffice).On {
		on = append(on, h.ID)
	}
	assert.Equal(t, []string{"app:office-editor", "onlyoffice"}, on, "the app's engine first, the Document Server after it")

	app.set(officeengine.StatePreparing)
	assert.True(t, b.Ready(ctx), "the Document Server takes the jobs meanwhile")
	assert.False(t, b.OfficePreparing(ctx))
	require.NoError(t, b.Convert(ctx, backendRequest(t)))
	assert.Len(t, ds.payloads, 1, "the Document Server converted it")
	assert.Empty(t, app.conv)

	app.set(officeengine.StateReady)
	req := backendRequest(t)
	require.NoError(t, b.Convert(ctx, req))
	require.Len(t, app.conv, 1, "the app's engine converted it")
	assert.Len(t, ds.payloads, 1)
	assert.Equal(t, officeengine.Conversion{Src: req.Src, From: "docx", To: "pdf", Dst: req.Dst, MaxBytes: 1 << 20}, app.conv[0])
	out, err := os.ReadFile(req.Dst)
	require.NoError(t, err)
	assert.Equal(t, "engine:docx->pdf", string(out))

	_, err = as.Put(ctx, assoc.CapEngine, assoc.EngineOffice, assoc.Rule{Order: []string{"onlyoffice"}}, nil)
	require.NoError(t, err)
	require.NoError(t, b.Convert(ctx, backendRequest(t)))
	assert.Len(t, ds.payloads, 2, "the administrator put the Document Server first")
	assert.Len(t, app.conv, 1)

	_, err = as.Put(ctx, assoc.CapEngine, assoc.EngineOffice, assoc.Rule{Off: []string{"app:office-editor"}}, nil)
	require.NoError(t, err)
	b.EnginesChanged()
	app.mu.Lock()
	last := app.pkgs[len(app.pkgs)-1]
	app.mu.Unlock()
	assert.Nil(t, last, "an engine switched off is not run")

	_, err = as.Reset(ctx, assoc.CapEngine, assoc.EngineOffice)
	require.NoError(t, err)
	b.EnginesChanged()
	app.mu.Lock()
	last = app.pkgs[len(app.pkgs)-1]
	app.mu.Unlock()
	require.NotNil(t, last)
	assert.Equal(t, officeengine.Package{App: "office-editor", Version: "0.4.0", Engine: "engine.zip", EngineSHA256: "e1",
		UI: "ui.zip", UISHA256: "u1", EditorDir: "editor", HostVersion: "test"}, *last)

	b.ReleaseEngine("office-editor")
	assert.Equal(t, []string{"office-editor"}, app.released)
}

// No Document Server: while the app's engine is being prepared nothing runs
// and the answer says so; failed or absent, it is not configured.
func TestOfficeBackends_PreparingWithoutADocumentServer(t *testing.T) {
	ctx := context.Background()
	b, app, _, _, _, _, _ := backendsWorld(t, nil)

	app.set(officeengine.StatePreparing)
	assert.False(t, b.Ready(ctx))
	assert.True(t, b.OfficePreparing(ctx))
	assert.ErrorIs(t, b.Convert(ctx, backendRequest(t)), wasmplugin.ErrOfficePreparing)
	_, err := b.DrawPage(ctx, thumb.OfficePage{Name: "a.docx"})
	var oe *thumb.OfficeError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, thumb.OfficeUnconfigured, oe.Class)
	assert.Equal(t, "preparing", oe.What)

	for _, s := range []officeengine.State{officeengine.StateFailed, officeengine.StateAbsent} {
		app.set(s)
		assert.False(t, b.OfficePreparing(ctx), s)
		assert.ErrorIs(t, b.Convert(ctx, backendRequest(t)), wasmplugin.ErrOfficeUnconfigured, s)
	}

	states := b.States(ctx)
	assert.Equal(t, "off", states["onlyoffice"].State)
	app.set(officeengine.StatePreparing)
	states = b.States(ctx)
	assert.Equal(t, "preparing", states["app:office-editor"].State)
	require.NotNil(t, states["app:office-editor"].Since)
}

// The engine's failures are said in the words the office engine's callers
// know: the Document Server's numbers, the result's ceiling, the clock.
func TestOfficeBackends_TheEnginesFailuresInTheCallersWords(t *testing.T) {
	ctx := context.Background()
	b, app, _, _, _, _, _ := backendsWorld(t, nil)
	app.set(officeengine.StateReady)

	app.convErr = &officeengine.Error{Code: officeengine.CodePassword, What: "x2t-91"}
	var oe *wasmplugin.OfficeError
	require.ErrorAs(t, b.Convert(ctx, backendRequest(t)), &oe)
	assert.Equal(t, -5, oe.Code, "the Document Server's password error: the same sentence")

	app.convErr = &officeengine.Error{Code: officeengine.CodeTooLarge, What: "result", Over: true}
	assert.ErrorIs(t, b.Convert(ctx, backendRequest(t)), wasmplugin.ErrOfficeTooLarge, "the result's ceiling, not the input's")

	app.convErr = &officeengine.Error{Code: officeengine.CodeTooLarge, What: "memory"}
	require.ErrorAs(t, b.Convert(ctx, backendRequest(t)), &oe)
	assert.Equal(t, -10, oe.Code)

	app.convErr = officeengine.ErrPreparing
	assert.ErrorIs(t, b.Convert(ctx, backendRequest(t)), wasmplugin.ErrOfficePreparing)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	app.convErr = errors.New("killed")
	assert.ErrorIs(t, b.Convert(cctx, backendRequest(t)), context.Canceled, "the job's clock decides")

	for code, want := range map[int]string{
		officeengine.CodeCorrupt:  thumb.OfficeCorrupt,
		officeengine.CodeParams:   thumb.OfficeCorrupt,
		officeengine.CodePassword: thumb.OfficePassword,
		officeengine.CodeTooLarge: thumb.OfficeTooLarge,
		officeengine.CodeUnknown:  thumb.OfficeTransient,
	} {
		got := engineThumbError(ctx, &officeengine.Error{Code: code, What: "x2t-86"})
		assert.Equal(t, want, got.Class, "code %d", code)
		assert.Equal(t, "x2t-86", got.What)
	}
	assert.Equal(t, thumb.OfficeUnconfigured, engineThumbError(ctx, officeengine.ErrNoEngine).Class)
}

func solidPagePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// The app's engine draws an office thumbnail as the Document Server does:
// the same drawer on the row ("onlyoffice"), the document's own bytes
// through the pipeline's reader, 320 x 320, a sheet laid out as the Document
// Server lays it out. And a picture the Document Server drew stays fresh
// when the app's engine takes over (decided, PARITY A1): the drawer is the
// same.
func TestOfficeBackends_TheAppsEngineIsTheSameThumbnailDrawer(t *testing.T) {
	ctx := context.Background()
	b, app, as, st, drv, root, store := backendsWorld(t, nil)
	app.set(officeengine.StateReady)
	app.png = solidPagePNG(t, 226, 320)

	body := append([]byte("PK\x03\x04"), make([]byte, 128)...)
	require.NoError(t, os.WriteFile(filepath.Join(root, "Bütçe.xlsx"), body, 0o644))
	n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "Bütçe.xlsx", Path: "/Bütçe.xlsx",
		PathHash: pathkey.Hash(st.ID, "/Bütçe.xlsx"), Type: model.NodeTypeFile, Size: int64(len(body)), Mime: "application/zip"})
	require.NoError(t, err)

	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true, SVG: true})
	pipe.AttachStorage(st.ID, drv)
	pipe.AttachApps(&appThumbs{assoc: as})
	pipe.AttachOffice(b)
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", row.State, row.Error)
	assert.Equal(t, "onlyoffice", row.Generator, "the same drawer, whichever back end ran")

	app.mu.Lock()
	require.Len(t, app.thumbs, 1)
	asked, src := app.thumbs[0], app.sources[0]
	app.mu.Unlock()
	assert.Equal(t, "xlsx", asked.From)
	assert.Equal(t, onlyoffice.ThumbSize, asked.Size)
	assert.Contains(t, string(asked.Params), `"spreadsheetLayout"`, "a sheet is laid out as the Document Server lays it out")
	assert.Equal(t, body, src, "the document's own bytes, read through the pipeline")
	_, err = os.Stat(asked.Src)
	assert.True(t, os.IsNotExist(err), "the staged copy is removed once the picture is drawn")

	// A row the Document Server drew: fresh with the app's engine running.
	drawn := time.Now().Add(-time.Hour)
	dsRow := &model.Thumbnail{NodeID: n.ID, State: "ready", StorageKey: "k", SourceSig: thumb.SourceSig(n), Generator: "onlyoffice",
		GeneratedAt: &drawn, AttemptedAt: &drawn, Attempts: `[{"h":"onlyoffice","r":"ok"}]`}
	assert.Equal(t, thumb.Leave, pipe.Assess(n, dsRow, time.Now()), "the Document Server's picture does not go stale")
	assert.Equal(t, []string{"onlyoffice"}, as.Chain(ctx, assoc.CapThumbnail, "Bütçe.xlsx", "application/zip").Keys())
}

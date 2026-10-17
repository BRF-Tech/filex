package wasmplugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── engines.office: an app that brings the office engine (filex 0.57) ────
//
// The Office editor brings the work a Document Server did on the server -
// office conversions for apps and office thumbnails - as engine.zip, named in
// its manifest's `engines.office` with its SHA-256. The registry checks it at
// install, keeps it beside the interface bundle as it came, holds it to its
// pin at every load, and tells the server's engine runner what runs - and to
// let go of the files before an upgrade moves them.
//
// All red before 0.57: the block, the derived grant, the `engine` part and
// the engine host were unknown (the manifest was refused as an unknown field).

const fakeWasm = "\x00asm\x01\x00\x00\x00"

// engineZip is an office engine package: the two modules (only their magic),
// the layout's scripts and whatever extra says.
func engineZip(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	files := map[string]string{
		"x2t-server.wasm":           fakeWasm,
		"layout-host.wasm":          fakeWasm,
		"layout/worker.js":          "print(JSON.stringify({ready:true}))",
		"layout/fonts_ie.js":        "var fonts;",
		"layout/xregexp-all-min.js": "var XRegExp;",
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	return uiZip(t, files)
}

// officeEditorBundle is an interface bundle with the editor's files where the
// engine reads them (editor/sdkjs/, editor/fonts/).
func officeEditorBundle(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	files := uiDoc()
	files["editor/sdkjs/word/sdk-all.js"] = "var sdk;"
	files["editor/sdkjs/common/AllFonts.js"] = "var fonts = [];"
	files["editor/fonts/000"] = "FONT"
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	return uiZip(t, files)
}

// officeEngineManifest is the Office editor's manifest shape: only an
// interface, and an engine pinned to sum.
func officeEngineManifest(t *testing.T, sum, version string, mutate func(m map[string]any)) []byte {
	t.Helper()
	return uiManifest(t, func(m map[string]any) {
		m["name"] = "office-editor"
		m["version"] = version
		m["label"] = map[string]any{"en": "Office editor"}
		m["views"] = []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "index.html",
			"label": map[string]any{"en": "Office editor"}, "applies": map[string]any{"ext": []any{"docx"}}}}
		m["engines"] = map[string]any{"office": map[string]any{"url": "engine.zip", "sha256": sum}}
		if mutate != nil {
			mutate(m)
		}
	})
}

// fakeEngineHost records what the registry tells the engine runner.
type fakeEngineHost struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeEngineHost) EnginesChanged() { f.add("changed") }

func (f *fakeEngineHost) ReleaseEngine(app string) { f.add("release:" + app) }

func (f *fakeEngineHost) add(e string) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}

func (f *fakeEngineHost) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeEngineHost) reset() {
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
}

func TestOfficeEngine_TheBlockDerivesItsGrantAndNeedsNoModule(t *testing.T) {
	m := mustParse(t, officeEngineManifest(t, strings.Repeat("ab", 32), "0.4.0", nil))
	assert.Contains(t, m.Perms, PermOfficeEngine)
	assert.False(t, m.NeedsModule(), "the engine runs in filex's engine process, never in a module: the office editor stays only an interface")
	assert.Equal(t, "editor", engineEditorDir(officeEngineOf(m)), "where the Office editor keeps the editor's files")

	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if Permission(r.ID) == PermOfficeEngine {
				labels[lang] = r.Label
			}
		}
	}
	assert.Contains(t, labels["en"], "office engine", "a sentence the review says, never the id")
	assert.Contains(t, labels["tr"], "ofis motoru")

	p, err := ParsePermission("office-engine")
	require.NoError(t, err, "a stored grant reads back")
	assert.Equal(t, PermOfficeEngine, p)
	assert.NotContains(t, mustParse(t, uiManifest(t, nil)).Perms, PermOfficeEngine, "only with the block")
}

func TestOfficeEngine_TheManifestRefusesWhatTheEngineCannotBe(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for name, mutate := range map[string]func(m map[string]any){
		"the grant written by hand": func(m map[string]any) {
			m["permissions"] = append(m["permissions"].([]any), "office-engine")
		},
		"no interface to read the editor from": func(m map[string]any) {
			delete(m, "ui")
			m["views"] = []any{map[string]any{"id": "s", "placement": "modal", "label": map[string]any{"en": "S"}}}
		},
		"no pin":      func(m map[string]any) { m["engines"] = map[string]any{"office": map[string]any{"url": "engine.zip"}} },
		"a short pin": func(m map[string]any) { m["engines"] = map[string]any{"office": map[string]any{"sha256": "abc"}} },
		"an editor outside": func(m map[string]any) {
			m["engines"] = map[string]any{"office": map[string]any{"sha256": sum, "editor": "../x"}}
		},
		"no engine named":   func(m map[string]any) { m["engines"] = map[string]any{} },
		"an unknown engine": func(m map[string]any) { m["engines"] = map[string]any{"video": map[string]any{"sha256": sum}} },
	} {
		_, err := ParseManifest(officeEngineManifest(t, sum, "0.4.0", mutate))
		assert.Error(t, err, name)
	}
}

// Installed: engine.zip kept as it came beside the interface, the running
// app listed for the engine runner, the runner told.
func TestOfficeEngine_AnInstallKeepsTheEngineAndTellsTheRunner(t *testing.T) {
	h := newBareHarness(t, nil)
	host := &fakeEngineHost{}
	h.reg.SetEngineHost(host)
	assert.Equal(t, []string{"changed"}, host.seen(), "wiring the host tells it what runs now")
	host.reset()

	engine := engineZip(t, nil)
	manifest := officeEngineManifest(t, hexSum(engine), "0.4.0", nil)
	m := mustParse(t, manifest)
	st, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: manifest, UI: bytes.NewReader(officeEditorBundle(t, nil)), Engine: bytes.NewReader(engine),
		Source: "upload", Granted: permStrings(m.Perms), Lang: "en",
	})
	require.NoError(t, err)
	assert.Contains(t, host.seen(), "changed")

	kept, err := os.ReadFile(filepath.Join(h.reg.Dir(), "office-editor", engineZipName))
	require.NoError(t, err)
	assert.Equal(t, engine, kept, "kept as it came, never unpacked")

	pkgs := h.reg.OfficeEngines()
	require.Len(t, pkgs, 1)
	assert.Equal(t, "office-editor", pkgs[0].App)
	assert.Equal(t, "0.4.0", pkgs[0].Version)
	assert.Equal(t, hexSum(engine), pkgs[0].EngineSHA256)
	assert.Equal(t, filepath.Join(h.reg.Dir(), "office-editor", engineZipName), pkgs[0].Engine)
	assert.Equal(t, filepath.Join(h.reg.Dir(), "office-editor", uiZipName), pkgs[0].UI)
	assert.Equal(t, "editor", pkgs[0].EditorDir)
	assert.NotEmpty(t, pkgs[0].UISHA256)

	hs := h.reg.EngineHandlers()
	require.Len(t, hs, 1)
	assert.Equal(t, "app:office-editor", hs[0].ID)
	assert.Equal(t, []string{"office"}, hs[0].Ext)

	// Switched off: no engine runs; on again: it does.
	host.reset()
	_, err = h.reg.SetEnabled(context.Background(), st.ID, false)
	require.NoError(t, err)
	assert.Empty(t, h.reg.OfficeEngines())
	assert.Contains(t, host.seen(), "changed")
	_, err = h.reg.SetEnabled(context.Background(), st.ID, true)
	require.NoError(t, err)
	assert.Len(t, h.reg.OfficeEngines(), 1)

	// Held by its license: no engine runs.
	h.reg.SetLicenseHold("office-editor", "revoked")
	assert.Empty(t, h.reg.OfficeEngines())
	h.reg.SetLicenseHold("office-editor", "")
	assert.Len(t, h.reg.OfficeEngines(), 1)
}

func TestOfficeEngine_AnInstallRefusesAnEngineThatIsNotRight(t *testing.T) {
	good := engineZip(t, nil)
	for name, c := range map[string]struct {
		engine   []byte
		pin      string
		bundle   []byte
		noEngine bool
		code     string
	}{
		"another engine than the pin": {engine: good, pin: strings.Repeat("ab", 32), code: ErrCodeSHA256Mismatch},
		"no x2t":                      {engine: engineZip(t, map[string]string{"x2t-server.wasm": ""}), code: ErrCodeManifestInvalid},
		"no layout worker":            {engine: engineZip(t, map[string]string{"layout/worker.js": ""}), code: ErrCodeManifestInvalid},
		"a module that is not wasm":   {engine: engineZip(t, map[string]string{"layout-host.wasm": "#!/bin/sh"}), code: ErrCodeManifestInvalid},
		"a name that climbs":          {engine: engineZip(t, map[string]string{"layout/../../x.js": "x"}), code: ErrCodeManifestInvalid},
		"not a zip":                   {engine: []byte("not a zip"), code: ErrCodeManifestInvalid},
		"no engine supplied":          {noEngine: true, code: ErrCodeManifestInvalid},
		"a bundle without the editor": {engine: good, bundle: officeEditorBundle(t, map[string]string{"editor/sdkjs/word/sdk-all.js": "", "editor/sdkjs/common/AllFonts.js": ""}), code: ErrCodeManifestInvalid},
		"a bundle without the fonts":  {engine: good, bundle: officeEditorBundle(t, map[string]string{"editor/fonts/000": ""}), code: ErrCodeManifestInvalid},
	} {
		h := newBareHarness(t, nil)
		pin := c.pin
		if pin == "" {
			pin = hexSum(c.engine)
		}
		bundle := c.bundle
		if bundle == nil {
			bundle = officeEditorBundle(t, nil)
		}
		manifest := officeEngineManifest(t, pin, "0.4.0", nil)
		in := &InstallInput{Manifest: manifest, UI: bytes.NewReader(bundle), Source: "upload",
			Granted: permStrings(mustParse(t, manifest).Perms), Lang: "en"}
		if !c.noEngine {
			in.Engine = bytes.NewReader(c.engine)
		}
		_, _, err := h.reg.Install(context.Background(), in)
		var ie *InstallError
		require.ErrorAs(t, err, &ie, name)
		assert.Equal(t, c.code, ie.Code, "%s: %s", name, ie.Message)
		assert.Empty(t, h.reg.OfficeEngines(), name)
	}

	// An engine supplied to an app that brings none is a mistake said out loud.
	h := newBareHarness(t, nil)
	manifest := uiManifest(t, nil)
	_, _, err := h.reg.Install(context.Background(), &InstallInput{Manifest: manifest, UI: bytes.NewReader(uiZip(t, uiDoc())),
		Engine: bytes.NewReader(good), Source: "upload", Granted: permStrings(mustParse(t, manifest).Perms)})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeManifestInvalid, ie.Code)
}

// The at-rest check: an engine on disk that is no longer the bytes approved
// fails the app, and no engine runs from it.
func TestOfficeEngine_ALoadThatFindsOtherBytesFailsTheApp(t *testing.T) {
	h := newBareHarness(t, nil)
	engine := engineZip(t, nil)
	manifest := officeEngineManifest(t, hexSum(engine), "0.4.0", nil)
	m := mustParse(t, manifest)
	st, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: manifest, UI: bytes.NewReader(officeEditorBundle(t, nil)), Engine: bytes.NewReader(engine),
		Source: "upload", Granted: permStrings(m.Perms),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(h.reg.Dir(), "office-editor", engineZipName), engineZip(t, map[string]string{"layout/extra.js": "x"}), 0o600))

	_, err = h.reg.SetEnabled(context.Background(), st.ID, false)
	require.NoError(t, err)
	_, err = h.reg.SetEnabled(context.Background(), st.ID, true)
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	state, why := p.State()
	assert.Equal(t, StateFailed, state)
	assert.Contains(t, why, "office engine")
	assert.Empty(t, h.reg.OfficeEngines())
}

// An upgrade lets the runner go of the app's files before it moves them (on
// Windows a file a process holds open cannot be moved), then tells it what
// runs; a removal the same. Going back brings the kept engine back.
func TestOfficeEngine_UpgradeRemoveAndRollBackReleaseTheEngineFirst(t *testing.T) {
	h := newBareHarness(t, nil)
	host := &fakeEngineHost{}
	h.reg.SetEngineHost(host)
	v1 := engineZip(t, nil)
	m1 := officeEngineManifest(t, hexSum(v1), "0.4.0", nil)
	st, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: m1, UI: bytes.NewReader(officeEditorBundle(t, nil)), Engine: bytes.NewReader(v1),
		Source: "upload", Granted: permStrings(mustParse(t, m1).Perms),
	})
	require.NoError(t, err)

	host.reset()
	v2 := engineZip(t, map[string]string{"layout/worker.js": "print('v2')"})
	m2 := officeEngineManifest(t, hexSum(v2), "0.5.0", nil)
	_, _, err = h.reg.Upgrade(context.Background(), st.ID, &InstallInput{
		Manifest: m2, UI: bytes.NewReader(officeEditorBundle(t, nil)), Engine: bytes.NewReader(v2),
		Source: "upload", Granted: permStrings(mustParse(t, m2).Perms),
	})
	require.NoError(t, err)
	seen := host.seen()
	require.NotEmpty(t, seen)
	assert.Equal(t, "release:office-editor", seen[0], "the files are let go of before anything moves")
	assert.Equal(t, "changed", seen[len(seen)-1], "and the runner is told what runs after")
	pkgs := h.reg.OfficeEngines()
	require.Len(t, pkgs, 1)
	assert.Equal(t, hexSum(v2), pkgs[0].EngineSHA256)
	assert.Equal(t, "0.5.0", pkgs[0].Version)

	host.reset()
	_, err = h.reg.Rollback(context.Background(), st.ID, nil, "en")
	require.NoError(t, err)
	pkgs = h.reg.OfficeEngines()
	require.Len(t, pkgs, 1)
	assert.Equal(t, hexSum(v1), pkgs[0].EngineSHA256, "the kept version's engine came back with it")
	assert.Equal(t, "release:office-editor", host.seen()[0])

	host.reset()
	require.NoError(t, h.reg.Remove(context.Background(), st.ID))
	assert.Equal(t, []string{"release:office-editor", "changed"}, host.seen())
	assert.Empty(t, h.reg.OfficeEngines())
}

// A module that describes another engine than its manifest pins is refused,
// as for the interface: the signature covers the module.
func TestOfficeEngine_AModuleMustDescribeTheSameEngine(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	m := mustParse(t, officeEngineManifest(t, sum, "0.4.0", nil))
	r := &Registry{}
	same := m.Manifest
	assert.NoError(t, r.checkDescribedUI(m, &same))

	other := m.Manifest
	other.Engines = &wire.EnginesSpec{Office: &wire.OfficeEngineSpec{SHA256: strings.Repeat("cd", 32)}}
	assert.ErrorContains(t, r.checkDescribedUI(m, &other), "office engine")

	none := m.Manifest
	none.Engines = nil
	assert.ErrorContains(t, r.checkDescribedUI(m, &none), "office engine")
}

// preparingOffice is a back end whose engine is being compiled.
type preparingOffice struct{ fakeOffice }

func (p *preparingOffice) OfficePreparing(context.Context) bool { return true }

// While the app's engine is being compiled (and nothing else converts) the
// office engine is unavailable with a sentence that says so, and a job that
// fails on it is office_preparing - a moment's wait, not a missing piece.
func TestOfficeEngine_BeingPreparedIsSaidAndClassified(t *testing.T) {
	s, ref := officeScope(t, &preparingOffice{}, "in.docx", "DOCX", "engines:office")
	_, err := runEngine(t, s, map[string]any{
		"engine": "office", "args": []string{"--convert-to", "pdf", "in.docx"},
		"inputs": map[string]string{"in.docx": ref},
	})
	he := asHostError(err)
	require.NotNil(t, he, "%v", err)
	assert.Equal(t, wire.ErrUnavailable, he.Code)
	assert.Contains(t, he.Message, "being prepared")
	code, engine := classifyJobError(model.AppPluginJobFailed, he.Message)
	assert.Equal(t, "office_preparing", code)
	assert.Equal(t, "office", engine)

	// The engine said "ready" and then "being prepared" (an upgrade between
	// the two): the same answer.
	ds := &fakeOffice{ready: true, answer: func(OfficeRequest, []byte) ([]byte, error) { return nil, ErrOfficePreparing }}
	s, ref = officeScope(t, ds, "in.docx", "DOCX", "engines:office")
	_, err = runEngine(t, s, map[string]any{
		"engine": "office", "args": []string{"--convert-to", "pdf", "in.docx"},
		"inputs": map[string]string{"in.docx": ref},
	})
	he = asHostError(err)
	require.NotNil(t, he, "%v", err)
	assert.Contains(t, he.Message, "being prepared")

	// Not there at all: the sentence names the app first.
	s, ref = officeScope(t, nil, "in.docx", "DOCX", "engines:office")
	_, err = runEngine(t, s, map[string]any{
		"engine": "office", "args": []string{"--convert-to", "pdf", "in.docx"},
		"inputs": map[string]string{"in.docx": ref},
	})
	he = asHostError(err)
	require.NotNil(t, he, "%v", err)
	assert.Contains(t, he.Message, "Office editor app")
	code, _ = classifyJobError(model.AppPluginJobFailed, he.Message)
	assert.Equal(t, "office_unconfigured", code)
}

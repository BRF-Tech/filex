package wasmplugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/enginebin"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// fakeOffice is the office engine's back end in tests: a document server that
// is connected (ready) or not, records what it was asked and answers with
// `answer` (default: "converted:<from>-><to>:" and the input).
type fakeOffice struct {
	ready  bool
	answer func(req OfficeRequest, in []byte) ([]byte, error)

	mu    sync.Mutex
	calls []OfficeRequest
}

func (f *fakeOffice) Ready(context.Context) bool { return f.ready }

func (f *fakeOffice) Convert(_ context.Context, req OfficeRequest) error {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if !f.ready {
		return ErrOfficeUnconfigured
	}
	in, err := os.ReadFile(req.Src)
	if err != nil {
		return err
	}
	out := append([]byte("converted:"+req.From+"->"+req.To+":"), in...)
	if f.answer != nil {
		if out, err = f.answer(req, in); err != nil {
			return err
		}
	}
	return os.WriteFile(req.Dst, out, 0o600)
}

// officeScope is the smallest action-job Scope engine_run runs in: one
// input file, the given grants, the office engine behind office.
func officeScope(t *testing.T, office OfficeConverter, inputName, content string, perms ...Permission) (*Scope, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "spooled-"+inputName)
	require.NoError(t, os.WriteFile(src, []byte(content), 0o600))
	reg := &Registry{opts: Options{MaxOutputBytes: 1 << 20}, engines: &engineSet{bins: map[string]string{}, office: office}}
	p := &Installed{Manifest: &Manifest{}, Grants: NewGrants(perms), logs: &logRing{}}
	s := &Scope{plugin: p, reg: reg, dir: dir, writable: true, files: map[string]*scopeFile{}, handles: map[uint64]*handle{}}
	s.files["in:0"] = &scopeFile{Ref: "in:0", Name: inputName, Size: int64(len(content)), Path: src}
	s.order = append(s.order, "in:0")
	return s, "in:0"
}

func runEngine(t *testing.T, s *Scope, req map[string]any) (*engineResult, error) {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	out, err := hfEngineRun(context.Background(), s, b)
	if err != nil {
		return nil, err
	}
	return out.(*engineResult), nil
}

func readOutput(t *testing.T, s *Scope, ref string) string {
	t.Helper()
	f, ok := s.file(ref)
	require.True(t, ok, "no such output %s", ref)
	b, err := os.ReadFile(f.Path)
	require.NoError(t, err)
	return string(b)
}

// The Convert app as it was built for LibreOffice (filex-convert
// internal/engines soffice()): `engines:libreoffice`, a soffice command line
// with LibreOffice's PDF/A option, the result expected as `in.pdf`. Since
// 0.50 it runs on the document server, unchanged.
//
// Red before 0.50: `libreoffice` was a binary looked up on PATH, and with no
// soffice the call answered "engine libreoffice is not installed on this
// host" (and the engineSet had no office back end to hand it to).
func TestOfficeEngine_ALibreOfficeCommandLineRunsOnTheDocumentServer(t *testing.T) {
	ds := &fakeOffice{ready: true}
	s, ref := officeScope(t, ds, "in.docx", "DOCX", "engines:libreoffice")
	res, err := runEngine(t, s, map[string]any{
		"engine": "libreoffice",
		"args":   []string{"--convert-to", `pdf:writer_pdf_Export:{"SelectPdfVersion":{"type":"long","value":"2"}}`, "in.docx"},
		"inputs": map[string]string{"in.docx": ref}, "outputs": []string{"in.pdf"}, "timeout_s": 60,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Exit)
	require.Len(t, res.Outputs, 1)
	assert.Equal(t, "in.pdf", res.Outputs[0].Name, "the name soffice gave the result")
	assert.Equal(t, "converted:docx->pdfa:DOCX", readOutput(t, s, res.Outputs[0].Ref))
	require.Len(t, ds.calls, 1)
	assert.Equal(t, "in.docx", ds.calls[0].Name)
	assert.Equal(t, "docx", ds.calls[0].From)
	assert.Equal(t, "pdfa", ds.calls[0].To, "LibreOffice's PDF/A option asks the document server for PDF/A")
	assert.Equal(t, int64(1<<20), ds.calls[0].MaxBytes, "the per-file limit goes with the request")
}

// The CSV line the Convert app writes: comma, double quote, UTF-8 - read as
// the conversion API's delimiter and code page. The new name runs the same.
func TestOfficeEngine_CSVOptionsAndTheNewName(t *testing.T) {
	ds := &fakeOffice{ready: true}
	s, ref := officeScope(t, ds, "in.xlsx", "XLSX", "engines:office")
	res, err := runEngine(t, s, map[string]any{
		"engine": "office",
		"args":   []string{"--headless", "--convert-to", "csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,false,false,false,-1", "--outdir", ".", "in.xlsx"},
		"inputs": map[string]string{"in.xlsx": ref},
	})
	require.NoError(t, err)
	require.Len(t, res.Outputs, 1)
	assert.Equal(t, "in.csv", res.Outputs[0].Name)
	require.Len(t, ds.calls, 1)
	assert.Equal(t, "csv", ds.calls[0].To)
	assert.Equal(t, 4, ds.calls[0].Delimiter, "comma")
	assert.Equal(t, 65001, ds.calls[0].CodePage, "UTF-8")
}

// No document server: the office engine is unavailable, and the sentence
// says what to CONNECT (not "install it and restart"). The job's error code
// is office_unconfigured, so the client says it in the person's language.
func TestOfficeEngine_NoDocumentServerSaysWhatToConnect(t *testing.T) {
	for _, ds := range []OfficeConverter{nil, &fakeOffice{ready: false}} {
		s, ref := officeScope(t, ds, "in.docx", "DOCX", "engines:libreoffice")
		_, err := runEngine(t, s, map[string]any{
			"engine": "libreoffice", "args": []string{"--convert-to", "pdf", "in.docx"},
			"inputs": map[string]string{"in.docx": ref},
		})
		he := asHostError(err)
		require.NotNil(t, he, "%v", err)
		assert.Equal(t, wire.ErrUnavailable, he.Code)
		assert.Contains(t, he.Message, "ONLYOFFICE")
		assert.Contains(t, he.Message, "External services")
		code, engine := classifyJobError(model.AppPluginJobFailed, he.Message)
		assert.Equal(t, "office_unconfigured", code)
		assert.Equal(t, "office", engine, "the engine by its own id, whichever name the app used")

		ok, err := hfEngineAvailable(context.Background(), s, []byte(`{"engine":"libreoffice"}`))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"available": false}, ok)
	}
}

// A conversion the document server refuses is a failed run - exit 1, the
// reason in stderr_tail, nothing collected - the shape a failed soffice had,
// which every app already handles.
func TestOfficeEngine_ARefusalIsAFailedRun(t *testing.T) {
	ds := &fakeOffice{ready: true, answer: func(OfficeRequest, []byte) ([]byte, error) {
		return nil, &OfficeError{Code: -7, Reason: "ds-7"}
	}}
	s, ref := officeScope(t, ds, "in.xlsx", "XLSX", "engines:libreoffice")
	res, err := runEngine(t, s, map[string]any{
		"engine": "libreoffice", "args": []string{"--convert-to", "html:HTML (StarCalc)", "in.xlsx"},
		"inputs": map[string]string{"in.xlsx": ref}, "outputs": []string{"in.html"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Exit)
	assert.Empty(t, res.Outputs)
	assert.Contains(t, res.StderrTail, "does not convert xlsx to html")
	assert.Contains(t, res.StderrTail, "error -7")
}

// What the office engine cannot read is refused before anything else - even
// with no document server - so an app's author sees it on every host. A
// LibreOffice-only switch is named.
func TestOfficeEngine_ACommandLineItCannotReadIsRefused(t *testing.T) {
	for _, args := range [][]string{
		{"--cat", "in.docx"},
		{"--print-to-file", "in.docx"},
		{"in.docx"},
		{"--convert-to", "pdf"},
		{"--convert-to", "pdf", "--outdir", "sub", "in.docx"},
	} {
		s, ref := officeScope(t, &fakeOffice{ready: false}, "in.docx", "DOCX", "engines:office")
		_, err := runEngine(t, s, map[string]any{"engine": "office", "args": args, "inputs": map[string]string{"in.docx": ref}})
		he := asHostError(err)
		require.NotNil(t, he, "%v: %v", args, err)
		assert.Equal(t, wire.ErrInvalid, he.Code, "%v", args)
		assert.Contains(t, he.Message, "office engine", "%v", args)
	}
}

// A LibreOffice on PATH is not the office engine, and is never run: the
// probe does not list it and `libreoffice` answers "not configured".
//
// Red before 0.50: the probe found soffice and engine_run executed it.
func TestOfficeEngine_ALibreOfficeOnThisMachineIsNeverRun(t *testing.T) {
	restore := enginebin.SetForTest(map[string]string{"libreoffice": "/bin/sh", "ffmpeg": "/bin/true"})
	defer restore()
	es := probeEngines(nil)
	ctx := context.Background()
	assert.False(t, es.available(ctx, "libreoffice"))
	assert.False(t, es.available(ctx, "office"))
	assert.True(t, es.available(ctx, "ffmpeg"))
	_, listed := es.Available(ctx)["libreoffice"]
	assert.False(t, listed, "the old name is not an engine of its own")
	_, listed = es.Available(ctx)["office"]
	assert.True(t, listed, "the office engine is listed, once")

	s, ref := officeScope(t, nil, "in.docx", "DOCX", "engines:libreoffice")
	s.reg.engines = es
	_, err := runEngine(t, s, map[string]any{"engine": "libreoffice", "args": []string{"--convert-to", "pdf", "in.docx"}, "inputs": map[string]string{"in.docx": ref}})
	he := asHostError(err)
	require.NotNil(t, he)
	assert.Equal(t, wire.ErrUnavailable, he.Code)
	assert.True(t, strings.Contains(he.Message, "not configured"), he.Message)
}

// The two names are ONE grant: an app granted `engines:libreoffice` may use
// `office` and the other way round; an upgrade that moves from one name to
// the other asks for nothing new; and a call carries both names with the
// same answer.
//
// Red before 0.50: grants were matched by their exact spelling.
func TestOfficeEngine_TheTwoNamesAreOneGrant(t *testing.T) {
	old := NewGrants([]Permission{"files:read", "engines:libreoffice"})
	assert.True(t, old.HasEngine("office"))
	assert.True(t, old.HasEngine("libreoffice"))
	assert.Empty(t, old.Missing([]Permission{"files:read", "engines:office"}))
	assert.False(t, old.HasEngine("ffmpeg"))

	p := &Installed{Row: &model.AppPlugin{Version: "0.1.1"}, Grants: old, Perms: []Permission{"files:read", "engines:libreoffice"}, Manifest: &Manifest{}}
	u := upgradeOf(p, &Manifest{Perms: []Permission{"files:read", "engines:office"}})
	assert.Empty(t, u.Added, "moving to the new name asks for nothing new")
	assert.Empty(t, u.Removed, "and drops nothing")

	ds := &fakeOffice{ready: true}
	reg := &Registry{engines: &engineSet{bins: map[string]string{}, office: ds}}
	got := reg.enginesFor(context.Background(), p)
	assert.True(t, got["office"])
	assert.True(t, got["libreoffice"], "an app built for LibreOffice reads its old name")
	ds.ready = false
	got = reg.enginesFor(context.Background(), p)
	assert.False(t, got["office"])
	assert.False(t, got["libreoffice"])
	_, has := got["libreoffice"]
	assert.True(t, has, "the old name is said, not left out")

	label := Permission("engines:libreoffice").Label("en")
	assert.Contains(t, label, "ONLYOFFICE", "the review names what the app will run")
	assert.NotContains(t, label, "LibreOffice")
}

// The install review says the office engine apart: a document server to
// connect (kind "office", read as ONLYOFFICE), not a program to install and
// restart for - and an app that asks for it by its old name is reviewed the
// same.
//
// Red before 0.50: `engines:libreoffice` was missing as {libreoffice,
// LibreOffice} with no kind, and the wizard said "install it and restart".
func TestDryRun_TheOfficeEngineIsADocumentServerToConnect(t *testing.T) {
	restore := enginebin.SetForTest(map[string]string{"ffmpeg": "/usr/bin/ffmpeg", "libreoffice": "/usr/bin/soffice"})
	defer restore()
	for _, perm := range []string{"engines:libreoffice", "engines:office"} {
		h := newHarness(t, nil)
		in := echoInput(t, func(m map[string]any) { m["permissions"] = append(m["permissions"].([]any), perm) })
		in.DryRun = true
		_, dry, err := h.reg.Install(context.Background(), in)
		require.NoError(t, err)
		name := strings.TrimPrefix(perm, "engines:")
		assert.Equal(t, []DryRunEngine{{ID: name, Name: "ONLYOFFICE", Kind: DryRunEngineOffice}}, dry.EnginesMissing,
			"%s: a soffice on PATH is not the office engine; no document server is connected", perm)

		h2 := newHarness(t, func(o *Options) { o.Office = &fakeOffice{ready: true} })
		in = echoInput(t, func(m map[string]any) { m["permissions"] = append(m["permissions"].([]any), perm) })
		in.DryRun = true
		_, dry, err = h2.reg.Install(context.Background(), in)
		require.NoError(t, err)
		assert.Empty(t, dry.EnginesMissing, "%s: a connected document server is not missing", perm)
	}
}

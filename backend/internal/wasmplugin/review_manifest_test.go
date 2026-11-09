package wasmplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/wasmfixture"
)

// ⚠⚠ A review reads the manifest, nothing else (fetch.go, "A REVIEW READS
// THE MANIFEST"). 2026-11-09: the Office editor's "Review update" downloaded
// its 126 MB ui.zip and 14 MB engine.zip for the review and the screen
// answered 504. These hold the review to filex-app.json, and the approval to
// the files the manifest pins.

// recordingWeb is fakeWeb that remembers every address asked.
type recordingWeb struct {
	*fakeWeb
	mu    sync.Mutex
	asked []string
}

func (w *recordingWeb) RoundTrip(req *http.Request) (*http.Response, error) {
	w.mu.Lock()
	w.asked = append(w.asked, req.URL.String())
	w.mu.Unlock()
	return w.fakeWeb.RoundTrip(req)
}

// take answers the addresses asked since the last take.
func (w *recordingWeb) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.asked
	w.asked = nil
	return out
}

func withRecordingWeb(web *recordingWeb) func(*Options) {
	return func(o *Options) { o.HTTP = &http.Client{Transport: web} }
}

// echoRelease is the echo app as a GitHub release publishes it: the module,
// an interface and an office engine, each a release asset pinned by hash.
func echoRelease(t *testing.T, wasm, bundle, engine []byte) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	asset := "https://github.com/Owner/echo/releases/download/{tag}/"
	m["wasm"] = map[string]any{"url": asset + "plugin.wasm", "sha256": sha256Hex(wasm)}
	m["ui"] = map[string]any{"bundle": map[string]any{"url": asset + "ui.zip", "sha256": sha256Hex(bundle)}}
	m["engines"] = map[string]any{"office": map[string]any{"url": asset + "engine.zip", "sha256": sha256Hex(engine)}}
	m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Panel", "tr": "Pano"}})
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestReview_AGitHubReviewReadsTheManifestAloneAndTheApprovalDownloads(t *testing.T) {
	wasmfixture.Require(t, fixtureWasm)
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	bundle := uiZip(t, uiDoc())
	engine := []byte("an engine.zip the review never asks for")
	manifest := echoRelease(t, wasm, bundle, engine)
	asset := "https://github.com/Owner/echo/releases/download/v1.2.0/"
	raw := "https://raw.githubusercontent.com/Owner/echo/v1.2.0/filex-app.json"
	web := &recordingWeb{fakeWeb: &fakeWeb{ok: map[string]string{
		raw:                   string(manifest),
		asset + "plugin.wasm": string(wasm),
		asset + "ui.zip":      string(bundle),
		asset + "engine.zip":  string(engine),
	}}}
	h := newHarness(t, withRecordingWeb(web))
	ctx := context.Background()

	in, err := h.reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/echo", Ref: "v1.2.0", Review: true})
	require.NoError(t, err)
	assert.Equal(t, []string{raw}, web.take(), "the review reads filex-app.json and nothing else")
	assert.True(t, in.ReviewOnly)
	assert.Nil(t, in.Wasm)
	assert.Nil(t, in.UI)
	assert.Nil(t, in.Engine)

	in.DryRun, in.Lang = true, "en"
	_, dry, err := h.reg.Install(ctx, in)
	require.NoError(t, err)
	assert.Empty(t, web.take(), "the dry run downloads nothing either (no mirrored file, no engine)")
	assert.True(t, dry.FromManifest)
	assert.True(t, dry.Engine, "the app brings a module")
	assert.Equal(t, sha256Hex(wasm), dry.WasmSHA256, "the module is named by the hash the manifest pins")
	assert.Zero(t, dry.WasmBytes, "its size is not known before it is downloaded")
	require.NotNil(t, dry.UI)
	assert.Equal(t, sha256Hex(bundle), dry.UI.SHA256, "the interface is named by the hash the manifest pins")
	assert.Zero(t, dry.UI.Files)
	var ids []string
	for _, p := range dry.Permissions {
		ids = append(ids, p.ID)
	}
	assert.Contains(t, ids, string(PermOfficeEngine), "the engine's permission is read from the manifest")
	assert.Contains(t, ids, "files:write")

	// A review's input installs nothing.
	in.DryRun = false
	in.Granted = ids
	_, _, err = h.reg.Install(ctx, in)
	require.Error(t, err)
	_, installed := h.reg.ByName("echo")
	assert.False(t, installed)

	// The approval's fetch downloads every file, to hold it to its pin.
	full, err := h.reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/echo", Ref: "v1.2.0"})
	require.NoError(t, err)
	assert.False(t, full.ReviewOnly)
	assert.Equal(t, []string{raw, asset + "ui.zip", asset + "engine.zip", asset + "plugin.wasm"}, web.take())
}

func TestReview_AnUpdateReviewReadsTheManifestAndTheApprovalHoldsTheFilesToIt(t *testing.T) {
	withHost(t, "0.47.0")
	wasmfixture.Require(t, fixtureWasm)
	wasm, err := os.ReadFile(fixtureWasm)
	require.NoError(t, err)
	const base = "https://apps.example.test/echo/"
	raw, err := os.ReadFile("testdata/echo/manifest.json")
	require.NoError(t, err)
	web := &recordingWeb{fakeWeb: &fakeWeb{ok: map[string]string{
		base + "filex-app.json": string(raw),
		base + "plugin.wasm":    string(wasm),
	}}}
	h := newHarness(t, withRecordingWeb(web))
	ctx := context.Background()
	in, err := h.reg.FetchURL(ctx, URLInput{URL: base + "plugin.wasm", ManifestURL: base + "filex-app.json", SHA256: sha256Hex(wasm)})
	require.NoError(t, err)
	var m Manifest
	require.NoError(t, json.Unmarshal(raw, &m.Manifest))
	in.Granted = m.Permissions
	st, _, err := h.reg.Install(ctx, in)
	require.NoError(t, err)

	// 0.0.2 brings an interface, pinned by its manifest.
	bundle := uiZip(t, uiDoc())
	web.ok[base+"ui.zip"] = string(bundle)
	nextEcho(t, web.fakeWeb, "0.0.2", func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"url": "ui.zip", "sha256": sha256Hex(bundle)}}
		m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Panel", "tr": "Pano"}})
	})
	web.take()

	// "Review update": the newer manifest, nothing else.
	rin, err := h.reg.ReviewUpdate(ctx, st.ID)
	require.NoError(t, err)
	for _, u := range web.take() {
		assert.Equal(t, base+"filex-app.json", u, "the review reads only the manifest")
	}
	rin.DryRun, rin.Lang = true, "en"
	_, dry, err := h.reg.Upgrade(ctx, st.ID, rin)
	require.NoError(t, err)
	assert.Empty(t, web.take())
	assert.True(t, dry.FromManifest)
	require.NotNil(t, dry.Upgrade)
	assert.Equal(t, "0.0.1", dry.Upgrade.From)
	assert.Equal(t, sha256Hex(wasm), dry.Upgrade.ModuleTo, "the module by its pin")
	assert.Equal(t, sha256Hex(wasm), dry.Upgrade.ModuleFrom)
	assert.Equal(t, sha256Hex(bundle), dry.Upgrade.UITo, "the interface by its pin")
	assert.Nil(t, dry.Upgrade.UIFiles, "no file list from a bundle that was not downloaded")
	assert.Contains(t, dry.Upgrade.Added, "ui", "what it adds to the grant comes from the manifest")

	// The approval downloads the files and holds them to the pins.
	granted := make([]string, 0, len(dry.Permissions))
	for _, p := range dry.Permissions {
		granted = append(granted, p.ID)
	}
	web.ok[base+"ui.zip"] = string(bundle) + "x"
	ain, err := h.reg.FetchUpdate(ctx, st.ID)
	require.NoError(t, err)
	ain.Granted = granted
	_, _, err = h.reg.Upgrade(ctx, st.ID, ain)
	require.Error(t, err, "a bundle that is not the reviewed one is refused")
	assert.Equal(t, ErrCodeSHA256Mismatch, installError(t, err).Code)
	p, _ := h.reg.ByID(st.ID)
	assert.Equal(t, "0.0.1", p.Row.Version, "nothing moved")

	// The bundle the manifest pins: the approval's fetch downloads both files
	// and stages them against the pins. (Staged as a dry run: the echo
	// fixture always describes itself as 0.0.1, so a real swap to 0.0.2 is
	// refused at describe, which is not what is held here.)
	web.ok[base+"ui.zip"] = string(bundle)
	web.take()
	ain, err = h.reg.FetchUpdate(ctx, st.ID)
	require.NoError(t, err)
	asked := strings.Join(web.take(), " ")
	assert.Contains(t, asked, base+"ui.zip")
	assert.Contains(t, asked, base+"plugin.wasm")
	ain.DryRun, ain.Lang = true, "en"
	_, staged, err := h.reg.Upgrade(ctx, st.ID, ain)
	require.NoError(t, err, "the downloaded files hash to the reviewed pins")
	assert.False(t, staged.FromManifest)
	assert.Equal(t, sha256Hex(bundle), staged.UI.SHA256)
	assert.Equal(t, len(uiDoc()), staged.UI.Files, "downloaded: the bundle's files are known")
	require.NotNil(t, staged.Upgrade.UIFiles)
	assert.Equal(t, len(uiDoc()), staged.Upgrade.UIFiles.AddedCount)
}

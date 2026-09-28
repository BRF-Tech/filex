package wasmplugin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Versions (M4): the review of an upgrade, the kept version, going back ──

func uiDocWith(change map[string]string, drop ...string) map[string]string {
	files := uiDoc()
	for _, d := range drop {
		delete(files, d)
	}
	for k, v := range change {
		files[k] = v
	}
	return files
}

func (h *harness) upgradeUI(t *testing.T, p *Installed, version string, bundle []byte) *Status {
	t.Helper()
	next := uiManifest(t, func(m map[string]any) { m["version"] = version })
	st, _, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(bundle), Lang: "en"})
	require.NoError(t, err)
	return st
}

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	require.NoError(t, err)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// The administrator's review of a newer version says what it changes beyond
// the grant: the interface's files (added, removed, changed), both hashes,
// the filex range, the signature, and the source's own notes.
func TestUIUpgrade_TheReviewShowsWhatTheNewVersionChanges(t *testing.T) {
	withHost(t, "0.47.0")
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, _ := h.installUI(t, uiManifest(t, nil), bundle)

	nextBundle := uiZip(t, uiDocWith(map[string]string{"app.js": "console.log('two')", "style.css": "body{}"}, "img/logo.svg"))
	next := uiManifest(t, func(m map[string]any) {
		m["version"] = "1.1.0"
		m["filex"] = ">=0.46.0"
	})
	_, dry, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{
		Manifest: next, UI: bytes.NewReader(nextBundle), DryRun: true, Notes: "Faster export.",
	})
	require.NoError(t, err)
	require.NotNil(t, dry.Upgrade)
	u := dry.Upgrade
	assert.Empty(t, u.Added, "nothing new asked")
	assert.Equal(t, hexSum(bundle), u.UIFrom)
	assert.Equal(t, hexSum(nextBundle), u.UITo)
	assert.Empty(t, u.ModuleFrom, "no module before")
	assert.Empty(t, u.ModuleTo, "no module after")
	assert.Equal(t, "", u.FilexFrom)
	assert.Equal(t, ">=0.46.0", u.FilexTo)
	assert.False(t, u.SignedFrom)
	assert.False(t, u.SignedTo)
	assert.Equal(t, "Faster export.", u.Notes)
	require.NotNil(t, u.UIFiles)
	assert.Equal(t, []string{"style.css"}, u.UIFiles.Added)
	assert.Equal(t, []string{"img/logo.svg"}, u.UIFiles.Removed)
	assert.Equal(t, []string{"app.js"}, u.UIFiles.Changed, "index.html and font.ttf are the same bytes")
	assert.Equal(t, 1, u.UIFiles.AddedCount)
	assert.Equal(t, 1, u.UIFiles.RemovedCount)
	assert.Equal(t, 1, u.UIFiles.ChangedCount)

	// A same-size change is still a change (the CRC, not the length).
	sameSize := uiZip(t, uiDocWith(map[string]string{"app.js": "console.log('APP')"}))
	_, dry, err = h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(sameSize), DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"app.js"}, dry.Upgrade.UIFiles.Changed)
	assert.Empty(t, dry.Upgrade.UIFiles.Added)
	assert.Empty(t, dry.Upgrade.UIFiles.Removed)
}

// ⭐ An approved version can be undone. The version an upgrade replaced is kept
// (one per app): a tab still open on it keeps loading its files, and "Back to
// 1.0.0" puts it back without asking again. The one before that is dropped.
func TestUIVersions_TheReplacedVersionIsKeptServedAndPutBack(t *testing.T) {
	h := newBareHarness(t, nil)
	v1 := uiZip(t, uiDoc())
	p, st := h.installUI(t, uiManifest(t, nil), v1)
	assert.Nil(t, st.Previous)
	_, err := h.reg.Rollback(context.Background(), p.Row.ID, nil, "en")
	assert.Equal(t, ErrCodeNotFound, installError(t, err).Code, "nothing kept to go back to")

	v2 := uiZip(t, uiDocWith(map[string]string{"app.js": "console.log('v2')"}))
	st = h.upgradeUI(t, p, "1.1.0", v2)
	require.NotNil(t, st.Previous)
	assert.Equal(t, "1.0.0", st.Previous.Version)
	assert.True(t, st.Previous.UI)

	srv := uiServer(t, h, "")
	s1, s2 := hexSum(v1)[:16], hexSum(v2)[:16]
	code, body := getBody(t, srv.URL+"/_appui/drawio/"+s1+"/app.js")
	assert.Equal(t, http.StatusOK, code, "a tab still open on 1.0.0 keeps loading its files")
	assert.Equal(t, "console.log('app')", body)
	code, body = getBody(t, srv.URL+"/_appui/drawio/"+s2+"/app.js")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "console.log('v2')", body)

	// A third version: 1.1.0 is kept, 1.0.0 is gone — from disk too.
	v3 := uiZip(t, uiDocWith(map[string]string{"app.js": "console.log('v3')"}))
	st = h.upgradeUI(t, p, "1.2.0", v3)
	assert.Equal(t, "1.1.0", st.Previous.Version)
	code, _ = getBody(t, srv.URL+"/_appui/drawio/"+s1+"/app.js")
	assert.Equal(t, http.StatusNotFound, code, "only one earlier version is kept")
	kept, err := os.ReadDir(filepath.Join(h.reg.Dir(), versionsDirName, "drawio"))
	require.NoError(t, err)
	assert.Len(t, kept, 1)

	// Back to 1.1.0: served at its own address, and 1.2.0 is kept in turn.
	st, err = h.reg.Rollback(context.Background(), p.Row.ID, nil, "en")
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", st.Version)
	assert.Equal(t, StateRunning, st.State)
	assert.Equal(t, hexSum(v2), st.UI.SHA256)
	assert.Equal(t, "1.2.0", st.Previous.Version)
	code, body = getBody(t, srv.URL+"/_appui/drawio/"+s2+"/app.js")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "console.log('v2')", body)

	// Removing the app removes what it kept.
	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	assert.NoDirExists(t, filepath.Join(h.reg.Dir(), versionsDirName, "drawio"))
	vs, err := h.reg.opts.Store.ListAppPluginVersions(context.Background(), p.Row.ID)
	require.NoError(t, err)
	assert.Empty(t, vs)
}

// ⚠ A kept version is held to the hashes recorded when it was replaced: files
// changed on disk since are not put back, and the running version stays.
func TestUIVersions_AKeptVersionChangedOnDiskIsNotPutBack(t *testing.T) {
	h := newBareHarness(t, nil)
	p, _ := h.installUI(t, uiManifest(t, nil), uiZip(t, uiDoc()))
	h.upgradeUI(t, p, "1.1.0", uiZip(t, uiDocWith(map[string]string{"app.js": "console.log('v2')"})))
	kept, err := os.ReadDir(filepath.Join(h.reg.Dir(), versionsDirName, "drawio"))
	require.NoError(t, err)
	require.Len(t, kept, 1)
	dir := filepath.Join(h.reg.Dir(), versionsDirName, "drawio", kept[0].Name())
	good, err := os.ReadFile(filepath.Join(dir, uiZipName))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, uiZipName), uiZip(t, map[string]string{"index.html": "<p>evil"}), 0o600))

	_, err = h.reg.Rollback(context.Background(), p.Row.ID, nil, "en")
	assert.Equal(t, ErrCodeSHA256Mismatch, installError(t, err).Code, "%v", err)
	got, _ := h.reg.ByID(p.Row.ID)
	assert.Equal(t, "1.1.0", got.Row.Version, "the running version stays")

	// The manifest too (the bundle put right first, so only the manifest
	// differs).
	require.NoError(t, os.WriteFile(filepath.Join(dir, uiZipName), good, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "filex-app.json"), uiManifest(t, func(m map[string]any) {
		m["permissions"] = []any{"files:read", "files:write", "http:evil.example"}
	}), 0o600))
	_, err = h.reg.Rollback(context.Background(), p.Row.ID, nil, "en")
	assert.Equal(t, ErrCodeSHA256Mismatch, installError(t, err).Code, "%v", err)
}

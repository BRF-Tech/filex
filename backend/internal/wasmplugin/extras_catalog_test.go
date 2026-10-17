package wasmplugin

// The kinds of added file an app names (`ui.extras`) and the catalog its
// release offers (extras_catalog.go, filex 0.57). Red before: an app that
// read its own package (draw.io) got a Dictionaries section it never uses,
// and there was no catalog - an administrator found and uploaded every file.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtras_TheManifestNamesTheKinds(t *testing.T) {
	ok := uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{
				"catalog": map[string]any{"url": "dictionaries.json", "sha256": strings.Repeat("AB", 32)}}}}
	})
	m, err := ParseManifest(ok)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("ab", 32), m.UI.Extras["dictionaries"].Catalog.SHA256, "lower-cased like the bundle's pin")
	assert.Equal(t, []string{"dictionaries"}, declaredKinds(m))
	for _, p := range m.Perms {
		assert.NotContains(t, string(p), "extra", "naming a kind is no permission: the administrator adds the files")
	}

	for name, ui := range map[string]map[string]any{
		"without package_fetch": {"bundle": map[string]any{}, "extras": map[string]any{"dictionaries": map[string]any{}}},
		"a kind filex does not take": {"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"fonts": map[string]any{}}},
		"a catalog without its pin": {"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{"catalog": map[string]any{"url": "d.json"}}}},
		"a catalog without an address": {"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{"catalog": map[string]any{"sha256": strings.Repeat("ab", 32)}}}},
	} {
		_, err := ParseManifest(uiManifest(t, func(m map[string]any) { m["ui"] = ui }))
		assert.Error(t, err, name)
	}
}

// draw.io reads its own package but takes no dictionary: no section, nothing
// accepted, nothing served.
func TestExtras_AnAppThatNamesNoKindIsGivenNone(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, _ := h.installUI(t, uiManifest(t, func(m map[string]any) {
		m["name"] = "drawio"
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true}
	}), bundle)
	ans, err := h.reg.Extras(p.Row.ID)
	require.NoError(t, err)
	assert.False(t, ans.Read)
	assert.Empty(t, ans.Kinds)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	assert.ErrorIs(t, err, ErrExtrasUnused)

	dir := filepath.Join(h.reg.Dir(), "_extras", "drawio", "dictionaries", "pl_PL")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pl_PL.dic.txt"), []byte(plDic), 0o600))
	srv := httptest.NewServer(h.reg.UIHandler(func(r *http.Request) (string, bool) { return "http://" + r.Host, true }))
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/_appui/drawio/" + hexSum(bundle)[:16] + "/extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode, "a file left on disk is not served to an app that names no such kind")
}

func sum256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// catalogRig is a repository on a fake GitHub with the office editor's
// release: a dictionary catalog beside the manifest, its files beside it.
type catalogRig struct {
	h     *harness
	p     *Installed
	files map[string][]byte
	mu    sync.Mutex
	asked []string
}

func newCatalogRig(t *testing.T, catalog func(files map[string][]byte) []byte) *catalogRig {
	t.Helper()
	g := &catalogRig{files: map[string][]byte{}}
	aff, dic, hyph := []byte(plAff), []byte(plDic), []byte("UTF-8\n1a1\n")
	g.files["/Owner/office/v0.4.0/dict/pl_PL.aff"] = aff
	g.files["/Owner/office/v0.4.0/dict/pl_PL.dic"] = dic
	g.files["/Owner/office/v0.4.0/dict/hyph_pl_PL.dic"] = hyph
	doc := catalog(g.files)
	g.files["/Owner/office/v0.4.0/dict/catalog.json"] = doc
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.asked = append(g.asked, r.URL.Path)
		b, ok := g.files[r.URL.Path]
		g.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(src.Close)
	g.h = newBareHarness(t, func(o *Options) {
		o.LoopbackSources = true
		o.GitHubRawBase = src.URL
	})
	manifest := uiManifest(t, func(m map[string]any) {
		m["name"] = "office"
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{
				"catalog": map[string]any{"url": "dict/catalog.json", "sha256": sum256(doc)}}}}
	})
	pm, err := ParseManifest(manifest)
	require.NoError(t, err)
	st, _, err := g.h.reg.Install(context.Background(), &InstallInput{
		Manifest: manifest, UI: strings.NewReader(string(uiZip(t, uiDoc()))), Source: "github",
		SourceURL: "https://github.com/Owner/office@v0.4.0", Granted: permStrings(pm.Perms), Lang: "en",
	})
	require.NoError(t, err)
	p, ok := g.h.reg.ByID(st.ID)
	require.True(t, ok)
	g.p = p
	return g
}

// polishCatalog is the release's catalog: one language, its three files
// beside the catalog (relative addresses), sizes and sha256s as published.
func polishCatalog(files map[string][]byte) []byte {
	f := func(name, rel string) map[string]any {
		b := files["/Owner/office/v0.4.0/dict/"+name]
		return map[string]any{"path": "pl_PL/" + rel, "url": name, "size": len(b), "sha256": sum256(b)}
	}
	doc, _ := json.Marshal(map[string]any{
		"format": ExtraCatalogFormat, "kind": "dictionaries",
		"items": []any{map[string]any{"id": "pl_PL", "label": "Polski", "license": "LGPL-2.1-or-later", "files": []any{
			f("pl_PL.aff", "pl_PL.aff.txt"), f("pl_PL.dic", "pl_PL.dic.txt"), f("hyph_pl_PL.dic", "hyph_pl_PL.dic.txt"),
		}}},
	})
	return doc
}

func TestExtraCatalog_OneClickInstallsAPinnedLanguage(t *testing.T) {
	g := newCatalogRig(t, polishCatalog)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ans, err := g.h.reg.Extras(g.p.Row.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"dictionaries"}, ans.Catalogs)

	cat, err := g.h.reg.ExtraCatalog(ctx, g.p.Row.ID, "dictionaries")
	require.NoError(t, err)
	require.Len(t, cat.Items, 1)
	assert.Equal(t, "pl_PL", cat.Items[0].ID)
	assert.Equal(t, 3, cat.Items[0].Files)
	assert.False(t, cat.Items[0].Installed)

	got, err := g.h.reg.InstallExtraItem(ctx, g.p.Row.ID, "dictionaries", "pl_PL")
	require.NoError(t, err)
	require.Len(t, got, 3)
	b, err := os.ReadFile(filepath.Join(g.h.reg.Dir(), "_extras", "office", "dictionaries", "pl_PL", "pl_PL.dic.txt"))
	require.NoError(t, err)
	assert.Equal(t, plDic, string(b), "stored where the editor reads it")
	assert.Equal(t, sum256([]byte(plDic)), got[1].SHA256)

	cat, err = g.h.reg.ExtraCatalog(ctx, g.p.Row.ID, "dictionaries")
	require.NoError(t, err)
	assert.True(t, cat.Items[0].Installed)

	_, err = g.h.reg.InstallExtraItem(ctx, g.p.Row.ID, "dictionaries", "de_DE")
	assert.ErrorIs(t, err, ErrExtraItemMissing)
	g.mu.Lock()
	assert.Contains(t, g.asked, "/Owner/office/v0.4.0/dict/catalog.json", "read at the tag the app was installed from")
	g.mu.Unlock()
}

// A file that is not what the catalog pins stores nothing - not even the
// files of the language that matched.
func TestExtraCatalog_AFileThatIsNotTheCatalogsStoresNothing(t *testing.T) {
	g := newCatalogRig(t, polishCatalog)
	g.mu.Lock()
	g.files["/Owner/office/v0.4.0/dict/pl_PL.dic"] = []byte("2\nkot/A\nXXXX/B\n")
	g.mu.Unlock()
	_, err := g.h.reg.InstallExtraItem(context.Background(), g.p.Row.ID, "dictionaries", "pl_PL")
	var ce *ExtraCatalogError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, "pl_PL/pl_PL.dic.txt", ce.File)
	assert.NoDirExists(t, filepath.Join(g.h.reg.Dir(), "_extras", "office", "dictionaries", "pl_PL"))
}

// The catalog itself is held to the manifest's pin, its format and kind, and
// the places its files may have.
func TestExtraCatalog_TheCatalogIsHeldToItsPinAndShape(t *testing.T) {
	g := newCatalogRig(t, polishCatalog)
	g.mu.Lock()
	g.files["/Owner/office/v0.4.0/dict/catalog.json"] = append(g.files["/Owner/office/v0.4.0/dict/catalog.json"], ' ')
	g.mu.Unlock()
	_, err := g.h.reg.ExtraCatalog(context.Background(), g.p.Row.ID, "dictionaries")
	var ce *ExtraCatalogError
	require.ErrorAs(t, err, &ce, "a byte more than the pinned catalog")

	for name, bad := range map[string]func(map[string][]byte) []byte{
		"another format": func(files map[string][]byte) []byte {
			b, _ := json.Marshal(map[string]any{"format": "filex-extras/9", "kind": "dictionaries", "items": []any{}})
			return b
		},
		"a file outside its item": func(files map[string][]byte) []byte {
			b, _ := json.Marshal(map[string]any{"format": ExtraCatalogFormat, "kind": "dictionaries", "items": []any{map[string]any{
				"id": "pl_PL", "files": []any{map[string]any{"path": "de_DE/de_DE.dic.txt", "url": "x", "size": 1, "sha256": strings.Repeat("a", 64)}}}}})
			return b
		},
		"a page among the files": func(files map[string][]byte) []byte {
			b, _ := json.Marshal(map[string]any{"format": ExtraCatalogFormat, "kind": "dictionaries", "items": []any{map[string]any{
				"id": "pl_PL", "files": []any{map[string]any{"path": "pl_PL/index.html", "url": "x", "size": 1, "sha256": strings.Repeat("a", 64)}}}}})
			return b
		},
	} {
		g := newCatalogRig(t, bad)
		_, err := g.h.reg.ExtraCatalog(context.Background(), g.p.Row.ID, "dictionaries")
		assert.ErrorAs(t, err, &ce, name)
	}
}

// An upload has no repository or manifest address a relative catalog could
// be read against: offered as no catalog, upload still works.
func TestExtraCatalog_AnUploadedAppHasNoCatalogToRead(t *testing.T) {
	h := newBareHarness(t, nil)
	manifest := uiManifest(t, func(m map[string]any) {
		m["name"] = "office"
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{
				"catalog": map[string]any{"url": "dict/catalog.json", "sha256": strings.Repeat("ab", 32)}}}}
	})
	p, _ := h.installUI(t, manifest, uiZip(t, uiDoc()))
	_, err := h.reg.ExtraCatalog(context.Background(), p.Row.ID, "dictionaries")
	assert.ErrorIs(t, err, ErrExtraNoCatalogSource)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	assert.NoError(t, err, "the administrator's own upload")
	_, err = h.reg.ExtraCatalog(context.Background(), p.Row.ID, "fonts")
	assert.ErrorIs(t, err, ErrExtraUndeclared)
}

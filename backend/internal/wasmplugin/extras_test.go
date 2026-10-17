package wasmplugin

// Files an administrator adds to an app (extras.go): the office editor's
// extra dictionaries, read by its interface at
// `<package>/extra/dictionaries/<lang>/<file>.txt`.
//
// RED on the code before 0.57: none of Extras / PutExtra / DeleteExtra
// existed, and the interface route answered 404 for every extra/ path, so a
// language an administrator wanted spell-checked could not be added at all.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// officeLike is an interface-only app that reads its own package
// (`ui.package_fetch`) and takes dictionaries an administrator adds
// (`ui.extras`), as the office editor does.
func officeLike(t *testing.T, version string) []byte {
	t.Helper()
	return uiManifest(t, func(m map[string]any) {
		m["name"] = "office"
		m["version"] = version
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true,
			"extras": map[string]any{"dictionaries": map[string]any{}}}
	})
}

const plAff = "SET UTF-8\nTRY aeoiu\n"
const plDic = "2\nkot/A\npies/B\n"

func TestExtras_OnlyTheKindsPathsAreTaken(t *testing.T) {
	for _, rel := range []string{
		"dictionaries/pl_PL/pl_PL.aff.txt",
		"dictionaries/pl_PL/pl_PL.dic.txt",
		"dictionaries/pl_PL/hyph_pl_PL.dic.txt",
		"dictionaries/ar/ar.dic.txt",
	} {
		k, ok := extraKindOf(rel)
		assert.True(t, ok, rel)
		assert.Equal(t, "dictionaries", k.Name, rel)
		assert.Equal(t, "text/plain; charset=utf-8", k.typ, "a dictionary is served as the bundle's own copies are")
	}
	for _, rel := range []string{
		"",
		"dictionaries/pl_PL/pl_PL.aff",         // the app asks for .txt
		"dictionaries/pl_PL/pl_PL.html",        // never a page
		"dictionaries/pl_PL/pl_PL.dic.js",      // never a script
		"dictionaries/pl_PL/pl_PL.svg",         // never an image that runs
		"dictionaries/pl-PL/pl_PL.dic.txt",     // the language's pattern
		"dictionaries/p/p.dic.txt",             // too short
		"dictionaries/../pl_PL/pl_PL.dic.txt",  // not normal
		"dictionaries/pl_PL/../../x.dic.txt",   // not normal
		"dictionaries//pl_PL.dic.txt",          // an empty segment
		"/dictionaries/pl_PL/pl_PL.dic.txt",    // absolute
		"dictionaries\\pl_PL\\pl_PL.dic.txt",   // a backslash
		"dictionaries/pl_PL/sub/pl_PL.dic.txt", // one level only
		"fonts/Inter.ttf",                      // not a kind (yet)
		"index.html",                           // the package's own kind of path
		"dictionaries/pl_PL/.partial-123",      // a write in progress
		"dictionaries/pl_PL/pl_PL.dic.txt\x00", // a NUL
		"dictionaries/pl_PL/" + strings.Repeat("a", 33) + ".dic.txt",
	} {
		_, ok := extraKindOf(rel)
		assert.False(t, ok, "%q", rel)
	}
}

func TestExtras_AddListReplaceRemove(t *testing.T) {
	h := newBareHarness(t, nil)
	p, _ := h.installUI(t, officeLike(t, "1.0.0"), uiZip(t, uiDoc()))

	ans, err := h.reg.Extras(p.Row.ID)
	require.NoError(t, err)
	assert.True(t, ans.Read, "the interface reads its package, so it can read what is added")
	assert.Empty(t, ans.Files)
	assert.Equal(t, []string{"dictionaries"}, ans.Kinds)
	assert.Equal(t, MaxExtraFileBytes, ans.MaxFileBytes)
	assert.Equal(t, MaxExtrasBytes, ans.MaxBytes)

	f, err := h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.aff.txt", strings.NewReader(plAff))
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(plAff))
	assert.Equal(t, hex.EncodeToString(sum[:]), f.SHA256, "the write says the sha256 its audit row records")
	assert.Equal(t, int64(len(plAff)), f.Size)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	require.NoError(t, err)

	disk := filepath.Join(h.reg.Dir(), "_extras", "office", "dictionaries", "pl_PL", "pl_PL.dic.txt")
	b, err := os.ReadFile(disk)
	require.NoError(t, err)
	assert.Equal(t, plDic, string(b), "kept beside the apps, outside the package")
	assert.NoFileExists(t, filepath.Join(h.reg.Dir(), "office", "extra", "dictionaries", "pl_PL", "pl_PL.dic.txt"), "never inside the package's folder")

	ans, err = h.reg.Extras(p.Row.ID)
	require.NoError(t, err)
	require.Len(t, ans.Files, 2)
	assert.Equal(t, "dictionaries/pl_PL/pl_PL.aff.txt", ans.Files[0].Path)
	assert.Equal(t, "dictionaries/pl_PL/pl_PL.dic.txt", ans.Files[1].Path)
	assert.Equal(t, int64(len(plAff)+len(plDic)), ans.Bytes)

	// Replaced in place: one file, the new bytes, nothing half-written left.
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader("1\nkot\n"))
	require.NoError(t, err)
	b, _ = os.ReadFile(disk)
	assert.Equal(t, "1\nkot\n", string(b))
	entries, err := os.ReadDir(filepath.Dir(disk))
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), extraTempPrefix), "a finished write leaves no partial file: %s", e.Name())
	}
	ans, _ = h.reg.Extras(p.Row.ID)
	assert.Len(t, ans.Files, 2)

	require.NoError(t, h.reg.DeleteExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt"))
	require.NoError(t, h.reg.DeleteExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.aff.txt"))
	assert.NoDirExists(t, filepath.Dir(disk), "an empty language folder goes with its last file")
	assert.ErrorIs(t, h.reg.DeleteExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.aff.txt"), ErrExtraMissing)
	assert.ErrorIs(t, h.reg.DeleteExtra(p.Row.ID, "../office/filex-app.json"), ErrExtraPath)
	assert.FileExists(t, filepath.Join(h.reg.Dir(), "office", "filex-app.json"), "a path outside the kinds touches nothing")
}

func TestExtras_RefusedWhereNothingWouldReadThem(t *testing.T) {
	h := newBareHarness(t, nil)
	p, _ := h.installUI(t, uiManifest(t, nil), uiZip(t, uiDoc()))
	ans, err := h.reg.Extras(p.Row.ID)
	require.NoError(t, err)
	assert.False(t, ans.Read, "no ui:package-fetch: the interface cannot read its package, nor what is added to it")
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	assert.ErrorIs(t, err, ErrExtrasUnused)
	assert.NoDirExists(t, filepath.Join(h.reg.Dir(), "_extras", "drawio"))
}

func TestExtras_PathsTextAndCeilings(t *testing.T) {
	h := newBareHarness(t, nil)
	p, _ := h.installUI(t, officeLike(t, "1.0.0"), uiZip(t, uiDoc()))

	_, err := h.reg.PutExtra(p.Row.ID, "dictionaries/../../office/ui.zip", strings.NewReader("x"))
	assert.ErrorIs(t, err, ErrExtraPath)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.html", strings.NewReader("<script>1</script>"))
	assert.ErrorIs(t, err, ErrExtraPath)

	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", bytes.NewReader([]byte("PK\x03\x04\x00\x00zip")))
	assert.ErrorIs(t, err, ErrExtraNotText, "a zip picked by mistake is not served as a dictionary")
	assert.NoDirExists(t, filepath.Join(h.reg.Dir(), "_extras", "office", "dictionaries", "pl_PL"), "a refused first write leaves no empty folder")

	old := extraCaps
	t.Cleanup(func() { extraCaps = old })
	extraCaps.file, extraCaps.total, extraCaps.files = 16, 24, 2
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(strings.Repeat("a", 17)))
	assert.ErrorIs(t, err, ErrExtraTooLarge)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(strings.Repeat("a", 16)))
	require.NoError(t, err)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.aff.txt", strings.NewReader(strings.Repeat("b", 9)))
	assert.ErrorIs(t, err, ErrExtrasFull, "16 + 9 is over the app's 24")
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(strings.Repeat("c", 16)))
	assert.NoError(t, err, "a replacement is measured without the file it replaces")
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.aff.txt", strings.NewReader("d"))
	require.NoError(t, err)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/de_AT/de_AT.aff.txt", strings.NewReader("e"))
	assert.ErrorIs(t, err, ErrExtrasFull, "the third file is over the count")
	b, _ := os.ReadFile(filepath.Join(h.reg.Dir(), "_extras", "office", "dictionaries", "pl_PL", "pl_PL.dic.txt"))
	assert.Equal(t, strings.Repeat("c", 16), string(b), "a refused write left the file as it was")
}

// They survive an upgrade (the package is replaced, the added files are the
// administrator's) and are served under the NEW version's address; removing
// the app takes them away.
func TestExtras_SurviveAnUpgradeAndGoWithTheApp(t *testing.T) {
	h := newBareHarness(t, nil)
	p, _ := h.installUI(t, officeLike(t, "1.0.0"), uiZip(t, uiDoc()))
	_, err := h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	require.NoError(t, err)

	files := uiDoc()
	files["readme.txt"] = "1.1.0"
	next := uiZip(t, files)
	_, _, err = h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: officeLike(t, "1.1.0"), UI: bytes.NewReader(next)})
	require.NoError(t, err)
	ans, err := h.reg.Extras(p.Row.ID)
	require.NoError(t, err)
	require.Len(t, ans.Files, 1, "the upgrade kept the added file")

	srv := uiServer(t, h, "")
	res, err := http.Get(srv.URL + "/_appui/office/" + hexSum(next)[:16] + "/extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, plDic, string(b))

	require.NoError(t, h.reg.Remove(context.Background(), p.Row.ID))
	assert.NoDirExists(t, filepath.Join(h.reg.Dir(), "_extras", "office"), "removing the app removes what was added to it")
}

func TestExtras_ServedUnderThePackageAsData(t *testing.T) {
	h := newBareHarness(t, nil)
	files := uiDoc()
	// The package's own extra/ file: never shadowed by an added one.
	files["extra/dictionaries/de_AT/de_AT.dic.txt"] = "from the package"
	bundle := uiZip(t, files)
	p, _ := h.installUI(t, officeLike(t, "1.0.0"), bundle)
	_, err := h.reg.PutExtra(p.Row.ID, "dictionaries/pl_PL/pl_PL.dic.txt", strings.NewReader(plDic))
	require.NoError(t, err)
	_, err = h.reg.PutExtra(p.Row.ID, "dictionaries/de_AT/de_AT.dic.txt", strings.NewReader("added"))
	require.NoError(t, err)
	short := hexSum(bundle)[:16]
	srv := uiServer(t, h, "/filex")
	base := srv.URL + "/_appui/office/" + short + "/"

	res, err := http.Get(base + "extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, plDic, string(b))
	assert.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Equal(t, "default-src 'none'; sandbox", res.Header.Get("Content-Security-Policy"), "data, never a page")
	assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"), "the sandboxed frame's worker reads it")
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"), "replaceable under one address: revalidated, never immutable")
	assert.Empty(t, res.Header.Values("Set-Cookie"))
	etag := res.Header.Get("ETag")
	require.NotEmpty(t, etag)

	req, _ := http.NewRequest(http.MethodGet, base+"extra/dictionaries/pl_PL/pl_PL.dic.txt", nil)
	req.Header.Set("If-None-Match", etag)
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotModified, res.StatusCode)

	res, err = http.Get(base + "extra/dictionaries/de_AT/de_AT.dic.txt")
	require.NoError(t, err)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, "from the package", string(b), "the reviewed package wins over an added file")

	for _, path := range []string{
		"extra/dictionaries/pl_PL/pl_PL.aff.txt", // not added
		"extra/dictionaries/pl_PL/../pl_PL/pl_PL.dic.txt",
		"extra/dictionaries/pl_PL/pl_PL.dic.txt/",
		"extra/dictionaries%2fpl_PL/pl_PL.dic.txt",
		"dictionaries/pl_PL/pl_PL.dic.txt", // only under extra/
		"extra/../extra/dictionaries/pl_PL/pl_PL.dic.txt",
	} {
		res, err := http.DefaultClient.Do(mustRaw(t, base+path))
		require.NoError(t, err)
		res.Body.Close()
		assert.Equal(t, http.StatusNotFound, res.StatusCode, path)
	}

	// An app without ui:package-fetch is never served an added file, even
	// one left on disk.
	plainBundle := uiZip(t, uiDoc())
	plain, _ := h.installUI(t, uiManifest(t, nil), plainBundle)
	dir := filepath.Join(h.reg.Dir(), "_extras", plain.Row.Name, "dictionaries", "pl_PL")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pl_PL.dic.txt"), []byte(plDic), 0o600))
	res, err = http.Get(srv.URL + "/_appui/" + plain.Row.Name + "/" + hexSum(plainBundle)[:16] + "/extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

package wasmplugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Language packs ─────────────────────────────────────────────────────
//
// A language pack is an app that adds a language to filex and nothing else.
// It has no module, so none of these tests needs the wasm fixture — which is
// itself the point: a translator's pack must install on a machine that never
// built a line of Go.

// packManifest builds a data-only manifest carrying `langs`.
func packManifest(t *testing.T, name string, langs map[string]map[string]string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"manifest_version": 1, "name": name, "version": "1.0.0",
		"label":       map[string]string{"en": "Spanish", "tr": "İspanyolca"},
		"languages":   []string{"en", "tr"},
		"permissions": []string{},
		"ui_locales":  langs,
	})
	require.NoError(t, err)
	return b
}

// fullLanguage is a complete-size translation: n keys shaped like the real
// catalogue's, each value `valueRunes` letters of a two-byte script — the
// worst case a real complete translation reaches.
func fullLanguage(n, valueRunes int) map[string]string {
	out := make(map[string]string, n)
	v := strings.Repeat("ж", valueRunes)
	for i := 0; i < n; i++ {
		out[fmt.Sprintf("section%d.group.key_%d", i%40, i)] = v
	}
	return out
}

func newPackRegistry(t *testing.T, opts func(*Options)) (*Registry, Options) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	o := Options{Store: store, Dir: filepath.Join(t.TempDir(), "app-plugins"), SecretKey: "0123456789abcdef0123456789abcdef"}
	if opts != nil {
		opts(&o)
	}
	reg, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	return reg, o
}

func TestManifest_UILocales_AWholeTranslationFits(t *testing.T) {
	// ⚠ The measurement that started this: 2 500 keys used to be refused
	// with "at most 2000" — and the catalogue has 3 593. A complete
	// translation in a two-byte script must install, with room to grow.
	for _, n := range []int{2500, 2900, 4000} {
		_, err := ParseManifest(packManifest(t, "lang-ru", map[string]map[string]string{"ru": fullLanguage(n, 60)}))
		require.NoError(t, err, "%d keys", n)
	}
}

func TestManifest_UILocales_LimitsAreBytes(t *testing.T) {
	cases := []struct {
		name  string
		langs map[string]map[string]string
		want  string
	}{
		{"one language over 1 MiB", map[string]map[string]string{"ru": fullLanguage(4000, 150)}, "bytes of strings, at most 1048576"},
		{"the manifest over 4 MiB across languages", map[string]map[string]string{
			"ru": fullLanguage(3000, 150), "uk": fullLanguage(3000, 150), "bg": fullLanguage(3000, 150),
			"sr": fullLanguage(3000, 150), "mk": fullLanguage(3000, 150),
		}, "across its languages, at most 4194304"},
		{"one string over 4 KiB", map[string]map[string]string{"es": {"a.b": strings.Repeat("x", wire.MaxUILocaleValueBytes+1)}}, "at most 4096"},
		{"an empty language", map[string]map[string]string{"es": {}}, "is empty"},
		{"a key with an empty segment", map[string]map[string]string{"es": {"a..b": "x"}}, "not a filex string key"},
		{"a key with a space", map[string]map[string]string{"es": {"a b": "x"}}, "not a filex string key"},
		{"a key over 128 bytes", map[string]map[string]string{"es": {strings.Repeat("k", 129): "x"}}, "not a filex string key"},
		{"__proto__", map[string]map[string]string{"es": {"__proto__.polluted": "x"}}, "not a filex string key"},
		{"constructor", map[string]map[string]string{"es": {"a.constructor.prototype": "x"}}, "not a filex string key"},
		{"one tag twice", map[string]map[string]string{"ES": {"a": "x"}, "es": {"a": "y"}}, "given twice"},
		{"not a tag", map[string]map[string]string{"spanish!": {"a": "x"}}, "not a language tag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseManifest(packManifest(t, "lang-x", c.langs))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestManifest_UILocales_TagsAreLowerCased(t *testing.T) {
	m, err := ParseManifest(packManifest(t, "lang-pt", map[string]map[string]string{"PT-BR": {"ctx.download": "Baixar"}}))
	require.NoError(t, err)
	assert.Contains(t, m.UILocales, "pt-br")
	assert.NotContains(t, m.UILocales, "PT-BR")
}

func TestIsLanguagePack(t *testing.T) {
	base := func() *wire.Manifest {
		return &wire.Manifest{Name: "p", UILocales: map[string]map[string]string{"es": {"a": "b"}}}
	}
	assert.True(t, base().IsLanguagePack(), "languages and nothing else")

	for name, mut := range map[string]func(m *wire.Manifest){
		"no languages":     func(m *wire.Manifest) { m.UILocales = nil },
		"an action":        func(m *wire.Manifest) { m.Actions = []wire.Action{{ID: "a"}} },
		"a view":           func(m *wire.Manifest) { m.Views = []wire.View{{ID: "v"}} },
		"a public page":    func(m *wire.Manifest) { m.PublicPages = []wire.PublicPage{{ID: "p"}} },
		"a setting":        func(m *wire.Manifest) { m.Settings = []wire.Field{{Key: "k"}} },
		"a permission":     func(m *wire.Manifest) { m.Permissions = []string{"state"} },
		"a module address": func(m *wire.Manifest) { m.Wasm = &wire.WasmSource{URL: "https://x/p.wasm"} },
		"schedule (tick)":  func(m *wire.Manifest) { m.Permissions = []string{"schedule"} },
		"a host grant":     func(m *wire.Manifest) { m.Permissions = []string{"http:api.example.com"} },
	} {
		m := base()
		mut(m)
		assert.False(t, m.IsLanguagePack(), name+" makes it an app with a module")
	}
}

func TestIsRTL(t *testing.T) {
	for _, tag := range []string{"ar", "AR", "he", "fa", "ur", "ps", "yi", "ckb", "ar-eg", "az-arab", "pa-arab"} {
		assert.True(t, wire.IsRTL(tag), tag)
	}
	for _, tag := range []string{"en", "es", "tr", "ru", "zh-hant", "ar-latn", "ku", "az"} {
		assert.False(t, wire.IsRTL(tag), tag)
	}
}

func TestInstall_LanguagePack_NeedsNoModule_AndRunsNothing(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	ctx := context.Background()
	manifest := packManifest(t, "lang-es", map[string]map[string]string{"es": {
		"ctx.download": "Descargar", "nav.files": "Archivos", "ctx.rename": "",
	}})

	_, dry, err := reg.Install(ctx, &InstallInput{Manifest: manifest, DryRun: true, Lang: "en"})
	require.NoError(t, err)
	assert.Equal(t, KindLanguagePack, dry.Kind)
	sum := sha256.Sum256(manifest)
	assert.Equal(t, hex.EncodeToString(sum[:]), dry.ManifestSHA256, "a pack is verified by its manifest's hash")
	assert.Empty(t, dry.WasmSHA256)
	assert.Zero(t, dry.WasmBytes)
	require.Len(t, dry.Languages, 1)
	assert.Equal(t, 2, dry.Languages[0].Keys, "an empty value is not a translation")

	st, _, err := reg.Install(ctx, &InstallInput{Manifest: manifest, Source: "upload", Granted: []string{}})
	require.NoError(t, err)
	assert.Equal(t, StateRunning, st.State)
	assert.Equal(t, KindLanguagePack, st.Kind)
	assert.Equal(t, dry.ManifestSHA256, st.SHA256)

	p, ok := reg.ByID(st.ID)
	require.True(t, ok)
	p.mu.RLock()
	assert.Nil(t, p.compiled, "no runtime instance is ever started for a pack")
	p.mu.RUnlock()
	assert.Empty(t, p.Row.WasmPath)
	assert.NoFileExists(t, filepath.Join(reg.Dir(), "lang-es", "plugin.wasm"))
	assert.FileExists(t, filepath.Join(reg.Dir(), "lang-es", "filex-app.json"))
	_, err = p.running()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "language pack")

	list := reg.UILocaleList()
	require.Len(t, list, 1)
	assert.Equal(t, UILocaleInfo{Code: "es", Source: "plugin", Plugin: "lang-es"}, list[0])
	assert.Equal(t, map[string]string{"ctx.download": "Descargar", "nav.files": "Archivos"}, reg.UILocale("ES"),
		"strings by tag, case-blind, and the untranslated empty one left out so English shows")

	// Switched off: its language goes with it; back on: it returns.
	_, err = reg.SetEnabled(ctx, st.ID, false)
	require.NoError(t, err)
	assert.Empty(t, reg.UILocaleList())
	assert.Nil(t, reg.UILocale("es"))
	_, err = reg.SetEnabled(ctx, st.ID, true)
	require.NoError(t, err)
	assert.Len(t, reg.UILocaleList(), 1)

	require.NoError(t, reg.Remove(ctx, st.ID))
	assert.Empty(t, reg.UILocaleList())
	assert.NoDirExists(t, filepath.Join(reg.Dir(), "lang-es"))
}

func TestInstall_LanguagePack_SurvivesARestart(t *testing.T) {
	reg, o := newPackRegistry(t, nil)
	ctx := context.Background()
	_, _, err := reg.Install(ctx, &InstallInput{Manifest: packManifest(t, "lang-es", map[string]map[string]string{"es": {"a.b": "c"}}), Granted: []string{}})
	require.NoError(t, err)

	// A second registry over the same rows and directory is a server restart.
	o.Dir = reg.Dir()
	again, err := New(o)
	require.NoError(t, err)
	t.Cleanup(func() { again.Close(ctx) })
	require.NoError(t, again.Load(ctx))
	p, ok := again.ByName("lang-es")
	require.True(t, ok)
	state, serr := p.State()
	assert.Equal(t, StateRunning, state, serr)
	assert.Equal(t, "c", again.UILocale("es")["a.b"])
}

func TestInstall_LanguagePack_RefusesAModule(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	_, _, err := reg.Install(context.Background(), &InstallInput{
		Manifest: packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "b"}}),
		Wasm:     bytes.NewReader([]byte("\x00asm\x01\x00\x00\x00")), Granted: []string{},
	})
	require.Error(t, err)
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeManifestInvalid, ie.Code)
	assert.Contains(t, ie.Message, "language pack")
	rows, _ := reg.opts.Store.ListAppPlugins(context.Background())
	assert.Empty(t, rows)
}

func TestInstall_AnAppStillNeedsItsModule(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	b, _ := json.Marshal(map[string]any{
		"manifest_version": 1, "name": "hello", "version": "1", "label": map[string]string{"en": "Hello"},
		"permissions": []string{"state"},
		"ui_locales":  map[string]any{"es": map[string]string{"a": "b"}},
	})
	_, _, err := reg.Install(context.Background(), &InstallInput{Manifest: b, Granted: []string{"state"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no module supplied", "a permission makes it an app, and an app has a module")
}

func TestInstall_LanguagePack_IsSignedOverItsManifest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	reg, _ := newPackRegistry(t, func(o *Options) { o.TrustedKeys = []string{hex.EncodeToString(pub)} })
	ctx := context.Background()
	manifest := packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "b"}})

	_, _, err = reg.Install(ctx, &InstallInput{Manifest: manifest, Granted: []string{}})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeSignatureRequired, ie.Code, "an instance that only runs signed apps still does, for a pack")

	sum := sha256.Sum256(manifest)
	sig := ed25519.Sign(priv, []byte(hex.EncodeToString(sum[:])))
	st, _, err := reg.Install(ctx, &InstallInput{Manifest: manifest, Signature: hex.EncodeToString(sig), Granted: []string{}})
	require.NoError(t, err)
	assert.True(t, st.Signed)

	// One changed byte in the manifest and the same signature no longer holds.
	tampered := bytes.Replace(manifest, []byte(`"b"`), []byte(`"B"`), 1)
	_, _, err = reg.Upgrade(ctx, st.ID, &InstallInput{Manifest: tampered, Signature: hex.EncodeToString(sig), Granted: []string{}})
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeSignatureInvalid, ie.Code)
}

func TestInstall_LanguagePack_PinnedBySHA256(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	_, _, err := reg.Install(context.Background(), &InstallInput{
		Manifest: packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "b"}}),
		SHA256:   strings.Repeat("0", 64), Granted: []string{},
	})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, ErrCodeSHA256Mismatch, ie.Code)
}

func TestUpgrade_LanguagePack(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	ctx := context.Background()
	st, _, err := reg.Install(ctx, &InstallInput{Manifest: packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "uno"}}), Granted: []string{}})
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "dos"}, "pt": {"a": "dois"}}), &raw))
	raw["version"] = "1.1.0"
	next, _ := json.Marshal(raw)
	up, _, err := reg.Upgrade(ctx, st.ID, &InstallInput{Manifest: next})
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", up.Version)
	assert.Equal(t, StateRunning, up.State)
	assert.Equal(t, "dos", reg.UILocale("es")["a"])
	assert.Equal(t, "dois", reg.UILocale("pt")["a"])
	assert.NoFileExists(t, filepath.Join(reg.Dir(), "lang-es", "plugin.wasm"))
}

func TestUILocale_FirstAppByNameWinsAContestedKey(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	ctx := context.Background()
	for _, name := range []string{"zz-es", "aa-es"} {
		_, _, err := reg.Install(ctx, &InstallInput{Manifest: packManifest(t, name, map[string]map[string]string{"es": {"k": name, name: "own"}}), Granted: []string{}})
		require.NoError(t, err)
	}
	got := reg.UILocale("es")
	assert.Equal(t, "aa-es", got["k"])
	assert.Equal(t, "own", got["zz-es"], "an uncontested key from the second app still arrives")
	require.Len(t, reg.UILocaleList(), 1)
	assert.Equal(t, "aa-es", reg.UILocaleList()[0].Plugin)
}

func TestLanguageRows_CoverageOfTheCurrentCatalogue(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	m, err := ParseManifest(packManifest(t, "lang-ar", map[string]map[string]string{
		"ar": {"a": "١", "b": "٢", "stale.key": "٣", "c": " "},
		"es": {"a": "uno"},
	}))
	require.NoError(t, err)

	// No catalogue in this binary: counts, but no pretend percentage.
	rows := reg.LanguageRows(m)
	require.Len(t, rows, 2)
	assert.Equal(t, LanguageRow{Code: "ar", Keys: 3, RTL: true}, rows[0])

	reg.SetCatalogue([]string{"a", "b", "c"})
	rows = reg.LanguageRows(m)
	assert.Equal(t, LanguageRow{Code: "ar", Keys: 3, Translated: 2, Unknown: 1, Total: 3, Percent: 66, RTL: true}, rows[0],
		"floor, not round: 2 of 3 is 66, and only a complete language reads 100")
	assert.Equal(t, LanguageRow{Code: "es", Keys: 1, Translated: 1, Total: 3, Percent: 33}, rows[1])

	var nilReg *Registry
	assert.Len(t, nilReg.LanguageRows(m), 2, "nil-safe: the wire fixture calls it without a registry")
}

func TestParseCatalogue(t *testing.T) {
	keys, err := ParseCatalogue([]byte(`{"b.x":"B","a":"A"}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b.x"}, keys)
	_, err = ParseCatalogue([]byte(`[1,2]`))
	assert.Error(t, err)
}

// roundTrip lets a test answer the registry's HTTP fetches from memory —
// raw.githubusercontent.com included — without a network.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetch_LanguagePackFromGitHubAndURL_NeedsNoModule(t *testing.T) {
	manifest := packManifest(t, "lang-es", map[string]map[string]string{"es": {"a": "b"}})
	var asked []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		asked = append(asked, r.URL.String())
		if strings.HasSuffix(r.URL.Path, "/filex-app.json") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(manifest)), Header: http.Header{}}, nil
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	reg, _ := newPackRegistry(t, func(o *Options) { o.HTTP = client })
	ctx := context.Background()

	in, err := reg.FetchGitHub(ctx, GitHubInput{Repo: "BRF-Tech/filex-lang-es"})
	require.NoError(t, err)
	assert.Nil(t, in.Wasm)
	assert.Equal(t, []string{"https://raw.githubusercontent.com/BRF-Tech/filex-lang-es/main/filex-app.json"}, asked,
		"the manifest and nothing else — there is no release asset to look for")
	in.Granted = []string{}
	st, _, err := reg.Install(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "github", st.Source)
	require.NoError(t, reg.Remove(ctx, st.ID))

	in, err = reg.FetchURL(ctx, URLInput{ManifestURL: "https://example.test/filex-app.json"})
	require.NoError(t, err)
	assert.Nil(t, in.Wasm)

	_, err = reg.FetchURL(ctx, URLInput{ManifestURL: "https://example.test/filex-app.json", URL: "https://example.test/plugin.wasm"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "language pack")
}

// The echo fixture is an app with a module; this checks the line can be
// crossed on upgrade in the direction a real author takes it: the pack an
// app used to carry becomes a pack of its own.
func TestUpgrade_AnAppBecomesALanguagePack(t *testing.T) {
	if _, err := os.Stat(fixtureWasm); err != nil {
		t.Skipf("%s not built", fixtureWasm)
	}
	h := newHarness(t, nil)
	p := h.install(t)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(packManifest(t, "echo", map[string]map[string]string{"es": {"a": "b"}}), &raw))
	raw["version"] = "0.0.2"
	next, _ := json.Marshal(raw)
	up, _, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next})
	require.NoError(t, err)
	assert.Equal(t, KindLanguagePack, up.Kind)
	assert.Equal(t, StateRunning, up.State)
	np, _ := h.reg.ByID(p.Row.ID)
	np.mu.RLock()
	assert.Nil(t, np.compiled)
	np.mu.RUnlock()
	assert.Empty(t, np.Row.WasmPath)
}

package wasmplugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── An app's own interface (M1): manifest, bundle, policy, serving ─────

// uiZip builds an interface bundle. A name that starts with "stored:" is
// written uncompressed.
func uiZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		method := zip.Deflate
		if n, ok := strings.CutPrefix(name, "stored:"); ok {
			name, method = n, zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		require.NoError(t, err)
		_, err = io.WriteString(w, body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func hexSum(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// uiManifest is an app that is ONLY an interface: a draw.io-like viewer.
func uiManifest(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"manifest_version": 1, "name": "drawio", "version": "1.0.0", "label": map[string]any{"en": "Draw"},
		"permissions": []any{"files:read", "files:write"},
		"ui":          map[string]any{"bundle": map[string]any{}},
		"views": []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "index.html",
			"label": map[string]any{"en": "Draw"}, "applies": map[string]any{"ext": []any{"drawio"}}}},
	}
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func uiDoc() map[string]string {
	return map[string]string{
		"index.html":      "<!doctype html><html><head><meta charset=utf-8><script src=app.js></script></head><body>draw</body></html>",
		"app.js":          "console.log('app')",
		"img/logo.svg":    "<svg xmlns='http://www.w3.org/2000/svg'><script>alert(1)</script></svg>",
		"stored:font.ttf": "FONTBYTES",
	}
}

// installUI installs an interface-only app through the ordinary Install, the
// way the admin upload does (manifest + `ui` part, no module).
func (h *harness) installUI(t *testing.T, manifest []byte, bundle []byte) (*Installed, *Status) {
	t.Helper()
	m, err := ParseManifest(manifest)
	require.NoError(t, err)
	st, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: manifest, UI: bytes.NewReader(bundle), Source: "upload", Granted: permStrings(m.Perms), Lang: "en",
	})
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	return p, st
}

func mustParse(t *testing.T, b []byte) *Manifest {
	t.Helper()
	m, err := ParseManifest(b)
	require.NoError(t, err)
	return m
}

func TestUIManifest_DerivesItsPermissionsFromTheUIBlock(t *testing.T) {
	m := mustParse(t, uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{
			"bundle": map[string]any{},
			"csp":    []any{"wasm-unsafe-eval", "unsafe-eval", "wasm-unsafe-eval"},
			"external": []any{
				map[string]any{"url": "https://fonts.example.net/inter/", "as": "font", "reason": map[string]any{"en": "Inter"}},
				map[string]any{"url": "https://cdn.example.net/katex@0.16.9/katex.min.css", "as": "style", "sha256": strings.Repeat("ab", 32), "reason": map[string]any{"en": "Maths"}},
			},
		}
	}))
	var got []string
	for _, p := range m.Perms {
		got = append(got, string(p))
	}
	assert.Equal(t, []string{"files:read", "files:write", "ui", "ui:wasm-eval", "ui:eval", "ui-net:font:https://fonts.example.net/inter/", "ui-viewer:.drawio"}, got,
		"ui, one permission per script exception, one per LIVE address; the mirrored file is no permission")
	rows := PermissionRows(m, "en")
	assert.Equal(t, "Inter", rows[len(rows)-2].Reason["en"], "a live address carries its own reason into the review")
	assert.False(t, m.NeedsModule(), "an interface that only opens files needs no module")
}

func TestUIManifest_RefusesWhatAnInterfaceMayNotDo(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"a ui permission written by hand": func(m map[string]any) { m["permissions"] = []any{"files:read", "ui"} },
		"a live address written by hand": func(m map[string]any) {
			m["permissions"] = []any{"ui-net:img:https://img.example.net/a.png"}
		},
		"an external script": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://cdn.example.net/x.js", "as": "script", "sha256": strings.Repeat("ab", 32), "reason": map[string]any{"en": "x"}}}}
		},
		"a connect address": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://api.example.net/", "as": "connect", "reason": map[string]any{"en": "x"}}}}
		},
		"a mirrored folder": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://cdn.example.net/dir/", "as": "font", "sha256": strings.Repeat("ab", 32), "reason": map[string]any{"en": "x"}}}}
		},
		"a wildcard host": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://*.example.net/a.css", "as": "style", "reason": map[string]any{"en": "x"}}}}
		},
		"a query string": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://cdn.example.net/a.css?v=1", "as": "style", "reason": map[string]any{"en": "x"}}}}
		},
		"plain http": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "http://cdn.example.net/a.css", "as": "style", "reason": map[string]any{"en": "x"}}}}
		},
		"a reason missing a declared language": func(m map[string]any) {
			m["languages"] = []any{"en", "tr"}
			m["label"] = map[string]any{"en": "Draw", "tr": "Çiz"}
			m["views"] = []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "index.html", "label": map[string]any{"en": "Draw", "tr": "Çiz"}}}
			m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{map[string]any{"url": "https://cdn.example.net/a.css", "as": "style", "reason": map[string]any{"en": "x"}}}}
		},
		"an unknown csp exception": func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{}, "csp": []any{"unsafe-inline"}}
		},
		"a viewer drawn by the module": func(m map[string]any) {
			m["views"] = []any{map[string]any{"id": "editor", "placement": "viewer", "label": map[string]any{"en": "Draw"}}}
		},
		"a ui path that climbs": func(m map[string]any) {
			m["views"] = []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "../index.html", "label": map[string]any{"en": "Draw"}}}
		},
		"a ui file that is not a page": func(m map[string]any) {
			m["views"] = []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "app.js", "label": map[string]any{"en": "Draw"}}}
		},
		"a view naming ui without a ui block": func(m map[string]any) { delete(m, "ui") },
		"a ui block no view opens": func(m map[string]any) {
			m["views"] = []any{map[string]any{"id": "s", "placement": "modal", "label": map[string]any{"en": "S"}}}
		},
	}
	for name, mutate := range cases {
		_, err := ParseManifest(uiManifest(t, mutate))
		assert.Error(t, err, name)
	}
}

func TestUIManifest_WhatNeedsAModule(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(m map[string]any)
		needs  bool
	}{
		"only an interface": {nil, false},
		"an action that opens the interface": {func(m map[string]any) {
			m["actions"] = []any{map[string]any{"id": "open", "view": "editor", "label": map[string]any{"en": "Open"}}}
		}, false},
		"an action that runs": {func(m map[string]any) {
			m["actions"] = []any{map[string]any{"id": "run", "label": map[string]any{"en": "Run"}}}
		}, true},
		"a surface view beside": {func(m map[string]any) {
			m["views"] = append(m["views"].([]any), map[string]any{"id": "s", "placement": "modal", "label": map[string]any{"en": "S"}})
		}, true},
		"a host permission": {func(m map[string]any) { m["permissions"] = []any{"files:read", "http:api.example.com"} }, true},
	} {
		assert.Equal(t, c.needs, mustParse(t, uiManifest(t, c.mutate)).NeedsModule(), name)
	}
}

func TestUIBundle_RefusesEveryPathThatIsNotAlreadyNormal(t *testing.T) {
	for _, bad := range []string{"../x.html", "a/../b.js", "./a.js", "a//b.js", "/abs.js", `a\b.js`, "C:/x.js", "a/./b.js", "x\x01.js"} {
		b := uiZip(t, map[string]string{"index.html": "<p>", bad: "x"})
		_, err := indexUIZip(bytes.NewReader(b), int64(len(b)), true)
		assert.Error(t, err, "%q", bad)
	}
	b := uiZip(t, map[string]string{"index.html": "<p>", "evil.exe": "MZ"})
	_, err := indexUIZip(bytes.NewReader(b), int64(len(b)), true)
	assert.ErrorContains(t, err, ".exe files are not served")
	b = uiZip(t, map[string]string{"Index.html": "<p>", "index.html": "<p>"})
	_, err = indexUIZip(bytes.NewReader(b), int64(len(b)), true)
	assert.ErrorContains(t, err, "differ only in case")
	b = uiZip(t, map[string]string{"index.html": "<p>", "LICENSE": "MIT", "js/app.mjs": "x", "dir/": ""})
	x, err := indexUIZip(bytes.NewReader(b), int64(len(b)), true)
	require.NoError(t, err)
	assert.Equal(t, 3, x.Count(), "directory entries are not files")
}

func TestUIBundle_RefusesALink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "index.html"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(hdr)
	_, _ = io.WriteString(w, "/etc/passwd")
	require.NoError(t, zw.Close())
	_, err := indexUIZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()), true)
	assert.ErrorContains(t, err, "link")
}

func TestUIPolicy_IsBuiltFromTheGrantAlone(t *testing.T) {
	pkg := "https://files.example.com/filex/_appui/drawio/0123456789abcdef/"
	csp, allow := UIPolicy(NewGrants([]Permission{PermUI}), pkg)
	assert.Equal(t, "default-src 'none'; script-src "+pkg+" "+uiBootstrapHash+"; style-src "+pkg+" 'unsafe-inline'; img-src "+pkg+" data: blob:; font-src "+pkg+" data:; media-src "+pkg+" blob:; connect-src 'none'; worker-src blob:; frame-src 'none'; child-src 'none'; object-src 'none'; manifest-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors *; sandbox allow-scripts", csp)
	assert.Equal(t, `("`+pkg+`*")`, allow)
	assert.NotContains(t, csp, "'self'", "WebKit reads 'self' as the frame's opaque origin and refuses the app's own scripts (lesson #633)")
	assert.NotContains(t, csp, "https://files.example.com ", "never the bare filex origin: WebKit sends the session cookie to it (lesson #634)")

	g := NewGrants([]Permission{PermUI, PermUIWasmEval, UINetPermission("font", "https://fonts.example.net/inter/"), UINetPermission("style", "https://cdn.example.net/a.css")})
	csp, allow = UIPolicy(g, pkg)
	assert.Contains(t, csp, "script-src "+pkg+" "+uiBootstrapHash+" 'wasm-unsafe-eval';")
	assert.NotContains(t, csp, "'unsafe-eval'", "only what was granted")
	assert.Contains(t, csp, "font-src "+pkg+" data: https://fonts.example.net/inter/;")
	assert.Contains(t, csp, "style-src "+pkg+" 'unsafe-inline' https://cdn.example.net/a.css;")
	assert.Contains(t, csp, "connect-src 'none'")
	assert.Equal(t, `("`+pkg+`*" "https://cdn.example.net/a.css" "https://fonts.example.net/inter/*")`, allow,
		"Chrome refuses a live address that is not in the allowlist too")
}

func TestUIBootstrap_ComesBeforeAnythingOfTheApps(t *testing.T) {
	h := sha256.Sum256([]byte(uiBootstrap))
	assert.Equal(t, "'sha256-"+base64.StdEncoding.EncodeToString(h[:])+"'", uiBootstrapHash)
	for in, want := range map[string]string{
		"<!DOCTYPE html>\n<html><head><script src=a.js></script>": "<!DOCTYPE html>" + uiBootstrapTag + "\n<html>",
		"<html><head>":                          uiBootstrapTag + "<html><head>",
		"\ufeff<!doctype html><meta charset=x>": "\ufeff<!doctype html>" + uiBootstrapTag + "<meta",
		"<script>evil()</script>":               uiBootstrapTag + "<script>evil()",
	} {
		got := string(uiInjectBootstrap([]byte(in)))
		assert.True(t, strings.HasPrefix(got, want), "%q → %q", in, got)
	}
}

func uiServer(t *testing.T, h *harness, base string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h.reg.UIHandler(func(r *http.Request) (string, bool) { return "http://" + r.Host + base, true }))
	t.Cleanup(srv.Close)
	return srv
}

func TestUIServe_TheInterfaceOnlyAppInstallsAndIsServedUnderItsPolicy(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, st := h.installUI(t, uiManifest(t, nil), bundle)
	state, serr := p.State()
	require.Equal(t, StateRunning, state, serr)
	assert.False(t, st.Engine, "no module")
	require.NotNil(t, st.UI)
	assert.Equal(t, hexSum(bundle), st.UI.SHA256)
	assert.Equal(t, 4, st.UI.Files)
	assert.FileExists(t, filepath.Join(h.reg.Dir(), "drawio", "ui.zip"))
	assert.NoFileExists(t, filepath.Join(h.reg.Dir(), "drawio", "plugin.wasm"))

	ans, err := h.reg.ActionsFor(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, ans.Views, 1)
	ref := ans.Views[0].UI
	require.NotNil(t, ref, "the viewer row says how to open the interface")
	short := hexSum(bundle)[:16]
	assert.Equal(t, "/_appui/drawio/"+short+"/index.html", ref.URL)
	assert.Equal(t, []string{"files:read", "files:write", "ui", "ui-viewer:.drawio"}, ref.Grants)
	assert.False(t, ref.Engine)

	srv := uiServer(t, h, "/filex")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_appui/drawio/"+short+"/index.html", nil)
	req.AddCookie(&http.Cookie{Name: "filex_session", Value: "secret"})
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	pkg := srv.URL + "/filex/_appui/drawio/" + short + "/"
	csp := res.Header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "script-src "+pkg+" "+uiBootstrapHash)
	assert.Contains(t, csp, "sandbox allow-scripts")
	assert.Equal(t, `("`+pkg+`*")`, res.Header.Get("Connection-Allowlist"))
	assert.Equal(t, "off", res.Header.Get("X-DNS-Prefetch-Control"))
	assert.Equal(t, "no-referrer", res.Header.Get("Referrer-Policy"))
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, res.Header.Get("Permissions-Policy"), "camera=()")
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"), "the page carries the grant's policy: revalidated (security review UI-4)")
	assert.Empty(t, res.Header.Values("Set-Cookie"))
	assert.True(t, strings.HasPrefix(string(body), "<!doctype html>"+uiBootstrapTag+"<html>"), "the bootstrap first")

	for path, ct := range map[string]string{"app.js": "text/javascript", "img/logo.svg": "image/svg+xml", "font.ttf": "font/ttf"} {
		res, err := http.Get(srv.URL + "/_appui/drawio/" + short + "/" + path)
		require.NoError(t, err)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode, path)
		assert.Contains(t, res.Header.Get("Content-Type"), ct, path)
		assert.Equal(t, "default-src 'none'; sandbox", res.Header.Get("Content-Security-Policy"), "%s opened as a document runs nothing", path)
		assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, uiDoc()[map[string]string{"app.js": "app.js", "img/logo.svg": "img/logo.svg", "font.ttf": "stored:font.ttf"}[path]], string(b))
	}
}

func TestUIServe_AnythingButAnExactPathOfTheRunningVersionIs404(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, _ := h.installUI(t, uiManifest(t, nil), bundle)
	short := hexSum(bundle)[:16]
	srv := uiServer(t, h, "")
	for _, path := range []string{
		"/_appui/drawio/" + short + "/../drawio/filex-app.json",
		"/_appui/drawio/" + short + "/%2e%2e/filex-app.json",
		"/_appui/drawio/" + short + "/img%2flogo.svg",
		"/_appui/drawio/" + short + "/img%5clogo.svg",
		"/_appui/drawio/" + short + "/./index.html",
		"/_appui/drawio/" + short + "//index.html",
		"/_appui/drawio/" + short + "/missing.html",
		"/_appui/drawio/" + short + "/",
		"/_appui/drawio/" + strings.Repeat("0", 16) + "/index.html",
		"/_appui/drawio/" + short[:15] + "/index.html",
		"/_appui/other/" + short + "/index.html",
		"/_appui/drawio/" + short + "/ui.zip",
		"/_appui/drawio/" + short + "/filex-app.json",
	} {
		res, err := http.DefaultClient.Do(mustRaw(t, srv.URL+path))
		require.NoError(t, err)
		res.Body.Close()
		assert.Equal(t, http.StatusNotFound, res.StatusCode, path)
	}
	_, err := h.reg.SetEnabled(context.Background(), p.Row.ID, false)
	require.NoError(t, err)
	res, err := http.Get(srv.URL + "/_appui/drawio/" + short + "/index.html")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode, "a switched-off app serves nothing")
	res, err = http.Post(srv.URL+"/_appui/drawio/"+short+"/index.html", "text/plain", nil)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
}

// mustRaw builds a request whose path goes out byte for byte (no cleaning,
// no re-encoding).
func mustRaw(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	require.NoError(t, err)
	if i := strings.Index(raw, "/_appui/"); i >= 0 {
		req.URL.Opaque = "//" + req.URL.Host + raw[i:]
	}
	return req
}

func TestUIInstall_TheBundleIsPinnedAndChecked(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	m := uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": strings.Repeat("0", 64)}}
	})
	mm := mustParse(t, m)
	_, _, err := h.reg.Install(context.Background(), &InstallInput{Manifest: m, UI: bytes.NewReader(bundle), Granted: permStrings(mm.Perms)})
	assert.Equal(t, ErrCodeSHA256Mismatch, installError(t, err).Code, "%v", err)

	_, _, err = h.reg.Install(context.Background(), &InstallInput{Manifest: uiManifest(t, nil), Granted: permStrings(mm.Perms)})
	assert.ErrorContains(t, err, "supply its bundle")

	noIndex := uiZip(t, map[string]string{"other.html": "<p>"})
	_, _, err = h.reg.Install(context.Background(), &InstallInput{Manifest: uiManifest(t, nil), UI: bytes.NewReader(noIndex), Granted: permStrings(mm.Perms)})
	assert.ErrorContains(t, err, "does not hold")

	needs := uiManifest(t, func(m map[string]any) {
		m["actions"] = []any{map[string]any{"id": "run", "label": map[string]any{"en": "Run"}}}
	})
	_, _, err = h.reg.Install(context.Background(), &InstallInput{Manifest: needs, UI: bytes.NewReader(bundle), Granted: permStrings(mm.Perms)})
	assert.ErrorContains(t, err, "action run runs the module")

	_, _, err = h.reg.Install(context.Background(), &InstallInput{Manifest: uiManifest(t, nil), UI: bytes.NewReader(bundle), Granted: []string{"files:read", "files:write"}})
	assert.Equal(t, ErrCodePermissionsIncomplete, installError(t, err).Code, "the derived `ui` permission must be granted like any other: %v", err)
}

func TestUIInstall_AnInstanceThatOnlyRunsSignedAppsWantsTheBundlePinned(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	h := newBareHarness(t, func(o *Options) { o.TrustedKeys = []string{hex.EncodeToString(pub)} })
	bundle := uiZip(t, uiDoc())
	mm := mustParse(t, uiManifest(t, nil))
	_, _, err = h.reg.Install(context.Background(), &InstallInput{Manifest: uiManifest(t, nil), UI: bytes.NewReader(bundle), Granted: permStrings(mm.Perms)})
	assert.Equal(t, ErrCodeSignatureRequired, installError(t, err).Code, "%v", err)
}

func TestUIInstall_MirroredFilesAreFetchedOnceAndServedFromThePackage(t *testing.T) {
	h := newBareHarness(t, nil)
	css := "body{font-family:Inter}"
	net := &assetNet{files: map[string]string{"https://cdn.example.net/katex@0.16.9/katex.min.css": css}}
	h.reg.SetHTTPTransport(net)
	bundle := uiZip(t, uiDoc())
	man := uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{
			map[string]any{"url": "https://cdn.example.net/katex@0.16.9/katex.min.css", "as": "style", "sha256": sumOf(css), "reason": map[string]any{"en": "Maths"}},
		}}
	})
	_, st := h.installUI(t, man, bundle)
	require.Len(t, net.hits, 1)
	require.Len(t, st.UI.External, 1)
	assert.Equal(t, "mirror", st.UI.External[0].Mode)
	assert.Equal(t, "ext/cdn.example.net/katex@0.16.9/katex.min.css", st.UI.External[0].Path)
	srv := uiServer(t, h, "")
	res, err := http.Get(srv.URL + "/_appui/drawio/" + hexSum(bundle)[:16] + "/ext/cdn.example.net/katex@0.16.9/katex.min.css")
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Equal(t, css, string(b))
	assert.Contains(t, res.Header.Get("Content-Type"), "text/css")
	assert.Len(t, net.hits, 1, "served from the server's copy: no browser, and no second download, asks the address")

	bad := &assetNet{files: map[string]string{"https://cdn.example.net/katex@0.16.9/katex.min.css": "tampered"}}
	h2 := newBareHarness(t, nil)
	h2.reg.SetHTTPTransport(bad)
	mm := mustParse(t, man)
	_, _, err = h2.reg.Install(context.Background(), &InstallInput{Manifest: man, UI: bytes.NewReader(bundle), Granted: permStrings(mm.Perms)})
	assert.Equal(t, ErrCodeSHA256Mismatch, installError(t, err).Code, "a mirrored file that does not match its hash is refused: %v", err)
}

func TestUIInstall_ATamperedBundleOnDiskStopsTheApp(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, _ := h.installUI(t, uiManifest(t, nil), bundle)
	require.NoError(t, os.WriteFile(filepath.Join(h.reg.Dir(), "drawio", "ui.zip"), uiZip(t, map[string]string{"index.html": "<p>evil"}), 0o600))
	h.reg.compile(context.Background(), p)
	state, serr := p.State()
	assert.Equal(t, StateFailed, state)
	assert.Contains(t, serr, "does not match")
}

func TestUIUpgrade_ANewLiveAddressIsANewPermission(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	p, _ := h.installUI(t, uiManifest(t, nil), bundle)
	next := uiManifest(t, func(m map[string]any) {
		m["version"] = "1.1.0"
		m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{
			map[string]any{"url": "https://fonts.example.net/inter/", "as": "font", "reason": map[string]any{"en": "Inter"}},
		}}
	})
	_, dry, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(bundle), DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"ui-net:font:https://fonts.example.net/inter/"}, dry.Upgrade.Added)
	_, _, err = h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(bundle)})
	assert.Equal(t, ErrCodePermissionsChanged, installError(t, err).Code, "%v", err)
}

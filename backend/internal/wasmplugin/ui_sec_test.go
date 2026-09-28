package wasmplugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Security review, feat/app-ui (docs/research/filex-app-security-review-2026-09.md §6) ──

// UI-3 · UI-13: an external address goes RAW into a CSP header and into the
// structured-field Connection-Allowlist, where a URLPattern reads it. Only the
// canonical, plain form of an address is accepted — anything that could end
// a directive, break the structured field or widen the pattern is refused —
// and no address names an IP literal or a name that is not public.
func TestUISec_AnExternalAddressIsCanonicalPlainAndPublic(t *testing.T) {
	for _, bad := range []string{
		`https://cdn.example.net/a\b.css`,    // backslash: a structured-field escape, a URLPattern escape
		`https://cdn.example.net/ä.css`,      // not ASCII: not a structured-field string
		`https://cdn.example.net/a(b).css`,   // URLPattern group
		`https://cdn.example.net/{a}.css`,    // URLPattern group
		`https://cdn.example.net/a+.css`,     // URLPattern modifier
		`https://cdn.example.net/:id/a.css`,  // URLPattern named group
		`https://cdn.example.net/a%zz.css`,   // a broken escape
		`https://cdn.example.net/a%2F..%2Fb`, // an escaped separator
		`https://cdn.example.net/a!.css`,     // outside the plain path set
		`https://cdn.example.net//a.css`,     // an empty segment
		`https://cdn.example.net:x/a.css`,    // a port that is not a number
		`https://Cdn.example.net/a.css`,      // not lower case
		`https://93.184.216.34/a.css`,        // an IP literal
		`https://[2001:db8::1]/a.css`,        // an IP literal
		`https://fonts.local/a.css`,          // not a public name
		`https://intranet.corp/a.css`,        // not a public name
		`https://files.home.arpa/a.css`,      // not a public name
		`https://cdn.localhost/a.css`,        // not a public name
		`https://cdn.internal/a.css`,         // not a public name
		`https://cdn.example.test/a.css`,     // reserved
		`https://cdn.example.net/a.css"`,     // a quote
	} {
		assert.Error(t, checkExternalURL(bad, false), bad)
		_, err := ParsePermission(`ui-net:style:` + bad)
		assert.Error(t, err, "an old grant carrying %s is not read either", bad)
	}
	for _, ok := range []string{
		`https://cdn.example.net/lib/a.css`,
		`https://fonts.gstatic.com/s/inter/`,
		`https://cdn.example.net:8443/x/y_z~1-2.woff2`,
		`https://cdn.example.net/a%20b.css`,
		`https://cdn.jsdelivr.net/npm/katex@0.16.9/dist/katex.min.css`,
	} {
		assert.NoError(t, checkExternalURL(ok, false), ok)
	}
}

// sfString is RFC 8941 sf-string: printable ASCII, no bare quote or backslash.
var sfString = regexp.MustCompile(`^"[\x20\x21\x23-\x5b\x5d-\x7e]*"$`)

// plainPattern is an https URL whose path holds no URLPattern special
// character; only a trailing `*` (a folder) may follow it.
var plainPattern = regexp.MustCompile(`^https?://[a-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~@/%-]*\*?$`)

// UI-3: whatever the grant holds, the Connection-Allowlist is an inner list
// of tokens and plain sf-strings, and no pattern carries a URLPattern special
// character but the trailing `*` filex adds to a folder.
func TestUISec_TheConnectionAllowlistIsAWellFormedInnerList(t *testing.T) {
	grants := NewGrants([]Permission{
		UINetPermission("font", "https://fonts.gstatic.com/s/inter/"),
		UINetPermission("img", "https://img.example.net/a%20b.png"),
		PermUI,
	})
	_, allow := UIPolicy(grants, "https://files.example.com/_appui/x/0123456789abcdef/")
	require.True(t, strings.HasPrefix(allow, "(") && strings.HasSuffix(allow, ")"), allow)
	for _, item := range strings.Fields(allow[1 : len(allow)-1]) {
		require.Regexp(t, sfString, item)
		assert.Regexp(t, plainPattern, strings.Trim(item, `"`))
	}
	// A grant row that was written before the check (or by hand in the
	// database) is dropped, never pasted into a header.
	bad := NewGrants([]Permission{Permission(`ui-net:img:https://x.example.net/a" 'unsafe-eval'`), PermUI})
	csp, allow2 := UIPolicy(bad, "https://files.example.com/_appui/x/0123456789abcdef/")
	assert.NotContains(t, csp, "unsafe-eval")
	assert.Equal(t, `("https://files.example.com/_appui/x/0123456789abcdef/*")`, allow2)
}

// UI-4 · UI-12: an interface's HTML carries a policy built from the grant, so
// it is never cached as immutable (a narrowed grant would keep the old policy
// for a year): no-cache with a validator, varied on the host it names. Its
// other files — the same bytes and the same fixed headers for a hash — stay
// immutable. An error is never cached.
func TestUISec_TheHTMLIsRevalidatedAndErrorsAreNotCached(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	h.installUI(t, uiManifest(t, nil), bundle)
	short := hexSum(bundle)[:16]
	srv := uiServer(t, h, "")

	res, err := http.Get(srv.URL + "/_appui/drawio/" + short + "/index.html")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	etag := res.Header.Get("ETag")
	require.NotEmpty(t, etag)
	assert.Contains(t, res.Header.Values("Vary"), "Host")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_appui/drawio/"+short+"/index.html", nil)
	req.Header.Set("If-None-Match", etag)
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotModified, res.StatusCode)

	res, err = http.Get(srv.URL + "/_appui/drawio/" + short + "/app.js")
	require.NoError(t, err)
	res.Body.Close()
	assert.Contains(t, res.Header.Get("Cache-Control"), "immutable")

	for _, p := range []string{"/_appui/drawio/" + short + "/missing.js", "/_appui/drawio/" + strings.Repeat("0", 16) + "/index.html"} {
		res, err = http.Get(srv.URL + p)
		require.NoError(t, err)
		res.Body.Close()
		assert.Equal(t, http.StatusNotFound, res.StatusCode, p)
		assert.Equal(t, "no-store", res.Header.Get("Cache-Control"), p)
	}
}

// UI-8: a mirrored file is served with the type its declaration (`as`)
// allows, never the one its name suggests: an "image" whose address ends in
// .js would otherwise be a script from the package's own address — which the
// page's script-src allows.
func TestUISec_AMirroredFileIsServedAsWhatItWasDeclared(t *testing.T) {
	h := newBareHarness(t, nil)
	js := "alert(document.domain)"
	css := "body{}"
	net := &assetNet{files: map[string]string{
		"https://cdn.example.net/evil.js":      js,
		"https://cdn.example.net/a/style.css":  css,
		"https://cdn.example.net/a/font.woff2": "WOFF2",
	}}
	h.reg.SetHTTPTransport(net)
	bundle := uiZip(t, uiDoc())
	man := uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{}, "external": []any{
			map[string]any{"url": "https://cdn.example.net/evil.js", "as": "img", "sha256": sumOf(js), "reason": map[string]any{"en": "x"}},
			map[string]any{"url": "https://cdn.example.net/a/style.css", "as": "font", "sha256": sumOf(css), "reason": map[string]any{"en": "x"}},
			map[string]any{"url": "https://cdn.example.net/a/font.woff2", "as": "font", "sha256": sumOf("WOFF2"), "reason": map[string]any{"en": "x"}},
		}}
	})
	h.installUI(t, man, bundle)
	srv := uiServer(t, h, "")
	get := func(p string) string {
		res, err := http.Get(srv.URL + "/_appui/drawio/" + hexSum(bundle)[:16] + "/" + p)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, p)
		assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		return res.Header.Get("Content-Type")
	}
	assert.Equal(t, "application/octet-stream", get("ext/cdn.example.net/evil.js"), "an image that is a script is not served as one")
	assert.Equal(t, "application/octet-stream", get("ext/cdn.example.net/a/style.css"), "a font that is a stylesheet is not served as one")
	assert.Equal(t, "font/woff2", get("ext/cdn.example.net/a/font.woff2"))
}

// UI-2: on an instance that only runs signed apps, the signature covers the
// MODULE — so a module app's interface must be vouched for by the module:
// its describe declares the same `ui` block (bundle hash, script exceptions,
// addresses) as the manifest it is installed with. A module that says nothing
// about an interface does not get one it was never signed with.
func TestUISec_ASignedModuleVouchesForItsInterface(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	h := newHarness(t, func(o *Options) { o.TrustedKeys = []string{hex.EncodeToString(pub)} })
	bundle := uiZip(t, uiDoc())
	in := echoInput(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": hexSum(bundle)}}
		m["views"] = append(m["views"].([]any), map[string]any{"id": "panel", "placement": "modal", "ui": "index.html", "label": map[string]any{"en": "Panel", "tr": "Pano"}})
	})
	wasm, err := io.ReadAll(in.Wasm)
	require.NoError(t, err)
	in.Wasm = bytes.NewReader(wasm)
	in.UI = bytes.NewReader(bundle)
	in.Signature = hex.EncodeToString(ed25519.Sign(priv, []byte(hexSum(wasm))))
	mm := mustParse(t, in.Manifest)
	in.Granted = permStrings(mm.Perms)
	_, _, err = h.reg.Install(context.Background(), in)
	require.Error(t, err, "the echo module describes no interface: the signature does not cover this one")
	assert.Equal(t, ErrCodeDescribeMismatch, installError(t, err).Code, "%v", err)
}

func TestUISec_DescribedInterfaceMustMatch(t *testing.T) {
	spec := func(sum string, csp ...string) *wire.UISpec {
		return &wire.UISpec{Bundle: wire.UIBundle{SHA256: sum}, CSP: csp,
			External: []wire.UIExternal{{URL: "https://fonts.example.net/inter/", As: "font"}}}
	}
	a := strings.Repeat("a", 64)
	m := &Manifest{}
	m.UI = spec(a)
	signed := &Registry{trusted: []ed25519.PublicKey{make(ed25519.PublicKey, ed25519.PublicKeySize)}}
	unsigned := &Registry{}
	assert.NoError(t, signed.checkDescribedUI(m, &wire.Manifest{UI: spec(strings.ToUpper(a))}))
	assert.Error(t, signed.checkDescribedUI(m, &wire.Manifest{UI: spec(strings.Repeat("b", 64))}), "another bundle")
	assert.Error(t, signed.checkDescribedUI(m, &wire.Manifest{UI: spec(a, "unsafe-eval")}), "another script exception")
	other := spec(a)
	other.External[0].URL = "https://evil.example.net/inter/"
	assert.Error(t, signed.checkDescribedUI(m, &wire.Manifest{UI: other}), "another address")
	assert.Error(t, signed.checkDescribedUI(m, &wire.Manifest{}), "signed: the module must vouch")
	assert.NoError(t, unsigned.checkDescribedUI(m, &wire.Manifest{}), "unsigned: the administrator's own pin is the check")
	assert.Error(t, unsigned.checkDescribedUI(m, &wire.Manifest{UI: spec(strings.Repeat("b", 64))}), "a module that describes another interface is refused everywhere")
	assert.NoError(t, signed.checkDescribedUI(&Manifest{}, &wire.Manifest{UI: spec(a)}), "no interface installed: nothing to vouch for")
}

// UI-6: a viewer names what it opens. One with no extension and no type
// would become the viewer of EVERY file — the text files, the PDFs, the
// photos — the moment it is installed.
func TestUISec_AViewerNamesWhatItOpens(t *testing.T) {
	for name, applies := range map[string]any{
		"no applies at all": nil,
		"an empty rule":     map[string]any{},
		"only a kind":       map[string]any{"kind": "file"},
		"a wildcard type":   map[string]any{"mime": []any{"*/*"}},
		"folders":           map[string]any{"kind": "dir", "ext": []any{"drawio"}},
		"not an extension":  map[string]any{"ext": []any{"a b"}},
		"engine extensions": map[string]any{"ext": []any{"drawio"}, "engine_ext": map[string]any{"libreoffice": []any{"docx"}}},
	} {
		_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
			v := m["views"].([]any)[0].(map[string]any)
			if applies == nil {
				delete(v, "applies")
			} else {
				v["applies"] = applies
			}
		}))
		assert.Error(t, err, name)
	}
	_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["views"].([]any)[0].(map[string]any)["applies"] = map[string]any{"mime": []any{"application/vnd.jgraph.mxfile"}}
	}))
	assert.NoError(t, err, "a type is enough")

	// The review says what it opens, kind by kind: an update that makes the
	// app the viewer of one more kind is a new grant.
	m, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["views"].([]any)[0].(map[string]any)["applies"] = map[string]any{"ext": []any{"drawio", ".DIO"}, "mime": []any{"application/vnd.jgraph.mxfile"}}
	}))
	require.NoError(t, err)
	var rows []string
	for _, r := range PermissionRows(m, "en") {
		rows = append(rows, r.ID+" = "+r.Label)
	}
	assert.Contains(t, rows, "ui-viewer:.drawio = Opens .drawio files in its own interface, in place of filex's preview")
	assert.Contains(t, rows, "ui-viewer:.dio = Opens .dio files in its own interface, in place of filex's preview")
	assert.Contains(t, rows, "ui-viewer:application/vnd.jgraph.mxfile = Opens application/vnd.jgraph.mxfile files in its own interface, in place of filex's preview")
	for _, r := range PermissionRows(m, "tr") {
		if r.ID == "ui-viewer:.drawio" {
			assert.Equal(t, ".drawio dosyalarını filex'in önizlemesi yerine kendi arayüzünde açar", r.Label)
		}
	}
	for _, id := range []string{"ui-viewer:.drawio", "ui-viewer:image/*"} {
		_, err := ParsePermission(id)
		assert.NoError(t, err, id)
	}
	for _, id := range []string{"ui-viewer:*/*", "ui-viewer:", "ui-viewer:.", "ui-viewer:a b", "ui-viewer:image/(x)"} {
		_, err := ParsePermission(id)
		assert.Error(t, err, id)
	}
	_, err = ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "ui-viewer:.pdf")
	}))
	assert.Error(t, err, "derived: never listed")
}

// UI-16: every powerful feature a frame could ask the browser for is off in
// the interface's Permissions-Policy — not only the camera and the
// microphone. Two are left on on purpose: sync-xhr (an editor reads its own
// package with it, draw.io does) and autoplay (a media app plays what the
// person opened); neither reaches anything the CSP does not already hold.
func TestUISec_EveryPowerfulFeatureIsOff(t *testing.T) {
	got := map[string]bool{}
	for _, part := range strings.Split(uiPermissionsPolicy, ",") {
		part = strings.TrimSpace(part)
		name, val, ok := strings.Cut(part, "=")
		require.True(t, ok, part)
		assert.Equal(t, "()", val, part)
		assert.False(t, got[name], "listed twice: %s", name)
		got[name] = true
	}
	for _, f := range []string{
		"accelerometer", "bluetooth", "browsing-topics", "camera", "captured-surface-control", "clipboard-read",
		"clipboard-write", "compute-pressure", "digital-credentials-get", "display-capture", "encrypted-media",
		"fullscreen", "gamepad", "geolocation", "gyroscope", "hid", "identity-credentials-get", "idle-detection",
		"keyboard-map", "local-fonts", "magnetometer", "microphone", "midi", "otp-credentials", "payment",
		"picture-in-picture", "private-state-token-issuance", "private-state-token-redemption",
		"publickey-credentials-create", "publickey-credentials-get", "screen-wake-lock", "serial",
		"storage-access", "usb", "web-share", "window-management", "xr-spatial-tracking",
	} {
		assert.True(t, got[f], "%s is not switched off", f)
	}
	// Measured, Chrome 153: an error line in the app author's console for
	// each feature it does not know.
	for _, f := range []string{
		"ambient-light-sensor", "attribution-reporting", "direct-sockets", "join-ad-interest-group",
		"private-aggregation", "run-ad-auction", "shared-storage", "shared-storage-select-url", "smart-card",
		"speaker-selection",
	} {
		assert.False(t, got[f], "%s: Chrome does not know it and says so in the console", f)
	}
	assert.False(t, got["sync-xhr"], "sync-xhr stays: an editor reads its own package with it")
	assert.False(t, got["autoplay"], "autoplay stays: a media app plays what was opened")
}

// Burak, 2026-09-27: `ui:package-fetch` — an interface may READ ITS OWN
// PACKAGE (draw.io loads its stencils and translations that way), and
// nothing else. The manifest says `ui.package_fetch: true`; the permission is
// derived and on the review ("reads its own package"); connect-src and the
// Connection-Allowlist name this version's path and nothing more.
func TestUISec_PackageFetchReadsItsOwnVersionOnly(t *testing.T) {
	m, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{}, "package_fetch": true}
	}))
	require.NoError(t, err)
	assert.Contains(t, m.Perms, PermUIPackageFetch)
	labels := map[string]string{}
	for _, lang := range []string{"en", "tr"} {
		for _, r := range PermissionRows(m, lang) {
			if r.ID == string(PermUIPackageFetch) {
				labels[lang] = r.Label
			}
		}
	}
	assert.Equal(t, "Its interface reads its own package — the files of this version, nothing else", labels["en"])
	assert.Equal(t, "Arayüzü kendi paketini okur — yalnız bu sürümün dosyalarını, başka hiçbir şeyi değil", labels["tr"])

	plain, err := ParseManifest(uiManifest(t, nil))
	require.NoError(t, err)
	assert.NotContains(t, plain.Perms, PermUIPackageFetch, "only when the manifest asks")
	_, err = ParseManifest(uiManifest(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "ui:package-fetch")
	}))
	assert.Error(t, err, "derived: never listed")

	pkg := "https://files.example.com/filex/_appui/drawio/0123456789abcdef/"
	csp, allow := UIPolicy(NewGrants([]Permission{PermUI, PermUIPackageFetch}), pkg)
	assert.Contains(t, csp, "; connect-src "+pkg+";")
	assert.Equal(t, `("`+pkg+`*")`, allow, "Chrome: this version's path, nothing else — not even filex's origin")
	cspNo, allowNo := UIPolicy(NewGrants([]Permission{PermUI}), pkg)
	assert.Contains(t, cspNo, "; connect-src 'none';", "not granted: no connection at all")
	assert.Equal(t, allow, allowNo, "the allowlist is the package's path either way")

	// What the connect-src lets through, the way a browser matches a
	// host-source with a path (a prefix ending in "/").
	var sources []string
	for _, d := range strings.Split(csp, ";") {
		if f := strings.Fields(d); len(f) > 0 && f[0] == "connect-src" {
			sources = f[1:]
		}
	}
	reaches := func(u string) bool {
		for _, s := range sources {
			if strings.HasPrefix(u, s) {
				return true
			}
		}
		return false
	}
	assert.True(t, reaches(pkg+"resources/dia.txt"), "its own files")
	assert.True(t, reaches(pkg+"ext/cdn.example.net/a.css"), "its mirrored files")
	for _, u := range []string{
		"https://files.example.com/filex/_appui/drawio/fedcba9876543210/resources/dia.txt", // another version
		"https://files.example.com/filex/_appui/other/0123456789abcdef/index.html",         // another app
		"https://files.example.com/filex/api/files/list",                                   // filex's API
		"https://files.example.com/filex/",                                                 // filex's pages
		"https://files.example.com/",
		"https://evil.example.net/drawio/0123456789abcdef/", // outside
	} {
		assert.False(t, reaches(u), u)
	}
}

// The module's describe carries package_fetch like the rest of the ui block
// (UI-2): a module that does not say it cannot have it granted by a manifest
// beside it.
func TestUISec_PackageFetchIsPartOfTheDescribedInterface(t *testing.T) {
	want, err := ParseManifest(uiManifest(t, func(m map[string]any) {
		m["ui"] = map[string]any{"bundle": map[string]any{"sha256": strings.Repeat("ab", 32)}, "package_fetch": true}
	}))
	require.NoError(t, err)
	r := &Registry{}
	got := &wire.Manifest{UI: &wire.UISpec{Bundle: wire.UIBundle{SHA256: strings.Repeat("ab", 32)}}}
	assert.Error(t, r.checkDescribedUI(want, got), "the module does not ask for it")
	got.UI.PackageFetch = true
	assert.NoError(t, r.checkDescribedUI(want, got))
}

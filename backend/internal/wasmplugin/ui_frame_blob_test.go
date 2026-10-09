package wasmplugin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── A frame of the interface's own package, and blob: in connect-src ──────
//
// The office editor app (filex-office-editor, task #189) runs ONLYOFFICE's
// editor in the browser: DocsAPI.DocEditor puts the editor page in a frame of
// its own (frameEditor), and the editor loads the document with XHR from a
// blob: address. Two permissions an app asks for in its `ui` block, the
// administrator grants at the review, and nothing has unless it asked:
//
//   - `ui:frame-package` (`ui.frame_package`): frame-src and child-src name
//     this version's own path and nothing else - never data: or blob:, never
//     another version, another app, a page of filex or the network;
//   - `ui:connect-blob` (`ui.connect_blob`): blob: in connect-src, and
//     nothing else changes.
//
// UIPolicy is the one place the policy is built; these tests hold it to the
// grant, the review to its sentences and the serving route to the policy on
// every page of the package, the framed ones included.

const frameBlobPkg = "https://files.example.com/filex/_appui/office-editor/0123456789abcdef/"

// cspSources is the source list of one directive of a policy, nil when the
// policy has no such directive.
func cspSources(csp, name string) []string {
	for _, d := range strings.Split(csp, ";") {
		if f := strings.Fields(d); len(f) > 0 && f[0] == name {
			return f[1:]
		}
	}
	return nil
}

// cspAllows reports whether a URL falls under one of the sources the way a
// browser matches a host-source with a path (a prefix ending in "/").
func cspAllows(sources []string, u string) bool {
	for _, s := range sources {
		if strings.HasSuffix(s, "/") && strings.HasPrefix(u, s) {
			return true
		}
	}
	return false
}

func frameBlobManifest(t *testing.T, ui map[string]any) []byte {
	t.Helper()
	return uiManifest(t, func(m map[string]any) {
		block := map[string]any{"bundle": map[string]any{}}
		for k, v := range ui {
			block[k] = v
		}
		m["ui"] = block
	})
}

func TestUIFrameAndBlob_AreDerivedReviewedAndOffUnlessAsked(t *testing.T) {
	m := mustParse(t, frameBlobManifest(t, map[string]any{"frame_package": true, "connect_blob": true}))
	assert.Contains(t, m.Perms, PermUIFramePackage)
	assert.Contains(t, m.Perms, PermUIConnectBlob)
	assert.False(t, m.NeedsModule(), "both belong to the interface: an app that is only an interface may hold them")

	labels := map[string]map[Permission]string{}
	for _, lang := range []string{"en", "tr"} {
		labels[lang] = map[Permission]string{}
		for _, r := range PermissionRows(m, lang) {
			labels[lang][Permission(r.ID)] = r.Label
		}
	}
	assert.Equal(t, "Its interface opens pages of its own package in frames inside it - this version's pages only, each in the same sandbox and under the same rules as the interface; never another site, another app or a page of filex",
		labels["en"][PermUIFramePackage])
	assert.Equal(t, "Its interface reads blob: addresses it created itself in your browser (a document it unpacked in memory) - such an address never leaves your browser and reaches no server",
		labels["en"][PermUIConnectBlob])
	assert.Equal(t, "Arayüzü, kendi paketindeki sayfaları kendi içindeki çerçevelerde açar - yalnız bu sürümün sayfalarını, her birini aynı yalıtım alanında ve arayüzle aynı kurallarla; başka bir siteyi, başka bir uygulamayı ya da filex'in bir sayfasını asla açamaz",
		labels["tr"][PermUIFramePackage])
	assert.Equal(t, "Arayüzü, tarayıcınızda kendi oluşturduğu blob: adreslerini okur (bellekte açtığı bir belge gibi) - böyle bir adres tarayıcınızdan çıkmaz, hiçbir sunucuya ulaşmaz",
		labels["tr"][PermUIConnectBlob])

	plain := mustParse(t, uiManifest(t, nil))
	assert.NotContains(t, plain.Perms, PermUIFramePackage, "off unless the manifest asks")
	assert.NotContains(t, plain.Perms, PermUIConnectBlob, "off unless the manifest asks")
	off := mustParse(t, frameBlobManifest(t, map[string]any{"frame_package": false, "connect_blob": false}))
	assert.NotContains(t, off.Perms, PermUIFramePackage, "false is off")
	assert.NotContains(t, off.Perms, PermUIConnectBlob, "false is off")

	for _, p := range []string{"ui:frame-package", "ui:connect-blob"} {
		_, err := ParseManifest(uiManifest(t, func(m map[string]any) {
			m["permissions"] = append(m["permissions"].([]any), p)
		}))
		assert.Error(t, err, "%s is derived from the ui block: never written into permissions", p)
	}
}

func TestUIPolicy_FramePackageFramesThisVersionsPagesOnly(t *testing.T) {
	pkg := frameBlobPkg
	csp, allow := UIPolicy(NewGrants([]Permission{PermUI, PermUIFramePackage}), pkg)
	frames := cspSources(csp, "frame-src")
	assert.Equal(t, []string{pkg}, frames, "this version's path, nothing else")
	assert.Equal(t, []string{pkg}, cspSources(csp, "child-src"))
	assert.Equal(t, []string{"'none'"}, cspSources(csp, "connect-src"), "a frame is not a connection")
	assert.Equal(t, []string{"blob:"}, cspSources(csp, "worker-src"))
	assert.Equal(t, []string{"allow-scripts"}, cspSources(csp, "sandbox"), "the page stays an opaque origin, and so does every page it frames")
	assert.Equal(t, `("`+pkg+`*")`, allow, "the framed pages are under the package's path already")
	for _, s := range frames {
		assert.NotContains(t, []string{"blob:", "data:", "'self'", "*", "https:", "https://files.example.com", "https://files.example.com/"}, s)
	}

	assert.True(t, cspAllows(frames, pkg+"web-apps/apps/documenteditor/main/index.html"), "a page of its own package")
	assert.True(t, cspAllows(frames, pkg+"index.html"), "its own first page")
	for why, u := range map[string]string{
		"another version":           "https://files.example.com/filex/_appui/office-editor/fedcba9876543210/index.html",
		"another app":               "https://files.example.com/filex/_appui/drawio/0123456789abcdef/index.html",
		"a page of filex":           "https://files.example.com/filex/admin/",
		"filex's API":               "https://files.example.com/filex/api/files/list",
		"a Document Server":         "https://docs.example.net/web-apps/apps/api/documents/api.js",
		"a document of its own":     "blob:https://files.example.com/0b6c3a1e-0000-4000-8000-000000000000",
		"a data: document":          "data:text/html,<p>hi</p>",
		"the same path, plain http": "http://files.example.com/filex/_appui/office-editor/0123456789abcdef/index.html",
	} {
		assert.False(t, cspAllows(frames, u), "%s: %s", why, u)
	}

	none, noneAllow := UIPolicy(NewGrants([]Permission{PermUI}), pkg)
	assert.Equal(t, []string{"'none'"}, cspSources(none, "frame-src"), "not granted: no frame at all")
	assert.Equal(t, []string{"'none'"}, cspSources(none, "child-src"))
	assert.Equal(t, allow, noneAllow, "the allowlist is the package's path either way")
	// Nothing but the two frame directives moves.
	assert.Equal(t, strings.Replace(none, "frame-src 'none'; child-src 'none'", "frame-src "+pkg+"; child-src "+pkg, 1), csp)
}

func TestUIPolicy_ConnectBlobAddsBlobToConnectSrcAndNothingElse(t *testing.T) {
	pkg := frameBlobPkg
	base, baseAllow := UIPolicy(NewGrants([]Permission{PermUI}), pkg)
	csp, allow := UIPolicy(NewGrants([]Permission{PermUI, PermUIConnectBlob}), pkg)
	assert.Equal(t, []string{"blob:"}, cspSources(csp, "connect-src"), "its own blob: addresses, no network address")
	assert.Equal(t, []string{"'none'"}, cspSources(csp, "frame-src"), "reading a blob: opens no frame")
	assert.Equal(t, baseAllow, allow, "blob: is no network address: the allowlist is the package's path either way")
	assert.Equal(t, strings.Replace(base, "connect-src 'none';", "connect-src blob:;", 1), csp, "connect-src is the one directive that changes")

	both, _ := UIPolicy(NewGrants([]Permission{PermUI, PermUIPackageFetch, PermUIConnectBlob}), pkg)
	connect := cspSources(both, "connect-src")
	assert.Equal(t, []string{pkg, "blob:"}, connect, "with ui:package-fetch: its own package and its own blob: addresses")
	for _, s := range connect {
		assert.NotContains(t, []string{"data:", "filesystem:", "'self'", "*", "https:", "https://files.example.com", "https://files.example.com/"}, s)
	}
	assert.False(t, cspAllows(connect, "https://files.example.com/filex/api/auth/me"), "not filex's API")
	assert.False(t, cspAllows(connect, "https://files.example.com/filex/_appui/office-editor/fedcba9876543210/x.bin"), "not another version")

	all, _ := UIPolicy(NewGrants([]Permission{PermUI, PermUIFramePackage, PermUIConnectBlob}), pkg)
	assert.Equal(t, "default-src 'none'; script-src "+pkg+" "+uiBootstrapHash+"; style-src "+pkg+" 'unsafe-inline'; img-src "+pkg+" data: blob:; font-src "+pkg+" data:; media-src "+pkg+" blob:; connect-src blob:; worker-src blob:; frame-src "+pkg+"; child-src "+pkg+"; object-src 'none'; manifest-src 'none'; form-action 'none'; base-uri 'none'; sandbox allow-scripts", all)
	assert.NotContains(t, all, "frame-ancestors", "none at all: the page framing one of its pages is the interface itself, an opaque origin no source matches, not even *")
}

// The module's describe carries both like the rest of the ui block (security
// review UI-2): a module that does not say it cannot have it granted by a
// manifest beside it.
func TestUIFrameAndBlob_ArePartOfTheDescribedInterface(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for _, field := range []string{"frame_package", "connect_blob"} {
		want := mustParse(t, uiManifest(t, func(m map[string]any) {
			m["ui"] = map[string]any{"bundle": map[string]any{"sha256": sum}, field: true}
		}))
		r := &Registry{}
		got := &wire.Manifest{UI: &wire.UISpec{Bundle: wire.UIBundle{SHA256: sum}}}
		assert.Error(t, r.checkDescribedUI(want, got), "%s: the module does not ask for it", field)
		if field == "frame_package" {
			got.UI.FramePackage = true
		} else {
			got.UI.ConnectBlob = true
		}
		assert.NoError(t, r.checkDescribedUI(want, got), field)
	}
}

// The install must grant them like any other permission, and an update that
// asks for them stops at the review.
func TestUIInstall_FrameAndBlobAreGrantedOrNothingIsInstalled(t *testing.T) {
	h := newBareHarness(t, nil)
	bundle := uiZip(t, uiDoc())
	manifest := frameBlobManifest(t, map[string]any{"frame_package": true, "connect_blob": true})
	_, _, err := h.reg.Install(context.Background(), &InstallInput{
		Manifest: manifest, UI: bytes.NewReader(bundle), Source: "upload", Lang: "en",
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.drawio"},
	})
	ie := installError(t, err)
	assert.Equal(t, ErrCodePermissionsIncomplete, ie.Code, "%v", err)
	assert.ElementsMatch(t, []string{"ui:frame-package", "ui:connect-blob"}, ie.Missing)

	p, _ := h.installUI(t, uiManifest(t, nil), bundle)
	next := uiManifest(t, func(m map[string]any) {
		m["version"] = "1.1.0"
		m["ui"] = map[string]any{"bundle": map[string]any{}, "frame_package": true, "connect_blob": true}
	})
	_, dry, err := h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(bundle), DryRun: true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ui:frame-package", "ui:connect-blob"}, dry.Upgrade.Added)
	_, _, err = h.reg.Upgrade(context.Background(), p.Row.ID, &InstallInput{Manifest: next, UI: bytes.NewReader(bundle)})
	assert.Equal(t, ErrCodePermissionsChanged, installError(t, err).Code, "%v", err)
}

// A page the interface frames is a page of the package, served by the same
// route under the same policy: the frame gets its own opaque origin (the
// policy's sandbox), the bootstrap first (no WebRTC inside it either) and the
// same connect-src - nothing the outer page does not have.
func TestUIServe_APageItFramesIsServedUnderTheSamePolicy(t *testing.T) {
	h := newBareHarness(t, nil)
	files := uiDoc()
	files["editor/frame.html"] = "<!doctype html><html><head><script src=../app.js></script></head><body>inner</body></html>"
	bundle := uiZip(t, files)
	h.installUI(t, frameBlobManifest(t, map[string]any{"frame_package": true, "connect_blob": true}), bundle)

	ans, err := h.reg.ActionsFor(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, ans.Views, 1)
	require.NotNil(t, ans.Views[0].UI)
	assert.Subset(t, ans.Views[0].UI.Grants, []string{"ui:frame-package", "ui:connect-blob"}, "the frame is told its grant")

	short := hexSum(bundle)[:16]
	srv := uiServer(t, h, "/filex")
	pkg := srv.URL + "/filex/_appui/drawio/" + short + "/"
	for _, page := range []string{"index.html", "editor/frame.html"} {
		res, err := http.Get(srv.URL + "/_appui/drawio/" + short + "/" + page)
		require.NoError(t, err)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, page)
		csp := res.Header.Get("Content-Security-Policy")
		assert.Equal(t, []string{pkg}, cspSources(csp, "frame-src"), page)
		assert.Equal(t, []string{pkg}, cspSources(csp, "child-src"), page)
		assert.Equal(t, []string{"blob:"}, cspSources(csp, "connect-src"), page)
		assert.Equal(t, []string{"allow-scripts"}, cspSources(csp, "sandbox"), "%s: an opaque origin of its own", page)
		assert.Equal(t, `("`+pkg+`*")`, res.Header.Get("Connection-Allowlist"), page)
		assert.NotContains(t, csp, "frame-ancestors", "%s: the interface frames it from an opaque origin, which no source matches", page)
		assert.Contains(t, res.Header.Get("Permissions-Policy"), "camera=()", page)
		assert.Empty(t, res.Header.Values("Set-Cookie"), page)
		assert.True(t, strings.HasPrefix(string(body), "<!doctype html>"+uiBootstrapTag), "%s: the bootstrap first - no WebRTC in a framed page either", page)
	}
}

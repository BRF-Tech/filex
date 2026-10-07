package onlyoffice

// The editor's frame (task #92, frame.go): the page ONLYOFFICE's api.js runs
// in on the interface origin, and the policy it is served with. Everything in
// it was absent before #92, so every test here is red on the old code.

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// directives splits a policy into name -> sources.
func directives(t *testing.T, csp string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, d := range strings.Split(csp, ";") {
		f := strings.Fields(d)
		if len(f) == 0 {
			continue
		}
		_, dup := out[f[0]]
		require.False(t, dup, "directive %s twice in %q", f[0], csp)
		out[f[0]] = f[1:]
	}
	return out
}

func TestFramePage_RunsOnlyItsOwnScriptAndTheDocumentServers(t *testing.T) {
	page, csp, ok := FramePage("https://Docs.Example.com/")
	require.True(t, ok)
	d := directives(t, csp)

	sum := sha256.Sum256([]byte(frameScript))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	assert.Equal(t, []string{hash, "https://docs.example.com/"}, d["script-src"],
		"frame.js by its hash and the document server: no 'self', no 'unsafe-inline', no eval")
	assert.Equal(t, []string{"'none'"}, d["default-src"])
	assert.Equal(t, []string{"https://docs.example.com/"}, d["frame-src"], "the editor's own frame, from that server only")
	assert.Equal(t, []string{"https://docs.example.com/"}, d["connect-src"])
	assert.Equal(t, []string{"'none'"}, d["base-uri"])
	assert.Equal(t, []string{"'none'"}, d["object-src"])
	assert.Equal(t, []string{"*"}, d["frame-ancestors"], "framed by the explorer wherever it is embedded")
	_, sandboxed := d["sandbox"]
	assert.False(t, sandboxed, "the page keeps its own origin: the explorer's frame element carries the sandbox flags")
	assert.NotContains(t, csp, "unsafe-eval")

	// The script in the page is exactly the one the hash allows.
	assert.Contains(t, string(page), "<script>"+frameScript+"</script>")
	assert.Equal(t, 1, strings.Count(string(page), "<script"), "one script element: frame.js; api.js is added by it")
	assert.Contains(t, string(page), `data-api="https://docs.example.com/web-apps/apps/api/documents/api.js"`)
	assert.Contains(t, string(page), `<div id="`+frameMount+`"></div>`)
	assert.Contains(t, frameScript, "'"+frameMount+"'", "frame.js mounts the editor on the page's element")
}

func TestFramePage_ADocumentServerUnderAPath(t *testing.T) {
	page, csp, ok := FramePage("https://example.com/ds")
	require.True(t, ok)
	assert.Equal(t, []string{"https://example.com/ds/"}, directives(t, csp)["frame-src"])
	assert.Contains(t, string(page), `data-api="https://example.com/ds/web-apps/apps/api/documents/api.js"`)
}

// An address that could end a directive, name another source or be no
// origin at all serves no page (the handler answers 404).
func TestFramePage_RefusesWhatCannotBeNamedInAPolicy(t *testing.T) {
	for _, bad := range []string{
		"",
		"   ",
		"docs.example.com",
		"javascript:alert(1)",
		"ftp://docs.example.com",
		"https://docs.example.com; script-src *",
		"https://docs.example.com/x'y",
		`https://docs.example.com/"x`,
		"https://docs.example.com/a,b",
		"https://*.example.com",
		"https://docs.example.com/a b",
		"https://user:pw@docs.example.com",
		"https://docs.example.com/?q=1",
		"https://docs.example.com/#f",
	} {
		_, _, ok := FramePage(bad)
		assert.False(t, ok, "%q", bad)
	}
}

// The document server's address goes into an attribute: escaped, so it
// cannot open an element of its own (the path is percent-encoded first, the
// rest HTML-escaped).
func TestFramePage_EscapesTheScriptAddress(t *testing.T) {
	page, csp, ok := FramePage("https://docs.example.com/a<b>&c")
	require.True(t, ok)
	assert.NotContains(t, string(page), "<b>")
	assert.Contains(t, string(page), `data-api="https://docs.example.com/a%3Cb%3E&amp;c/web-apps/apps/api/documents/api.js"`)
	assert.NotContains(t, csp, "<")
}

// frame.js speaks the protocol core lib/officeFrame.ts speaks: the same name
// and version, the session from the fragment, a hello to the parent, and an
// open taken from the parent only. A change on one side alone is a frame that
// never opens a document.
func TestFrameScript_SpeaksTheExplorersProtocol(t *testing.T) {
	for _, want := range []string{
		"var PROTO = 'filex-oo';",
		"var V = 1;",
		"location.hash",
		"ev.source !== parentWin",
		"m.session !== session",
		"ev.ports.length !== 1",
		"new D.DocEditor(MOUNT, config)",
		"parentWin.postMessage({ proto: PROTO, v: V, type: 'hello', session: session }, '*')",
	} {
		assert.Contains(t, frameScript, want)
	}
	assert.NotContains(t, frameScript, "</script", "frame.js is inlined into the page")
	assert.NotContains(t, frameScript, "sessionStorage")
	assert.NotContains(t, frameScript, "localStorage")
}

func TestFramePath_IsUnderTheInterfaceRouteAndNoAppsName(t *testing.T) {
	assert.True(t, strings.HasPrefix(FramePath, "/_appui/_"), "on the interface origin only the interface route answers")
}

// On a frame origin (the document server's own host) the one path its proxy
// sends to filex: under a prefix no document server path starts with.
func TestFrameHostPath_IsUnderItsOwnPrefixAndNoDocumentServerPath(t *testing.T) {
	assert.True(t, strings.HasPrefix(FrameHostPath, FrameHostPrefix))
	assert.True(t, strings.HasSuffix(FrameHostPrefix, "/"))
	for _, ds := range []string{"/web-apps/", "/sdkjs/", "/fonts/", "/cache/", "/coauthoring/", "/healthcheck", "/ConvertService.ashx", "/welcome/", "/info/", "/9.4.0-129/"} {
		assert.False(t, strings.HasPrefix(ds, FrameHostPrefix), ds)
		assert.False(t, strings.HasPrefix(FrameHostPrefix, ds), ds)
	}
}

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

var bases = []wire.RouteBase{
	{ID: 1, URL: "https://l.example.com"},
	{ID: 2, URL: "https://files.example.com/l", Default: true},
}

func share(token string) wire.ShortenInput {
	return wire.ShortenInput{Target: wire.ShortenTarget{Kind: wire.ShortenShare, URL: "https://files.example.com/s/" + token, Link: token, Name: "Proposal.pdf"}, Bases: bases}
}

func codeOf(t *testing.T, url string) string {
	t.Helper()
	code, ok := strings.CutPrefix(url, "https://files.example.com/l/")
	require.True(t, ok, "on the default base: %s", url)
	return code
}

// The manifest the module describes is the file an administrator installs,
// and it passes the kit's own manifest checks.
func TestManifest_IsFilexAppJSON(t *testing.T) {
	raw, err := os.ReadFile("filex-app.json")
	require.NoError(t, err)
	var onDisk wire.Manifest
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	a, _ := json.Marshal(onDisk)
	b, _ := json.Marshal(manifest)
	assert.JSONEq(t, string(a), string(b))
	r := plugintest.InspectManifest(manifest)
	assert.Empty(t, r.Errors(), "%v", r)
}

func TestSetup_KeepsTheAlphabetAndSaysTheLength(t *testing.T) {
	h := harness()
	s, err := h.Open("setup")
	require.NoError(t, err)
	assert.False(t, s.Done)
	s, err = h.Press("setup", s, "save", map[string]any{"alphabet": "digits"})
	require.NoError(t, err)
	require.True(t, s.Done, "the app's part is finished")
	assert.Contains(t, s.Nodes[0].Props["text"].(wire.Text)["en"], "16 symbols", "50 bits need 16 digits")

	out, err := h.Shorten(ptr(share("tok1")))
	require.NoError(t, err)
	code := codeOf(t, out.URL)
	assert.Len(t, code, 16)
	assert.Equal(t, strings.Trim(code, "0123456789"), "", "digits only")
}

func TestShorten_OneCodePerLink(t *testing.T) {
	h := harness()
	in := share("tok1")
	in.Existing = true
	none, err := h.Shorten(&in)
	require.NoError(t, err)
	assert.Empty(t, none.URL, "Existing asks for one it already has")

	first, err := h.Shorten(ptr(share("tok1")))
	require.NoError(t, err)
	again, err := h.Shorten(ptr(share("tok1")))
	require.NoError(t, err)
	other, err := h.Shorten(ptr(share("tok2")))
	require.NoError(t, err)
	assert.Equal(t, first.URL, again.URL)
	assert.NotEqual(t, first.URL, other.URL)
	assert.Len(t, codeOf(t, first.URL), 10, "letters and digits until the setup says otherwise: 10 symbols for 50 bits")

	noBase := share("tok3")
	noBase.Bases = nil
	refused, err := h.Shorten(&noBase)
	require.NoError(t, err)
	assert.NotEmpty(t, refused.Error)
}

func TestRoute_RedirectPreviewAndMiss(t *testing.T) {
	h := harness()
	out, err := h.Shorten(ptr(share("tok1")))
	require.NoError(t, err)
	code := codeOf(t, out.URL)
	h.Host.SetLinkLive("tok1", true)

	visit, err := h.Route(&wire.RouteInput{Route: "go", Base: bases[1], Path: strings.ToUpper(code), Countable: true})
	require.NoError(t, err)
	assert.Equal(t, "https://files.example.com/s/tok1", visit.Redirect, "case does not matter")

	preview, err := h.Route(&wire.RouteInput{Route: "go", Base: bases[1], Path: code + "+"})
	require.NoError(t, err)
	require.NotNil(t, preview.Page)
	assert.Equal(t, "https://files.example.com/s/tok1", preview.Page.Link)

	miss, err := h.Route(&wire.RouteInput{Route: "go", Base: bases[1], Path: "zzzzzzzzzz"})
	require.NoError(t, err)
	assert.True(t, miss.Miss)

	h.Host.SetLinkLive("tok1", false)
	ended, err := h.Route(&wire.RouteInput{Route: "go", Base: bases[1], Path: code})
	require.NoError(t, err)
	assert.True(t, ended.Miss, "a link that ended leads nowhere")
}

func harness() *plugintest.Harness {
	return plugintest.NewFor(manifest, func(host *plugintest.Host) *pluginkit.Plugin { return newPlugin(host) })
}

func ptr(in wire.ShortenInput) *wire.ShortenInput { return &in }

package printframe

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The page runs print.js and nothing else: the policy names its hash, and the
// page carries it inline, byte for byte.
func TestPrintFrame_RunsOnlyItsOwnScript(t *testing.T) {
	sum := sha256.Sum256([]byte(script))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	assert.Contains(t, CSP, "script-src "+hash)
	assert.NotContains(t, CSP, "unsafe-eval")
	assert.Contains(t, string(Page()), "<script>"+script+"</script>")
	assert.Equal(t, 1, strings.Count(string(Page()), "<script"), "one script, inline")
}

// It frames the blob: PDF it made itself and nothing else: no data:, no
// network address, no connection of its own.
func TestPrintFrame_FramesOnlyBlobAndFetchesNothing(t *testing.T) {
	for _, d := range []string{"frame-src blob:", "child-src blob:", "object-src blob:", "connect-src 'none'", "default-src 'none'", "form-action 'none'", "base-uri 'none'"} {
		assert.Contains(t, CSP, d)
	}
	assert.NotContains(t, CSP, "data:")
	assert.NotContains(t, CSP, "http")
	assert.NotContains(t, CSP, "'self'")
}

// Who may frame it (security review sec055 S9): filex itself, the desktop
// app and the pages the operator allows to frame filex - never any site.
// Red before: the page named no frame-ancestors and told the middleware to
// add none (secheaders.OpenFraming), so every site could frame it.
func TestPrintFrame_OnlyFilexTheDesktopAppAndTheListedPagesMayFrameIt(t *testing.T) {
	assert.Equal(t, CSP+"; frame-ancestors 'self' app://filex", Policy(nil))
	assert.Equal(t, CSP+"; frame-ancestors 'self' https://home.example.com https://*.example.org app://filex",
		Policy([]string{"https://home.example.com", "https://*.example.org"}))
	assert.Equal(t, 1, strings.Count(Policy(nil), "frame-ancestors"))
	assert.NotContains(t, Policy(nil), "frame-ancestors *")

	ancestors := []string{"https://home.example.com"}
	_ = Policy(ancestors)
	assert.Equal(t, []string{"https://home.example.com"}, ancestors, "the operator's list is not appended to")

	rec := httptest.NewRecorder()
	Handler([]string{"https://home.example.com"})(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, CSP+"; frame-ancestors 'self' https://home.example.com app://filex", rec.Header().Get("Content-Security-Policy"))
}

// The script listens to its parent only, checks the bytes are a PDF and
// serves them as one, whatever type they came with.
func TestPrintFrame_ScriptListensToItsParentAndChecksThePDF(t *testing.T) {
	assert.Contains(t, script, "ev.source !== parentWin")
	assert.Contains(t, script, "'filex:print'")
	assert.Contains(t, script, "'filex:print-ready'")
	assert.Contains(t, script, "0x25", "%PDF- is checked")
	assert.Contains(t, script, "new Blob([d.pdf], { type: 'application/pdf' })")
}

// print() is called in one place, reached only from the person's own click on
// the page's button: a trusted click, on an armed button, while the browser
// reports the person's activation on the page (security review sec055 S9).
// Red before: the page called print() as soon as the PDF's frame loaded.
func TestPrintFrame_PrintsOnlyOnThePersonsClick(t *testing.T) {
	assert.Equal(t, 1, strings.Count(script, ".print()"), "one print() call")
	assert.Contains(t, script, "ev.isTrusted !== true")
	assert.Contains(t, script, "navigator.userActivation")
	assert.Contains(t, script, "ua.isActive !== true")
	assert.Contains(t, script, "!j.armed")
	assert.Contains(t, script, "x.type === 'arm'")
	assert.Contains(t, script, "x.type === 'cancel'")
	// The frame's load prints only after the click (j.clicked), never by itself.
	at := strings.Index(script, "f.addEventListener('load'")
	require.GreaterOrEqual(t, at, 0, "the PDF's frame has a load handler")
	load := script[at:]
	end := strings.Index(load, "});")
	require.Greater(t, end, 0)
	assert.Contains(t, load[:end], "if (j.clicked)")
}

func TestPrintFrame_ServesGetAndHeadOnly(t *testing.T) {
	h := Handler(nil)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, Policy(nil), rec.Header().Get("Content-Security-Policy"))
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Empty(t, rec.Header().Get("Set-Cookie"))
	assert.Equal(t, string(Page()), rec.Body.String())

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodHead, Path, nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Body.String())

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, Path, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

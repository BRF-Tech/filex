package httpx

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInertPolicy_ActiveKindsAreSandboxedPassiveOnesAreNot(t *testing.T) {
	for _, ct := range []string{
		"text/html", "text/html; charset=utf-8", "TEXT/HTML", "image/svg+xml", "image/svg+xml; charset=utf-8",
		"application/xhtml+xml", "application/xml", "text/xml", "application/rss+xml", "text/xsl",
		"application/xslt+xml", "application/javascript", "application/json", "text/markdown",
		"application/octet-stream", "", "not a type",
	} {
		p := InertPolicy(ct)
		assert.Contains(t, p, "sandbox", "%q", ct)
		assert.Contains(t, p, "default-src 'none'", "%q", ct)
		assert.NotContains(t, p, "allow-scripts", "%q", ct)
		assert.NotContains(t, p, "allow-same-origin", "%q", ct)
	}
	for _, ct := range []string{
		"application/pdf", "image/png", "image/jpeg", "image/webp", "image/gif", "video/mp4", "audio/mpeg",
		"text/plain", "text/plain; charset=utf-8",
	} {
		assert.Empty(t, InertPolicy(ct), "%q is shown as itself", ct)
	}
}

func TestServedType_NeverEmpty(t *testing.T) {
	assert.Equal(t, "image/png", ServedType("image/png", "x.html"), "the recorded type wins")
	assert.Contains(t, ServedType("", "page.html"), "text/html")
	assert.Equal(t, "application/octet-stream", ServedType("", "noext"))
}

func TestProtectServedFile(t *testing.T) {
	h := http.Header{}
	ProtectServedFile(h, "text/html; charset=utf-8")
	assert.Equal(t, "text/html; charset=utf-8", h.Get("Content-Type"))
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Contains(t, h.Get("Content-Security-Policy"), "sandbox")

	h = http.Header{}
	ProtectServedFile(h, "application/pdf")
	assert.Empty(t, h.Get("Content-Security-Policy"))
}

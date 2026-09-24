package onlyoffice

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCallback_UnsignedIsRefused: with a JWT secret configured, a callback
// that carries no token is refused and nothing is written.
//
// Measured before the fix: the route is public, the token was only checked
// when one was present, and this exact request — no token, status 2, a URL of
// the caller's choosing — replaced budget.docx with the caller's bytes.
func TestCallback_UnsignedIsRefused(t *testing.T) {
	h := newHarness(t)
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ATTACKER BYTES"))
	}))
	t.Cleanup(ds.Close)

	payload := fmt.Sprintf(`{"key":"k","status":%d,"url":%q}`, StatusReadyForSaving, ds.URL+"/x.docx")
	req := httptest.NewRequest(http.MethodPost, "/api/files/onlyoffice/callback?node=1", strings.NewReader(payload))
	_, err := h.svc.HandleCallback(req, h.node.ID)
	require.Error(t, err, "an unsigned callback was accepted")
	assert.Contains(t, err.Error(), "not signed")

	onDisk, rerr := os.ReadFile(filepath.Join(h.root, "budget.docx"))
	require.NoError(t, rerr)
	assert.Equal(t, "OLD BYTES", string(onDisk), "an unsigned callback overwrote the document")

	// A forged signature is refused too (this part always worked).
	tok, serr := signHS256(map[string]any{"status": 2}, "not-the-secret")
	require.NoError(t, serr)
	payload = fmt.Sprintf(`{"key":"k","status":%d,"url":%q,"token":%q}`, StatusReadyForSaving, ds.URL+"/x.docx", tok)
	req = httptest.NewRequest(http.MethodPost, "/api/files/onlyoffice/callback?node=1", strings.NewReader(payload))
	_, err = h.svc.HandleCallback(req, h.node.ID)
	require.Error(t, err)
}

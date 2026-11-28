package handlers_test

// GitHub #143: behind a reverse proxy that ends TLS, the browser opens
// draw.io and the document server at https://… while the filex container
// reaches them at http://… - one URL cannot be both. The service's URL stays
// the browser's; FILEX_DRAWIO_INTERNAL_URL and FILEX_ONLYOFFICE_INTERNAL_URL
// give the address filex's own requests go to.
//
// Each test saves a browser address nothing here can reach. Against the code
// before the variables (the config fields and their wiring absent) they do
// not build; with the variables read and not used, every server leg below is
// "unreachable".

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

func TestInternalURL_DrawioIsProbedAtItsInternalAddress(t *testing.T) {
	drawio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(drawio.Close)
	h, _ := liveExternalServer(t, func(c *config.Config) {
		publishedAt(c)
		c.ExternalServices.Drawio.InternalURL = drawio.URL
	})
	const browser = "https://draw.invalid"
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/drawio", map[string]any{
		"enabled": true, "url": browser,
	}).StatusCode)

	status, out := h.PostJSON(t, "/api/admin/external/drawio/test", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["server_reachable"], "the server's leg goes to the internal address: %v", out["detail"])
	require.Equal(t, browser, out["url"], "the service's URL is still the browser's")

	// The explorer is handed the browser's address, and never the internal one.
	resp := h.Get(t, "/api/files/capabilities")
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	var caps struct {
		External map[string]struct {
			URL   string `json:"url"`
			State string `json:"state"`
		} `json:"external"`
	}
	require.NoError(t, json.Unmarshal(raw, &caps))
	require.Equal(t, browser, caps.External["drawio"].URL)
	require.Equal(t, "ok", caps.External["drawio"].State)
	require.False(t, strings.Contains(string(raw), drawio.URL), "the internal address reached the browser: %s", raw)
}

func TestInternalURL_OnlyOfficeIsAskedAtItsInternalAddress(t *testing.T) {
	ds := convertingStub(t, convertStub{enforceJWT: true})
	h, nodeID := liveExternalServer(t, func(c *config.Config) {
		publishedAt(c)
		c.ExternalServices.OnlyOffice.InternalURL = ds
	})
	const browser = "https://office.invalid"
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": browser, "secret": "s3cr3t", "callback_url": h.srv.URL,
	}).StatusCode)

	status, out := h.PostJSON(t, "/api/admin/external/onlyoffice/test", nil)
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["server_reachable"], "the health check goes to the internal address: %v", out["detail"])
	leg, _ := out["service_to_filex"].(map[string]any)
	require.Equal(t, true, leg["ok"], "the route back to filex was asked at the internal address: %v", leg["detail"])

	// Test now on the form's unsaved values: the questions still go there.
	status, out = h.PostJSON(t, "/api/admin/external/onlyoffice/test", map[string]any{
		"url": "https://office-new.invalid", "secret": "candidate", "callback_url": h.srv.URL,
	})
	require.Equal(t, http.StatusOK, status, "%v", out)
	require.Equal(t, true, out["unsaved"])
	require.Equal(t, true, out["server_reachable"], "%v", out["detail"])
	leg, _ = out["service_to_filex"].(map[string]any)
	require.Equal(t, true, leg["ok"], "%v", leg["detail"])

	// The editor loads api.js in the browser, from the browser's address.
	resp := h.Get(t, "/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=view")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var cfg struct {
		DocumentServerURL string `json:"documentServerUrl"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cfg))
	resp.Body.Close()
	require.Equal(t, browser, cfg.DocumentServerURL)
}

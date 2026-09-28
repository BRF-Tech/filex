package handlers_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── An app's own interface asking its module (M3) ───────────────────────

func TestAppUI_CallReachesTheModuleOrSaysThereIsNone(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	post := func(plugin, view, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/ui/"+plugin+"/"+view+"/call", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := f.admin.Do(req)
		require.NoError(t, err)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(raw)
	}
	code, body := post("sketch", "editor", `{"method":"x","paths":["main://doc.sketch"]}`)
	assert.Equal(t, http.StatusNotFound, code, "an interface-only app has no module: %s", body)
	assert.Contains(t, body, "unsupported")
}

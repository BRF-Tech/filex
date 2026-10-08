package handlers_test

// The editor config names the editor's frame (task #92): with an origin of
// its own for app interfaces (FILEX_APP_UI_ORIGIN) every config the explorer
// is handed carries `frame`, the page on that origin api.js runs in, and the
// config itself is the one filex signed, whichever page loads it. Without
// that origin there is no `frame` and api.js runs in filex's page, as before.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type editorAnswer struct {
	DocumentServerURL string         `json:"documentServerUrl"`
	Frame             *string        `json:"frame"`
	Config            map[string]any `json:"config"`
	// ds is the fixture's document server address (not in the answer).
	ds string
}

func openEditorConfig(t *testing.T, cfgMutate func(*config.Config), base string) editorAnswer {
	t.Helper()
	root := t.TempDir()
	srv, store := editorFixtureWith(t, root, cfgMutate)
	adminID, _ := testutil.SeedAdminUser(t, store)
	admin := issueToken(t, store, adminID, fullScopes, nil)
	seedServerFile(t, store, "oo", "Documents/Rapor.docx", "PK not really a docx")
	status, body := fxPost(t, srv.URL+base+"/api/files/onlyoffice/config", admin,
		map[string]any{"path": "oo://Documents/Rapor.docx", "mode": "edit"})
	require.Equal(t, http.StatusOK, status, body)
	var got editorAnswer
	require.NoError(t, json.Unmarshal([]byte(body), &got), body)
	got.ds = editorDocServerOf(t, srv.URL).srv.URL
	return got
}

// Red before #92: the answer had no `frame`.
func TestOnlyOfficeConfig_NamesTheFrameOnTheInterfaceOrigin(t *testing.T) {
	got := openEditorConfig(t, func(c *config.Config) {
		c.AppUIOrigin = "https://apps.usercontent.example"
	}, "")
	require.NotNil(t, got.Frame, "the config names the editor's frame")
	assert.Equal(t, "https://apps.usercontent.example/_appui/_onlyoffice/editor", *got.Frame)
	assert.Equal(t, got.ds, got.DocumentServerURL)

	// The signed config, nothing of the page's: the frame adds the events.
	assert.NotEmpty(t, got.Config["token"], "signed as before")
	_, hasEvents := got.Config["events"]
	assert.False(t, hasEvents)
	doc, _ := got.Config["document"].(map[string]any)
	assert.NotEmpty(t, doc["key"])
}

// Under a base path the frame is under it too, on the interface origin.
func TestOnlyOfficeConfig_TheFrameCarriesTheBasePath(t *testing.T) {
	got := openEditorConfig(t, func(c *config.Config) {
		c.AppUIOrigin = "https://apps.usercontent.example"
		c.BasePath = "/filex"
	}, "/filex")
	require.NotNil(t, got.Frame)
	assert.Equal(t, "https://apps.usercontent.example/filex/_appui/_onlyoffice/editor", *got.Frame)
}

// No interface origin: no frame - the explorer loads api.js into its page,
// as every release before #92 did.
func TestOnlyOfficeConfig_NoFrameWithoutAnInterfaceOrigin(t *testing.T) {
	got := openEditorConfig(t, nil, "")
	assert.Nil(t, got.Frame, "`frame` is left out, not empty")
	assert.NotEmpty(t, got.Config["token"])
}

// Task #92, the maintainers' ruling: the frame lives on the document server's own
// origin (FILEX_ONLYOFFICE_FRAME_ORIGIN) at /filex-frame/editor - at that
// host's root, whatever filex's base path - and it comes before the
// interface origin. Red before: the setting did not exist.
func TestOnlyOfficeConfig_TheFrameOriginComesFirst(t *testing.T) {
	got := openEditorConfig(t, func(c *config.Config) {
		c.AppUIOrigin = "https://apps.usercontent.example"
		c.ExternalServices.OnlyOffice.FrameOrigin = "https://docs.example.com"
		c.BasePath = "/filex"
	}, "/filex")
	require.NotNil(t, got.Frame)
	assert.Equal(t, "https://docs.example.com/filex-frame/editor", *got.Frame)
	assert.NotEmpty(t, got.Config["token"], "the same signed config")
}

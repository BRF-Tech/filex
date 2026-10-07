package config

// FILEX_ONLYOFFICE_FRAME_ORIGIN (task #92): where the ONLYOFFICE editor's
// frame is served - normally the document server's own origin. New in #92, so
// every test here is red on the old code (the field did not exist).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOfficeFrameOriginIsOffByDefault(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_FRAME_ORIGIN", "")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.ExternalServices.OnlyOffice.FrameOrigin)
}

func TestOfficeFrameOriginFromTheEnvironment(t *testing.T) {
	t.Setenv("FILEX_PUBLIC_URL", "https://files.example.com")
	t.Setenv("FILEX_ONLYOFFICE_FRAME_ORIGIN", " HTTPS://Docs.Example.com/ ")
	c, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "https://docs.example.com", c.ExternalServices.OnlyOffice.FrameOrigin,
		"the same site as filex is fine: origins, not sites, keep storage and pages apart")
}

// Anything but an origin stops the server, saying what to write; so do
// filex's own origin (a frame there would run the script with filex's
// session) and the app-interface origin (that host answers the interface
// route only).
func TestOfficeFrameOriginRefusesWhatIsNotAnotherOrigin(t *testing.T) {
	t.Setenv("FILEX_PUBLIC_URL", "https://files.example.com")
	t.Setenv("FILEX_APP_UI_ORIGIN", "https://apps.usercontent.example")
	for _, bad := range []string{
		"docs.example.com", "ftp://docs.example.com", "https://docs.example.com/filex-frame", "https://*.example.com",
		"https://user@docs.example.com", "https://docs.example.com?x=1", "https://docs.example.com'",
		"https://files.example.com", "https://apps.usercontent.example",
	} {
		t.Setenv("FILEX_ONLYOFFICE_FRAME_ORIGIN", bad)
		_, err := Load("")
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "FILEX_ONLYOFFICE_FRAME_ORIGIN", bad)
	}
}

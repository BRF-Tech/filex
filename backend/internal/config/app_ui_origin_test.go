package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppUIOriginDefaultsToFilexOwn(t *testing.T) {
	t.Setenv("FILEX_APP_UI_ORIGIN", "")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.AppUIOrigin)
}

func TestAppUIOriginFromTheEnvironment(t *testing.T) {
	t.Setenv("FILEX_PUBLIC_URL", "https://files.example.com")
	t.Setenv("FILEX_APP_UI_ORIGIN", " HTTPS://Apps.Files-Usercontent.example:8443/ ")
	c, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "https://apps.files-usercontent.example:8443", c.AppUIOrigin)
}

// Anything but an origin — and filex's own — stops the server, saying what
// to write: a path, a wildcard or a quote would end up inside a policy.
func TestAppUIOriginRefusesWhatIsNotASeparateOrigin(t *testing.T) {
	t.Setenv("FILEX_PUBLIC_URL", "https://files.example.com")
	for _, bad := range []string{
		"apps.example.com", "ftp://apps.example.com", "https://apps.example.com/ui", "https://*.example.com",
		"https://user@apps.example.com", "https://apps.example.com?x=1", "https://apps.example.com'", "https://files.example.com",
	} {
		t.Setenv("FILEX_APP_UI_ORIGIN", bad)
		_, err := Load("")
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "FILEX_APP_UI_ORIGIN", bad)
	}
}

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameAncestorsDefaultToFilexOnly(t *testing.T) {
	t.Setenv("FILEX_FRAME_ANCESTORS", "")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.FrameAncestors)
}

func TestFrameAncestorsFromTheEnvironment(t *testing.T) {
	t.Setenv("FILEX_FRAME_ANCESTORS", "https://home.example.com, http://10.0.0.5:7575 https://*.example.org")
	c, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, []string{"https://home.example.com", "http://10.0.0.5:7575", "https://*.example.org"}, c.FrameAncestors)
}

// A value that is not an origin stops the server, saying what to write: a
// dropped one would leave the dashboard it was meant for refused, silently.
func TestFrameAncestorsRefusesWhatIsNotAnOrigin(t *testing.T) {
	t.Setenv("FILEX_FRAME_ANCESTORS", "home.example.com")
	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https://home.example.com")
}

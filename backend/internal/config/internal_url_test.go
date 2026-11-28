package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GitHub #143: FILEX_ONLYOFFICE_INTERNAL_URL and FILEX_DRAWIO_INTERNAL_URL,
// the addresses filex reaches the services at when the browser's is another.
func TestInternalURLsFromTheEnvironment(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_URL", "https://office.example.com")
	t.Setenv("FILEX_ONLYOFFICE_INTERNAL_URL", " http://onlyoffice/ ")
	t.Setenv("FILEX_DRAWIO_URL", "https://draw.example.com")
	t.Setenv("FILEX_DRAWIO_INTERNAL_URL", "http://drawio:8080")
	c, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "https://office.example.com", c.ExternalServices.OnlyOffice.URL, "the URL stays the browser's")
	assert.Equal(t, "http://onlyoffice", c.ExternalServices.OnlyOffice.InternalURL)
	assert.Equal(t, "http://drawio:8080", c.ExternalServices.Drawio.InternalURL)
	assert.Equal(t, map[string]string{"onlyoffice": "http://onlyoffice", "drawio": "http://drawio:8080"},
		c.ExternalServices.InternalURLs())
}

func TestInternalURLsUnsetAreNone(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_INTERNAL_URL", "")
	t.Setenv("FILEX_DRAWIO_INTERNAL_URL", "")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.ExternalServices.InternalURLs())
}

// A value that is not an absolute http(s) address is said in the log and not
// used: the requests then go to the service's URL, as with no value.
func TestInternalURLsThatAreNoAddressAreNotUsed(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_INTERNAL_URL", "onlyoffice:80")
	t.Setenv("FILEX_DRAWIO_INTERNAL_URL", "ftp://drawio")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.ExternalServices.OnlyOffice.InternalURL)
	assert.Empty(t, c.ExternalServices.Drawio.InternalURL)
	assert.Empty(t, c.ExternalServices.InternalURLs())
}

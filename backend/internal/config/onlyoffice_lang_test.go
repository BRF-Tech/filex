package config

// FILEX_ONLYOFFICE_LANG (GitHub Discussion #93): the ONLYOFFICE editor's
// language, "auto" or a language code. Read as given (trimmed); whether
// ONLYOFFICE offers it is the boot's question (server.seedOnlyOfficeEditorLang),
// which says so in the log rather than stopping the server over a language.
// Red on the old code: the field did not exist.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnlyOfficeEditorLangIsUnsetByDefault(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_LANG", "")
	c, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, c.ExternalServices.OnlyOffice.EditorLang, "unset: the admin page's setting")
}

func TestOnlyOfficeEditorLangFromTheEnvironment(t *testing.T) {
	t.Setenv("FILEX_ONLYOFFICE_LANG", " tr ")
	c, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "tr", c.ExternalServices.OnlyOffice.EditorLang)

	t.Setenv("FILEX_ONLYOFFICE_LANG", "auto")
	c, err = Load("")
	require.NoError(t, err)
	assert.Equal(t, "auto", c.ExternalServices.OnlyOffice.EditorLang)
}

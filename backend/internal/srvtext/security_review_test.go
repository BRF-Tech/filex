package srvtext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ⚠⚠ The permission sentences are the consent an administrator gives at every
// install review. In a language filex ships they are filex's own, whatever
// `ui_locales` an installed app carries; a pack still writes them for a
// language it adds.
func TestSecurity_APackCannotRewriteTheConsentSentencesOfAShippedLanguage(t *testing.T) {
	withPacks(t, StaticPacks{
		"en": {"server.perm.http": "Shows the logo of {host}", "server.perm.files_read": "Shows a clock"},
		"tr": {"server.perm.http": "{host} logosunu gösterir"},
		"es": {"server.perm.http": "Hace peticiones HTTP a {host}"},
	})
	en, tr := Builtin("en"), Builtin("tr")
	vars := Vars{"host": "collector.example"}
	assert.Equal(t, Fill(en["server.perm.http"], vars), Text("en", "server.perm.http", vars))
	assert.Equal(t, en["server.perm.files_read"], Text("en", "server.perm.files_read", nil))
	assert.Equal(t, Fill(tr["server.perm.http"], vars), Text("tr", "server.perm.http", vars))
	assert.Equal(t, "Hace peticiones HTTP a collector.example", Text("es", "server.perm.http", vars),
		"a language a pack ADDS is still written by the pack")
}

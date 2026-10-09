package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A store refusal's sentence is the server's (app_store_words.go): every
// code internal/appstore declares is said in both shipped languages, from
// what its detail carries, and Go's English stays as detail.reason.
//
// RED PROOF (int/055-wave b5c58508): storeFail answered appstore.Error as it
// was - an English Go sentence in `message` for every reader - and the admin
// panel rebuilt the sentence from the code (web lib/storeRefusal.ts with the
// locales' appStore.err.*); store_trust_required, store_key_changed and the
// storage plugin's codes had no sentence at all outside #215's storage path.

// storeCodes are the codes internal/appstore declares (client.go), read
// from its source so a code added there without a sentence turns this red.
func storeCodes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "appstore", "client.go"))
	require.NoError(t, err)
	re := regexp.MustCompile(`(?m)^\s*Code[A-Za-z]+\s*=\s*"([a-z0-9_]+)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	require.GreaterOrEqual(t, len(out), 25, "the codes were not found in appstore/client.go")
	return out
}

func TestStoreRefusal_EveryCodeIsSaidInBothLanguages(t *testing.T) {
	const goWords = "the store answered something in English"
	for _, code := range storeCodes(t) {
		said := map[string]string{}
		for _, lang := range []string{"en", "tr"} {
			out := storeSaid(lang, &appstore.Error{Code: code, Message: goWords})
			require.NotNil(t, out, code)
			assert.Equal(t, code, out.Code, "the code stays")
			assert.NotEmpty(t, out.Message, "%s %s", lang, code)
			assert.NotEqual(t, goWords, out.Message, "%s %s is still Go's English", lang, code)
			assert.NotContains(t, out.Message, "server.store.", "%s %s printed a key", lang, code)
			if code != appstore.CodeStoreRefusal {
				assert.Equal(t, goWords, out.Detail["reason"], "%s: Go's words stay beside it, for a log", code)
			}
			if lang == "tr" {
				assert.NotRegexp(t, "[\u2013\u2014]", out.Message, "%s: a long dash in Turkish", code)
			}
			said[lang] = out.Message
		}
		assert.NotEqual(t, said["en"], said["tr"], "%s is not translated", code)
	}
}

func TestStoreRefusal_AnAppsRefusalSaysWhatItsDetailCarries(t *testing.T) {
	const a, b = "https://a.example", "https://b.example"

	// Another store, another repository: both sources and the way out.
	e := &appstore.Error{Code: appstore.CodeSourceChanged, Message: "lang-eo is installed from ...",
		Detail: map[string]any{"kind": appstore.KindApp,
			"installed": installedSource{Store: a, Repo: "Owner/app", Version: "1.0.0"},
			"link":      installedSource{Store: b, Repo: "Other/app", Version: "1.2.0"}}}
	en := storeSaid("en", e).Message
	assert.Contains(t, en, "the store "+a+" (Owner/app)")
	assert.Contains(t, en, "the store "+b+" (Other/app)")
	assert.Contains(t, en, "1.0.0")
	assert.Contains(t, en, "Remove the installed app first")
	tr := storeSaid("tr", e).Message
	assert.Contains(t, tr, a+" mağazasından (Owner/app)")
	assert.Contains(t, tr, "kaldırın")

	// Installed straight from the same repository, and a store's paid link
	// for it: said apart.
	e.Detail["installed"] = installedSource{Repo: "Owner/app", Version: "1.0.0"}
	e.Detail["link"] = installedSource{Store: b, Repo: "owner/APP", Version: "1.2.0"}
	en = storeSaid("en", e).Message
	assert.Contains(t, en, "straight from its repository (Owner/app), not from a store")
	assert.Contains(t, en, "The paid link of "+b)
	assert.Contains(t, storeSaid("tr", e).Message, "bir mağazadan değil, doğrudan deposundan (Owner/app) kuruldu")

	// Installed without a store from another repository: names where it came from.
	e.Detail["link"] = installedSource{Store: b, Repo: "Other/app", Version: "1.2.0"}
	assert.Contains(t, storeSaid("en", e).Message, "GitHub directly, without a store (Owner/app)")
	e.Detail["installed"] = installedSource{SourceURL: "https://x.example/app.json", Version: "1"}
	assert.Contains(t, storeSaid("en", e).Message, "an upload or an address, without a store (https://x.example/app.json)")

	// A version that is not newer names both.
	rb := storeSaid("en", &appstore.Error{Code: appstore.CodeVersionRollback, Message: "x",
		Detail: map[string]any{"installed": "1.2.0", "link": "1.0.0"}}).Message
	assert.Equal(t, srvtext.Text("en", "server.store.intent_version_rollback", srvtext.Vars{"installed": "1.2.0", "link": "1.0.0"}), rb)
	assert.Contains(t, rb, "1.2.0 is installed")

	// A moved tag says so; another pin keeps its sentence.
	c := storeSaid("en", &appstore.Error{Code: appstore.CodePinMismatch, Message: "x",
		Detail: map[string]any{"mismatches": []appstore.Mismatch{{Field: "commit", Link: "v1@abc", Source: "y"}}}}).Message
	assert.Contains(t, c, "tag no longer serves")
	assert.Contains(t, c, "(commit)")
	m := storeSaid("en", &appstore.Error{Code: appstore.CodePinMismatch, Message: "x",
		Detail: map[string]any{"mismatches": []appstore.Mismatch{{Field: "manifest_sha256"}}}}).Message
	assert.Contains(t, m, "(manifest_sha256)")
	assert.NotContains(t, m, "tag")

	// A link for another filex names both; an unusable FILEX_PUBLIC_URL names the setting.
	w := storeSaid("en", &appstore.Error{Code: appstore.CodeWrongInstance, Message: "x",
		Detail: map[string]any{"filex_origin": "https://other.example", "this_filex": "https://files.example"}}).Message
	assert.Contains(t, w, "https://other.example")
	assert.Contains(t, w, "https://files.example")
	p := storeSaid("tr", &appstore.Error{Code: appstore.CodeWrongInstance, Message: "x", Detail: map[string]any{"public_url_invalid": true}}).Message
	assert.Contains(t, p, "FILEX_PUBLIC_URL ayarı")

	// The store's own refusal is quoted.
	assert.Contains(t, storeSaid("en", &appstore.Error{Code: appstore.CodeStoreRefusal, Message: "slow down"}).Message, "slow down")
}

// A storage plugin's refusal already carries its sentence (#215): it is
// answered as it came.
func TestStoreRefusal_AStoragePluginsSentenceIsKept(t *testing.T) {
	e := &appstore.Error{Code: appstore.CodeVersionRollback, Message: "myfs 1.3.0 is installed; ...",
		Detail: map[string]any{"kind": appstore.KindStorage, "installed": "1.3.0", "link": "1.2.0"}}
	assert.Same(t, e, storeSaid("tr", e))
}

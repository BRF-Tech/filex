package wasmplugin

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// An install's refusal is said by the server (InstallRefusal.Said,
// server.install.*): every install code and every reason a download fails
// has a sentence in both shipped languages, Go's English moves to `detail`,
// and the stored refusal keeps its English.
//
// RED PROOF (int/055-wave d8a8cc2f): there was no Said; `message` was Go's
// English ("filex-app.json not found in BRF-Tech/x: http 404 from
// raw.githubusercontent.com") and the panel built the sentence from the code
// (web lib/appPluginRefusal.ts, appPlugins.wizard.errors.*).

// constsIn are the string constants of a name prefix declared in file.
func constsIn(t *testing.T, file, prefix string) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	require.NoError(t, err)
	re := regexp.MustCompile(`(?m)^\s*` + prefix + `\w*\s*=\s*"([a-z0-9_]+)"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	return out
}

func TestInstallRefusal_EveryCodeAndReasonIsSaidInBothLanguages(t *testing.T) {
	codes := constsIn(t, "registry.go", "ErrCode")
	require.GreaterOrEqual(t, len(codes), 14, "the install codes were not found in registry.go")
	reasons := constsIn(t, "registry.go", "FetchReason")
	require.GreaterOrEqual(t, len(reasons), 9, "the fetch reasons were not found in registry.go")

	say := func(ref InstallRefusal) {
		t.Helper()
		en, tr := ref.Said("en"), ref.Said("tr")
		for _, got := range []*InstallRefusal{en, tr} {
			assert.Equal(t, ref.Code, got.Code, "the code stays")
			assert.NotEmpty(t, got.Message, "%s %s", ref.Code, ref.Reason)
			assert.NotEqual(t, ref.Message, got.Message, "%s %s is still Go's English", ref.Code, ref.Reason)
			assert.NotContains(t, got.Message, "server.install.", "%s printed a key", ref.Code)
			assert.NotContains(t, got.Message, "{", "%s %s left a placeholder open: %s", ref.Code, ref.Reason, got.Message)
			assert.Equal(t, ref.Message, got.Detail, "Go's English moves to detail")
		}
		assert.NotEqual(t, en.Message, tr.Message, "%s %s is not translated", ref.Code, ref.Reason)
		assert.NotRegexp(t, "[\u2013\u2014]", tr.Message, "%s: a long dash in Turkish", ref.Code)
	}
	for _, code := range codes {
		if code == ErrCodeFetch {
			continue
		}
		if code != ErrCodeOutOfRange {
			assert.True(t, srvtext.Has("server.install."+code), "server.install.%s is missing", code)
		}
		say(InstallRefusal{Code: code, Message: "go's own words", Missing: []string{"net:tsa"}, Requires: ">=9.0.0", Filex: "0.55.0"})
	}
	for _, reason := range reasons {
		assert.True(t, srvtext.Has("server.install.fetch."+reason), "server.install.fetch.%s is missing", reason)
		say(InstallRefusal{Code: ErrCodeFetch, Reason: reason, Message: "go's own words", Where: "BRF-Tech/x", Refs: []string{"main", "master"}, Status: 404})
	}
}

func TestInstallRefusal_SaidFillsItsFieldsAndKeepsTheStoredOne(t *testing.T) {
	stored := &InstallRefusal{Code: ErrCodeFetch, Reason: FetchReasonManifestNotFound, Where: "BRF-Tech/yok", Refs: []string{"main", "master"},
		Status: 404, Message: "filex-app.json not found in BRF-Tech/yok: http 404"}
	got := stored.Said("tr")
	assert.Contains(t, got.Message, "BRF-Tech/yok")
	assert.Contains(t, got.Message, "main, master")
	assert.Equal(t, "filex-app.json not found in BRF-Tech/yok: http 404", got.Detail)
	assert.Equal(t, "filex-app.json not found in BRF-Tech/yok: http 404", stored.Message, "what was stored keeps its English")
	assert.Empty(t, stored.Detail)
	assert.Same(t, got, got.Said("en"), "a refusal already said is not said again")

	inc := (&InstallRefusal{Code: ErrCodeIncompatible, Requires: ">=0.48.0", Filex: "0.47.0", Message: "x"}).Said("en")
	assert.Contains(t, inc.Message, ">=0.48.0")
	assert.Contains(t, inc.Message, "0.47.0")

	// A code no sentence is written for (an update check's plain error) says
	// that it did not go through, with Go's words.
	other := (&InstallRefusal{Code: "error", Message: "context deadline exceeded"}).Said("en")
	assert.Equal(t, srvtext.Text("en", "server.install.failed", srvtext.Vars{"detail": "context deadline exceeded"}), other.Message)
	var nilRef *InstallRefusal
	assert.Nil(t, nilRef.Said("en"))
}

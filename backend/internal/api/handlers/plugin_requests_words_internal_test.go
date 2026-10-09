package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pluginreq"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A plugin request's refusal is said in the reader's language
// (server.plugin_request.*): every code and reason internal/pluginreq
// refuses with has a sentence in both shipped languages - bad_request too
// (0.55, second round): every one names its reason, the field it wants.
//
// RED PROOF (int/055-wave b5c58508): PluginRequests.fail answered pe.Message,
// English for every reader ("you have 3 requests waiting for an
// administrator; ...", "sign 1.2.0 is installed already"), and the store
// screen said two of them in a copy of its own (storeScreen.tooMany,
// storeScreen.alreadyInstalled).
func TestPluginRequestRefusal_EveryReasonIsSaidInBothLanguages(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "pluginreq", "service.go"))
	require.NoError(t, err)
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`refuse\(http\.Status\w+,\s*"([a-z0-9_]+)"`).FindAllStringSubmatch(string(src), -1) {
		names[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`refuseSaid\(http\.Status\w+,\s*"([a-z0-9_]+)",\s*"([a-z0-9_]*)"`).FindAllStringSubmatch(string(src), -1) {
		if m[2] != "" {
			names[m[2]] = true
		} else {
			names[m[1]] = true
		}
	}
	require.GreaterOrEqual(t, len(names), 10, "the refusals were not found in pluginreq/service.go: %v", names)
	// Every bad_request names its reason (refuseSaid): a plain refuse with
	// it would answer the generic sentence and lose which field was wrong.
	// RED before the second round: sixteen of them were English only.
	assert.NotRegexp(t, `refuse\(http\.Status\w+,\s*"bad_request"`, string(src))
	p := map[string]string{"name": "sign", "version": "1.2.0", "requires": ">=0.60.0", "filex": "0.55.0"}
	for name := range names {
		pe := &pluginreq.Error{Code: name, Message: "english detail", Params: p}
		en, tr := pluginRequestSaid("en", pe), pluginRequestSaid("tr", pe)
		assert.NotEmpty(t, en, name)
		assert.NotEmpty(t, tr, name)
		assert.NotEqual(t, en, tr, "%s is not translated", name)
		assert.NotContains(t, en+tr, "{", "%s left a placeholder open", name)
	}
	assert.Equal(t, srvtext.Text("tr", "server.plugin_request.bad_op", nil),
		pluginRequestSaid("tr", &pluginreq.Error{Code: "bad_request", Say: "bad_op", Message: "op must be install or upgrade"}))
	assert.Equal(t, srvtext.Text("en", "server.plugin_request.bad_request", nil),
		pluginRequestSaid("en", &pluginreq.Error{Code: "bad_request", Message: "something else"}), "a reason with no sentence of its own says the code's")
}

func TestPluginRequestRefusal_TheReasonPicksTheSentence(t *testing.T) {
	got := pluginRequestSaid("tr", &pluginreq.Error{Code: "already_installed", Params: map[string]string{"name": "sign", "version": "1.2.0"}})
	assert.Equal(t, srvtext.Text("tr", "server.plugin_request.already_installed", srvtext.Vars{"name": "sign", "version": "1.2.0"}), got)
	assert.Contains(t, got, "sign 1.2.0")
	assert.Equal(t, srvtext.Text("en", "server.plugin_request.no_request", nil),
		pluginRequestSaid("en", &pluginreq.Error{Code: "not_found", Say: "no_request"}))
	assert.NotEqual(t, pluginRequestSaid("en", &pluginreq.Error{Code: "not_found", Say: "no_app"}),
		pluginRequestSaid("en", &pluginreq.Error{Code: "not_found", Say: "no_storage_plugin"}))
	// A request whose source moved on (pluginreq.Superseded) is said too.
	assert.NotEqual(t, srvtext.Text("en", "server.plugin_request.superseded", nil), srvtext.Text("tr", "server.plugin_request.superseded", nil))
}

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// The second round of 0.55's "every refusal carries the server's sentence":
// an install's refusal, an update check's stored refusal, the host's own
// refusals in an app call, a Default apps rule and a server fault.
//
// RED PROOF (int/055-wave d8a8cc2f): installErrorBody took no language and
// answered Go's English as `message` (the wizard and the Apps list built the
// sentence in the browser), sayStatus did not exist, callFail answered the
// host's English ("the app is busy; try again in a moment"),
// FileTypesAdmin.fail answered the rule's English, and a 500 put Go's error
// text in `error`.

func trRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "tr")
	return req
}

func bodyOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func TestInstallErrorBody_IsTheReadersSentence(t *testing.T) {
	got := installErrorBody("tr", &wasmplugin.InstallError{Code: wasmplugin.ErrCodeIncompatible, Requires: ">=0.48.0", Filex: "0.47.0",
		Message: "sign 1.2.0 works with filex >=0.48.0; this is filex 0.47.0"})
	assert.Equal(t, srvtext.Text("tr", "server.install.incompatible", srvtext.Vars{"requires": ">=0.48.0", "filex": "0.47.0"}), got.Message)
	assert.Equal(t, "sign 1.2.0 works with filex >=0.48.0; this is filex 0.47.0", got.Detail)
	assert.Equal(t, ">=0.48.0", got.Requires, "the fields stay for a program")

	rec := httptest.NewRecorder()
	writeAppFailure(rec, "tr", errors.New("database is locked"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	b := bodyOf(t, rec)
	assert.Equal(t, "internal_error", b["error"], "a fault answers a code, not Go's text in `error`")
	assert.Equal(t, apierr.Text("tr", "internal_error", nil), b["message"])
	assert.Equal(t, "database is locked", b["detail"])
}

func TestSayStatus_AStoredUpdateRefusalIsSaidAndTheStoredOneIsKept(t *testing.T) {
	stored := &wasmplugin.UpdateInfo{Status: wasmplugin.UpdateCheckFailed, Refusal: &wasmplugin.InstallRefusal{
		Code: wasmplugin.ErrCodeFetch, Reason: wasmplugin.FetchReasonUnreachable, Where: "BRF-Tech/filex-sign", Message: "dial tcp: i/o timeout"}}
	st := sayStatus("tr", &wasmplugin.Status{Name: "sign", Update: stored})
	require.NotNil(t, st.Update.Refusal)
	assert.Equal(t, srvtext.Text("tr", "server.install.fetch.unreachable", srvtext.Vars{"where": "BRF-Tech/filex-sign"}), st.Update.Refusal.Message)
	assert.Equal(t, "dial tcp: i/o timeout", st.Update.Refusal.Detail)
	assert.Equal(t, "dial tcp: i/o timeout", stored.Refusal.Message, "the stored refusal keeps its English, to be said again for the next reader")
	assert.Nil(t, sayStatus("tr", nil))
	plain := &wasmplugin.Status{Name: "x"}
	assert.Same(t, plain, sayStatus("en", plain))
}

func TestCallFail_TheHostsRefusalsAreSaidAndTheAppsOwnWordsPass(t *testing.T) {
	h := &AppPlugins{}
	for code, said := range callFailSaid {
		rec := httptest.NewRecorder()
		h.callFail(rec, trRequest(), &wasmplugin.CallError{Code: code, Message: "the host's English"})
		b := bodyOf(t, rec)
		assert.Equal(t, code, b["error"], "the code stays")
		assert.Equal(t, apierr.Text("tr", said, apierr.Params{"detail": "the host's English"}), b["message"], "%s", code)
		assert.NotEmpty(t, b["message"], "%s", code)
		assert.Equal(t, "the host's English", b["detail"])
	}
	rec := httptest.NewRecorder()
	h.callFail(rec, trRequest(), &wasmplugin.CallError{Code: wasmplugin.CodePluginError, Message: "İmzacının e-posta adresi geçersiz"})
	assert.Equal(t, "İmzacının e-posta adresi geçersiz", bodyOf(t, rec)["message"], "an app's own refusal is the app's words")
}

func TestFileTypesFail_ARuleIsRefusedInTheReadersWords(t *testing.T) {
	for say, params := range map[string]map[string]string{
		"capability":  {"capability": "sideways"},
		"kind":        {"ext": "BAD.X"},
		"not_handler": {"id": "x", "capability": "open"},
		"cannot":      {"id": "sign:viewer", "ext": "pdf"},
		"unknown":     nil,
		"placement":   {"placement": "open .pdf x"},
		"place":       {"place": "middle"},
	} {
		rec := httptest.NewRecorder()
		(&FileTypesAdmin{}).fail(rec, trRequest(), &assoc.ErrInvalid{Message: "english rule", Say: say, Params: params})
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		b := bodyOf(t, rec)
		assert.Equal(t, "invalid_rule", b["error"])
		assert.Equal(t, apierr.Text("tr", "rule_"+say, apierr.Params(params)), b["message"], "%s", say)
		assert.NotEqual(t, apierr.Text("en", "rule_"+say, apierr.Params(params)), b["message"], "%s is not translated", say)
		assert.Equal(t, "english rule", b["detail"])
	}
}

// A tenant's administrator at a platform operator's door is told so in the
// reader's language; what was refused stays as English detail.
// RED before: `message` was the English phrase the door passed ("app plugins
// are managed by the platform operator").
func TestRequireSupertenant_IsSaidInTheReadersLanguage(t *testing.T) {
	rec := httptest.NewRecorder()
	req := trRequest()
	req = req.WithContext(tenant.WithScope(req.Context(), &tenant.Scope{}))
	assert.False(t, requireSupertenant(rec, req, "app plugins are managed by the platform operator"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	b := bodyOf(t, rec)
	assert.Equal(t, "supertenant_only", b["error"])
	assert.Equal(t, apierr.Text("tr", "supertenant_only", nil), b["message"])
	assert.Equal(t, "app plugins are managed by the platform operator", b["detail"])
}

// The File types choices an install could not write are one sentence each in
// the reader's language. RED before: they were assoc's English.
func TestPlaceFailuresSaid_EachInTheReadersLanguage(t *testing.T) {
	got := placeFailuresSaid("tr", []assoc.PlaceFailure{
		{Capability: "open", Ext: "drawio", Handler: "app:drawio/editor", Say: "place_not_own", Message: "x"},
		{Capability: "open", Ext: "svg", Say: "place_not_new", Message: "y"},
		{Capability: "open", Ext: "pdf", Say: "rule_place", Params: map[string]string{"place": "middle"}, Message: "z"},
		{Capability: "open", Ext: "png", Say: "nothing_like_it", Message: "database is locked"},
	})
	require.Len(t, got, 4)
	assert.Equal(t, apierr.Text("tr", "place_not_own", apierr.Params{"handler": "app:drawio/editor", "ext": "drawio"}), got[0])
	assert.Equal(t, apierr.Text("tr", "place_not_new", apierr.Params{"ext": "svg"}), got[1])
	assert.Equal(t, apierr.Text("tr", "rule_place", apierr.Params{"place": "middle"}), got[2])
	assert.Contains(t, got[3], "database is locked", "an unknown reason keeps its detail")
	assert.Contains(t, got[3], ".png")
	assert.Nil(t, placeFailuresSaid("tr", nil))
}

// The Apps list's lines about an app's updates, a range outside this filex
// and the version kept to go back to are the server's, in the reader's
// language (0.55). RED before: sayStatus said only update.refusal, and the
// list built every other line in the browser (web lib/appPluginUpdates.ts:
// appPlugins.update.needsNewerFilex, failedDetail, newPermissions, addsModule,
// noSource, noManifestAddress, previous, appPlugins.compat.needs) - deciding
// there too which one a row gets.
func TestSayStatus_TheAppsListLinesAreTheServers(t *testing.T) {
	say := func(key string, v srvtext.Vars) string { return srvtext.Text("tr", "server.update_line."+key, v) }

	assert.Equal(t, say("no_source", nil), sayStatus("tr", &wasmplugin.Status{Name: "x", Source: "upload"}).UpdateSaid)
	assert.Equal(t, say("no_manifest_address", nil), sayStatus("tr", &wasmplugin.Status{Name: "x", Source: "url"}).UpdateSaid,
		"an app installed from an address filex did not keep is not said to be from a file")

	row := func(u *wasmplugin.UpdateInfo) *wasmplugin.Status {
		return &wasmplugin.Status{Name: "sign", Version: "1.2.0", UpdateSource: "github", Update: u}
	}
	assert.Equal(t, say("needs_newer_filex", srvtext.Vars{"version": "2.0.0", "requires": ">=0.60.0"}),
		sayStatus("tr", row(&wasmplugin.UpdateInfo{Status: wasmplugin.UpdateIncompatible, Version: "2.0.0", Requires: ">=0.60.0"})).UpdateSaid)
	assert.Equal(t, say("failed_detail", srvtext.Vars{"version": "1.3.0", "current": "1.2.0"}),
		sayStatus("tr", row(&wasmplugin.UpdateInfo{Status: wasmplugin.UpdateFailed, Version: "1.3.0"})).UpdateSaid)
	assert.Equal(t, say("new_permissions", srvtext.Vars{"permissions": "mail:send, public_pages"})+" · "+say("adds_module", nil),
		sayStatus("tr", row(&wasmplugin.UpdateInfo{Status: wasmplugin.UpdateNeedsApproval, Version: "1.3.0",
			Added: []string{"mail:send", "public_pages"}, AddsModule: true})).UpdateSaid)
	assert.Empty(t, sayStatus("tr", row(&wasmplugin.UpdateInfo{Status: wasmplugin.UpdateAvailable, Version: "1.3.0"})).UpdateSaid,
		"a status word says it all")

	out := &wasmplugin.Status{Name: "lang-es", Version: "1.0.0", UpdateSource: "github",
		Compat:   &wasmplugin.Compat{Requires: ">=0.45.0 <0.47.0", OK: false, Filex: "0.47.0"},
		Previous: &wasmplugin.PreviousVersion{Version: "0.9.0", ReplacedAt: time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)}}
	got := sayStatus("tr", out)
	assert.Equal(t, say("compat_outside", srvtext.Vars{"requires": ">=0.45.0 <0.47.0", "filex": "0.47.0"}), got.Compat.Message)
	assert.Equal(t, say("previous", srvtext.Vars{"version": "0.9.0", "when": "2026-09-24 08:00 UTC"}), got.Previous.Message)
	assert.Empty(t, sayStatus("tr", &wasmplugin.Status{Name: "ok", UpdateSource: "github",
		Compat: &wasmplugin.Compat{Requires: ">=0.45.0", OK: true, Filex: "0.47.0"}}).Compat.Message, "a range that holds says nothing")
}

// The storage plugins list's line about a plugin's updates is the server's.
// RED before: storageUpdateSaid did not exist; the tab said
// plugins.update.incompatible itself and printed the check's English error.
func TestStorageUpdateSaid_IsTheReadersSentence(t *testing.T) {
	assert.Equal(t, srvtext.Text("tr", "server.update_line.needs_newer_filex", srvtext.Vars{"version": "2.0.0", "requires": ">=0.60.0"}),
		storageUpdateSaid("tr", &plugin.UpdateInfo{Status: plugin.UpdateIncompatible, Version: "2.0.0", Requires: ">=0.60.0"}))
	got := storageUpdateSaid("tr", &plugin.UpdateInfo{Status: plugin.UpdateCheckFailed, Error: "filex-storage.json: http 404"})
	assert.Equal(t, srvtext.Text("tr", "server.update_line.check_failed", srvtext.Vars{"detail": "filex-storage.json: http 404"}), got)
	assert.Contains(t, got, "http 404", "the check's reason stays in the sentence")
	assert.Empty(t, storageUpdateSaid("tr", &plugin.UpdateInfo{Status: plugin.UpdateCurrent}))
	assert.Empty(t, storageUpdateSaid("tr", nil))
}

// The Apps tab's header is the server's: which lines, and their words, on the
// reader's clock. RED before 0.55: runtimeSaid did not exist and the tab
// picked and worded each line from the facts (appPlugins.runtime.*).
func TestRuntimeSaid_TheAppsTabHeaderIsTheServers(t *testing.T) {
	say := func(lang, key string, v srvtext.Vars) string { return srvtext.Text(lang, "server.app_runtime."+key, v) }
	at := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC)
	on := map[string]any{"enabled": true, "arch_ok": true, "requires_signature": false, "filex_version": "0.55.0",
		"compat_enforced": true, "update_check": true, "updates_checked_at": at}
	got := runtimeSaid("tr", utcClock, on)
	assert.Equal(t, map[string]string{"state": say("tr", "on", nil),
		"update_check": say("tr", "last_check", srvtext.Vars{"when": "2026-09-26 03:00 UTC"})}, got)

	dev := map[string]any{"enabled": true, "arch_ok": true, "requires_signature": true, "filex_version": "0.55.0-dev",
		"compat_enforced": false, "update_check": false}
	got = runtimeSaid("en", utcClock, dev)
	assert.Equal(t, say("en", "dev_build", srvtext.Vars{"version": "0.55.0-dev"}), got["dev_build"])
	assert.Equal(t, say("en", "update_check_off", nil), got["update_check"])
	assert.Equal(t, say("en", "signature", nil), got["signature"])
	delete(dev, "update_check")
	dev["update_check"] = true
	assert.Equal(t, say("en", "never_checked", nil), runtimeSaid("en", utcClock, dev)["update_check"])

	off := map[string]any{"enabled": false, "arch_ok": false}
	got = runtimeSaid("tr", utcClock, off)
	assert.Equal(t, map[string]string{"state": say("tr", "off", nil), "arch": say("tr", "arch_bad", nil)}, got,
		"a platform that is off says nothing about checks")

	// Task #110: the processor warning names every kind of app, not modules
	// alone - now the server's sentence.
	assert.Regexp(t, `(?i)\binterface\b`, say("en", "arch_bad", nil))
	assert.Regexp(t, `(?i)\blanguage\b`, say("en", "arch_bad", nil))
	assert.Contains(t, say("tr", "arch_bad", nil), "arayüz")
}

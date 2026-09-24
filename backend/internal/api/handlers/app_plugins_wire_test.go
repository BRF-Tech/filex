package handlers

// The wire fixtures the web client's tests run against, WRITTEN BY THE SERVER.
//
// ⚠⚠ Why these exist. Twice in v0.43.0 the browser typed an Apps answer
// differently from what this package sends, and both times every test stayed
// green, because each test's fixture had been typed the browser's way:
//
//  1. The app detail drawer said a signed app was "Unsigned" and drew its
//     name, version and dates blank — the app's own fields arrive under
//     `plugin`, the client read them flat, and 1300 tests passed over it
//     because their fixtures were flat too.
//  2. The install review printed every permission's reason as raw
//     `{"en": …, "tr": …}` — `reason` is a wire.Text, the client typed it
//     `string`, and the wizard's own test fed it a flat string. The picture
//     of that screen was about to put Turkish into the English README.
//
// A hand-written fixture is the client's belief about the wire, so it can
// only ever agree with the client. These files are the server's own bytes:
// this test serialises the exact values the handlers send (writeJSON of a
// wasmplugin.DryRunAnswer; appPluginDetailBody for the detail) and fails when
// the file on disk differs, and the web tests read the same files
// (web/tests/components/appPluginInstallWizard.test.ts,
// web/tests/api/appPluginsApi.test.ts). Change a type here and this test
// fails until the fixture is regenerated — and then the client's tests run
// against the new shape instead of the one somebody remembered.
//
//	FILEX_UPDATE_WIRE_FIXTURES=1 go test ./internal/api/handlers/ -run TestAppPluginWireFixtures
//
// ⚠ Every localised field in them carries BOTH languages with different
// words, so a client that shows the object, or always the English half,
// cannot pass a test that reads the Turkish screen.

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// wireManifest is a small app that exercises every localised field the admin
// Apps screens read: label, description, an action label and confirm, a view
// label, a public page label, and permission reasons — one permission
// deliberately WITHOUT a reason, because "no reason given" is a state the
// review must render too.
const wireManifest = `{
  "manifest_version": 1,
  "name": "sign",
  "version": "1.2.0",
  "label": {"en": "e-Signature", "tr": "e-İmza"},
  "description": {"en": "Send a document out for signature.", "tr": "Bir belgeyi imzaya gönderin."},
  "languages": ["en", "tr"],
  "permissions": ["files:read", "files:write", "public_pages", "state"],
  "permission_reasons": {
    "files:read": {"en": "To read the document you sign or send.", "tr": "İmzaladığınız ya da gönderdiğiniz belgeyi okumak için."},
    "files:write": {"en": "To write the signed document beside the original.", "tr": "İmzalı belgeyi özgün belgenin yanına yazmak için."},
    "public_pages": {"en": "To let a signer without an account open their own link.", "tr": "Hesabı olmayan bir imzacının kendi bağlantısını açabilmesi için."}
  },
  "settings": [
    {"key": "tsa_url", "type": "string", "label": "Timestamp authority"}
  ],
  "actions": [
    {"id": "sign", "label": {"en": "Sign…", "tr": "İmzala…"}, "confirm": {"en": "Send it?", "tr": "Gönderilsin mi?"},
     "applies": {"kind": "file", "ext": ["pdf"]}, "view": "wizard", "output": {"mode": "sibling"}}
  ],
  "views": [
    {"id": "wizard", "placement": "page", "label": {"en": "Signing", "tr": "İmzalama"}}
  ],
  "public_pages": [
    {"id": "signer", "label": {"en": "Sign the document", "tr": "Belgeyi imzala"}, "pin": "optional"}
  ]
}`

func wireFixtureApp(t *testing.T) *wasmplugin.Installed {
	t.Helper()
	m, err := wasmplugin.ParseManifest([]byte(wireManifest))
	require.NoError(t, err)
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	return &wasmplugin.Installed{
		Row: &model.AppPlugin{
			ID: 7, Name: m.Name, Version: m.Version, Source: "github",
			SourceURL: "https://github.com/BRF-Tech/filex-sign", Signed: true, Enabled: true,
			SHA256:    "a9950e0d84a62a1b30c952736b097e5f3cde019564724dc3c0432a36f7c0d091",
			CreatedAt: at, UpdatedAt: at,
		},
		Manifest: m,
		Perms:    m.Perms,
	}
}

// wireBytes is what writeJSON puts on the wire for body — the same encoder,
// so a change to how the handlers encode is a change here.
func wireBytes(t *testing.T, body any) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, body)
	var v any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(v))
	return out.Bytes()
}

// fixtureLock is one app's hold on a file, as the store keeps it.
func fixtureLock(plugin string) *model.AppPluginLock {
	until := time.Date(2026, 9, 30, 15, 8, 0, 0, time.UTC)
	return &model.AppPluginLock{
		StorageID: 1, Rel: "Contracts/service-agreement.pdf", PluginID: 7, PluginName: plugin,
		Reason: "signatures are being collected", Until: &until,
		CreatedAt: time.Date(2026, 9, 23, 15, 8, 0, 0, time.UTC),
	}
}

func TestAppPluginWireFixtures(t *testing.T) {
	app := wireFixtureApp(t)
	/* The label resolver the handlers hold at runtime (NewAppPlugins wires
	   the registry's). Only the fixture app answers, so `ghost` writes the
	   half of the fixture where a label cannot be found. */
	SetAppLabels(func(name string) wire.Text {
		if name == app.Row.Name {
			return app.Manifest.Label
		}
		return nil
	})
	t.Cleanup(func() { SetAppLabels(nil) })
	var reg *wasmplugin.Registry // StatusOf reads only the app it is given
	fixtures := map[string]any{
		// POST /api/admin/app-plugins?dry_run=1 — Install's dry-run answer.
		"app-plugin-dry-run.json": &wasmplugin.DryRunAnswer{
			Manifest:    &app.Manifest.Manifest,
			Permissions: wasmplugin.PermissionRows(app.Manifest, "en"),
			WasmSHA256:  app.Row.SHA256,
			WasmBytes:   5242880,
			Signed:      true,
			// What dryRun always says of a module (a language pack's review
			// is app-plugin-language-pack.json).
			Kind: wasmplugin.KindApp,
			// Both said at the review since v0.43.0's sweep: the name is
			// taken (and by which install, for "upgrade it instead"), and
			// which engines the app asks for are not on this server.
			Installed:      &wasmplugin.DryRunInstalled{ID: 7, Version: "0.2.0"},
			EnginesMissing: []wasmplugin.DryRunEngine{{ID: "libreoffice", Name: "LibreOffice"}},
		},
		// A repository install that found no manifest: the refusal the
		// wizard turns into a sentence that says what to check.
		"app-plugin-fetch-failed.json": installErrorBody(&wasmplugin.InstallError{
			Code: wasmplugin.ErrCodeFetch, Reason: wasmplugin.FetchReasonManifestNotFound,
			Where: "BRF-Tech/yok-boyle-bir-depo", Refs: []string{"main", "master"}, Status: 404,
			Message: "filex-app.json not found in BRF-Tech/yok-boyle-bir-depo: http 404 from raw.githubusercontent.com",
		}),
		// GET /api/admin/app-plugins/{id} — the envelope.
		"app-plugin-detail.json": appPluginDetailBody(
			reg.StatusOf(app), app, "en",
			map[string]string{"tsa_url": "https://freetsa.org/tsr"},
			app.Manifest.Settings,
			[]wasmplugin.OverrideRow{{ID: "sign", Enabled: true}},
			nil,
		),
		// GET /api/admin/app-plugins — one row of the list.
		"app-plugin-list.json": map[string]any{
			"runtime": map[string]any{"enabled": true, "arch_ok": true, "disabled_reason": "", "requires_signature": false, "engines": map[string]bool{"ffmpeg": true}},
			"plugins": []*wasmplugin.Status{reg.StatusOf(app)},
		},
		// A language pack: its list row and its install review. ⚠ The rows
		// carry coverage as a binary WITH a catalogue reports it (a nil
		// registry has none, so the numbers are set here — the shape is the
		// server's type either way), and Arabic is there so the client has an
		// `rtl: true` to read.
		"app-plugin-language-pack.json": map[string]any{
			"row":     packStatus(t, reg),
			"dry_run": packDryRun(t),
		},
		// The lock a listing row carries (lockView) and the 423 repeats: who
		// holds the file, why, until when. Both halves are here because they
		// are one sentence with two fates — `labelled` is an installed app,
		// `unlabelled` is one this server cannot resolve (uninstalled since,
		// or no app runtime at all), and the client has to read as a sentence
		// either way rather than printing "undefined locked this file".
		"app-plugin-lock.json": map[string]any{
			"labelled":   lockView(fixtureLock(app.Row.Name)),
			"unlabelled": lockView(fixtureLock("ghost")),
		},
		// GET /api/public/branding — the languages apps add are a LIST of
		// rows without strings (it used to be a map the browser never read).
		"public-branding.json": PublicBranding{
			Name: "Acme", Accent: "#2f6ceb", Theme: "system", Locale: "en",
			Locales: []string{"ar", "en", "es", "tr"},
			UILocales: []wasmplugin.UILocaleInfo{
				{Code: "ar", Source: "plugin", Plugin: "lang-ar", RTL: true},
				{Code: "es", Source: "plugin", Plugin: "lang-es"},
			},
		},
		// GET /api/public/ui-locales/es — one language's strings, fetched
		// when somebody picks it.
		"public-ui-locale.json": PublicUILocale{Code: "es", Strings: map[string]string{
			"ctx.download": "Descargar", "ctx.rename": "Cambiar nombre",
			"appPlugins.title": "Aplicaciones", "common.cancel": "Cancelar",
		}},
	}
	update := os.Getenv("FILEX_UPDATE_WIRE_FIXTURES") != ""
	for name, body := range fixtures {
		got := wireBytes(t, body)
		path := filepath.Join("testdata", "wire", name)
		if update {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, got, 0o644))
			continue
		}
		want, err := os.ReadFile(path)
		require.NoError(t, err, "missing wire fixture — run with FILEX_UPDATE_WIRE_FIXTURES=1")
		// ⚠ Line endings normalised: a checkout with autocrlf hands back CRLF.
		require.Equalf(t, string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), string(got),
			"%s no longer matches what the server sends. Regenerate it (FILEX_UPDATE_WIRE_FIXTURES=1), then run the web tests that read it — they are the ones that find out whether the client still reads this shape.", name)
	}
}

// packManifestJSON is a language pack as a translator ships it: languages and
// nothing else — no module, no permission, no screen.
const packManifestJSON = `{
  "manifest_version": 1,
  "name": "lang-es",
  "version": "1.0.0",
  "label": {"en": "Spanish language pack", "tr": "İspanyolca dil paketi"},
  "description": {"en": "The whole interface in Spanish.", "tr": "Tüm arayüz İspanyolca."},
  "languages": ["en", "tr"],
  "permissions": [],
  "ui_locales": {
    "es": {"ctx.download": "Descargar", "appPlugins.title": "Aplicaciones"},
    "ar": {"ctx.download": "تحميل"}
  }
}`

// packRows is what LanguageRows answers for the pack against a 2 900-key
// catalogue — set by hand because the fixture has no registry to load one.
var packRows = []wasmplugin.LanguageRow{
	{Code: "ar", Keys: 2410, Translated: 2398, Unknown: 12, Total: 2900, Percent: 82, RTL: true},
	{Code: "es", Keys: 2830, Translated: 2830, Total: 2900, Percent: 97},
}

func packStatus(t *testing.T, reg *wasmplugin.Registry) *wasmplugin.Status {
	t.Helper()
	m, err := wasmplugin.ParseManifest([]byte(packManifestJSON))
	require.NoError(t, err)
	require.True(t, m.IsLanguagePack())
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	st := reg.StatusOf(&wasmplugin.Installed{
		Row: &model.AppPlugin{
			ID: 9, Name: m.Name, Version: m.Version, Source: "github",
			SourceURL: "https://github.com/BRF-Tech/filex-lang-es@main", Enabled: true,
			SHA256:    "5d41402abc4b2a76b9719d911017c592ae2e8e9e5d41402abc4b2a76b9719d91",
			CreatedAt: at, UpdatedAt: at,
		},
		Manifest: m,
	})
	st.State = wasmplugin.StateRunning
	st.Languages = packRows
	return st
}

func packDryRun(t *testing.T) *wasmplugin.DryRunAnswer {
	t.Helper()
	m, err := wasmplugin.ParseManifest([]byte(packManifestJSON))
	require.NoError(t, err)
	return &wasmplugin.DryRunAnswer{
		Manifest: &m.Manifest, Permissions: wasmplugin.PermissionRows(m, "en"),
		Kind: wasmplugin.KindLanguagePack, ManifestSHA256: "5d41402abc4b2a76b9719d911017c592ae2e8e9e5d41402abc4b2a76b9719d91",
		Languages: packRows,
	}
}

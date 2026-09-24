package wasmplugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// What filex itself writes around an app — the footer under its mail, the
// install review's permission sentences — speaks the reader's language from
// the server catalogue, a language pack's included.

func withSpanishPack(t *testing.T) {
	t.Helper()
	srvtext.SetPacks(srvtext.StaticPacks{"es": {
		"server.mail.app_footer":   "Enviado por la aplicación {app} de filex.",
		"server.perm.files_read":   "Lee el contenido de los archivos que elijas",
		"server.perm.http":         "Hace peticiones HTTP a {host}",
		"server.perm.files_write":  "Escribe archivos", // lost nothing: no placeholder in English either
		"server.perm.engines":      "Ejecuta un motor", // ⚠ lost {engine}: refused, English shows
		"server.perm.notify_send":  "",                 // untranslated
		"server.perm.users_lookup": "Busca usuarios por nombre o correo",
	}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })
}

// mailScope is the smallest Scope hfMailSend runs in.
func mailScope(ml mailSink, locale string) *Scope {
	return &Scope{
		plugin: &Installed{
			Row:      nil,
			Manifest: &Manifest{Manifest: wire.Manifest{Label: wire.Text{"en": "Signatures", "tr": "İmzalar"}}},
			logs:     &logRing{},
		},
		reg:    &Registry{mailer: ml},
		locale: locale,
	}
}

func sendMail(t *testing.T, s *Scope, req map[string]string) string {
	t.Helper()
	b, _ := json.Marshal(req)
	_, err := hfMailSend(context.Background(), s, b)
	require.NoError(t, err)
	ml := s.reg.mailer.(*fakeMailer)
	require.NotEmpty(t, ml.sent)
	return ml.sent[len(ml.sent)-1]
}

// ⚠ The footer follows the MAIL's language: the one the app states
// (MailSendIn), else the one the call runs in — never English under a
// Turkish invitation, which is what every app mail carried before.
func TestMailFooter_SpeaksTheMailsLanguage(t *testing.T) {
	withSpanishPack(t)
	base := map[string]string{"to": "ana@example.test", "subject": "Firma", "body": "Hola"}

	s := mailScope(&fakeMailer{}, "tr")
	assert.Contains(t, sendMail(t, s, base), "Bu e-postayı filex üzerindeki İmzalar uygulaması gönderdi.",
		"no lang stated: the language the call runs in, and the app's label in it")

	withLang := map[string]string{"to": "ana@example.test", "subject": "Firma", "body": "Hola", "lang": "es"}
	assert.Contains(t, sendMail(t, mailScope(&fakeMailer{}, "tr"), withLang), "Enviado por la aplicación Signatures de filex.",
		"the app's stated language wins over the call's")

	assert.Contains(t, sendMail(t, mailScope(&fakeMailer{}, ""), base), "Sent by the Signatures app on filex.",
		"a scheduled wake-up has no language: the instance default, then English")
}

func TestPermissionLabels_InThePacksLanguage(t *testing.T) {
	withSpanishPack(t)
	assert.Equal(t, "Lee el contenido de los archivos que elijas", PermFilesRead.Label("es"))
	assert.Equal(t, "Hace peticiones HTTP a api.example.com", Permission("http:api.example.com").Label("es"))
	// The engine by its product name (enginebin.DisplayName, feat/043-apps-polish),
	// not its permission id.
	assert.Equal(t, "Runs the server's conversion engine: FFmpeg", Permission("engines:ffmpeg").Label("es"),
		"a translation that lost {engine} is refused: the administrator must still read WHICH engine")
	assert.Equal(t, "Sends filex notifications to users", PermNotifySend.Label("es"), "untranslated: English")
	assert.Equal(t, "Seçtiğiniz dosyaların içeriğini okur", PermFilesRead.Label("tr"))
	assert.Equal(t, "Reads the contents of the files you pick", PermFilesRead.Label("de"), "no pack: English, not Turkish")

	rows := PermissionRows(&Manifest{Perms: []Permission{PermUsersLookup}}, "es")
	require.Len(t, rows, 1)
	assert.Equal(t, "Busca usuarios por nombre o correo", rows[0].Label)
}

// UIString and UILocaleCodes are the registry's side of srvtext.Packs:
// running apps only, first by name, blanks are no value.
func TestRegistry_ServesPackStringsToTheServerCatalogue(t *testing.T) {
	var _ srvtext.Packs = (*Registry)(nil)
	var nilReg *Registry
	v, ok := nilReg.UIString("es", "server.mail.greeting")
	assert.False(t, ok)
	assert.Empty(t, v)
	assert.Empty(t, nilReg.UILocaleCodes())
}

// An installed, running language pack is what srvtext reads — switched off,
// its language leaves the mails and pages at once.
func TestRegistry_AnInstalledPackSpeaksForTheServer(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	ctx := context.Background()
	st, _, err := reg.Install(ctx, &InstallInput{Manifest: packManifest(t, "lang-es", map[string]map[string]string{"es": {
		"server.mail.greeting": "Hola:", "server.mail.label.file": "Archivo: {name}", "nav.files": "Archivos",
	}}), Granted: []string{}})
	require.NoError(t, err)
	srvtext.SetPacks(reg)
	t.Cleanup(func() { srvtext.SetPacks(nil) })

	assert.Equal(t, []string{"es"}, reg.UILocaleCodes())
	assert.Equal(t, "es", srvtext.Pick("es-AR"))
	assert.Equal(t, "Hola:", srvtext.Text("es", "server.mail.greeting", nil))
	assert.Equal(t, "Archivo: x.pdf", srvtext.Text("es", "server.mail.label.file", srvtext.Vars{"name": "x.pdf"}))

	_, err = reg.SetEnabled(ctx, st.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "Hello,", srvtext.Text("es", "server.mail.greeting", nil), "a stopped pack speaks for nobody")
	assert.Equal(t, "en", srvtext.Pick("es"))
}

// Coverage counts the server's keys like any other, and a plural form for a
// category English does not have is neither coverage nor "unknown".
func TestLanguageRows_PluralFormsAreNotUnknown(t *testing.T) {
	reg, _ := newPackRegistry(t, nil)
	m, err := ParseManifest(packManifest(t, "lang-ar", map[string]map[string]string{"ar": {
		"toast.restored": "x", "toast.restored_few": "y", "toast.restored_two": "z",
		"server.mail.greeting": "مرحبًا،", "nope_few": "w",
	}}))
	require.NoError(t, err)
	reg.SetCatalogue([]string{"toast.restored", "toast.restored_one", "server.mail.greeting", "other.key"})
	rows := reg.LanguageRows(m)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].Translated)
	assert.Equal(t, 1, rows[0].Unknown, "only nope_few: its base is not a key filex has")
	assert.Equal(t, 50, rows[0].Percent)
}

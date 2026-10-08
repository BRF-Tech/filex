package handlers_test

// Text the SERVER writes, in a language pack's language.
//
// Language packs translated every screen, and the server still wrote its
// e-mails and no-JS pages in English or Turkish. Measured 2026-09-22 with a
// Spanish pack, a Spanish account and the explorer sending `locale: "es"`:
// the share-link mail arrived as "informe.txt dosyası sizinle paylaşıldı"
// (Turkish — the `else` of an en/tr pair), the drop notice to the owner too,
// and a Spanish browser's drop page said "Send files". These tests read what
// the recipient actually gets: the bytes the SMTP server receives, the HTML a
// visitor is served.

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// spanish is a PARTIAL pack, on purpose: the keys it lacks must arrive in
// English, one line at a time — never in Turkish, never as a raw key.
var spanish = srvtext.StaticPacks{"es": {
	"server.mail.greeting":                  "Hola:",
	"server.mail.share.subject_file":        "{name} se ha compartido contigo",
	"server.mail.label.file":                "Archivo: {name}",
	"server.mail.label.size":                "Tamaño: {size}",
	"server.mail.share.download":            "Descárgalo aquí:",
	"server.mail.label.pin":                 "PIN (código de acceso): {pin}",
	"server.mail.valid_days":                "Este enlace es válido durante {count} días.",
	"server.mail.valid_days_one":            "Este enlace es válido durante un día.",
	"server.mail.drop_received.subject":     "Nueva subida de archivos",
	"server.mail.drop_received.body":        "{who} dejó {count} archivos en «{folder}» ({submission}).",
	"server.mail.drop_received.body_one":    "{who} dejó un archivo en «{folder}» ({submission}).",
	"server.notify.word.someone":            "Alguien",
	"server.notify.drop.received.title":     "{count} archivos recibidos",
	"server.notify.drop.received.title_one": "{count} archivo recibido",
	"server.public.drop_title":              "Enviar archivos",
	"server.public.drop_heading":            "Enviar archivos",
	"server.public.drop_done_sub":           "{count} archivos enviados.",
	"server.public.drop_done_sub_one":       "Un archivo enviado.",
	"server.public.err_drop_notfound_title": "No encontrado",
	// ⚠ A pack cannot put markup on a page strangers open: the sentence is
	// escaped before the brand link goes in.
	"server.public.footer": "<img src=x onerror=alert(1)> Compartido con {filex}",
}}

func withSpanish(t *testing.T) {
	t.Helper()
	srvtext.SetPacks(spanish)
	t.Cleanup(func() { srvtext.SetPacks(nil) })
}

// mailParts splits a received message into its headers (decoded) and body.
func mailParts(t *testing.T, raw string) (map[string]string, string) {
	t.Helper()
	head, body, ok := strings.Cut(raw, "\n\n")
	require.True(t, ok, "a message has headers and a body:\n%s", raw)
	h := map[string]string{}
	dec := new(mime.WordDecoder)
	for _, line := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		d, err := dec.DecodeHeader(v)
		require.NoError(t, err)
		h[k] = d
	}
	return h, body
}

// shareMailOn mails a link to informe.txt in locale and answers the mail's
// headers, its body and the link's address.
func shareMailOn(t *testing.T, locale string) (map[string]string, string, string) {
	t.Helper()
	f := newTenantFixture(t)
	sink := newSMTPSink(t)
	g := handlers.NewGrants(f.store, nil)
	g.AttachInvite(share.NewService(f.store), sink.mailer(t, f.store), tenantOperatorURL)
	g.AttachTenants(f.tenants(false))

	// The mail is written from the LINK (share_mail.go): a 4-byte file, a PIN,
	// seven days left.
	node := fileNode(t, f.store, f.storage, f.root, "docs/informe.txt", "hola")
	week := time.Now().Add(7 * 24 * time.Hour)
	owner := f.owner.ID
	sh, err := share.NewService(f.store).Create(context.Background(), share.CreateOpts{
		NodeID: node.ID, PIN: "4321", ExpiresAt: &week, CreatedBy: &owner,
	})
	require.NoError(t, err)
	raw, _ := json.Marshal(map[string]any{"share": sh.Token, "email": "amigo@example.test", "locale": locale})
	req := httptest.NewRequest(http.MethodPost, "/api/files/permissions/share-mail", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.ShareMail(rec, f.asOwner(req))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	h, body := mailParts(t, sink.only(t))
	return h, body, tenantOperatorURL + "/s/" + sh.Token
}

func TestServerText_ShareMailArrivesInThePacksLanguage(t *testing.T) {
	withSpanish(t)
	h, body, link := shareMailOn(t, "es")
	assert.Equal(t, "informe.txt se ha compartido contigo", h["Subject"])
	assert.Equal(t, "es", h["Content-Language"])
	for _, want := range []string{
		"Hola:", "Archivo: informe.txt", "Tamaño: 4 B", "Descárgalo aquí:\n" + link,
		"Este enlace es válido durante 7 días.",
		// Not in the pack: those lines in English.
		"A file has been shared with you:",
		"This link is protected with a PIN.",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, strings.ReplaceAll(body, link, ""), "4321", "a share mail never carries the PIN (share_mail.go)")
	assert.NotContains(t, body, "paylaşıldı", "a third language is never the Turkish branch")
}

func TestServerText_ALanguageNobodySpeaksIsEnglish(t *testing.T) {
	withSpanish(t)
	h, body, _ := shareMailOn(t, "de")
	assert.Equal(t, "informe.txt has been shared with you", h["Subject"])
	assert.Contains(t, body, "This link is valid for 7 days.")
	assert.NotContains(t, body, "paylaşıldı")
	assert.Equal(t, "en", h["Content-Language"])
}

// The owner of a drop folder hears about uploads in THEIR account language —
// the anonymous uploader has none.
func TestServerText_DropOwnerHearsInTheirPacksLanguage(t *testing.T) {
	withSpanish(t)
	f := newTenantFixture(t)
	require.NoError(t, f.store.UpdateUserLocale(context.Background(), f.owner.ID, "es", "UTC"))
	sink := newSMTPSink(t)
	folder := mkdirNode(t, f.store, f.storage, f.root, "buzon")
	mh := handlers.NewManager(f.store, f.resolver)
	svc := share.NewService(f.store)
	dh := handlers.NewDrop(f.store, mh, svc, nil, sink.mailer(t, f.store), tenantOperatorURL)
	dh.AttachTenants(f.tenants(false))
	r := chi.NewRouter()
	r.Post("/d/{token}", dh.Upload)
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, CreatedBy: &f.owner.ID})
	require.NoError(t, err)

	rec := dropUploadOn(t, r, "/d/"+sh.Token, "files.example.test")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	h, body := mailParts(t, sink.only(t))
	// The notification's own words (notify say.go), even with notifications
	// switched off: one code path says what a drop says, in the pack's
	// language where it has the phrase - its singular for one - and English
	// where it has not.
	assert.Equal(t, "1 archivo recibido", h["Subject"])
	assert.Equal(t, "es", h["Content-Language"])
	assert.Contains(t, body, "Alguien → buzon", "the pack's word for an unnamed uploader")
	assert.NotContains(t, body, "Nueva subida de archivos", "the mail's own second wording is gone")
}

func TestServerText_PublicPagesSpeakThePacksLanguage(t *testing.T) {
	withSpanish(t)
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "entrada", false)

	rec := getPage(t, r, "/d/"+tok, "es-ES,es;q=0.9,en;q=0.5")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assertNoBlankChrome(t, body)
	assert.Contains(t, body, `<html lang="es" dir="ltr">`)
	assert.Contains(t, body, "<title>Enviar archivos - entrada</title>")
	assert.Contains(t, body, "Drop your files here", "a key the pack lacks is English")
	// The script's strings, with the plural forms the page's language has.
	assert.Contains(t, body, `"drop_done_sub":"{count} archivos enviados."`)
	assert.Contains(t, body, `"drop_done_sub_one":"Un archivo enviado."`)
	// The footer: the pack's sentence, escaped, around filex's own link.
	assert.Contains(t, body, `&lt;img src=x onerror=alert(1)&gt; Compartido con <a href="https://filex.sh"`)
	assert.NotContains(t, body, "<img src=x")

	// ?lang= still wins, and a language nobody speaks is English.
	assert.Contains(t, getPage(t, r, "/d/"+tok+"?lang=tr", "es").Body.String(), "Dosya gönder")
	de := getPage(t, r, "/d/"+tok, "de-DE,de").Body.String()
	assert.Contains(t, de, `<html lang="en" dir="ltr">`)
	assert.Contains(t, de, "Send files")

	missing := getPage(t, r, "/d/nope", "es")
	assert.Contains(t, missing.Body.String(), "No encontrado")
}

// An invitee whose account the admin creates starts in the language PICKED
// for it in the form (#191: never the composer's screen language; nothing
// picked is the server's) — any language the server speaks. It was forced to tr/en, so a
// Spanish admin's invitee got a Turkish account and a Turkish welcome mail.
func TestServerText_ANewAccountStartsInTheComposersLanguage(t *testing.T) {
	withSpanish(t)
	got := inviteOn(t, false, tenantHost, map[string]any{
		"email": "nuevo@example.test", "level": model.GrantViewer, "create_user": true,
		"role": model.RoleViewer, "locale": "es",
	})
	h, body := mailParts(t, got.mail)
	assert.Equal(t, "es", h["Content-Language"])
	assert.Equal(t, "Your filex account was created", h["Subject"], "the pack has no word for it: English, never Turkish")
	assert.Contains(t, body, "Hola:")
	assert.NotContains(t, body, "Merhaba")
}

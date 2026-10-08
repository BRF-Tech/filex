package notify_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// What a notification SAYS (say.go) - composed on the server, in Go, in the
// reader's language, and nowhere else (the maintainers' decision,
// 2026-10-08). Before it the sentence was composed by every reader's screen
// (packages/core lib/notificationText.ts) and a second time in Go for a push,
// and the two disagreed on the same row. These tests pin the words the
// screens used to compose - the cases web/tests/lib/notificationText.test.ts,
// notificationDigest.test.ts, notificationPack.test.ts, officeNotices.test.ts
// and notificationOperational.test.ts held - now that the server says them.
//
// ⚠ Red on the code before it: notify.Say, notify.SayEvent, notify.SayRows,
// notify.ToastBody, model.NotificationE2E and the `server.notify.<event>.*`
// keys do not exist.

func sayRow(event, title, body string, meta map[string]any) *model.Notification {
	raw, _ := json.Marshal(meta)
	return &model.Notification{Event: event, Title: title, Body: body, MetaJSON: raw}
}

// sayTB is a row's title and body in lang.
func sayTB(lang string, n *model.Notification) [2]string {
	s := notify.Say(lang, n)
	return [2]string{s.Title, s.Body}
}

func withNotifyPacks(t *testing.T, p srvtext.StaticPacks) {
	t.Helper()
	srvtext.SetPacks(p)
	t.Cleanup(func() { srvtext.SetPacks(nil) })
}

var (
	fsi = string(rune(0x2068))
	pdi = string(rune(0x2069))
)

func uploadedRow() *model.Notification {
	return &model.Notification{
		Event: "file.uploaded",
		Title: "file.uploaded", // what Send puts there when the emitter set none
		Body:  "Belgeler/rapor.pdf",
		MetaJSON: json.RawMessage(`{"origin":"manager",` +
			`"node":{"storage_id":3,"path":"Belgeler/rapor.pdf","name":"rapor.pdf","size":1234},` +
			`"actor":{"id":7,"email":"alice@example.com"},` +
			`"target":{"kind":"file","storage":"team","path":"Belgeler/rapor.pdf"}}`),
		Target: &model.NotificationTarget{Kind: model.TargetFile, Storage: "team", Path: "Belgeler/rapor.pdf"},
	}
}

func TestSay_OneRowEachReaderInTheirOwnLanguage(t *testing.T) {
	assert.Equal(t, [2]string{"New file: rapor.pdf", "Belgeler/rapor.pdf"}, sayTB("en", uploadedRow()))
	assert.Equal(t, [2]string{"Yeni dosya: rapor.pdf", "Belgeler/rapor.pdf"}, sayTB("tr", uploadedRow()))
	// A language nobody offers is the instance default's, then English.
	assert.Equal(t, "New file: rapor.pdf", notify.Say("xx", uploadedRow()).Title)
	for _, lang := range []string{"en", "tr"} {
		s := notify.Say(lang, uploadedRow())
		assert.NotContains(t, s.Title, "file.uploaded", "the wire id on somebody's screen")
		assert.NotContains(t, s.Title+s.Body, "alice@example.com", "an e-mail address is an identity, not a name")
		assert.Equal(t, []string{s.Body}, s.Lines)
		assert.Nil(t, s.E2E)
	}
}

func TestSay_WithoutAPhraseTheEmittersWordsThenTheID(t *testing.T) {
	// An alarm a later version adds carries a real, if English, sentence.
	assert.Equal(t, [2]string{"Disk almost full", "/data at 96%"},
		sayTB("tr", sayRow("disk_full", "Disk almost full", "/data at 96%", nil)))
	// The event id only when there is nothing else.
	assert.Equal(t, "future.thing", notify.Say("en", sayRow("future.thing", "future.thing", "", nil)).Title)
}

func TestSay_TheShapesTheEmittersProduce(t *testing.T) {
	moved := sayRow("file.moved", "file.moved", "Yeni/rapor.pdf", map[string]any{
		"origin": "manager", "from": "Eski/rapor.pdf", "to": "Yeni/rapor.pdf",
		"node": map[string]any{"path": "Yeni/rapor.pdf", "name": "rapor.pdf"},
	})
	assert.Equal(t, [2]string{"Taşındı: rapor.pdf", "Eski/rapor.pdf → Yeni/rapor.pdf"}, sayTB("tr", moved))

	assert.Equal(t, [2]string{"Archive created: backup.7z", "Archives/backup.7z"}, sayTB("en",
		sayRow("archive.created", "archive.created", "Archives/backup.7z", map[string]any{
			"path": "Archives/backup.7z", "node": map[string]any{"path": "Archives/backup.7z", "name": "backup.7z"},
		})))
	assert.Equal(t, [2]string{"Extraction completed", "1 file extracted to Restored"}, sayTB("en",
		sayRow("archive.extracted", "archive.extracted", "", map[string]any{"path": "Restored", "count": 1})))

	drop := func(uploader string, n int) *model.Notification {
		return sayRow("drop.received", "New file upload", "ignored", map[string]any{
			"folder": "Gelen", "count": n, "uploader": uploader, "node": map[string]any{"path": "Gelen", "name": "Gelen"},
		})
	}
	assert.Equal(t, [2]string{"3 dosya geldi", "Birisi → Gelen"}, sayTB("tr", drop("", 3)))
	assert.Equal(t, [2]string{"2 files received", "Ayşe → Gelen"}, sayTB("en", drop("Ayşe", 2)))
	assert.Equal(t, "1 file received", notify.Say("en", drop("Ayşe", 1)).Title, "English inflects after a number")
	assert.Equal(t, "1 dosya geldi", notify.Say("tr", drop("Ayşe", 1)).Title, "Turkish does not")

	// "{signature} - {path}" with no signature: the separator goes with it.
	infected := sayRow("file.infected", "Infected file detected", "Belgeler/rapor.pdf: ", map[string]any{
		"quarantined": false, "node": map[string]any{"path": "Belgeler/rapor.pdf", "name": "rapor.pdf"},
	})
	assert.Equal(t, "Belgeler/rapor.pdf", notify.Say("en", infected).Body)

	// share.created with no node stands on its own.
	assert.Equal(t, [2]string{"Paylaşım bağlantısı oluşturuldu", ""},
		sayTB("tr", sayRow("share.created", "share.created", "", map[string]any{"kind": "download", "has_pin": false})))

	// A multi-line comment is one line: a toast clips, a newline is a gap.
	comment := sayRow("comment.added", "comment.added", "Belgeler/rapor.pdf", map[string]any{
		"comment_id": 12, "body": "ilk satır\n\nikinci satır", "node": map[string]any{"path": "Belgeler/rapor.pdf", "name": "rapor.pdf"},
	})
	assert.Equal(t, [2]string{"rapor.pdf için yeni yorum", "ilk satır ikinci satır"}, sayTB("tr", comment))

	// A name recovered from the path; the path from the target, then the body.
	assert.Equal(t, "File deleted: c.txt", notify.Say("en", sayRow("file.deleted", "", "A/B/c.txt",
		map[string]any{"node": map[string]any{"path": "A/B/c.txt"}})).Title)
	assert.Equal(t, "M/z", notify.Say("en", sayRow("file.trashed", "", "", map[string]any{
		"target": map[string]any{"kind": "dir", "storage": "s", "path": "M/z"}})).Body)
	assert.Equal(t, "B/y", notify.Say("en", sayRow("file.updated", "", "B/y", nil)).Body)
}

func TestSay_AnOpenWithSaveNamesTheDocumentNeverItsWorkingCopy(t *testing.T) {
	row := sayRow("file.updated", "", "/.filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx", map[string]any{
		"node": map[string]any{"storage_id": 1, "path": "", "name": "Bütçe Özeti.xlsx"}, "open_with": true, "origin": "onlyoffice",
	})
	assert.Equal(t, [2]string{"File changed: Bütçe Özeti.xlsx", "Opened with the filex desktop app"}, sayTB("en", row))
	assert.Equal(t, [2]string{"Dosya değişti: Bütçe Özeti.xlsx", "filex masaüstü uygulamasıyla açıldı"}, sayTB("tr", row))
	infected := sayRow("file.infected", "", "", map[string]any{
		"node": map[string]any{"path": "", "name": "Plan.docx"}, "open_with": true, "signature": "Eicar-Test-Signature",
	})
	assert.Equal(t, [2]string{"Plan.docx dosyasında virüs bulundu", "Eicar-Test-Signature - filex masaüstü uygulamasıyla açıldı"}, sayTB("tr", infected))
}

func TestSay_AnAppsNoticeInItsOwnWordsAndTheReadersLanguage(t *testing.T) {
	meta := map[string]any{
		"plugin": "sign", "plugin_label_en": "e-Signature", "plugin_label_tr": "e-İmza", "plugin_label_de": "E-Signatur",
		"title_en": "Signature requested", "title_tr": "İmza istendi", "title_de": "Unterschrift angefordert",
		"body_en": "“contract.pdf” is waiting for your signature", "body_tr": "“sözleşme.pdf” imzanızı bekliyor",
		"body_de": "„vertrag.pdf“ wartet auf Ihre Unterschrift",
	}
	row := sayRow("plugin.notice", "Signature requested", "", meta)
	assert.Equal(t, [2]string{"İmza istendi", "e-İmza: “sözleşme.pdf” imzanızı bekliyor"}, sayTB("tr", row))
	// A pack's language the app wrote: its words, its label.
	withNotifyPacks(t, srvtext.StaticPacks{"de": {"server.notify.word.someone": "Jemand"}, "fr": {"server.notify.word.someone": "Quelqu’un"}})
	assert.Equal(t, [2]string{"Unterschrift angefordert", "E-Signatur: „vertrag.pdf“ wartet auf Ihre Unterschrift"}, sayTB("de", row))
	// One it did not write: English.
	assert.Equal(t, [2]string{"Signature requested", "e-Signature: “contract.pdf” is waiting for your signature"}, sayTB("fr", row))
	// A row from before the labels: no "sign:" prefix, never the install id.
	old := sayRow("plugin.notice", "Signature requested", "", map[string]any{"plugin": "sign", "title_tr": "İmza istendi", "body_tr": "“sözleşme.pdf” imzanızı bekliyor"})
	assert.Equal(t, "“sözleşme.pdf” imzanızı bekliyor", notify.Say("tr", old).Body)
}

func TestSay_AnAppUpdateByItsLabelNeverTheServersEnglish(t *testing.T) {
	row := func(event string, extra map[string]any) *model.Notification {
		meta := map[string]any{"plugin": "lang-es", "plugin_label_en": "Spanish language pack", "plugin_label_tr": "İspanyolca dil paketi", "version": "0.1.4", "from": "0.1.3"}
		for k, v := range extra {
			meta[k] = v
		}
		return sayRow(event, "lang-es updated to 0.1.4", "", meta)
	}
	assert.Equal(t, [2]string{"Spanish language pack is now 0.1.4", "An administrator approved it; it was 0.1.3."}, sayTB("en", row("app_updated", nil)))
	assert.Equal(t, [2]string{"İspanyolca dil paketi artık 0.1.4 sürümünde", "Bir yönetici onayladı; önceki sürüm 0.1.3."}, sayTB("tr", row("app_updated", nil)))
	assert.Equal(t, [2]string{"İspanyolca dil paketi 0.1.4 onayınızı bekliyor", "mail:send, http:freetsa.org"},
		sayTB("tr", row("app_update_needs_approval", map[string]any{"added": "mail:send, http:freetsa.org"})))
	assert.Equal(t, [2]string{"myfs depo eklentisinin 1.1.0 sürümü yayında", "Eklentiler → Depolama altında sizi bekliyor."},
		sayTB("tr", sayRow("plugin_update_available", "myfs 1.1.0 is available", "", map[string]any{"plugin": "myfs", "plugin_label_en": "myfs", "version": "1.1.0"})))
	assert.Equal(t, [2]string{"Eklenti isteği: İspanyolca dil paketi 0.1.4", "work-agent: Ekip İspanyolca arayüz istiyor"},
		sayTB("tr", row("plugin_requested", map[string]any{"requester": "work-agent", "reason": "Ekip İspanyolca arayüz istiyor"})))
	assert.Equal(t, [2]string{"Eklenti isteği: myfs 1.0.0", "Birisi: arşiv"},
		sayTB("tr", sayRow("plugin_requested", "x asks to install myfs 1.0.0", "", map[string]any{"plugin": "myfs", "plugin_label_en": "myfs", "version": "1.0.0", "reason": "arşiv"})))
	failed := notify.Say("tr", row("app_update_failed", map[string]any{"error": "describe: version mismatch"}))
	assert.Equal(t, "İspanyolca dil paketi 0.1.4 kurulamadı", failed.Title)
	assert.Equal(t, "Önceki sürüm 0.1.3 çalışmaya devam ediyor.", failed.Body)
}

func TestSay_TheOperatorAlarmsInTheReadersLanguage(t *testing.T) {
	for _, c := range []struct {
		row    *model.Notification
		tr, en string
	}{
		{sayRow("update_available", "filex 0.42.0 available", "policy notify", map[string]any{"version": "0.42.0", "current": "0.41.1"}),
			"filex 0.42.0 yayınlandı", "filex 0.42.0 is available"},
		{sayRow("replica_fail", "Replica put failed", "Path docs/a.pdf - timeout", map[string]any{"path": "docs/a.pdf", "op": "put", "error": "timeout"}),
			"Kopyada put başarısız: a.pdf", "Replica put failed: a.pdf"},
		{sayRow("primary_read_fail", "Primary read failed", "", map[string]any{"path": "docs/a.pdf", "primary_error": "EOF"}),
			"a.pdf kopyadan sunuldu", "a.pdf was served from the replica"},
		{sayRow("replica_reconcile_done", "Replica reconciliation queued", "", map[string]any{"queued": 1}),
			"1 kopya yeniden denemesi kuyruğa alındı", "1 replica retry queued"},
		{sayRow("replica_status_report", "Replica status report", "", map[string]any{"failed_count": 3, "repaired_count": 2}),
			"Kopya raporu: 3 çözülmemiş, 2 onarıldı", "Replica report: 3 unresolved, 2 repaired"},
		{sayRow("auth_provider_down", "Sign-in provider pam could not start", "pamtester missing. It is left out.", map[string]any{"provider": "pam", "reason": "pamtester missing"}),
			"Oturum açma sağlayıcısı başlatılamadı: pam", "Sign-in provider could not start: pam"},
		{sayRow("admin_test", "filex test notification", "If you're reading this...", map[string]any{"source": "admin_test"}),
			"filex deneme bildirimi", "filex test notification"},
		{sayRow("ldap_legacy_account_elsewhere", "Directory account alex is in another tenant", "", map[string]any{
			"title_en": "Directory account alex is in another tenant", "title_tr": "alex dizin hesabı başka bir kiracıda",
			"body_en": "x", "body_tr": "y"}),
			"alex dizin hesabı başka bir kiracıda", "Directory account alex is in another tenant"},
	} {
		assert.Equal(t, c.tr, notify.Say("tr", c.row).Title, c.row.Event)
		assert.Equal(t, c.en, notify.Say("en", c.row).Title, c.row.Event)
	}
	down := notify.Say("tr", sayRow("auth_provider_down", "", "", map[string]any{"provider": "pam", "reason": "pamtester missing"}))
	assert.Equal(t, "Diğer oturum açma yolları çalışmaya devam ediyor. pamtester missing", down.Body)
}

func TestSay_ASingleEncryptedFileAndAFolderAreTwoWordings(t *testing.T) {
	file := func(event string) *model.Notification {
		return sayRow(event, event, "", map[string]any{"kind": "file", "file": "Arşiv/Rapor 2027.pdf.fxe", "storage": "docs",
			"node": map[string]any{"path": "Arşiv/Rapor 2027.pdf.fxe", "name": "Rapor 2027.pdf.fxe"}})
	}
	folder := func(event string) *model.Notification {
		return sayRow(event, event, "", map[string]any{"folder": "Kasa", "storage": "docs", "node": map[string]any{"path": "Kasa", "name": "Kasa"}})
	}
	assert.Equal(t, [2]string{"Encrypted file opened with the escrow key", "Arşiv/Rapor 2027.pdf.fxe"}, sayTB("en", file("e2e.escrow_used")))
	assert.Equal(t, "Şifreli dosya emanet anahtarıyla açıldı", notify.Say("tr", file("e2e.escrow_used")).Title)
	assert.Equal(t, "Şifreli dosyanın parolası değişti", notify.Say("tr", file("e2e.password_changed")).Title)
	assert.Equal(t, "Encrypted folder opened with the escrow key", notify.Say("en", folder("e2e.escrow_used")).Title)
	assert.Equal(t, [2]string{"Şifreli klasörün parolası değişti", "Kasa"}, sayTB("tr", folder("e2e.password_changed")))

	// A pack's folder sentence is never said about a file; its _file key is.
	withNotifyPacks(t, srvtext.StaticPacks{"de": {"server.notify.e2e.escrow_used.title": "Verschlüsselter Ordner mit dem Treuhandschlüssel geöffnet"}})
	assert.Equal(t, "Encrypted file opened with the escrow key", notify.Say("de", file("e2e.escrow_used")).Title)
	withNotifyPacks(t, srvtext.StaticPacks{"de": {
		"server.notify.e2e.escrow_used.title":      "Verschlüsselter Ordner mit dem Treuhandschlüssel geöffnet",
		"server.notify.e2e.escrow_used.title_file": "Verschlüsselte Datei mit dem Treuhandschlüssel geöffnet",
	}})
	assert.Equal(t, "Verschlüsselte Datei mit dem Treuhandschlüssel geöffnet", notify.Say("de", file("e2e.escrow_used")).Title)
	assert.Equal(t, "Verschlüsselter Ordner mit dem Treuhandschlüssel geöffnet", notify.Say("de", folder("e2e.escrow_used")).Title)
}

func TestSay_AnEncryptionRequestAndBothOfItsAnswers(t *testing.T) {
	node := map[string]any{"path": "Muhasebe/Maaşlar", "name": "Maaşlar"}
	created := sayRow("e2e.request_created", "e2e.request_created", "", map[string]any{"node": node, "requester": "Ayşe", "reason": "Bordro dosyaları", "request_id": 7, "target_kind": "folder"})
	decided := func(decision, note string) *model.Notification {
		return sayRow("e2e.request_decided", "e2e.request_decided", "", map[string]any{"node": node, "decision": decision, "note": note, "request_id": 7, "target_kind": "file"})
	}
	assert.Equal(t, [2]string{"Encryption request: Maaşlar", "Ayşe: Bordro dosyaları"}, sayTB("en", created))
	assert.Equal(t, [2]string{"Şifreleme isteği: Maaşlar", "Ayşe: Bordro dosyaları"}, sayTB("tr", created))
	assert.Equal(t, [2]string{"Encryption request approved: Maaşlar", ""}, sayTB("en", decided("approved", "")))
	assert.Equal(t, [2]string{"Şifreleme isteği reddedildi: Maaşlar", "Not for payroll"}, sayTB("tr", decided("rejected", "Not   for\npayroll")))
	assert.NotContains(t, notify.Say("tr", decided("rejected", "")).Title, "rejected", "the wire word")

	// A request at the TOP of a storage: no path; the storage names it, never the body.
	top := map[string]any{"storage_id": 1, "path": "", "name": "Dosyalar"}
	atTop := sayRow("e2e.request_created", "Encryption request: Dosyalar", "Ayşe: Rapor müşteri verisi içeriyor", map[string]any{
		"node": top, "reason": "Rapor müşteri verisi içeriyor", "requester": "Ayşe", "storage": "Dosyalar", "target_kind": "file",
		"target": map[string]any{"kind": "dir", "storage": "Dosyalar"}})
	assert.Equal(t, [2]string{"Encryption request: Dosyalar", "Ayşe: Rapor müşteri verisi içeriyor"}, sayTB("en", atTop))

	// A pack's _rejected key says its NO; without one, never its yes for a no.
	withNotifyPacks(t, srvtext.StaticPacks{"de": {
		"server.notify.e2e.request_decided.title":          "Verschlüsselung genehmigt: {folder}",
		"server.notify.e2e.request_decided.title_rejected": "Verschlüsselung abgelehnt: {folder}",
	}})
	assert.Equal(t, "Verschlüsselung abgelehnt: Maaşlar", notify.Say("de", decided("rejected", "")).Title)
	assert.Equal(t, "Verschlüsselung genehmigt: Maaşlar", notify.Say("de", decided("approved", "")).Title)
	withNotifyPacks(t, srvtext.StaticPacks{"de": {"server.notify.e2e.request_decided.title": "Verschlüsselung genehmigt: {folder}"}})
	assert.Equal(t, "Encryption request rejected: Maaşlar", notify.Say("de", decided("rejected", "")).Title)
}

// wiring:e2 names - inside an encrypted folder whose names are encrypted the
// server has only ciphertext. The sentence is still the server's; the name
// stands in it as the lock word, and the row says where, so a browser with
// the folder unlocked swaps the real name in (and composes nothing).
func TestSay_AnEncryptedNameIsTheLockWordAndSaysWhereItStands(t *testing.T) {
	const S = "cnCYVvOrMoH0uQKjxUUeYr9h7KREShFsI3Y"
	const DIR = "CiGQLHEtou8eDKjFPpDgRJ_zFRPkQ-q1qiCsooM.U1LSU6ksO0TVNYdn3zrSjQ"
	inside := func(event, p string, extra map[string]any) *model.Notification {
		meta := map[string]any{
			"node":     map[string]any{"storage_id": 3, "path": p, "name": p[strings.LastIndex(p, "/")+1:]},
			"target":   map[string]any{"kind": "file", "storage": "team", "path": p},
			"e2e_root": "team://Kasa",
		}
		for k, v := range extra {
			meta[k] = v
		}
		return sayRow(event, event, p, meta)
	}

	s := notify.Say("en", inside("file.uploaded", "Kasa/"+DIR+"/"+S, nil))
	assert.Equal(t, "New file: 🔒 Encrypted item", s.Title)
	assert.Equal(t, "Kasa/…/🔒 Encrypted item", s.Body)
	assert.NotContains(t, s.Title+s.Body+strings.Join(s.Lines, ""), S)
	assert.NotContains(t, s.Title+s.Body, DIR)
	assert.Equal(t, [2]string{"Yeni dosya: 🔒 Şifreli öğe", "Kasa/…/🔒 Şifreli öğe"}, sayTB("tr", inside("file.uploaded", "Kasa/"+DIR+"/"+S, nil)))

	// Where the words stand: the title's name, the body's path - for a reader
	// that can name them. Filled with Locked, it is exactly Title and Body.
	require.NotNil(t, s.E2E)
	require.Len(t, s.E2E.Names, 2)
	assert.Equal(t, "New file: 🔒 Encrypted item", fillE2E(s.E2E.Title, s.E2E.Names, nil))
	assert.Equal(t, "Kasa/…/🔒 Encrypted item", fillE2E(s.E2E.Body, s.E2E.Names, nil))
	named := map[string]string{"name": "Rapor.docx", "path": "Kasa/Sözleşmeler/Rapor.docx"}
	assert.Equal(t, "New file: Rapor.docx", fillE2E(s.E2E.Title, s.E2E.Names, named))
	assert.Equal(t, "Kasa/Sözleşmeler/Rapor.docx", fillE2E(s.E2E.Body, s.E2E.Names, named))
	for _, n := range s.E2E.Names {
		assert.Equal(t, "team://Kasa/"+DIR+"/"+S, n.Wire)
		assert.Equal(t, "team://Kasa", n.Root)
	}

	// One level down, no "…".
	assert.Equal(t, "Kasa/🔒 Encrypted item", notify.Say("en", inside("file.uploaded", "Kasa/"+S, nil)).Body)
	// A folder with readable names (level 1) is left as it is.
	plain := notify.Say("en", inside("file.uploaded", "Kasa/rapor.pdf", nil))
	assert.Equal(t, [2]string{"New file: rapor.pdf", "Kasa/rapor.pdf"}, [2]string{plain.Title, plain.Body})
	assert.Nil(t, plain.E2E)
	// Both ends of a move.
	mv := notify.Say("en", inside("file.moved", "Kasa/"+DIR+"/"+S, map[string]any{"from": "Kasa/" + S, "to": "Kasa/" + DIR + "/" + S}))
	assert.Equal(t, "Moved: 🔒 Encrypted item", mv.Title)
	assert.Equal(t, "Kasa/🔒 Encrypted item → Kasa/…/🔒 Encrypted item", mv.Body)
	// The lock word is the catalogue's, so a pack translates it.
	withNotifyPacks(t, srvtext.StaticPacks{"de": {"server.notify.word.locked": "🔒 Verschlüsseltes Element"}})
	assert.Equal(t, "New file: 🔒 Verschlüsseltes Element", notify.Say("de", inside("file.uploaded", "Kasa/"+S, nil)).Title)
}

// fillE2E is what a browser does with model.NotificationE2E: the words as they
// are, each name from its key (by part) or its Locked text.
func fillE2E(parts []model.NotificationTextPart, names []model.NotificationE2EName, known map[string]string) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Name == nil {
			b.WriteString(p.Text)
			continue
		}
		n := names[*p.Name]
		if v, ok := known[n.Part]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(n.Locked)
		}
	}
	return b.String()
}

func digestRow(meta map[string]any) *model.Notification {
	return sayRow("notification.digest", "x new notifications", "server words", meta)
}

var twoFolders = map[string]any{
	"count": 34,
	"groups": []any{
		map[string]any{"storage": "ekip", "path": "Rapor", "name": "Rapor", "count": 30, "parts": []any{map[string]any{"key": "file_uploaded", "count": 30}}},
		map[string]any{"storage": "ekip", "path": "Fotograflar", "name": "Fotoğraflar", "count": 4, "parts": []any{
			map[string]any{"key": "file_trashed", "count": 3}, map[string]any{"key": "comment_added", "count": 1}}},
	},
}

func TestSay_ADigestFolderByFolder(t *testing.T) {
	tr := notify.Say("tr", digestRow(twoFolders))
	assert.Equal(t, "34 bildirim", tr.Title)
	assert.Equal(t, "Rapor: 30 dosya eklendi; Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum", tr.Body)
	assert.Equal(t, []string{"Rapor: 30 dosya eklendi", "Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum"}, tr.Lines, "an email says them one under the other")
	assert.Equal(t, [2]string{"34 notifications", "Rapor: 30 files added; Fotoğraflar: 3 files moved to the trash, 1 comment"}, sayTB("en", digestRow(twoFolders)))

	one := map[string]any{"count": 2, "groups": []any{map[string]any{"storage": "ekip", "path": "Rapor", "name": "Rapor", "count": 2,
		"parts": []any{map[string]any{"key": "file_uploaded", "count": 1}, map[string]any{"key": "comment_added", "count": 1}}}}}
	assert.Equal(t, [2]string{"2 notifications", "Rapor: 1 file added, 1 comment"}, sayTB("en", digestRow(one)))

	// A digest of one says what that one row says.
	single := map[string]any{"count": 1,
		"groups": []any{map[string]any{"storage": "ekip", "path": "Rapor", "name": "Rapor", "count": 1, "parts": []any{map[string]any{"key": "file_uploaded", "count": 1}}}},
		"item":   map[string]any{"event": "file.uploaded", "title": "file.uploaded", "body": "Rapor/a.pdf", "meta": map[string]any{"node": map[string]any{"path": "Rapor/a.pdf", "name": "a.pdf"}}},
	}
	assert.Equal(t, [2]string{"New file: a.pdf", "Rapor/a.pdf"}, sayTB("en", digestRow(single)))
	assert.Equal(t, "Yeni dosya: a.pdf", notify.Say("tr", digestRow(single)).Title)

	// The folders it does not name, the rows that name no folder, a part a
	// later version adds.
	many := map[string]any{"count": 40, "groups": twoFolders["groups"], "more_folders": 3, "other_parts": []any{map[string]any{"key": "admin", "count": 2}}}
	assert.Equal(t, "Rapor: 30 files added; Fotoğraflar: 3 files moved to the trash, 1 comment; 3 more folders; 2 administrator alerts", notify.Say("en", digestRow(many)).Body)
	assert.Equal(t, "Rapor: 30 dosya eklendi; Fotoğraflar: 3 dosya çöp kutusuna taşındı, 1 yorum; 3 klasör daha; 2 yönetici uyarısı", notify.Say("tr", digestRow(many)).Body)
	unknown := map[string]any{"count": 2, "groups": []any{map[string]any{"storage": "ekip", "path": "Rapor", "name": "Rapor", "count": 2, "parts": []any{map[string]any{"key": "some_later_kind", "count": 2}}}}}
	assert.Equal(t, "Rapor: 2 other notifications", notify.Say("en", digestRow(unknown)).Body)

	// A folder whose name is encrypted: the lock word, and where it stands.
	cipher := "Kasa/AbCdEfGhIjKlMnOpQrStUvWx"
	locked := map[string]any{"count": 2, "groups": []any{map[string]any{"storage": "ekip", "path": cipher, "name": "", "encrypted": true, "e2e_root": "ekip://Kasa", "count": 2,
		"parts": []any{map[string]any{"key": "file_uploaded", "count": 2}}}}}
	ls := notify.Say("en", digestRow(locked))
	assert.Equal(t, "🔒 Encrypted item: 2 files added", ls.Body)
	assert.NotContains(t, ls.Body, "AbCdEf")
	require.NotNil(t, ls.E2E)
	require.Len(t, ls.E2E.Names, 1)
	assert.Equal(t, "ekip://"+cipher, ls.E2E.Names[0].Wire)
	assert.Equal(t, "Projeler: 2 files added", fillE2E(ls.E2E.Body, ls.E2E.Names, map[string]string{"name": "Projeler"}))

	// A pack translates the title and the parts, per phrase.
	withNotifyPacks(t, srvtext.StaticPacks{"es": {
		"server.notify.notification.digest.title":     "{count} notificaciones",
		"server.notify.digest.file_uploaded":          "{count} archivos añadidos",
		"server.notify.digest.file_uploaded_one":      "{count} archivo añadido",
		"server.notify.word.someone":                  "Alguien",
		"server.notify.notification.digest.title_one": "{count} notificación",
	}})
	es := notify.Say("es", digestRow(twoFolders))
	assert.Equal(t, "34 notificaciones", es.Title)
	assert.True(t, strings.HasPrefix(es.Body, "Rapor: 30 archivos añadidos; "), es.Body)
	assert.Contains(t, es.Body, "3 files moved to the trash", "a phrase the pack lacks is English")
}

// A language pack installed on the server (an app: its ui_locales reach
// srvtext) says every notification in its language, its plural forms by the
// CLDR category of the count - and right to left with every value isolated.
func TestSay_ALanguagePacksWordsFormsAndDirection(t *testing.T) {
	withNotifyPacks(t, srvtext.StaticPacks{
		"de": {
			"server.notify.file.uploaded.title":     "Neue Datei: {name}",
			"server.notify.drop.received.title":     "{count} Dateien empfangen",
			"server.notify.drop.received.title_one": "{count} Datei empfangen",
		},
		"ar": {
			"server.notify.file.uploaded.title":     "ملف جديد: {name}",
			"server.notify.drop.received.title":     "{count} ملف وصل",
			"server.notify.drop.received.title_two": "ملفان وصلا",
		},
	})
	assert.Equal(t, [2]string{"Neue Datei: rapor.pdf", "Belgeler/rapor.pdf"}, sayTB("de", uploadedRow()))
	drop := func(n int) *model.Notification {
		return sayRow("drop.received", "", "", map[string]any{"folder": "Gelen", "count": n, "uploader": "Ayşe"})
	}
	assert.Equal(t, "1 Datei empfangen", notify.Say("de", drop(1)).Title)
	assert.Equal(t, "3 Dateien empfangen", notify.Say("de", drop(3)).Title)
	// A phrase the pack lacks is the English one, in the pack's language's
	// sentence otherwise: never the Turkish.
	assert.Equal(t, "Ayşe → Gelen", notify.Say("de", drop(3)).Body)

	// Arabic: the dual has its own form (it may leave {count} out), and every
	// value placed in a sentence is isolated so a path's slash keeps its side.
	assert.Equal(t, "ملفان وصلا", notify.Say("ar", drop(2)).Title)
	ar := notify.Say("ar", uploadedRow())
	assert.Equal(t, "ملف جديد: "+fsi+"rapor.pdf"+pdi, ar.Title)
	assert.Equal(t, fsi+"Belgeler/rapor.pdf"+pdi, ar.Body)
	// Left to right: not one mark.
	for _, lang := range []string{"en", "tr", "de"} {
		s := notify.Say(lang, uploadedRow())
		assert.NotContains(t, s.Title+s.Body, fsi, lang)
		assert.NotContains(t, s.Title+s.Body, pdi, lang)
	}
}

func TestSay_AnEventSaysWhatItsRowWillSay(t *testing.T) {
	ev := notify.Event{
		Event: notify.EventFileUploaded, Severity: notify.SeverityInfo, Body: "Rapor/a.pdf",
		Node:   &notify.NodeRef{StorageID: 3, Path: "Rapor/a.pdf", Name: "a.pdf"},
		Target: notify.FileTarget("Rapor/a.pdf"),
	}
	s := notify.SayEvent("tr", ev)
	assert.Equal(t, [2]string{"Yeni dosya: a.pdf", "Rapor/a.pdf"}, [2]string{s.Title, s.Body})

	rows := []*model.Notification{uploadedRow(), nil}
	notify.SayRows("tr", rows)
	assert.Equal(t, "Yeni dosya: rapor.pdf", rows[0].Title)
	assert.Equal(t, "Belgeler/rapor.pdf", rows[0].Body)

	assert.Equal(t, "New file: a.pdf - Rapor/a.pdf", notify.ToastBody(notify.Said{Title: "New file: a.pdf", Body: "Rapor/a.pdf"}))
	assert.Equal(t, "Share link created", notify.ToastBody(notify.Said{Title: "Share link created"}))
}

// Every event filex emits has a sentence in both languages it ships, and none
// says its wire id, a placeholder or a dangling separator - whatever facts a
// realistic row carries. event.go is read as source, so a new constant is in
// this test the moment it is declared.
func TestSay_EveryEventIsPhrasedAndNoneSaysItsWireID(t *testing.T) {
	// Declared and kept, never emitted (event.go says so): nothing to phrase.
	notEmitted := map[string]bool{
		"replica_fail_spike": true, "quota_near_full": true, "quota_full": true, "queue_stuck": true,
		"auth_fail_spike": true, "disk_full": true, "update_applied": true,
	}
	events := declaredEvents(t)
	require.Greater(t, len(events), 25, "parsed no EventType constants")
	en, tr := srvtext.Builtin("en"), srvtext.Builtin("tr")
	placeholder := regexp.MustCompile(`\{\w+\}`)
	dangling := regexp.MustCompile(`(^\s*[-:]|[-:]\s*$)`)
	for _, ev := range events {
		if notEmitted[ev] {
			continue
		}
		key := "server.notify." + ev + ".title"
		assert.NotEmpty(t, en[key], "%s has no English sentence (%s)", ev, key)
		assert.NotEmpty(t, tr[key], "%s has no Turkish sentence (%s)", ev, key)
		if ev == string(notify.EventNotificationDigest) {
			continue
		}
		row := sayRow(ev, ev, "Belgeler/rapor.pdf", map[string]any{
			"origin": "manager", "node": map[string]any{"path": "Belgeler/rapor.pdf", "name": "rapor.pdf", "size": 12},
			"reason": "driver refused the write", "signature": "Eicar-Test-Signature", "from": "Belgeler/eski.pdf",
			"to": "Belgeler/rapor.pdf", "folder": "Gelen", "count": 3, "uploader": "", "body": "looks good to me",
			"storage": "team", "version": "1.2.0", "current": "1.1.0", "plugin_label_en": "Signatures", "plugin_label_tr": "İmzalar",
			"title_en": "An app says hello", "title_tr": "Bir uygulama merhaba diyor", "provider": "pam", "op": "put", "error": "timeout",
			"failed_count": 1, "repaired_count": 2, "queued": 3, "requester": "Ayşe",
		})
		for _, lang := range []string{"en", "tr"} {
			s := notify.Say(lang, row)
			assert.NotEmpty(t, strings.TrimSpace(s.Title), "%s %s: empty title", ev, lang)
			assert.NotContains(t, s.Title, ev, "%s %s: the title is the wire id", ev, lang)
			assert.False(t, placeholder.MatchString(s.Title+" "+s.Body), "%s %s: a placeholder survived: %q / %q", ev, lang, s.Title, s.Body)
			assert.False(t, dangling.MatchString(s.Title), "%s %s: dangling punctuation: %q", ev, lang, s.Title)
		}
	}
}

// declaredEvents is every `Event… EventType = "…"` constant of event.go.
func declaredEvents(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", "event.go"), nil, 0)
	require.NoError(t, err)
	var out []string
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, sp := range g.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "EventType" {
				continue
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}

// filex 0.51: an ONLYOFFICE save the server phrased per language when it wrote
// the row (`meta.title_<lang>` from srvtext `server.onlyoffice.*`), on the
// plain file events. Its words win - a pack's phrase for the plain event does
// not stand in for them - and a plain row of the same event is said as ever.
func TestSay_WordsTheServerPhrasedWhenItWroteTheRowWin(t *testing.T) {
	savedBeside := sayRow("file.uploaded", "file.uploaded", "/Belgeler/rapor.docx", map[string]any{
		"origin": "onlyoffice", "saved_beside": "/Belgeler/rapor.doc",
		"node":     map[string]any{"storage_id": 3, "path": "/Belgeler/rapor.docx", "name": "rapor.docx"},
		"title_en": "Your edit was saved as rapor.docx", "title_tr": "Düzenlemeniz rapor.docx olarak kaydedildi",
		"body_en": "ONLYOFFICE saved rapor.doc as DOCX. rapor.doc did not change.",
		"body_tr": "ONLYOFFICE rapor.doc dosyasını DOCX biçiminde kaydetti. rapor.doc değişmedi.",
	})
	assert.Equal(t, [2]string{"Düzenlemeniz rapor.docx olarak kaydedildi", "ONLYOFFICE rapor.doc dosyasını DOCX biçiminde kaydetti. rapor.doc değişmedi."}, sayTB("tr", savedBeside))
	assert.Equal(t, "Your edit was saved as rapor.docx", notify.Say("en", savedBeside).Title)
	refused := sayRow("file.upload_failed", "Your edit to list.csv was not saved", "/list.csv", map[string]any{
		"origin": "onlyoffice", "node": map[string]any{"path": "/list.csv", "name": "list.csv"},
		"reason":   "ONLYOFFICE saved it as PDF.",
		"title_en": "Your edit to list.csv was not saved", "title_tr": "list.csv dosyasındaki düzenlemeniz kaydedilmedi",
		"body_en": "ONLYOFFICE saved it as PDF. list.csv did not change.", "body_tr": "ONLYOFFICE dosyayı PDF biçiminde kaydetti. list.csv değişmedi.",
	})
	assert.Equal(t, [2]string{"list.csv dosyasındaki düzenlemeniz kaydedilmedi", "ONLYOFFICE dosyayı PDF biçiminde kaydetti. list.csv değişmedi."}, sayTB("tr", refused))

	withNotifyPacks(t, srvtext.StaticPacks{"de": {"server.notify.file.uploaded.title": "Neue Datei: {name}"}})
	assert.Equal(t, "Your edit was saved as rapor.docx", notify.Say("de", savedBeside).Title)

	plain := sayRow("file.uploaded", "file.uploaded", "/a/b.txt", map[string]any{"node": map[string]any{"path": "/a/b.txt", "name": "b.txt"}})
	assert.Equal(t, "Yeni dosya: b.txt", notify.Say("tr", plain).Title)
	assert.Equal(t, "Neue Datei: b.txt", notify.Say("de", plain).Title)
	failed := sayRow("file.upload_failed", "Upload failed", "/a/b.txt", map[string]any{"node": map[string]any{"path": "/a/b.txt", "name": "b.txt"}, "reason": "disk full"})
	assert.Equal(t, [2]string{"Upload failed: b.txt", "disk full"}, sayTB("en", failed))
}

// No emitter writes a notification's sentence. An event carries FACTS (its
// meta, node, share, target); the words are said by the server from them, in
// each reader's language (say.go). A `Title:` or `Body:` built out of an
// English string literal - "Replica copy failed", "filex " + v + " available",
// fmt.Sprintf("%s asks to install %s", ...) - is a second sentence that nobody
// reads any more and that drifts from the catalogue's: the coordinator's audit
// of 2026-10-08 (A16) found fifteen of them. A variable is allowed (the app's
// own words in plugin.notice, a sentence srvtext wrote per language), and so is
// an event outside the catalogue (one a later version adds) - but every event
// event.go declares is phrased (TestSay_EveryEventIsPhrasedAndNoneSaysItsWireID).
func TestSay_NoEmitterWritesItsOwnSentence(t *testing.T) {
	root := backendRootFromTest(t)
	fset := token.NewFileSet()
	var offenders []string
	seen := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isNotifyEventLit(lit) {
				return true
			}
			catalogued := false
			var words []*ast.KeyValueExpr
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Event":
					name := ""
					switch v := kv.Value.(type) {
					case *ast.SelectorExpr:
						name = v.Sel.Name
					case *ast.Ident:
						name = v.Name
					}
					// An EventType constant of event.go: Event<Name>.
					if strings.HasPrefix(name, "Event") {
						catalogued = true
					}
				case "Title", "Body":
					words = append(words, kv)
				}
			}
			if !catalogued {
				return true
			}
			seen++
			for _, kv := range words {
				if hasWordLiteral(kv.Value) {
					rel, _ := filepath.Rel(root, p)
					offenders = append(offenders, fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel), fset.Position(kv.Pos()).Line, kv.Key.(*ast.Ident).Name))
				}
			}
			return true
		})
		// …and a sentence set on the event after the literal (`ev.Title =
		// "…"`, the escrow handler's file wording did that), in a function
		// that builds one.
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			builds := false
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				if lit, ok := m.(*ast.CompositeLit); ok && isNotifyEventLit(lit) {
					builds = true
				}
				return true
			})
			if !builds {
				return true
			}
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				as, ok := m.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || (sel.Sel.Name != "Title" && sel.Sel.Name != "Body") || i >= len(as.Rhs) {
						continue
					}
					if hasWordLiteral(as.Rhs[i]) {
						rel, _ := filepath.Rel(root, p)
						offenders = append(offenders, fmt.Sprintf("%s:%d %s =", filepath.ToSlash(rel), fset.Position(as.Pos()).Line, sel.Sel.Name))
					}
				}
				return true
			})
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, seen, 20, "found no emitters at all (a scan of nothing passes everything)")
	require.Empty(t, offenders, "these emitters write a sentence of their own; put the facts in Meta and the words in server.notify.<event>.* (say.go)")
}

// hasWordLiteral reports whether an expression is built from a string literal
// that carries words: a run of letters and a space ("Replica copy failed",
// "filex " + v). A key ("server.x.y"), a language tag ("en") or "" is not.
func hasWordLiteral(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return true
		}
		if strings.Contains(s, " ") && regexp.MustCompile(`[A-Za-z]{3,}`).MatchString(s) {
			found = true
		}
		return true
	})
	return found
}

// Whether a click goes somewhere is the server's verdict too (Opens): the API
// says it on every row and a push carries it, so the bell, the page's pop-up
// and a phone agree.
func TestSay_WhetherARowOpensIsTheServersOneRule(t *testing.T) {
	for _, c := range []struct {
		target *model.NotificationTarget
		want   bool
	}{
		{nil, false},
		{&model.NotificationTarget{Kind: model.TargetNone}, false},
		{&model.NotificationTarget{Kind: model.TargetFile, Storage: "team", Path: "a/b.pdf"}, true},
		{&model.NotificationTarget{Kind: model.TargetFile, Path: "a/b.pdf"}, false},
		{&model.NotificationTarget{Kind: model.TargetDir, Storage: "team"}, true},
		{&model.NotificationTarget{Kind: model.TargetTrash}, true},
		{&model.NotificationTarget{Kind: model.TargetShare, ID: "tok"}, true},
		{&model.NotificationTarget{Kind: model.TargetShare}, false},
		{&model.NotificationTarget{Kind: model.TargetApp, Open: &model.NotificationOpen{Plugin: "sign", View: "home"}}, true},
		{&model.NotificationTarget{Kind: model.TargetApp, Open: &model.NotificationOpen{Plugin: "sign"}}, false},
	} {
		assert.Equal(t, c.want, notify.Opens(c.target), "%+v", c.target)
	}
	rows := []*model.Notification{uploadedRow(), {Event: "update_available"}}
	notify.SayRows("en", rows)
	require.NotNil(t, rows[0].Opens)
	require.NotNil(t, rows[1].Opens)
	assert.True(t, *rows[0].Opens)
	assert.False(t, *rows[1].Opens)
}

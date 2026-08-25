package handlers

/* Public pages speak ONE language per visitor.

They grew one at a time and each picked its own: the PIN gate was English, the
page behind it Turkish, and the "PIN accepted" screen managed both at once — an
English <title> over a Turkish heading. Somebody opening a share link therefore
changed language by entering a PIN.

There is no session and no user here — a share link is opened by strangers — so
the language comes from the request itself: an explicit ?lang= (useful for
testing and for sending a link to somebody whose browser is set to neither),
then Accept-Language, then the server's own default. Resolved ONCE per request
and handed to the template, so every string on a page comes from the same
table. */

import (
	"html/template"
	"net/http"
	"strings"
)

// publicLocales are the languages the public pages ship. Anything else falls
// back to English rather than rendering half-translated.
var publicLocales = map[string]bool{"tr": true, "en": true}

// publicLocale picks the language for one public request.
func publicLocale(r *http.Request, serverDefault string) string {
	if r != nil {
		if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); publicLocales[v] {
			return v
		}
		// Accept-Language: take the first tag we actually ship. Deliberately
		// ignores q-values — browsers list their preference first, and a
		// weighted parse buys nothing for a two-language table.
		for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
			tag := strings.ToLower(strings.TrimSpace(part))
			if i := strings.IndexByte(tag, ';'); i >= 0 {
				tag = strings.TrimSpace(tag[:i])
			}
			if i := strings.IndexByte(tag, '-'); i >= 0 {
				tag = tag[:i]
			}
			if publicLocales[tag] {
				return tag
			}
		}
	}
	if d := strings.ToLower(strings.TrimSpace(serverDefault)); publicLocales[d] {
		return d
	}
	return "en"
}

// publicText is every user-visible string on the public pages, per language.
// Keys are grouped by the page that uses them.
var publicText = map[string]map[string]string{
	"tr": {
		// PIN gate
		"pin_title":    "PIN girin",
		"pin_heading":  "Bu paylaşım PIN korumalı",
		"pin_sub":      "Erişmek için PIN'i girin.",
		"pin_submit":   "Kilidi aç",
		"pin_aria":     "PIN",
		"pin_wrong":    "PIN yanlış — tekrar deneyin.",
		"pin_required": "PIN gerekli.",

		// PIN accepted → download starts
		"unlocked_title":   "PIN doğru",
		"unlocked_heading": "PIN doğru",
		"unlocked_sub":     "İndirme birazdan başlayacak…",

		// ZIP being prepared
		"zip_title":   "Dosya hazırlanıyor…",
		"zip_heading": "Dosya hazırlanıyor…",
		"zip_sub":     "%s — klasör ZIP arşivi olarak paketleniyor.",
		"zip_hint_a":  "İndirme hazır olduğunda otomatik başlayacak. Başlamazsa ",
		"zip_hint_b":  "buraya tıklayın",
		"zip_hint_c":  ".",

		// Prepared local copy for a big file on slow storage (filecache)
		"cache_title":   "İndirme hazırlanıyor…",
		"cache_heading": "İndirme hazırlanıyor…",
		"cache_sub":     "%s — bu dosya yavaş bir depoda; yerel bir kopyası hazırlanıyor.",
		"cache_hint":    "Kopya hazır olduğunda indirme kendiliğinden başlar. Bu sayfayı açık bırakabilirsiniz.",

		// Shared folder listing
		"folder_title_suffix": "paylaşılan klasör",
		"folder_counts":       "%d klasör · %d dosya",
		"folder_up":           "← Üst klasör",
		"folder_zip":          "Tümünü indir (ZIP)",
		"folder_empty":        "Bu klasör boş.",

		// Errors
		"err_expired_title":     "Bağlantının süresi doldu",
		"err_expired_body":      "Bu bağlantının süresi dolmuş ya da indirme limitine ulaşılmış.",
		"err_notfound_title":    "Bulunamadı",
		"err_notfound_body":     "Bu bağlantı geçersiz, süresi dolmuş ya da kaldırılmış.",
		"err_folder_title":      "Klasör bulunamadı",
		"err_folder_body":       "Bu klasör paylaşımda yok.",
		"err_unavailable_title": "Dosya kullanılamıyor",
		"err_unavailable_body":  "Paylaşılan öğe okunamadı. Bağlantıyı paylaşan kişiye bildirin.",
		"err_limit_title":       "İndirme limiti doldu",
		"err_limit_body":        "Bu bağlantı izin verilen sayıda indirildi.",

		// File-drop (public upload link) — the inverse of a download share.
		"drop_title":        "Dosya gönder",
		"drop_heading":      "Dosya gönder",
		"drop_sub":          "Aşağıya dosyaları sürükleyin veya seçin. Yalnızca yükleyebilirsiniz; klasördeki dosyalar size görünmez.",
		"drop_zone_big":     "Dosyaları buraya bırakın",
		"drop_zone_hint":    "veya seçmek için tıklayın",
		"drop_zone_aria":    "Dosya seçin veya sürükleyip bırakın",
		"drop_name_label":   "Adınız (isteğe bağlı)",
		"drop_name_ph":      "Örn. Ahmet Yılmaz",
		"drop_note_label":   "Not (isteğe bağlı)",
		"drop_note_ph":      "Kısa bir mesaj ekleyebilirsiniz",
		"drop_send":         "Gönder",
		"drop_sending":      "Gönderiliyor…",
		"drop_remove_aria":  "Kaldır: %s",
		"drop_limit_files":  "En fazla %s dosya",
		"drop_limit_size":   "dosya başına %s MB",
		"drop_limit_ext":    "izinli türler: %s",
		"drop_done_heading": "Teşekkürler!",
		"drop_done_sub":     "%s dosya başarıyla gönderildi.",

		// Drop failures the visitor sees. `drop_err_storage` is the one that
		// matters most: the link is fine and the files are fine — the backing
		// storage is unreachable — so the message says so instead of leaving
		// somebody retrying a link they think is broken.
		"drop_err_too_many":  "En fazla %s dosya gönderebilirsiniz.",
		"drop_err_too_large": "%s çok büyük (en fazla %s MB).",
		"drop_err_ext":       "%s için izin verilmeyen dosya türü.",
		"drop_err_ext_any":   "İzin verilmeyen dosya türü.",
		"drop_err_large_any": "Bir dosya izin verilen boyuttan büyük (en fazla %s MB).",
		"drop_err_bad_pin":   "Yanlış PIN.",
		"drop_err_expired":   "Bağlantının süresi dolmuş.",
		"drop_err_rate":      "Çok fazla deneme — biraz sonra tekrar deneyin.",
		"drop_err_no_files":  "Dosya seçilmedi.",
		"drop_err_storage":   "Dosya deposuna şu an ulaşılamıyor, gönderiminiz kaydedilemedi. Biraz sonra tekrar deneyin — bağlantınız geçerli kalmaya devam ediyor.",
		"drop_err_quota":     "Bu bağlantının klasöründe yeterli yer kalmamış. Bağlantıyı paylaşan kişiye bildirin.",
		"drop_err_generic":   "Gönderilemedi, lütfen tekrar deneyin.",

		// Drop error pages (link resolution — before any upload)
		"err_drop_notfound_title": "Bulunamadı",
		"err_drop_notfound_body":  "Bu bağlantı mevcut değil veya kaldırılmış.",
		"err_drop_notdrop_title":  "Bulunamadı",
		"err_drop_notdrop_body":   "Bu bağlantı bir dosya yükleme bağlantısı değil.",
		"err_drop_expired_title":  "Süresi doldu",
		"err_drop_expired_body":   "Bu yükleme bağlantısının süresi dolmuş veya limiti dolmuş.",
	},
	"en": {
		"pin_title":    "Enter PIN",
		"pin_heading":  "This share is PIN-protected",
		"pin_sub":      "Enter the PIN to access it.",
		"pin_submit":   "Unlock",
		"pin_aria":     "PIN",
		"pin_wrong":    "Wrong PIN — try again.",
		"pin_required": "PIN required.",

		"unlocked_title":   "PIN accepted",
		"unlocked_heading": "PIN accepted",
		"unlocked_sub":     "Your download will start in a moment…",

		"zip_title":   "Preparing your download…",
		"zip_heading": "Preparing your download…",
		"zip_sub":     "%s — packing the folder into a ZIP archive.",
		"zip_hint_a":  "The download starts on its own when it is ready. If it does not, ",
		"zip_hint_b":  "click here",
		"zip_hint_c":  ".",

		// Prepared local copy for a big file on slow storage (filecache)
		"cache_title":   "Preparing your download…",
		"cache_heading": "Preparing your download…",
		"cache_sub":     "%s — this file lives on slow storage, so a local copy is being prepared.",
		"cache_hint":    "The download starts on its own once the copy is ready. You can leave this page open.",

		"folder_title_suffix": "shared folder",
		"folder_counts":       "%d folders · %d files",
		"folder_up":           "← Up one level",
		"folder_zip":          "Download all (ZIP)",
		"folder_empty":        "This folder is empty.",

		"err_expired_title":     "Link expired",
		"err_expired_body":      "This link has expired or reached its download limit.",
		"err_notfound_title":    "Not found",
		"err_notfound_body":     "This link is invalid, expired or has been removed.",
		"err_folder_title":      "Folder not found",
		"err_folder_body":       "This folder does not exist in the share.",
		"err_unavailable_title": "File unavailable",
		"err_unavailable_body":  "The shared item could not be read. Let whoever sent you the link know.",
		"err_limit_title":       "Download limit reached",
		"err_limit_body":        "This link has been downloaded the maximum number of times.",

		"drop_title":        "Send files",
		"drop_heading":      "Send files",
		"drop_sub":          "Drag files below or pick them. You can only upload; the folder's contents stay hidden from you.",
		"drop_zone_big":     "Drop your files here",
		"drop_zone_hint":    "or click to choose",
		"drop_zone_aria":    "Choose files or drag and drop them",
		"drop_name_label":   "Your name (optional)",
		"drop_name_ph":      "e.g. Alex Smith",
		"drop_note_label":   "Note (optional)",
		"drop_note_ph":      "You can add a short message",
		"drop_send":         "Send",
		"drop_sending":      "Sending…",
		"drop_remove_aria":  "Remove: %s",
		"drop_limit_files":  "Up to %s files",
		"drop_limit_size":   "%s MB per file",
		"drop_limit_ext":    "allowed types: %s",
		"drop_done_heading": "Thank you!",
		"drop_done_sub":     "%s file(s) sent successfully.",

		"drop_err_too_many":  "You can send at most %s files.",
		"drop_err_too_large": "%s is too big (max %s MB).",
		"drop_err_ext":       "%s has a file type that is not allowed.",
		"drop_err_ext_any":   "That file type is not allowed.",
		"drop_err_large_any": "One of the files is over the size limit (max %s MB).",
		"drop_err_bad_pin":   "Wrong PIN.",
		"drop_err_expired":   "This link has expired.",
		"drop_err_rate":      "Too many attempts — try again shortly.",
		"drop_err_no_files":  "No file selected.",
		"drop_err_storage":   "The file storage is unreachable right now, so your upload could not be saved. Try again shortly — your link stays valid.",
		"drop_err_quota":     "The folder behind this link is out of space. Let whoever sent you the link know.",
		"drop_err_generic":   "Could not send, please try again.",

		"err_drop_notfound_title": "Not found",
		"err_drop_notfound_body":  "This link does not exist or has been removed.",
		"err_drop_notdrop_title":  "Not found",
		"err_drop_notdrop_body":   "This link is not a file-upload link.",
		"err_drop_expired_title":  "Expired",
		"err_drop_expired_body":   "This upload link has expired or reached its limit.",
	},
}

// publicPageLang resolves everything a public page needs to render in ONE
// language: the tag for <html lang>, the string table, the branded chrome and
// the footer that matches the language.
//
// Share and Drop both go through here. Drop used to render its own pages with
// no table at all — the PIN gate on a drop link came out with an empty <title>,
// an empty heading and an unlabelled input, because the template asks for
// {{.T.pin_heading}} and nobody passed T (html/template renders a missing key
// as the empty string, and the Execute error was discarded). A public surface
// that renders its own strings is a surface that can silently lose them.
func publicPageLang(br *BrandingSource, r *http.Request, defaultLocale string) (string, map[string]string, publicChrome, template.HTML) {
	lang := publicLocale(r, defaultLocale)
	t := publicT(lang)
	c := publicChromeFor(br, r)
	if lang == "tr" {
		return lang, t, c, c.FooterTR
	}
	return lang, t, c, c.FooterEN
}

// publicT returns the string table for a language, always non-nil.
func publicT(lang string) map[string]string {
	if t, ok := publicText[lang]; ok {
		return t
	}
	return publicText["en"]
}

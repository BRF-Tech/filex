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
	},
}

// publicT returns the string table for a language, always non-nil.
func publicT(lang string) map[string]string {
	if t, ok := publicText[lang]; ok {
		return t
	}
	return publicText["en"]
}

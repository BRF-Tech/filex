package handlers

import (
	"fmt"
	"strings"
)

// Localized email bodies for the invite/share flow. Turkish is the default
// (the operator's primary language); any locale starting with "en" renders
// English. The recipient's own stored locale is preferred when they have an
// account; otherwise the composer's UI locale (passed from the frontend) is
// used.

func mailLangEN(locale string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "en")
}

// shareMailText builds the subject + body for a public share-link notice,
// including the PIN and validity window when present.
func shareMailText(locale, link, pin string, expiresDays int) (string, string) {
	if mailLangEN(locale) {
		var b strings.Builder
		b.WriteString("Hello,\n\nA file has been shared with you. Download it here:\n\n")
		b.WriteString(link + "\n")
		if pin != "" {
			b.WriteString("\nPIN (access code): " + pin + "\n")
		}
		if expiresDays > 0 {
			b.WriteString(fmt.Sprintf("\nThis link is valid for %d day(s).\n", expiresDays))
		} else {
			b.WriteString("\nThis link does not expire.\n")
		}
		return "A file has been shared with you", b.String()
	}
	var b strings.Builder
	b.WriteString("Merhaba,\n\nSizinle bir dosya paylaşıldı. İndirmek için:\n\n")
	b.WriteString(link + "\n")
	if pin != "" {
		b.WriteString("\nPIN (erişim kodu): " + pin + "\n")
	}
	if expiresDays > 0 {
		b.WriteString(fmt.Sprintf("\nBu bağlantı %d gün geçerlidir.\n", expiresDays))
	} else {
		b.WriteString("\nBu bağlantının süresi yoktur.\n")
	}
	return "Bir dosya sizinle paylaşıldı", b.String()
}

// itemGrantText builds the notice sent when an existing account is granted
// access to an item.
func itemGrantText(locale, item, exploreURL string) (string, string) {
	if mailLangEN(locale) {
		return "An item has been shared with you",
			"Hello,\n\nA folder/file has been shared with you on filex: " + item + "\n\n" + exploreURL
	}
	return "Bir öğe sizinle paylaşıldı",
		"Merhaba,\n\nfilex üzerinde bir klasör/dosya sizinle paylaşıldı: " + item + "\n\n" + exploreURL
}

// accountCreatedText builds the welcome notice for a freshly-created account.
func accountCreatedText(locale, loginURL, email, tempPw string) (string, string) {
	if mailLangEN(locale) {
		return "Your filex account was created",
			"Hello,\n\nA filex account was created for you.\n\nSign in: " + loginURL +
				"\nEmail: " + email + "\nTemporary password: " + tempPw +
				"\n\nPlease change your password after signing in."
	}
	return "filex hesabınız oluşturuldu",
		"Merhaba,\n\nSizin için bir filex hesabı oluşturuldu.\n\nGiriş: " + loginURL +
			"\nE-posta: " + email + "\nGeçici parola: " + tempPw +
			"\n\nLütfen giriş yaptıktan sonra parolanızı değiştirin."
}

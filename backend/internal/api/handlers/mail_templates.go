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

// humanSize renders a byte count as a short human-readable string (1.4 MB).
func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// shareMailText builds the subject + body for a public share-link notice.
// The subject/body name the item (file vs folder), show its size (files only),
// prefix the site name, and include the PIN + validity window when present.
func shareMailText(locale, siteName, name string, isDir bool, size int64, link, pin string, expiresDays int) (string, string) {
	if name == "" {
		if isDir {
			name = "/"
		} else {
			name = "dosya"
		}
	}
	if mailLangEN(locale) {
		kind := "file"
		if isDir {
			kind = "folder"
		}
		subject := name + " has been shared with you"
		var b strings.Builder
		b.WriteString("Hello,\n\n")
		if siteName != "" {
			b.WriteString("A " + kind + " has been shared with you via " + siteName + ":\n\n")
		} else {
			b.WriteString("A " + kind + " has been shared with you:\n\n")
		}
		if isDir {
			b.WriteString("Folder: " + name + "\n")
		} else {
			b.WriteString("File: " + name + "\n")
			if size > 0 {
				b.WriteString("Size: " + humanSize(size) + "\n")
			}
		}
		b.WriteString("\nDownload it here:\n" + link + "\n")
		if pin != "" {
			b.WriteString("\nPIN (access code): " + pin + "\n")
		}
		if expiresDays > 0 {
			b.WriteString(fmt.Sprintf("\nThis link is valid for %d day(s).\n", expiresDays))
		} else {
			b.WriteString("\nThis link does not expire.\n")
		}
		return subject, b.String()
	}
	kind := "dosya"
	kindPossessive := "dosyası"
	if isDir {
		kind = "klasör"
		kindPossessive = "klasörü"
	}
	subject := name + " " + kindPossessive + " sizinle paylaşıldı"
	var b strings.Builder
	b.WriteString("Merhaba,\n\n")
	if siteName != "" {
		b.WriteString(siteName + " üzerinden bir " + kind + " sizinle paylaşıldı:\n\n")
	} else {
		b.WriteString("Sizinle bir " + kind + " paylaşıldı:\n\n")
	}
	if isDir {
		b.WriteString("Klasör: " + name + "\n")
	} else {
		b.WriteString("Dosya: " + name + "\n")
		if size > 0 {
			b.WriteString("Boyut: " + humanSize(size) + "\n")
		}
	}
	b.WriteString("\nİndirmek için:\n" + link + "\n")
	if pin != "" {
		b.WriteString("\nPIN (erişim kodu): " + pin + "\n")
	}
	if expiresDays > 0 {
		b.WriteString(fmt.Sprintf("\nBu bağlantı %d gün geçerlidir.\n", expiresDays))
	} else {
		b.WriteString("\nBu bağlantının süresi yoktur.\n")
	}
	return subject, b.String()
}

// dropInviteMailText builds the subject + body for a public file-drop
// (upload) link invite — the inverse of shareMailText. It asks the recipient
// to UPLOAD files into a named folder, and spells out the configured limits
// (max files, size per file, allowed types, validity) so the sender's terms
// are clear up front. maxFiles/maxFileSizeMB <= 0 fall back to the drop
// defaults; an empty allowedExt means all types.
func dropInviteMailText(locale, siteName, folder, link, pin string, expiresDays, maxFiles, maxFileSizeMB int, allowedExt []string) (string, string) {
	if folder == "" {
		folder = "/"
	}
	if maxFiles <= 0 {
		maxFiles = dropDefaultMaxFiles
	}
	if maxFileSizeMB <= 0 {
		maxFileSizeMB = dropDefaultMaxFileSizeMB
	}
	if mailLangEN(locale) {
		subject := "You've been asked to add files to " + folder
		var b strings.Builder
		b.WriteString("Hello,\n\n")
		if siteName != "" {
			b.WriteString("You've been invited to upload files via " + siteName + ".\n\n")
		} else {
			b.WriteString("You've been invited to upload files.\n\n")
		}
		b.WriteString("Folder: " + folder + "\n")
		b.WriteString(fmt.Sprintf("Limit: up to %d file(s), %d MB per file.\n", maxFiles, maxFileSizeMB))
		if len(allowedExt) > 0 {
			b.WriteString("Allowed types: " + strings.Join(allowedExt, ", ") + "\n")
		} else {
			b.WriteString("Allowed types: all\n")
		}
		b.WriteString("\nUpload your files here:\n" + link + "\n")
		if pin != "" {
			b.WriteString("\nPIN (access code): " + pin + "\n")
		}
		if expiresDays > 0 {
			b.WriteString(fmt.Sprintf("\nThis link is valid for %d day(s).\n", expiresDays))
		} else {
			b.WriteString("\nThis link does not expire.\n")
		}
		return subject, b.String()
	}
	subject := folder + " adlı klasöre dosya eklemeniz istendi"
	var b strings.Builder
	b.WriteString("Merhaba,\n\n")
	if siteName != "" {
		b.WriteString(siteName + " üzerinden dosya yüklemeniz istendi.\n\n")
	} else {
		b.WriteString("Dosya yüklemeniz istendi.\n\n")
	}
	b.WriteString("Klasör: " + folder + "\n")
	b.WriteString(fmt.Sprintf("Sınır: en fazla %d dosya, dosya başına %d MB.\n", maxFiles, maxFileSizeMB))
	if len(allowedExt) > 0 {
		b.WriteString("İzinli türler: " + strings.Join(allowedExt, ", ") + "\n")
	} else {
		b.WriteString("İzinli türler: tüm türler\n")
	}
	b.WriteString("\nDosyalarınızı buradan yükleyebilirsiniz:\n" + link + "\n")
	if pin != "" {
		b.WriteString("\nPIN (erişim kodu): " + pin + "\n")
	}
	if expiresDays > 0 {
		b.WriteString(fmt.Sprintf("\nBu bağlantı %d gün geçerlidir.\n", expiresDays))
	} else {
		b.WriteString("\nBu bağlantının süresi yoktur.\n")
	}
	return subject, b.String()
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

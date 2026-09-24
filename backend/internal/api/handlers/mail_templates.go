package handlers

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The e-mails the invite/share/drop flows send, and the notice texts that go
// with them. Every sentence comes from the server catalogue
// (internal/srvtext, keys `server.mail.*`), so a language pack translates
// them the way it translates the screens.
//
// ⚠⚠ The `lang` each builder takes is the RECIPIENT's language as the calling
// flow decided it (grants.go, drop.go, settings.go — each says whose); the
// builders never guess one. An unknown tag reads as English: the catalogue
// falls back per key, so a pack that translated half a mail sends half of it
// in its language and the rest in English — never in Turkish, which is what
// the old `if en … else Turkish` pair sent every third language (measured
// 2026-09-22: a Spanish account's share mail arrived as "informe.txt dosyası
// sizinle paylaşıldı").
//
// Each line of a mail is its own key: a mail is assembled from the facts it
// has (a PIN or none, a size or none, an expiry or none), so a translator
// gets whole sentences and never a fragment glued to a number.

// mailLines joins a mail body: paragraphs separated by a blank line, a
// trailing newline. An empty paragraph is dropped, so an optional part that
// is absent (no PIN, no size) leaves no gap.
func mailLines(paras ...string) string {
	var out []string
	for _, p := range paras {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n") + "\n"
}

// validityLine is "valid for N days" / "does not expire".
func validityLine(lang string, days int) string {
	if days > 0 {
		return srvtext.Plural(lang, "server.mail.valid_days", days, nil)
	}
	return srvtext.Text(lang, "server.mail.no_expiry", nil)
}

// shareMailText builds the subject + body for a public share-link notice.
// The subject/body name the item (file vs folder), show its size (files only),
// prefix the site name, and include the PIN + validity window when present.
func shareMailText(lang, siteName, name string, isDir bool, size int64, link, pin string, expiresDays int) (string, string) {
	if name == "" {
		if isDir {
			name = "/"
		} else {
			// ⚠ ONE word for "a file with no name", shared with the bell
			// (`server.notify.word.unnamed`). It was written twice — a mail key
			// and a notification key — until v0.43.0, so a translator answered
			// the same question in two places.
			name = srvtext.Text(lang, "server.notify.word.unnamed", nil)
		}
	}
	// ⚠ Two keys per sentence, not one with a "{kind}" in it: Turkish (and
	// most languages) inflect the sentence around the noun — "x dosyası" /
	// "x klasörü" — so the noun cannot be a value slotted into it.
	kind := "file"
	if isDir {
		kind = "folder"
	}
	subject := srvtext.Text(lang, "server.mail.share.subject_"+kind, srvtext.Vars{"name": name})
	intro := srvtext.Text(lang, "server.mail.share.intro_"+kind, nil)
	if siteName != "" {
		intro = srvtext.Text(lang, "server.mail.share.intro_"+kind+"_site", srvtext.Vars{"site": siteName})
	}
	facts := srvtext.Text(lang, "server.mail.label."+kind, srvtext.Vars{"name": name})
	if !isDir && size > 0 {
		facts += "\n" + srvtext.Text(lang, "server.mail.label.size", srvtext.Vars{"size": srvtext.Bytes(lang, size)})
	}
	pinLine := ""
	if pin != "" {
		pinLine = srvtext.Text(lang, "server.mail.label.pin", srvtext.Vars{"pin": pin})
	}
	return subject, mailLines(
		srvtext.Text(lang, "server.mail.greeting", nil),
		intro,
		facts,
		srvtext.Text(lang, "server.mail.share.download", nil)+"\n"+link,
		pinLine,
		validityLine(lang, expiresDays),
	)
}

// dropInviteMailText builds the subject + body for a public file-drop
// (upload) link invite — the inverse of shareMailText. It asks the recipient
// to UPLOAD files into a named folder, and spells out the configured limits
// (max files, size per file, allowed types, validity) so the sender's terms
// are clear up front. maxFiles/maxFileSizeMB <= 0 fall back to the drop
// defaults; an empty allowedExt means all types.
func dropInviteMailText(lang, siteName, folder, link, pin string, expiresDays, maxFiles, maxFileSizeMB int, allowedExt []string) (string, string) {
	if folder == "" {
		folder = "/"
	}
	if maxFiles <= 0 {
		maxFiles = dropDefaultMaxFiles
	}
	if maxFileSizeMB <= 0 {
		maxFileSizeMB = dropDefaultMaxFileSizeMB
	}
	intro := srvtext.Text(lang, "server.mail.drop_invite.intro", nil)
	if siteName != "" {
		intro = srvtext.Text(lang, "server.mail.drop_invite.intro_site", srvtext.Vars{"site": siteName})
	}
	types := srvtext.Text(lang, "server.mail.drop_invite.types_all", nil)
	if len(allowedExt) > 0 {
		types = srvtext.Text(lang, "server.mail.drop_invite.types", srvtext.Vars{"types": strings.Join(allowedExt, ", ")})
	}
	facts := srvtext.Text(lang, "server.mail.label.folder", srvtext.Vars{"name": folder}) + "\n" +
		srvtext.Plural(lang, "server.mail.drop_invite.limit", maxFiles, srvtext.Vars{"mb": fmt.Sprint(maxFileSizeMB)}) + "\n" +
		types
	pinLine := ""
	if pin != "" {
		pinLine = srvtext.Text(lang, "server.mail.label.pin", srvtext.Vars{"pin": pin})
	}
	return srvtext.Text(lang, "server.mail.drop_invite.subject", srvtext.Vars{"folder": folder}), mailLines(
		srvtext.Text(lang, "server.mail.greeting", nil),
		intro,
		facts,
		srvtext.Text(lang, "server.mail.drop_invite.upload", nil)+"\n"+link,
		pinLine,
		validityLine(lang, expiresDays),
	)
}

// itemGrantText builds the notice sent when an existing account is granted
// access to an item.
func itemGrantText(lang, item, exploreURL string) (string, string) {
	return srvtext.Text(lang, "server.mail.grant.subject", nil), strings.TrimSuffix(mailLines(
		srvtext.Text(lang, "server.mail.greeting", nil),
		srvtext.Text(lang, "server.mail.grant.body", srvtext.Vars{"item": item}),
		exploreURL,
	), "\n")
}

// accountCreatedText builds the welcome notice for a freshly-created account.
func accountCreatedText(lang, loginURL, email, tempPw string) (string, string) {
	return srvtext.Text(lang, "server.mail.account.subject", nil), strings.TrimSuffix(mailLines(
		srvtext.Text(lang, "server.mail.greeting", nil),
		srvtext.Text(lang, "server.mail.account.intro", nil),
		srvtext.Text(lang, "server.mail.account.sign_in", srvtext.Vars{"url": loginURL})+"\n"+
			srvtext.Text(lang, "server.mail.account.email", srvtext.Vars{"email": email})+"\n"+
			srvtext.Text(lang, "server.mail.account.password", srvtext.Vars{"password": tempPw}),
		srvtext.Text(lang, "server.mail.account.change", nil),
	), "\n")
}

// ─────────────────── file-drop notices ───────────────────
//
// A drop's uploader is anonymous by design, so there is no uploader locale to
// read. Every one of these strings is read by the folder's OWNER — in the
// notification bell, in the webhook v2 payload and in the owner e-mail — so
// the owner's stored locale is the one that applies. See Drop.ownerLocale.

// dropUploaderFallback names an uploader who did not fill the optional name
// field. It appears inside dropNotifyText's title/body.
func dropUploaderFallback(lang string) string {
	// ⚠ ONE word for "somebody who did not say who they are", shared with the
	// bell (`server.notify.word.someone`); it was written twice until v0.43.0.
	return srvtext.Text(lang, "server.notify.word.someone", nil)
}

// dropNotifyText builds the title + body for the "files were dropped in your
// folder" notice. `who` is already resolved (a real name, or
// dropUploaderFallback); `sub` is the submission subfolder.
func dropNotifyText(lang, who, folder string, count int, sub string) (string, string) {
	return srvtext.Text(lang, "server.mail.drop_received.subject", nil),
		srvtext.Plural(lang, "server.mail.drop_received.body", count, srvtext.Vars{"who": who, "folder": folder, "submission": sub})
}

// dropNoteHeader prefixes the NOT.txt written beside a submission with the
// uploader's name. The file is opened by the owner, so it follows the owner's
// locale like the notification does.
func dropNoteHeader(lang, uploaderName string) string {
	return srvtext.Text(lang, "server.drop.note_from", srvtext.Vars{"name": uploaderName}) + "\n\n"
}

// smtpTestMailText is the body of the admin panel's "Test" send. It goes to
// whichever address the acting admin typed, so it follows that admin's locale.
func smtpTestMailText(lang string) (string, string) {
	return srvtext.Text(lang, "server.mail.smtp_test.subject", nil), srvtext.Text(lang, "server.mail.smtp_test.body", nil)
}

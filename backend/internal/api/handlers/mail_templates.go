package handlers

import (
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The e-mails the invite/share/drop flows send are templates
// (internal/templates: mail.share, mail.file_request, mail.grant,
// mail.account, mail.smtp_test - an administrator can edit each); the
// builders below are the data each one is given. Every sentence the default
// templates say comes from the server catalogue (`server.mail.*`), in the
// language the calling flow decided (grants.go, drop.go, settings.go - each
// says whose: the RECIPIENT's).
//
// ⚠ A name a mail shows is made visible first (leafname.Visible): a bidi
// control in it is shown, not obeyed - an RLO would reverse the subject line
// and the facts, `invoice` + U+202E + `fdp.exe` reading as `invoiceexe.pdf`
// (task #238). The template escapes it for HTML; the subject and the plain
// text carry it as text.

// mailName is a file or folder name as a mail shows it: visible, and the
// catalogue's words for a file with no name ("/" for a folder).
func mailName(lang, name string, isDir bool) string {
	if name == "" {
		if isDir {
			return "/"
		}
		// ⚠ ONE word for "a file with no name", shared with the bell
		// (`server.notify.word.unnamed`).
		return srvtext.Text(lang, "server.notify.word.unnamed", nil)
	}
	return leafname.Visible(name)
}

// shareMailData is mail.share's data: a public link to a file or a folder.
// pin is the PIN itself, only from the flow that made the link a moment ago
// (Grants.Invite); pinWithheld says a PIN guards the link and the sender
// gives it separately (share-mail never mails one).
func shareMailData(lang, name string, isDir bool, size int64, link, pin string, pinWithheld bool, expiresDays int) map[string]any {
	kind := "file"
	if isDir {
		kind, size = "folder", 0
	}
	sizeText := ""
	if size > 0 {
		sizeText = srvtext.Bytes(lang, size)
	}
	return map[string]any{
		"item": map[string]any{"name": mailName(lang, name, isDir), "kind": kind, "size": size, "size_text": sizeText},
		"link": map[string]any{"url": link, "pin": pin, "pin_withheld": pin == "" && pinWithheld, "days_left": expiresDays},
	}
}

// fileRequestMailData is mail.file_request's data: a file request (drop)
// link, with the limits it was made with. maxFiles/maxFileSizeMB <= 0 are the
// drop defaults; an empty allowedExt is every type.
func fileRequestMailData(folder, link, pin string, pinWithheld bool, expiresDays, maxFiles, maxFileSizeMB int, allowedExt []string) map[string]any {
	if folder == "" {
		folder = "/"
	}
	if maxFiles <= 0 {
		maxFiles = dropDefaultMaxFiles
	}
	if maxFileSizeMB <= 0 {
		maxFileSizeMB = dropDefaultMaxFileSizeMB
	}
	types := make([]any, 0, len(allowedExt))
	for _, e := range allowedExt {
		types = append(types, e)
	}
	return map[string]any{
		"folder": map[string]any{"name": leafname.Visible(folder)},
		"link":   map[string]any{"url": link, "pin": pin, "pin_withheld": pin == "" && pinWithheld, "days_left": expiresDays},
		"limits": map[string]any{"max_files": maxFiles, "max_file_mb": maxFileSizeMB, "types": types},
	}
}

// grantMailData is mail.grant's data: an existing account was given access
// to an item (storage://path), and where it opens it.
func grantMailData(item, exploreURL string) map[string]any {
	return map[string]any{
		"item": map[string]any{"path": leafname.Visible(item)},
		"link": map[string]any{"url": exploreURL},
	}
}

// accountMailData is mail.account's data: a new account, where it signs in,
// and its first password.
func accountMailData(loginURL, email, tempPw string) map[string]any {
	return map[string]any{
		"account": map[string]any{"email": email, "password": tempPw},
		"link":    map[string]any{"url": loginURL},
	}
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
		srvtext.Plural(lang, "server.mail.drop_received.body", count, srvtext.Vars{"who": who, "folder": leafname.Visible(folder), "submission": sub})
}

// dropNoteHeader prefixes the NOT.txt written beside a submission with the
// uploader's name. The file is opened by the owner, so it follows the owner's
// locale like the notification does.
func dropNoteHeader(lang, uploaderName string) string {
	return srvtext.Text(lang, "server.drop.note_from", srvtext.Vars{"name": uploaderName}) + "\n\n"
}

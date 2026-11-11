package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/templates"
)

// The mails as a person reads them - the subject and the plain-text part -
// rendered from filex's own templates with the data the flows give them
// (mail_templates.go). The tests read them the way the plain-text builders
// these replaced were read, so the sentences stay held to what they said.

func renderMailText(lang, site, key string, data map[string]any) (string, string) {
	r := &templates.Renderer{Brand: func(context.Context, templates.Target) templates.Brand {
		return templates.Brand{Name: site}
	}}
	m, err := r.Mail(context.Background(), templates.Target{Lang: lang}, key, data)
	if err != nil {
		panic(err)
	}
	return m.Subject, m.Text
}

func shareMailText(lang, site, name string, isDir bool, size int64, link, pin string, pinWithheld bool, days int) (string, string) {
	return renderMailText(lang, site, "mail.share", shareMailData(lang, name, isDir, size, link, pin, pinWithheld, days))
}

func dropInviteMailText(lang, site, folder, link, pin string, pinWithheld bool, days, maxFiles, maxMB int, ext []string) (string, string) {
	return renderMailText(lang, site, "mail.file_request", fileRequestMailData(folder, link, pin, pinWithheld, days, maxFiles, maxMB, ext))
}

func itemGrantText(lang, item, exploreURL string) (string, string) {
	return renderMailText(lang, "", "mail.grant", grantMailData(item, exploreURL))
}

func accountCreatedText(lang, loginURL, email, tempPw string) (string, string) {
	return renderMailText(lang, "", "mail.account", accountMailData(loginURL, email, tempPw))
}

package server

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// appMailer sends an app's mail (wasmplugin mail_send) through the mail.app
// template: what the app wrote, framed like every other filex mail and
// signed with the app's name in the mail's language.
type appMailer struct {
	templates *templates.Renderer
	mailer    *mailer.Service
}

func (a appMailer) SendAppMail(ctx context.Context, m wasmplugin.AppMail) error {
	if a.mailer == nil {
		return mailer.ErrNotConfigured
	}
	return templates.Send(ctx, a.templates, a.mailer, m.To, templates.Target{Lang: m.Lang}, "mail.app", map[string]any{
		"app":     map[string]any{"name": m.App},
		"message": map[string]any{"subject": m.Subject, "body": m.Body},
	})
}

// stringsAny is a []string as the []any a template's data holds.
func stringsAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

package templates

import (
	"context"
	"sync"

	"github.com/brf-tech/filex/backend/internal/mailer"
)

// MessageSender delivers a rendered mail (*mailer.Service).
type MessageSender interface {
	SendMessage(ctx context.Context, to string, m mailer.Message) error
}

// Send renders the mail key for t and delivers it to to, tagged with its
// language. A nil sender is mailer.ErrNotConfigured; the mailer's own errors
// come back as they are (callers tell "not set up" from "failed").
func Send(ctx context.Context, r *Renderer, m MessageSender, to string, t Target, key string, data map[string]any) error {
	if m == nil {
		return mailer.ErrNotConfigured
	}
	if r == nil {
		r = &Renderer{}
	}
	msg, err := r.Mail(ctx, t, key, data)
	if err != nil {
		return err
	}
	return m.SendMessage(mailer.WithLanguage(ctx, t.Lang), to, mailer.Message{Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML})
}

// brandMu guards the identity hooks, which the HTTP layer wires after the
// renderer is made (the branding source lives there) while a mail may already
// be on its way.
var brandMu sync.RWMutex

// SetBrand wires the identity a render wears and the tenant an address
// belongs to.
func (r *Renderer) SetBrand(brand func(context.Context, Target) Brand, tenantForHost func(context.Context, string) int64) {
	brandMu.Lock()
	r.Brand, r.TenantForHost = brand, tenantForHost
	brandMu.Unlock()
}

func (r *Renderer) hooks() (func(context.Context, Target) Brand, func(context.Context, string) int64) {
	if r == nil {
		return nil, nil
	}
	brandMu.RLock()
	defer brandMu.RUnlock()
	return r.Brand, r.TenantForHost
}

package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// defaultAccent is the product blue the public pages and mails wear when the
// branding names no accent (publicPageStyle's --px-accent).
const defaultAccent = "#2f6ceb"

// ForShell is the branding the application's document (index.html: its
// title, theme colour and icon) is served in: on a public link page whose
// link is live (withShellPage set a link target), the link's tenant's -
// the same identity /api/public/{s,d}/{token}/branding then answers - and
// the address's everywhere else. Nil-safe.
func (b *BrandingSource) ForShell(r *http.Request) BrandingConfig {
	if b == nil || r == nil {
		return BrandingConfig{}
	}
	if t, _, ok := ShellPageFrom(r.Context()); ok && !t.ByHost {
		id := t.TenantID
		return b.ForProvider(r.Context(), &id)
	}
	return b.ForRequest(r)
}

// TemplateBrand is the identity a template renders in (templates.Brand): the
// branding of the tenant the render is for - or of the address, for a page
// that belongs to no link (Target.ByHost) - and, for the pages, the instance
// theme and the accent stylesheet the old pages wore (publicChromeFor).
// Nil-safe: a nil source is the product's own look.
func (b *BrandingSource) TemplateBrand(ctx context.Context, t templates.Target) templates.Brand {
	var cfg BrandingConfig
	if b != nil {
		if t.ByHost {
			cfg = b.For(ctx, t.Host)
		} else {
			id := t.TenantID
			cfg = b.ForProvider(ctx, &id)
		}
	}
	accent := defaultAccent
	if brandingAccentRe.MatchString(cfg.Accent) {
		accent = cfg.Accent
	}
	out := templates.Brand{
		Name:          cfg.Name,
		LogoURL:       cfg.LogoURL,
		FooterText:    cfg.FooterText,
		HidePoweredBy: cfg.HidePoweredBy,
		Accent:        expandHex(accent),
		AccentHover:   brandingDarken(accent, 0.85),
		OnAccent:      accentOnText(accent),
	}
	css := string(chromeFor(cfg).BrandCSS)
	if b != nil && b.Appearance != nil {
		css = publicThemeCSS(b.Appearance.DefaultTokens(ctx, false), b.Appearance.DefaultTokens(ctx, true)) + css
	}
	out.ThemeCSS = css
	return out
}

// expandHex is #rgb as #rrggbb (a mail's CSS is read by programs that know
// only the long form).
func expandHex(h string) string {
	if len(h) == 4 && h[0] == '#' {
		return "#" + string([]byte{h[1], h[1], h[2], h[2], h[3], h[3]})
	}
	return strings.ToLower(h)
}

// mailTenant is the tenant a mail is written for: the link's (a share or a
// file request names where it was made), else the caller's own.
func mailTenant(ctx context.Context, sh *model.Share) int64 {
	if sh != nil && sh.TenantID > 0 {
		return sh.TenantID
	}
	if sc, ok := tenant.FromContext(ctx); ok && sc != nil && !sc.IsSupertenant && sc.ProviderID > 0 {
		return sc.ProviderID
	}
	return 0
}

// userTenant is the tenant an account belongs to (0: none, or the install).
func userTenant(u *model.User) int64 {
	if u != nil && u.ProviderID != nil {
		return *u.ProviderID
	}
	return 0
}

// sendTemplateMail renders the template mail key for t and sends it to to
// (templates.Send).
func sendTemplateMail(ctx context.Context, r *templates.Renderer, m *mailer.Service, to string, t templates.Target, key string, data map[string]any) error {
	if m == nil {
		return mailer.ErrNotConfigured
	}
	return templates.Send(ctx, r, m, to, t, key, data)
}

// orDefaultTemplates is r, or a renderer of filex's own templates when none
// is wired (a handler built by a test).
func orDefaultTemplates(r *templates.Renderer) *templates.Renderer {
	if r != nil {
		return r
	}
	return &templates.Renderer{}
}

// AttachTemplates wires the renderer the public pages are made with.
func (h *Share) AttachTemplates(r *templates.Renderer) { h.Templates = r }

// AttachTemplates wires the renderer the file request pages and the owner's
// mail are made with.
func (h *Drop) AttachTemplates(r *templates.Renderer) { h.Templates = r }

// AttachTemplates wires the renderer the link page's text slots are made with.
func (h *PublicAPI) AttachTemplates(r *templates.Renderer) { h.Templates = r }

// withMailLanguage tags the mail sent under ctx with lang (Content-Language).
func withMailLanguage(ctx context.Context, lang string) context.Context {
	return mailer.WithLanguage(ctx, lang)
}

// mailMessage is a previewed mail as the mailer sends it.
func mailMessage(p templates.Preview) mailer.Message {
	return mailer.Message{Subject: p.Subject, Text: p.Text, HTML: p.HTML}
}

// pageTemplates is the renderer a public handler's pages use: the one wired
// (routes.go), else filex's own templates in the handler's branding (a
// handler a test built).
func pageTemplates(r *templates.Renderer, br *BrandingSource) *templates.Renderer {
	if r != nil {
		return r
	}
	d := &templates.Renderer{}
	d.SetBrand(br.TemplateBrand, nil)
	return d
}

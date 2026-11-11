package templates

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Draft is what the editor holds: a template's texts before they are saved.
type Draft struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Text    string `json:"text"`
}

// Preview is a draft rendered with sample data, in the sandbox, strictly (a
// variable the template has not got is an error).
type Preview struct {
	Subject string `json:"subject,omitempty"`
	HTML    string `json:"html"`
	Text    string `json:"text,omitempty"`
	// Removed is what the sanitizer took out of the draft's output ("<script>",
	// "onclick") - it will not reach anybody.
	Removed []string `json:"removed,omitempty"`
	// Missing are the hooks the page's own script needs that the draft does
	// not have (Def.Hooks); a draft with any is not saved.
	Missing []string `json:"missing,omitempty"`
}

// ErrTooLarge: a draft's text is over MaxSource.
var ErrTooLarge = fmt.Errorf("a template may be at most %d KiB", MaxSource>>10)

// showcase is what a layout is previewed around: a mail and a page filex
// sends and shows.
var showcase = map[string]string{"mail.layout": "mail.share", "page.layout": "page.pin"}

// shellPreviewApp stands where the link page's application goes in the
// shell's preview, which cannot run it.
const shellPreviewApp = `<div id="app" style="max-width:420px;margin:48px auto;padding:28px;border:1px dashed #9aa3af;border-radius:14px;text-align:center;font:15px system-ui,sans-serif;color:#66727f">filex public link page</div>`

// Preview renders draft (nil: the template as it is now in t's scope) with
// the template's sample data. A layout is previewed around the mail or page
// it frames; the shell around a stand-in for the application.
func (r *Renderer) Preview(ctx context.Context, t Target, key string, draft *Draft) (Preview, error) {
	def := Lookup(key)
	if def == nil {
		return Preview{}, ErrUnknown
	}
	if draft != nil && (len(draft.Body) > MaxSource || len(draft.Subject) > MaxSource || len(draft.Text) > MaxSource) {
		return Preview{}, ErrTooLarge
	}
	scope := r.Scope(ctx, t)
	stand := &model.Template{TenantID: scope, Key: key, Revision: -1}
	target := def
	if sc, ok := showcase[key]; ok {
		target = Lookup(sc)
	}
	p := r.parts(ctx, scope, target, false)
	if draft != nil {
		switch {
		case def.Kind == KindLayout:
			p.layout = &piece{key: key, src: draft.Body, override: stand}
		default:
			p.body = piece{key: key, src: draft.Body, override: stand}
			if def.Subject && strings.TrimSpace(draft.Subject) != "" {
				p.subject = piece{key: key + ".subject", src: draft.Subject, override: stand}
			}
			p.text = piece{}
			if def.Text && strings.TrimSpace(draft.Text) != "" {
				p.text = piece{key: key + ".text", src: draft.Text, override: stand}
			}
		}
	}
	// Always the sandbox: a preview is the template as it will run.
	if !p.overridden() {
		p.body.override = stand
	}
	data := target.Sample()
	var out Preview
	switch target.Kind {
	case KindMail:
		m, err := r.mail(ctx, t, target, p, data, true, &out.Removed)
		if err != nil {
			return Preview{}, err
		}
		out.Subject, out.HTML, out.Text = m.Subject, m.HTML, m.Text
	case KindShell:
		marker, headMarker := "<!--filex-preview-app-->", "<!--filex-preview-app-head-->"
		data["app"] = HTML(marker)
		data["app_head"] = HTML(headMarker)
		html, err := r.page(ctx, t, target, p, data, true, &out.Removed)
		if err != nil {
			return Preview{}, err
		}
		if strings.Count(html, headMarker) != 1 {
			out.Missing = append(out.Missing, "{{ app_head }}")
		}
		if strings.Count(html, marker) != 1 {
			out.Missing = append(out.Missing, "{{ app }}")
		}
		out.HTML = strings.Replace(strings.Replace(html, marker, shellPreviewApp, 1), headMarker, "", 1)
	default:
		html, err := r.page(ctx, t, target, p, data, true, &out.Removed)
		if err != nil {
			return Preview{}, err
		}
		out.HTML = html
		out.Missing = missingHooks(html, target.Hooks)
	}
	return out, nil
}

// ErrMissingHooks: the draft lacks what the page's script needs.
var ErrMissingHooks = errors.New("the page's own script needs something the template took out")

// Validate is what a save asks: the draft renders (Preview) and keeps every
// hook. The preview is returned either way, for the editor to show.
func (r *Renderer) Validate(ctx context.Context, t Target, key string, draft Draft) (Preview, error) {
	p, err := r.Preview(ctx, t, key, &draft)
	if err != nil {
		return p, err
	}
	if len(p.Missing) > 0 {
		return p, ErrMissingHooks
	}
	return p, nil
}

func missingHooks(html string, hooks []string) []string {
	var miss []string
	for _, h := range hooks {
		var re *regexp.Regexp
		if name, ok := strings.CutPrefix(h, "name="); ok {
			re = regexp.MustCompile(`(?i)\sname\s*=\s*["']?` + regexp.QuoteMeta(name) + `["'\s>/]`)
		} else {
			re = regexp.MustCompile(`(?i)\sid\s*=\s*["']?` + regexp.QuoteMeta(h) + `["'\s>/]`)
		}
		if !re.MatchString(html) {
			miss = append(miss, h)
		}
	}
	return miss
}

package handlers

/* The template editor's API: /api/admin/templates (internal/templates).

Every administrator edits the templates of their own scope and nothing else:

	- the install's administrator (single-tenant, or the platform's own tenant
	  on a multi-tenant install): the install's templates, tenant 0, which
	  every tenant without its own uses;
	- a tenant's administrator: that tenant's templates, which only that
	  tenant's mails and link pages use.

⚠⚠ The scope comes from the caller's session (tenant.FromContext), never from
the request: there is no tenant id in any route or body here, so there is no
id to change. A tenant's row is read and written with its provider id in the
WHERE clause (db.TemplateSQL), and the rows of another tenant are not in any
answer - not even as "customized".

A save is checked before it is written (templates.Renderer.Validate): it must
render with the template's sample data in the sandbox, strictly (a variable
it does not have is an error), and keep what the page's own script needs. A
template that fails later anyway (a ceiling at run time) is noted on its row
and filex's own is used. */

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/templates/sandbox"
)

// TemplatesAdmin serves /api/admin/templates.
type TemplatesAdmin struct {
	Store     db.Store
	Templates *templates.Renderer
	// Send delivers the editor's test mail (the mailer); nil = no test mail.
	Send templates.MessageSender
	// Ready reports whether mail can go at all (mailer.Service.Ready).
	Ready func(context.Context) bool
}

// NewTemplatesAdmin constructs the handler.
func NewTemplatesAdmin(store db.Store, r *templates.Renderer) *TemplatesAdmin {
	return &TemplatesAdmin{Store: store, Templates: r}
}

// templateScope is the caller's template scope: their tenant's provider id,
// 0 for the install's administrator.
func templateScope(ctx context.Context) int64 {
	if sc, confined := confinedScope(ctx); confined {
		return sc.ProviderID
	}
	return 0
}

// templateRow is one template as the editor's list shows it.
type templateRow struct {
	templates.Def
	AllVars []string `json:"all_vars"`
	// Source is whose text is in force for the caller: "tenant" (their own),
	// "instance" (the install's, which a tenant inherits) or "default".
	Source      string `json:"source"`
	Revision    int64  `json:"revision"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	LastErrorAt string `json:"last_error_at,omitempty"`
}

func (h *TemplatesAdmin) rowFor(ctx context.Context, scope int64, d templates.Def) (templateRow, *model.Template) {
	out := templateRow{Def: d, AllVars: d.AllVars(), Source: "default"}
	var own *model.Template
	if t, err := h.Store.GetTemplate(ctx, scope, d.Key); err == nil && t != nil {
		own = t
		out.Source = "instance"
		if scope > 0 {
			out.Source = "tenant"
		}
	} else if scope > 0 {
		if t, err := h.Store.GetTemplate(ctx, 0, d.Key); err == nil && t != nil {
			out.Source = "instance"
		}
	}
	if own != nil {
		out.Revision = own.Revision
		out.UpdatedAt = own.UpdatedAt.Format("2006-01-02T15:04:05Z")
		out.LastError = own.LastError
		if own.LastErrorAt != nil {
			out.LastErrorAt = own.LastErrorAt.Format("2006-01-02T15:04:05Z")
		}
	}
	return out, own
}

// List answers every template with whose text is in force for the caller.
//
//	GET /api/admin/templates
func (h *TemplatesAdmin) List(w http.ResponseWriter, r *http.Request) {
	scope := templateScope(r.Context())
	rows := make([]templateRow, 0, len(templates.Defs))
	for _, d := range templates.Defs {
		row, _ := h.rowFor(r.Context(), scope, d)
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":     map[string]any{"tenant": scope > 0},
		"templates": rows,
		"max_bytes": templates.MaxSource,
		"languages": srvtext.BuiltinLanguages(),
	})
}

// templateTexts are a template's three texts.
type templateTexts struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Text    string `json:"text"`
}

// Get answers one template: the caller's own text when they replaced it,
// else the text in force for them (the install's, or filex's own), and
// filex's own beside it for "back to the default".
//
//	GET /api/admin/templates/{key}
func (h *TemplatesAdmin) Get(w http.ResponseWriter, r *http.Request) {
	d := templates.Lookup(chi.URLParam(r, "key"))
	if d == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	scope := templateScope(r.Context())
	row, own := h.rowFor(r.Context(), scope, *d)
	def := templateTexts{Subject: templates.Default(d.Key, "subject"), Body: templates.Default(d.Key, ""), Text: templates.Default(d.Key, "text")}
	cur := def
	switch {
	case own != nil:
		cur = templateTexts{Subject: own.Subject, Body: own.Body, Text: own.TextBody}
	case row.Source == "instance":
		if t, err := h.Store.GetTemplate(r.Context(), 0, d.Key); err == nil && t != nil {
			cur = templateTexts{Subject: t.Subject, Body: t.Body, Text: t.TextBody}
		}
	}
	if strings.TrimSpace(cur.Subject) == "" {
		cur.Subject = def.Subject
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": row, "current": cur, "default": def})
}

type templatePutReq struct {
	templateTexts
	// Revision is the revision the editor opened (0: a first save). A save
	// over another one is refused with 409 template_changed.
	Revision int64  `json:"revision"`
	Lang     string `json:"lang"`
}

// Put saves the caller's replacement of one template, after checking it.
//
//	PUT /api/admin/templates/{key} {subject, body, text, revision}
func (h *TemplatesAdmin) Put(w http.ResponseWriter, r *http.Request) {
	d := templates.Lookup(chi.URLParam(r, "key"))
	if d == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req templatePutReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*templates.MaxSource)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if !d.Subject {
		req.Subject = ""
	}
	if !d.Text {
		req.Text = ""
	}
	scope := templateScope(r.Context())
	lang := srvtext.Pick(req.Lang, requestLang(r))
	preview, err := h.Templates.Validate(r.Context(), templates.Target{TenantID: scope, Lang: lang}, d.Key,
		templates.Draft{Subject: req.Subject, Body: req.Body, Text: req.Text})
	if err != nil {
		writeTemplateRefusal(w, r, err, preview)
		return
	}
	var by *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		by = &id
	}
	saved, err := h.Store.PutTemplate(r.Context(), &model.Template{
		TenantID: scope, Key: d.Key, Subject: req.Subject, Body: req.Body, TextBody: req.Text, UpdatedBy: by,
	}, req.Revision)
	if errors.Is(err, db.ErrTemplateChanged) {
		writeError(w, r, http.StatusConflict, "template_changed", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.Templates.Forget()
	auth.SetAuditTarget(r.Context(), d.Key, d.Key)
	auth.AddAuditDetail(r.Context(), "revision", saved.Revision)
	if scope > 0 {
		auth.AddAuditDetail(r.Context(), "scope", "tenant")
	}
	row, _ := h.rowFor(r.Context(), scope, *d)
	writeJSON(w, http.StatusOK, map[string]any{"template": row, "preview": preview})
}

// Delete takes the caller's replacement away: the template in force is the
// install's again (for a tenant), or filex's own.
//
//	DELETE /api/admin/templates/{key}
func (h *TemplatesAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	d := templates.Lookup(chi.URLParam(r, "key"))
	if d == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	scope := templateScope(r.Context())
	removed, err := h.Store.DeleteTemplate(r.Context(), scope, d.Key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.Templates.Forget()
	auth.SetAuditTarget(r.Context(), d.Key, d.Key)
	auth.AddAuditDetail(r.Context(), "removed", removed)
	row, _ := h.rowFor(r.Context(), scope, *d)
	writeJSON(w, http.StatusOK, map[string]any{"template": row, "removed": removed})
}

type templatePreviewReq struct {
	templateTexts
	Lang string `json:"lang"`
	// Saved previews the template as it is in force (the texts are ignored).
	Saved bool `json:"saved"`
}

// Preview renders a draft with the template's sample data, in the sandbox,
// in the language asked (else the caller's).
//
//	POST /api/admin/templates/{key}/preview {subject, body, text, lang}
func (h *TemplatesAdmin) Preview(w http.ResponseWriter, r *http.Request) {
	d := templates.Lookup(chi.URLParam(r, "key"))
	if d == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req templatePreviewReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*templates.MaxSource)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var draft *templates.Draft
	if !req.Saved {
		draft = &templates.Draft{Subject: req.Subject, Body: req.Body, Text: req.Text}
	}
	lang := srvtext.Pick(req.Lang, requestLang(r))
	p, err := h.Templates.Preview(r.Context(), templates.Target{TenantID: templateScope(r.Context()), Lang: lang}, d.Key, draft)
	if err != nil {
		writeTemplateRefusal(w, r, err, p)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": p, "lang": lang})
}

// TestMail sends a draft (or the template in force) with its sample data to
// the caller's OWN address, and nowhere else: an editor is not a way to mail
// strangers through the install's SMTP.
//
//	POST /api/admin/templates/{key}/test {subject, body, text, lang}
func (h *TemplatesAdmin) TestMail(w http.ResponseWriter, r *http.Request) {
	d := templates.Lookup(chi.URLParam(r, "key"))
	if d == nil || (d.Kind != templates.KindMail && d.Key != "mail.layout") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil || strings.TrimSpace(u.Email) == "" || !strings.Contains(u.Email, "@") {
		writeError(w, r, http.StatusConflict, "template_test_no_address", nil)
		return
	}
	if h.Send == nil || (h.Ready != nil && !h.Ready(r.Context())) {
		writeError(w, r, http.StatusServiceUnavailable, "template_test_no_mail", nil)
		return
	}
	var req templatePreviewReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*templates.MaxSource)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var draft *templates.Draft
	if !req.Saved {
		draft = &templates.Draft{Subject: req.Subject, Body: req.Body, Text: req.Text}
	}
	lang := srvtext.Pick(req.Lang, requestLang(r))
	p, err := h.Templates.Preview(r.Context(), templates.Target{TenantID: templateScope(r.Context()), Lang: lang}, d.Key, draft)
	if err != nil {
		writeTemplateRefusal(w, r, err, p)
		return
	}
	auth.SetAuditTarget(r.Context(), d.Key, d.Key)
	auth.AddAuditDetail(r.Context(), "to_self", true)
	if err := h.Send.SendMessage(withMailLanguage(r.Context(), lang), u.Email, mailMessage(p)); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"sent": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": u.Email})
}

// writeTemplateRefusal answers a draft that does not render or lacks what its
// page needs: 422 with the template engine's own words (they carry the line)
// and the preview there is.
func writeTemplateRefusal(w http.ResponseWriter, r *http.Request, err error, p templates.Preview) {
	switch {
	case errors.Is(err, templates.ErrUnknown):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, templates.ErrTooLarge), errors.Is(err, sandbox.ErrTooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, "template_too_large", apierr.Params{"kb": strconv.Itoa(templates.MaxSource >> 10)}, "max", templates.MaxSource)
	case errors.Is(err, sandbox.ErrTooSlow):
		writeError(w, r, http.StatusUnprocessableEntity, "template_too_slow", nil, "detail", err.Error())
	case errors.Is(err, sandbox.ErrStopped):
		writeError(w, r, http.StatusUnprocessableEntity, "template_stopped", nil, "detail", err.Error())
	case errors.Is(err, templates.ErrMissingHooks):
		writeError(w, r, http.StatusUnprocessableEntity, "template_hooks_missing", nil, "missing", p.Missing, "preview", p)
	default:
		writeError(w, r, http.StatusUnprocessableEntity, "template_invalid", nil, "detail", err.Error())
	}
}

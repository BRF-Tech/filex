// Package templates renders filex's mails and the pages it shows strangers
// from Liquid templates an administrator can change: filex ships one for each
// (defaults/, the catalogue in catalog.go), the install's administrator may
// replace any of them for the whole install, and a tenant's administrator for
// their own tenant. Removing a replacement is "back to the default".
//
// How one is rendered (docs/TEMPLATES.md):
//
//   - The template to use is the tenant's replacement, else the install's,
//     else filex's own; the same for the layout it is put into.
//   - A render that involves only filex's own templates runs in-process
//     (internal/templates/engine): they are part of the program. A render
//     that involves ANY replaced template runs in the sandbox
//     (internal/templates/sandbox), where time, memory and output are capped.
//   - Whatever comes out passes htmlx.Sanitize: no script, no event handler,
//     no javascript: address. A mail's CSS is then written into its
//     elements (htmlx.InlineCSS) and its plain-text part made from the HTML,
//     unless the template has a plain-text body of its own.
//   - ⚠⚠ A replaced template that fails - a syntax error, a ceiling, a
//     filter error - is noted on its row (the editor shows it) and the render
//     is done again with filex's own templates. A broken template never costs
//     a visitor their page or a person their mail.
package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/templates/engine"
	"github.com/brf-tech/filex/backend/internal/templates/htmlx"
	"github.com/brf-tech/filex/backend/internal/templates/sandbox"
)

// The ceilings of a render of a replaced template (sandbox has the memory
// and output ones).
const (
	// PageTimeout bounds a page: a visitor is waiting for it. ⚠ Wall time,
	// the fresh instance included: a render that takes 10 ms on an idle
	// machine took over 250 ms (the first ceiling) on a busy one, and a
	// template that is merely slow under load must not be replaced and marked
	// failed - the ceiling is there for a loop that never ends.
	PageTimeout = time.Second
	// MailTimeout bounds a mail: it is sent off the request's path.
	MailTimeout = 3 * time.Second
	// MaxSource is the longest template the editor saves.
	MaxSource = sandbox.MaxSource
)

// Brand is a tenant's (or the install's) identity as the templates see it:
// the branding settings, the theme the pages wear, and the colours of a
// mail's button. The handlers' branding source fills it (Renderer.Brand).
type Brand struct {
	Name          string
	LogoURL       string
	FooterText    string
	HidePoweredBy bool
	// Accent, AccentHover and OnAccent are #rrggbb colours, checked.
	Accent, AccentHover, OnAccent string
	// ThemeCSS is the pages' theme and accent stylesheet (<style> blocks
	// filex made from checked values).
	ThemeCSS string
	// SiteURL is the address the install (or the tenant) is reached at.
	SiteURL string
}

// Target is whose templates a render uses, and in what language.
type Target struct {
	// TenantID is the tenant whose templates and branding are used: the
	// tenant a link was made in, the addressee's tenant. 0 is the install.
	TenantID int64
	// Host is the address a page was asked for at.
	Host string
	// ByHost: the branding is the address's (a page that belongs to no link,
	// such as the "this link is not available" page, says nothing about
	// whose link the token was).
	ByHost bool
	// Lang is the reader's language (srvtext tag).
	Lang string
	// SelfContained: the page is served where it may load nothing from
	// anywhere - an app's public address, whose Content-Security-Policy is
	// default-src 'none', on a host that may serve nothing else of filex
	// (#324). The brand's logo is an address, so the template is not given
	// one (brand.logo_url is empty); the theme is inline CSS and stays.
	SelfContained bool
}

// Renderer renders templates. The zero value works on filex's own templates
// alone; Store makes replacements count.
type Renderer struct {
	Store       db.Store
	MultiTenant bool
	// Brand answers the identity a render wears; nil = none.
	Brand func(ctx context.Context, t Target) Brand
	// TenantForHost names the tenant a page's address belongs to (0 = the
	// install's), for a page that belongs to no link; nil = the install.
	TenantForHost func(ctx context.Context, host string) int64

	mu     sync.Mutex
	scopes map[int64]scopeEntry
	cache  outputCache
	// gen counts Forget calls: a caller that keeps what a render made (the
	// app route door's pages) asks Generation, so a saved template is shown
	// at once rather than when the caller's own cache runs out.
	gen atomic.Uint64
}

// Generation changes whenever a template is saved or removed (Forget): what
// was rendered under another generation may be out of date.
func (r *Renderer) Generation() uint64 {
	if r == nil {
		return 0
	}
	return r.gen.Load()
}

type scopeEntry struct {
	scope int64
	exp   time.Time
}

// scope is whose replacements count for a tenant id: the tenant's own when it
// is a customer tenant, the install's (0) for the platform's own tenant, an
// unknown id, or a single-tenant install.
func (r *Renderer) scope(ctx context.Context, tenantID int64) int64 {
	if r == nil || !r.MultiTenant || tenantID <= 0 || r.Store == nil {
		return 0
	}
	r.mu.Lock()
	if e, ok := r.scopes[tenantID]; ok && time.Now().Before(e.exp) {
		r.mu.Unlock()
		return e.scope
	}
	r.mu.Unlock()
	s := int64(0)
	if p, err := r.Store.GetProvider(ctx, tenantID); err == nil && p != nil && !p.IsSupertenant {
		s = p.ID
	}
	r.mu.Lock()
	if r.scopes == nil {
		r.scopes = map[int64]scopeEntry{}
	}
	r.scopes[tenantID] = scopeEntry{scope: s, exp: time.Now().Add(30 * time.Second)}
	r.mu.Unlock()
	return s
}

// Scope is Target t's template scope: the tenant whose replacements count,
// 0 for the install's. The admin handlers ask it for the caller's tenant.
func (r *Renderer) Scope(ctx context.Context, t Target) int64 {
	id := t.TenantID
	if _, forHost := r.hooks(); t.ByHost && forHost != nil {
		id = forHost(ctx, t.Host)
	}
	return r.scope(ctx, id)
}

// piece is one template text and where it came from.
type piece struct {
	key      string
	src      string
	override *model.Template // nil = filex's own
}

func (p piece) revision() string {
	if p.override == nil {
		return "0"
	}
	return strconv.FormatInt(p.override.TenantID, 10) + ":" + strconv.FormatInt(p.override.Revision, 10)
}

// row is the replacement of key that counts in scope: the scope's own, else
// the install's; nil for none.
func (r *Renderer) row(ctx context.Context, scope int64, key string) *model.Template {
	if r == nil || r.Store == nil {
		return nil
	}
	if scope > 0 {
		if t, err := r.Store.GetTemplate(ctx, scope, key); err == nil && t != nil {
			return t
		}
	}
	if t, err := r.Store.GetTemplate(ctx, 0, key); err == nil && t != nil {
		return t
	}
	return nil
}

// parts are a template's three texts as they count in a scope (or filex's
// own, defaultsOnly).
type parts struct {
	body, subject, text piece
	layout              *piece
}

func (p parts) overridden() bool {
	return p.body.override != nil || p.subject.override != nil || p.text.override != nil ||
		(p.layout != nil && p.layout.override != nil)
}

func (r *Renderer) parts(ctx context.Context, scope int64, def *Def, defaultsOnly bool) parts {
	var row *model.Template
	if !defaultsOnly {
		row = r.row(ctx, scope, def.Key)
	}
	p := parts{
		body:    piece{key: def.Key, src: Default(def.Key, "")},
		subject: piece{key: def.Key + ".subject", src: Default(def.Key, "subject")},
	}
	if row != nil {
		p.body = piece{key: def.Key, src: row.Body, override: row}
		if strings.TrimSpace(row.Subject) != "" {
			p.subject = piece{key: def.Key + ".subject", src: row.Subject, override: row}
		}
		if strings.TrimSpace(row.TextBody) != "" {
			p.text = piece{key: def.Key + ".text", src: row.TextBody, override: row}
		}
	}
	if def.Layout != "" {
		l := piece{key: def.Layout, src: Default(def.Layout, "")}
		if !defaultsOnly {
			if lr := r.row(ctx, scope, def.Layout); lr != nil {
				l = piece{key: def.Layout, src: lr.Body, override: lr}
			}
		}
		p.layout = &l
	}
	return p
}

// ErrUnknown: no template has that name.
var ErrUnknown = errors.New("there is no template with that name")

// Mail is a rendered mail.
type Mail struct {
	Subject string
	HTML    string
	Text    string
}

// Mail renders the mail key for t with data (its own variables; the globals
// are added). It falls back to filex's own templates when a replacement
// fails, so its error is only ever filex's own template's.
func (r *Renderer) Mail(ctx context.Context, t Target, key string, data map[string]any) (Mail, error) {
	def := Lookup(key)
	if def == nil || def.Kind != KindMail {
		return Mail{}, ErrUnknown
	}
	return withFallback(r, ctx, t, def, func(p parts) (Mail, error) {
		return r.mail(ctx, t, def, p, data, false, nil)
	})
}

// withFallback renders def for t with the parts in force; when a replaced
// part fails, the failure is noted on its row and filex's own parts are
// rendered instead, so the error that comes back is only ever filex's own.
func withFallback[T any](r *Renderer, ctx context.Context, t Target, def *Def, render func(parts) (T, error)) (T, error) {
	scope := r.Scope(ctx, t)
	p := r.parts(ctx, scope, def, false)
	out, err := render(p)
	if err != nil && p.overridden() {
		r.noteFailure(ctx, p, err)
		out, err = render(r.parts(ctx, scope, def, true))
	}
	return out, err
}

func (r *Renderer) mail(ctx context.Context, t Target, def *Def, p parts, data map[string]any, strict bool, removed *[]string) (Mail, error) {
	sandboxed := p.overridden()
	full := r.globals(ctx, t, def, data)
	subject, err := r.run(ctx, sandboxed, job{text: true, strict: strict, lang: t.Lang, name: p.subject.key, src: p.subject.src, data: full, timeout: MailTimeout})
	if err != nil {
		return Mail{}, err
	}
	subject = oneLine(subject)
	setPath(full, "mail.subject", subject)
	html, err := r.run(ctx, sandboxed, bodyJob(t, p, full, strict, MailTimeout))
	if err != nil {
		return Mail{}, err
	}
	html = sanitize(html, removed)
	if inlined, ierr := htmlx.InlineCSS(html); ierr == nil {
		html = inlined
	}
	text := ""
	if p.text.src != "" {
		text, err = r.run(ctx, sandboxed, job{text: true, strict: strict, lang: t.Lang, name: p.text.key, src: p.text.src, data: full, timeout: MailTimeout})
		if err != nil {
			return Mail{}, err
		}
		text = strings.TrimSpace(text) + "\n"
	} else {
		text = htmlx.Text(html) + "\n"
	}
	return Mail{Subject: subject, HTML: html, Text: text}, nil
}

// bodyJob is the render of p's body, inside its layout when it has one.
func bodyJob(t Target, p parts, full map[string]any, strict bool, timeout time.Duration) job {
	j := job{strict: strict, lang: t.Lang, name: p.body.key, src: p.body.src, data: full, timeout: timeout}
	if p.layout != nil {
		j.layoutName, j.layout = p.layout.key, p.layout.src
	}
	return j
}

// Page renders the page key for t. Like Mail, it falls back to filex's own
// templates when a replacement fails.
func (r *Renderer) Page(ctx context.Context, t Target, key string, data map[string]any) (string, error) {
	def := Lookup(key)
	if def == nil || (def.Kind != KindPage && def.Kind != KindShell) {
		return "", ErrUnknown
	}
	return withFallback(r, ctx, t, def, func(p parts) (string, error) {
		return r.page(ctx, t, def, p, data, false, nil)
	})
}

func (r *Renderer) page(ctx context.Context, t Target, def *Def, p parts, data map[string]any, strict bool, removed *[]string) (string, error) {
	sandboxed := p.overridden()
	full := r.globals(ctx, t, def, data)
	j := bodyJob(t, p, full, strict, PageTimeout)
	var cacheKey string
	if sandboxed && !strict {
		cacheKey = r.cacheKey(t, def, p, full)
		if out, ok := r.cache.get(cacheKey); ok {
			return out, nil
		}
	}
	out, err := r.run(ctx, sandboxed, j)
	if err != nil {
		return "", err
	}
	out = sanitize(out, removed)
	if cacheKey != "" {
		r.cache.put(cacheKey, out)
	}
	return out, nil
}

// Slot renders the text slot key: "" when neither the tenant nor the install
// wrote one (the page then says its own words), or when it failed.
func (r *Renderer) Slot(ctx context.Context, t Target, key string, data map[string]any) string {
	def := Lookup(key)
	if def == nil || def.Kind != KindSlot {
		return ""
	}
	scope := r.Scope(ctx, t)
	p := r.parts(ctx, scope, def, false)
	if strings.TrimSpace(p.body.src) == "" {
		return ""
	}
	out, err := r.page(ctx, t, def, p, data, false, nil)
	if err != nil {
		if p.overridden() {
			r.noteFailure(ctx, p, err)
		}
		return ""
	}
	return strings.TrimSpace(out)
}

// noteFailure records a replacement's failure on its row and in the log.
func (r *Renderer) noteFailure(ctx context.Context, p parts, err error) {
	msg := err.Error()
	var rows []*model.Template
	for _, pc := range []piece{p.body, p.subject, p.text} {
		if pc.override != nil {
			rows = append(rows, pc.override)
		}
	}
	if p.layout != nil && p.layout.override != nil {
		rows = append(rows, p.layout.override)
	}
	// The failing one, when the message names it; every replaced one else.
	var named []*model.Template
	for _, row := range rows {
		if strings.Contains(msg, row.Key) {
			named = append(named, row)
		}
	}
	if len(named) > 0 {
		rows = named
	}
	seen := map[int64]bool{}
	for _, row := range rows {
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		slog.Warn("templates: an edited template failed, filex's own was used instead",
			slog.String("template", row.Key), slog.Int64("tenant_id", row.TenantID), slog.String("err", firstLine(msg)))
		if r.Store != nil {
			_ = r.Store.NoteTemplateFailure(context.WithoutCancel(ctx), row.TenantID, row.Key, row.Revision, firstLine(msg), time.Now())
		}
	}
}

// sanitize is htmlx.Sanitize, adding what it took out to removed (nil: not
// asked).
func sanitize(html string, removed *[]string) string {
	out, what := htmlx.SanitizeReport(html)
	if removed != nil {
		*removed = append(*removed, what...)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 250 {
		s = string(r[:250])
	}
	return s
}

// job is one render: a template, optionally the layout it goes into.
type job struct {
	text       bool
	strict     bool
	lang       string
	name, src  string
	layoutName string
	layout     string
	data       map[string]any
	timeout    time.Duration
}

// run renders j: in the sandbox when sandboxed, in-process otherwise. The
// data go through JSON either way, so a template sees the same kinds of value
// (numbers, strings, lists, maps) wherever it runs.
func (r *Renderer) run(ctx context.Context, sandboxed bool, j job) (string, error) {
	plain, safe := flatten(j.data)
	if sandboxed {
		release, err := sandbox.Slot(ctx)
		if err != nil {
			return "", err
		}
		defer release()
		if err := sandbox.Prepare(); err != nil {
			return "", err
		}
		rctx, cancel := context.WithTimeout(ctx, j.timeout)
		defer cancel()
		return sandbox.Render(rctx, sandbox.Request{
			Text: j.text, Strict: j.strict, Name: j.name, Source: j.src,
			Layout: j.layout, LayoutAs: j.layoutName, Data: plain, Safe: safe,
			Host: catalogueHost{lang: j.lang},
		})
	}
	b, err := json.Marshal(plain)
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return "", err
	}
	engine.MarkSafe(data, safe)
	o := engine.Options{Host: catalogueHost{lang: j.lang}, Strict: j.strict}
	if j.text {
		o.Mode = engine.Text
	}
	var out strings.Builder
	if err := renderNative(&out, o, j.name, j.src, data); err != nil {
		return "", err
	}
	if j.layout == "" {
		return out.String(), nil
	}
	data["content"] = engine.Safe(out.String())
	var framed strings.Builder
	if err := renderNative(&framed, o, j.layoutName, j.layout, data); err != nil {
		return "", err
	}
	return framed.String(), nil
}

// renderNative is engine.RenderTo with a library panic (osteele/liquid #149,
// slice with a negative length) turned into an error: filex's own templates
// do not do that, but a panic must not take a request down if one ever does.
func renderNative(w *strings.Builder, o engine.Options, name, src string, data map[string]any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%s: the template engine failed: %v", name, p)
		}
	}()
	return engine.RenderTo(w, o, name, src, data)
}

// flatten copies data with filex's own markup (safeString) as plain strings,
// and names where that markup is.
func flatten(data map[string]any) (map[string]any, []string) {
	var safe []string
	var walk func(prefix string, m map[string]any) map[string]any
	walk = func(prefix string, m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			switch x := v.(type) {
			case safeString:
				out[k] = string(x)
				safe = append(safe, path)
			case map[string]any:
				out[k] = walk(path, x)
			default:
				out[k] = v
			}
		}
		return out
	}
	return walk("", data), safe
}

func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	for _, k := range parts[:len(parts)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// ProductMark is the filex mark the public pages' footer shows: the Go side's
// one copy of LogoMark (packages/core), for the pages that are drawn without
// a bundler - the templates' `product.mark` and the handlers' fallback pages
// (handlers.publicBrandMark). Kept in step with LogoMark by the duplication
// gate (web/tests/quality/duplication.test.ts, brand-mark).
const ProductMark = `<svg viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#2f6ceb"/><path d="M7 11a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V11z" fill="none" stroke="#fff" stroke-width="1.8" stroke-linejoin="round"/><path d="M11.5 17.5l3 2.5 5.5-6" fill="none" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`

// productLink is the product's name as a link; in a mail's plain text, the
// name alone (data-text="label": "Sent with filex", not "filex: https://…").
const productLink = `<a href="https://filex.sh" target="_blank" rel="noopener" data-text="label">filex</a>`

// globals is data with the variables every template has.
func (r *Renderer) globals(ctx context.Context, t Target, def *Def, data map[string]any) map[string]any {
	full := make(map[string]any, len(data)+8)
	for k, v := range data {
		full[k] = v
	}
	var b Brand
	if brand, _ := r.hooks(); brand != nil {
		b = brand(ctx, t)
	}
	if b.Accent == "" {
		b.Accent, b.AccentHover, b.OnAccent = "#2f6ceb", "#2559c9", "#ffffff"
	}
	if t.SelfContained || def.selfContained {
		b.LogoURL = ""
	}
	dir := "ltr"
	if srvtext.IsRTL(t.Lang) {
		dir = "rtl"
	}
	full["lang"] = t.Lang
	full["dir"] = dir
	// Shopify's `blank` and `empty` are not words of osteele/liquid (they
	// would be undefined variables): here they are the empty text, so
	// `{% if x != blank %}` reads as it does in a Shopify theme for text. A
	// list is asked with `list.size > 0`.
	full["blank"] = ""
	full["empty"] = ""
	full["brand"] = map[string]any{"name": b.Name, "logo_url": b.LogoURL, "footer_text": b.FooterText, "hide_powered_by": b.HidePoweredBy}
	theme := map[string]any{"accent": b.Accent, "accent_hover": b.AccentHover, "on_accent": b.OnAccent}
	if def.Kind != KindMail && !strings.HasPrefix(def.Key, "mail.") {
		theme["css"] = safeString(b.ThemeCSS)
	}
	full["theme"] = theme
	full["product"] = map[string]any{"name": "filex", "url": "https://filex.sh", "link": safeString(productLink), "mark": safeString(ProductMark)}
	site, _ := full["site"].(map[string]any)
	if site == nil {
		site = map[string]any{}
	}
	if _, ok := site["name"]; !ok {
		site["name"] = b.Name
	}
	if _, ok := site["url"]; !ok {
		site["url"] = b.SiteURL
	}
	full["site"] = site
	if def.Kind == KindPage || def.Kind == KindShell {
		pg, _ := full["page"].(map[string]any)
		if pg == nil {
			pg = map[string]any{}
		}
		if _, ok := pg["title"]; !ok {
			pg["title"] = ""
		}
		full["page"] = pg
	}
	return full
}

func (r *Renderer) cacheKey(t Target, def *Def, p parts, data map[string]any) string {
	h := sha256.New()
	plain, safe := flatten(data)
	b, _ := json.Marshal(plain)
	fmt.Fprintf(h, "%s|%s|%s|%s|%v|", def.Key, t.Lang, p.body.revision(), layoutRevision(p), safe)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

func layoutRevision(p parts) string {
	if p.layout == nil {
		return "-"
	}
	return p.layout.revision()
}

// outputCache keeps what a replaced page rendered, for a minute: a page that
// a link's visitors reload, or that many visitors open, is rendered in the
// sandbox once. The key is the template revisions and the whole of the data,
// so an edit or a change in what the page shows is a new entry.
type outputCache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

type cacheEntry struct {
	out string
	exp time.Time
}

const (
	cacheTTL = time.Minute
	cacheMax = 256
)

func (c *outputCache) get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.exp) {
		return "", false
	}
	return e.out, true
}

func (c *outputCache) put(k, out string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]cacheEntry{}
	}
	if len(c.m) >= cacheMax {
		now := time.Now()
		for key, e := range c.m {
			if now.After(e.exp) {
				delete(c.m, key)
			}
		}
		if len(c.m) >= cacheMax {
			for key := range c.m {
				delete(c.m, key)
				break
			}
		}
	}
	c.m[k] = cacheEntry{out: out, exp: time.Now().Add(cacheTTL)}
}

// Forget drops the cached output (a template was saved or removed).
func (r *Renderer) Forget() {
	if r == nil {
		return
	}
	r.cache.mu.Lock()
	r.cache.m = nil
	r.cache.mu.Unlock()
	r.gen.Add(1)
}

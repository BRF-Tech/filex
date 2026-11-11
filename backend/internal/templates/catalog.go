package templates

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// Kind is what a template is.
type Kind string

const (
	// KindMail is a mail's content: a subject, an HTML body put into the mail
	// layout, and optionally a plain-text body of its own.
	KindMail Kind = "mail"
	// KindPage is a page filex serves a stranger without JavaScript, put into
	// the page layout.
	KindPage Kind = "page"
	// KindLayout frames every mail (mail.layout) or every page (page.layout):
	// `{{ content }}` is where the mail's or page's own part goes.
	KindLayout Kind = "layout"
	// KindShell is the document the public link page's application is
	// loaded in (page.shell): `{{ app }}` is where it goes.
	KindShell Kind = "shell"
	// KindSlot is a short text the public link page shows in a fixed place of
	// its card (a title, an introduction, a footer line); empty = the page's
	// own words.
	KindSlot Kind = "slot"
)

// Def describes one template filex ships.
type Def struct {
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
	// Layout is the layout the template is put into ("" for none).
	Layout string `json:"layout,omitempty"`
	// Subject: the template has a subject line (a mail).
	Subject bool `json:"subject,omitempty"`
	// Text: the template may have a plain-text body of its own (a mail).
	Text bool `json:"text,omitempty"`
	// Hooks are what the page's own script needs to find in the page: element
	// ids, and `name=…` for a form field. A template without one of them is
	// not saved - the page would stop working.
	Hooks []string `json:"hooks,omitempty"`
	// Vars are the variables the template is given, besides the ones every
	// template has (Globals), as dotted paths.
	Vars []string `json:"vars"`
	// sample is the data the editor's preview and the save check render with.
	sample func() map[string]any
}

// Globals are the variables every template has.
var Globals = []string{
	"lang", "dir",
	"brand.name", "brand.logo_url", "brand.footer_text", "brand.hide_powered_by",
	"theme.accent", "theme.accent_hover", "theme.on_accent",
	"product.name", "product.url", "product.link",
	"site.name", "site.url",
}

// mailGlobals are the variables every mail has, besides Globals.
var mailGlobals = []string{"mail.subject"}

// pageGlobals are the variables every page has, besides Globals.
var pageGlobals = []string{"page.title", "theme.css"}

//go:embed defaults/*.liquid
var defaultsFS embed.FS

// Defs is every template filex ships, in the order the editor lists them.
var Defs = []Def{
	{Key: "mail.layout", Kind: KindLayout, Vars: []string{"content"}, sample: func() map[string]any {
		return map[string]any{"content": sampleSafe("<h1>Report.pdf was shared with you</h1><p>Hello,</p>")}
	}},
	{Key: "mail.share", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"item.name", "item.kind", "item.size", "item.size_text", "link.url", "link.pin", "link.pin_withheld", "link.days_left"},
		sample: func() map[string]any {
			return map[string]any{
				"item": map[string]any{"name": "Q3 report.pdf", "kind": "file", "size": 2_400_000, "size_text": "2.4 MB"},
				"link": map[string]any{"url": "https://files.example.com/s/k3x9p2", "pin": "", "pin_withheld": true, "days_left": 7},
			}
		}},
	{Key: "mail.file_request", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"folder.name", "link.url", "link.pin", "link.pin_withheld", "link.days_left", "limits.max_files", "limits.max_file_mb", "limits.types"},
		sample: func() map[string]any {
			return map[string]any{
				"folder": map[string]any{"name": "Invoices 2026"},
				"link":   map[string]any{"url": "https://files.example.com/d/r7m2q8", "pin": "", "pin_withheld": false, "days_left": 14},
				"limits": map[string]any{"max_files": 20, "max_file_mb": 100, "types": []any{"pdf", "jpg"}},
			}
		}},
	{Key: "mail.grant", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"item.path", "link.url"},
		sample: func() map[string]any {
			return map[string]any{
				"item": map[string]any{"path": "projects://Acme/Brief"},
				"link": map[string]any{"url": "https://files.example.com/admin/explore"},
			}
		}},
	{Key: "mail.account", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"account.email", "account.password", "link.url"},
		sample: func() map[string]any {
			return map[string]any{
				"account": map[string]any{"email": "alice@example.com", "password": "Xq7-pL2m-9wRt"},
				"link":    map[string]any{"url": "https://files.example.com/admin/"},
			}
		}},
	{Key: "mail.notification", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"notice.event", "notice.title", "notice.lines", "link.url"},
		sample: func() map[string]any {
			return map[string]any{
				"notice": map[string]any{"event": "drop.received", "title": "Files were sent to you",
					"lines": []any{"Bob sent 3 files to Invoices 2026 (Bob 2026-11-11)."}},
				"link": map[string]any{"url": "https://files.example.com/admin/"},
			}
		}},
	{Key: "mail.digest", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"notice.title", "notice.lines", "digest.minutes", "link.url"},
		sample: func() map[string]any {
			return map[string]any{
				"notice": map[string]any{"title": "3 things happened in your folders",
					"lines": []any{"Invoices 2026: 2 file uploads", "Brief: 1 comment"}},
				"digest": map[string]any{"minutes": 60},
				"link":   map[string]any{"url": "https://files.example.com/admin/"},
			}
		}},
	{Key: "mail.smtp_test", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true, Vars: []string{},
		sample: func() map[string]any { return map[string]any{} }},
	{Key: "mail.cloud_verify", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"tenant.slug", "verify.token", "verify.ttl"},
		sample: func() map[string]any {
			return map[string]any{
				"tenant": map[string]any{"slug": "acme"},
				"verify": map[string]any{"token": "7f3a9c2e5b81d4f6", "ttl": "24h0m0s"},
			}
		}},
	{Key: "mail.app", Kind: KindMail, Layout: "mail.layout", Subject: true, Text: true,
		Vars: []string{"app.name", "message.subject", "message.body"},
		sample: func() map[string]any {
			return map[string]any{
				"app":     map[string]any{"name": "Sign"},
				"message": map[string]any{"subject": "Please sign the contract", "body": "Hello Alice,\n\nBob asks you to sign Contract.pdf.\nhttps://files.example.com/s/q8w2e4"},
			}
		}},

	{Key: "page.layout", Kind: KindLayout, Vars: []string{"content"}, sample: func() map[string]any {
		return map[string]any{"content": sampleSafe(`<div class="card"><h1>This link is not available</h1></div>`)}
	}},
	{Key: "page.pin", Kind: KindPage, Layout: "page.layout", Hooks: []string{"name=pin"},
		Vars: []string{"form.action", "form.error", "link.kind"},
		sample: func() map[string]any {
			return map[string]any{
				"form": map[string]any{"action": "/s/k3x9p2", "error": ""},
				"link": map[string]any{"kind": "share"},
			}
		}},
	{Key: "page.unlocked", Kind: KindPage, Layout: "page.layout", Vars: []string{},
		sample: func() map[string]any { return map[string]any{} }},
	{Key: "page.error", Kind: KindPage, Layout: "page.layout",
		Vars: []string{"error.code", "error.key", "error.title", "error.body"},
		sample: func() map[string]any {
			return map[string]any{"error": map[string]any{"code": 404, "key": "notfound", "title": "This link is not available", "body": "It may have expired or been withdrawn."}}
		}},
	{Key: "page.zip_wait", Kind: KindPage, Layout: "page.layout", Hooks: []string{"bar", "pct", "dl"},
		Vars: []string{"folder.name", "zip.url"},
		sample: func() map[string]any {
			return map[string]any{"folder": map[string]any{"name": "Photos"}, "zip": map[string]any{"url": "?zip=wait"}}
		}},
	{Key: "page.folder", Kind: KindPage, Layout: "page.layout",
		Vars: []string{"folder.name", "folder.path", "folder.up_url", "folder.zip_url", "folder.gallery", "folder.counts", "entries"},
		sample: func() map[string]any {
			return map[string]any{
				"folder": map[string]any{"name": "Photos", "path": "", "up_url": "", "zip_url": "/s/k3x9p2?zip=1", "gallery": false, "counts": "1 folder · 2 files"},
				"entries": []any{
					map[string]any{"name": "Raw", "kind": "dir", "url": "/s/k3x9p2?dir=Raw", "size": "", "date": "", "thumb": ""},
					map[string]any{"name": "beach.jpg", "kind": "image", "url": "/s/k3x9p2/f/beach.jpg", "size": "3.1 MB", "date": "11.11.2026 09:12", "thumb": ""},
					map[string]any{"name": "notes.txt", "kind": "file", "url": "/s/k3x9p2/f/notes.txt", "size": "2 KB", "date": "10.11.2026 18:40", "thumb": ""},
				},
			}
		}},
	{Key: "page.file_request", Kind: KindPage, Layout: "page.layout",
		Hooks: []string{"card", "sub", "drop", "hint", "file", "nameField", "uploaderName", "note", "files", "bar", "barfill", "msg", "send", "foot"},
		Vars:  []string{"folder.name", "form.name_max"},
		sample: func() map[string]any {
			return map[string]any{"folder": map[string]any{"name": "Invoices 2026"}, "form": map[string]any{"name_max": 40}}
		}},
	{Key: "page.app", Kind: KindPage, Layout: "page.layout",
		Vars: []string{"app.title", "files"},
		sample: func() map[string]any {
			return map[string]any{"app": map[string]any{"title": "Contract.pdf"},
				"files": []any{map[string]any{"name": "Contract.pdf", "url": "/api/public/s/q8w2e4/file/pub:1?download=1"}}}
		}},

	{Key: "page.delete_confirm", Kind: KindPage, Layout: "page.layout", Hooks: []string{"name=confirm"},
		Vars: []string{"file.name", "form.action"},
		sample: func() map[string]any {
			return map[string]any{"file": map[string]any{"name": "screenshot 2026-11-11.png"}, "form": map[string]any{"action": "/x/k3x9p2"}}
		}},
	{Key: "page.delete_done", Kind: KindPage, Layout: "page.layout", Vars: []string{},
		sample: func() map[string]any { return map[string]any{} }},
	{Key: "page.cache_wait", Kind: KindPage, Layout: "page.layout", Hooks: []string{"bar", "pct"},
		Vars: []string{"file.name", "file.percent"},
		sample: func() map[string]any {
			return map[string]any{"file": map[string]any{"name": "Archive 2025.zip", "percent": 35}}
		}},

	{Key: "page.shell", Kind: KindShell, Hooks: []string{"{{ app_head }}", "{{ app }}"}, Vars: []string{"app", "app_head", "link.kind"},
		sample: func() map[string]any {
			return map[string]any{"app": sampleSafe(`<div id="app"></div>`), "app_head": sampleSafe(""), "link": map[string]any{"kind": "share"}}
		}},
	{Key: "slot.share.title", Kind: KindSlot, Vars: slotVars, sample: slotSample},
	{Key: "slot.share.intro", Kind: KindSlot, Vars: slotVars, sample: slotSample},
	{Key: "slot.share.footer", Kind: KindSlot, Vars: slotVars, sample: slotSample},
	{Key: "slot.file_request.title", Kind: KindSlot, Vars: slotVars, sample: slotSample},
	{Key: "slot.file_request.intro", Kind: KindSlot, Vars: slotVars, sample: slotSample},
	{Key: "slot.file_request.footer", Kind: KindSlot, Vars: slotVars, sample: slotSample},
}

var slotVars = []string{"item.name", "item.kind", "item.size", "item.size_text", "link.kind", "link.expires_at", "link.message"}

func slotSample() map[string]any {
	return map[string]any{
		"item": map[string]any{"name": "Q3 report.pdf", "kind": "file", "size": 2_400_000, "size_text": "2.4 MB"},
		"link": map[string]any{"kind": "share", "expires_at": "2026-11-18T10:00:00Z", "message": "Here is the report we talked about."},
	}
}

var byKey = func() map[string]*Def {
	m := make(map[string]*Def, len(Defs))
	for i := range Defs {
		m[Defs[i].Key] = &Defs[i]
	}
	return m
}()

// Lookup is the template named key; nil for a name filex does not ship.
func Lookup(key string) *Def { return byKey[key] }

// Default is filex's own text of a template's part: "" (the body), "subject"
// or "text". "" when it has none.
func Default(key, part string) string {
	name := key
	if part != "" {
		name += "." + part
	}
	b, err := fs.ReadFile(defaultsFS, "defaults/"+name+".liquid")
	if err != nil {
		return ""
	}
	return string(b)
}

// AllVars is every variable a template has: the globals, its kind's and its own.
func (d *Def) AllVars() []string {
	out := append([]string{}, Globals...)
	switch d.Kind {
	case KindMail:
		out = append(out, mailGlobals...)
	case KindPage, KindShell:
		out = append(out, pageGlobals...)
	case KindLayout:
		if strings.HasPrefix(d.Key, "mail.") {
			out = append(out, mailGlobals...)
		} else {
			out = append(out, pageGlobals...)
		}
	}
	out = append(out, d.Vars...)
	sort.Strings(out)
	return out
}

// Sample is the data a preview of the template renders with: realistic values
// for every variable it has.
func (d *Def) Sample() map[string]any {
	if d.sample == nil {
		return map[string]any{}
	}
	return d.sample()
}

// safeString is markup filex itself made, in a template's data: written as
// it is (engine.MarkSafe), never escaped. Callers make one with HTML.
type safeString string

// HTML marks s as markup filex made, for a template's data.
func HTML(s string) any { return safeString(s) }

func sampleSafe(s string) any { return safeString(s) }

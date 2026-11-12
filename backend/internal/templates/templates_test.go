package templates_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func brand(_ context.Context, t templates.Target) templates.Brand {
	b := templates.Brand{Name: "Acme", Accent: "#2f6ceb", AccentHover: "#2559c9", OnAccent: "#ffffff", SiteURL: "https://files.example.com"}
	if t.TenantID == 7 {
		b.Name = "Globex"
	}
	return b
}

var leftover = regexp.MustCompile(`\{\{|\{%|server\.(mail|public)\.`)

// Every template filex ships renders, strictly (every variable it uses is one
// it is given), in both built-in languages: a key that does not exist, a
// variable spelled wrong, a filter that is not there would fail here.
func TestEveryDefaultRendersStrictlyInEveryLanguage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	r := &templates.Renderer{Store: store, Brand: brand}
	for _, d := range templates.Defs {
		if d.Kind == templates.KindSlot {
			continue // empty by default: the page says its own words
		}
		for _, lang := range []string{"en", "tr"} {
			p, err := r.Preview(context.Background(), templates.Target{Lang: lang}, d.Key, nil)
			if err != nil {
				t.Errorf("%s (%s): %v", d.Key, lang, err)
				continue
			}
			if leftover.MatchString(p.HTML) || leftover.MatchString(p.Subject) || leftover.MatchString(p.Text) {
				t.Errorf("%s (%s): unrendered markup or a raw key in the output:\n%s\n%s", d.Key, lang, p.Subject, p.HTML)
			}
			if len(p.Missing) > 0 {
				t.Errorf("%s (%s): filex's own template lacks its hooks %v", d.Key, lang, p.Missing)
			}
			if len(p.Removed) > 0 {
				t.Errorf("%s (%s): the sanitizer had to take %v out of filex's own template", d.Key, lang, p.Removed)
			}
			if d.Kind == templates.KindMail && (strings.TrimSpace(p.Subject) == "" || strings.TrimSpace(p.Text) == "") {
				t.Errorf("%s (%s): no subject or no text part: %q / %q", d.Key, lang, p.Subject, p.Text)
			}
		}
	}
}

// The mail a person gets: HTML with its CSS in the elements, a plain-text
// part with the link, the subject, in their language.
func TestShareMailReadsRight(t *testing.T) {
	r := &templates.Renderer{Brand: brand}
	m, err := r.Mail(context.Background(), templates.Target{Lang: "tr"}, "mail.share", map[string]any{
		"item": map[string]any{"name": "Rapor <1>.pdf", "kind": "file", "size": 2048, "size_text": "2 KB"},
		"link": map[string]any{"url": "https://files.example.com/s/abc", "pin": "", "pin_withheld": true, "days_left": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Rapor <1>.pdf dosyası sizinle paylaşıldı" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{`href="https://files.example.com/s/abc"`, "Rapor &lt;1&gt;.pdf", `style="`, "Acme", "3 gün geçerlidir", `lang="tr"`} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	for _, want := range []string{"https://files.example.com/s/abc", "Rapor <1>.pdf", "PIN"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "<") && strings.Contains(m.Text, "<p>") {
		t.Errorf("markup in the text part:\n%s", m.Text)
	}
}

// A digest's mail says each of the bell's lines on a line of its own, in the
// HTML and in the plain-text part (internal/notify hands the lines over; its
// TestText_ADigestSaysTheSameOnEveryChannel checks what it hands).
func TestDigestMailSaysEachLineOnItsOwnLine(t *testing.T) {
	r := &templates.Renderer{Brand: brand}
	lines := []any{"Gelen: dosya isteğine 2 yükleme", "Arsiv: dosya isteğine 1 yükleme"}
	m, err := r.Mail(context.Background(), templates.Target{Lang: "tr"}, "mail.digest", map[string]any{
		"notice": map[string]any{"title": "3 bildirim", "lines": lines},
		"digest": map[string]any{"minutes": 15},
		"link":   map[string]any{"url": "https://dosya.example.test/admin/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "3 bildirim" {
		t.Errorf("subject %q", m.Subject)
	}
	text := strings.Split(m.Text, "\n")
	for _, l := range lines {
		found := false
		for _, got := range text {
			if strings.TrimSpace(got) == l {
				found = true
			}
		}
		if !found {
			t.Errorf("the text part does not have %q as a line of its own:\n%s", l, m.Text)
		}
	}
	if !strings.Contains(m.HTML, "Gelen: dosya isteğine 2 yükleme<br") {
		t.Errorf("the HTML does not break after the first line:\n%s", m.HTML)
	}
}

func put(t *testing.T, store db.Store, tenant int64, key, body string) *model.Template {
	t.Helper()
	row, err := store.PutTemplate(context.Background(), &model.Template{TenantID: tenant, Key: key, Body: body}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func tenants(t *testing.T, store db.Store) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	a, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Realm: "acme", Name: "Acme", AuthType: model.AuthTypeLocal, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.CreateProvider(ctx, &model.Provider{Slug: "globex", Realm: "globex", Name: "Globex", AuthType: model.AuthTypeLocal, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID, b.ID
}

// ⚠⚠ One tenant's template is that tenant's only: the other tenant gets the
// install's (or filex's own), and the install's applies to every tenant that
// has not replaced it.
func TestATenantsTemplateIsItsOwn(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	a, b := tenants(t, store)
	r := &templates.Renderer{Store: store, MultiTenant: true, Brand: brand}
	ctx := context.Background()
	page := func(tenant int64) string {
		out, err := r.Page(ctx, templates.Target{TenantID: tenant, Lang: "en"}, "page.unlocked", nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	put(t, store, a, "page.unlocked", `<div class="card">ACME-ONLY</div>`)
	if !strings.Contains(page(a), "ACME-ONLY") {
		t.Fatal("tenant A does not get its own template")
	}
	if strings.Contains(page(b), "ACME-ONLY") || strings.Contains(page(0), "ACME-ONLY") {
		t.Fatal("tenant A's template reached tenant B or the install")
	}
	put(t, store, 0, "page.unlocked", `<div class="card">INSTALL</div>`)
	r.Forget()
	if !strings.Contains(page(b), "INSTALL") || !strings.Contains(page(0), "INSTALL") {
		t.Fatal("the install's template does not reach a tenant without its own")
	}
	if !strings.Contains(page(a), "ACME-ONLY") {
		t.Fatal("the install's template overrode tenant A's own")
	}
	// A single-tenant install has no tenants: every template is the install's.
	single := &templates.Renderer{Store: store, Brand: brand}
	out, _ := single.Page(ctx, templates.Target{TenantID: a, Lang: "en"}, "page.unlocked", nil)
	if strings.Contains(out, "ACME-ONLY") {
		t.Fatal("a tenant row counted on a single-tenant install")
	}
}

// A replaced template that fails at render time is noted on its row, and the
// page is filex's own - in time.
func TestABrokenTemplateFallsBackAndIsNoted(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	r := &templates.Renderer{Store: store, Brand: brand}
	ctx := context.Background()
	for name, body := range map[string]string{
		"unknown sentence": `{{ 'public.no_such_key' | t }}`,
		"endless loop":     `{% for i in (1..1000000000) %}{% endfor %}`,
		"memory bomb":      `{% assign s = "xxxxxxxxxx" %}{% for i in (1..40) %}{% assign s = s | append: s %}{% endfor %}{{ s }}`,
	} {
		t.Run(name, func(t *testing.T) {
			row := put(t, store, 0, "page.error", body)
			start := time.Now()
			out, err := r.Page(ctx, templates.Target{Lang: "en"}, "page.error", map[string]any{
				"error": map[string]any{"code": 404, "key": "notfound", "title": "Not found", "body": "Gone."},
			})
			took := time.Since(start)
			if err != nil || !strings.Contains(out, "Not found") || !strings.Contains(out, `class="code"`) {
				t.Fatalf("no fallback: %v\n%s", err, out)
			}
			if took > 2*time.Second {
				t.Fatalf("took %v", took)
			}
			got, _ := store.GetTemplate(ctx, 0, "page.error")
			if got == nil || got.LastError == "" || got.Revision != row.Revision {
				t.Fatalf("the failure was not noted: %+v", got)
			}
		})
	}
}

// Whatever a replaced template writes, and whatever the data hold, no script
// reaches the page.
func TestNoScriptComesOutOfAnEditedPage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	r := &templates.Renderer{Store: store, Brand: brand}
	put(t, store, 0, "page.error", `<div class="card"><script>alert(1)</script><img src=x onerror=alert(2)>`+
		`<a href="javascript:alert(3)">x</a><scr{{ "" }}ipt>alert(4)</script>{{ error.title }}{{ error.title | safe }}</div>`)
	out, err := r.Page(context.Background(), templates.Target{Lang: "en"}, "page.error", map[string]any{
		"error": map[string]any{"code": 404, "key": "x", "title": `<script>alert(5)</script>`, "body": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?i)<script|onerror|javascript:`).MatchString(out) {
		t.Fatalf("a script survived:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(5)") {
		t.Fatalf("the data was not shown as text:\n%s", out)
	}
}

// A self-contained page (an app's public address, #324) names no logo
// address - it is served where it may load nothing - but keeps the brand's
// name and its inline theme; page.route is only ever served that way, so its
// preview is drawn that way too. Other pages keep their logo.
func TestASelfContainedPageNamesNoLogo(t *testing.T) {
	r := &templates.Renderer{Brand: func(context.Context, templates.Target) templates.Brand {
		return templates.Brand{Name: "Acme", LogoURL: "https://cdn.example.com/acme.png", ThemeCSS: "<style>:root{--px-accent:#123456}</style>"}
	}}
	ctx := context.Background()
	data := map[string]any{"error": map[string]any{"code": 404, "key": "route_miss", "title": "Gone", "body": ""}}
	own, err := r.Page(ctx, templates.Target{Lang: "en", SelfContained: true}, "page.error", data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(own, "cdn.example.com") || strings.Contains(own, "<img") {
		t.Fatalf("a self-contained page names the logo:\n%s", own)
	}
	if !strings.Contains(own, "Acme") || !strings.Contains(own, "--px-accent:#123456") {
		t.Fatalf("the brand's name or theme is missing:\n%s", own)
	}
	linked, _ := r.Page(ctx, templates.Target{Lang: "en"}, "page.error", data)
	if !strings.Contains(linked, `src="https://cdn.example.com/acme.png"`) {
		t.Fatalf("a link's page lost its logo:\n%s", linked)
	}
	p, err := r.Preview(ctx, templates.Target{Lang: "en"}, "page.route", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.HTML, "cdn.example.com") || !strings.Contains(p.HTML, "Where this short link goes") {
		t.Fatalf("page.route's preview:\n%s", p.HTML)
	}
}

// The save check: a page that lost what its script needs is refused, and a
// syntax error names its line.
func TestValidateRefusesWhatWouldBreakThePage(t *testing.T) {
	r := &templates.Renderer{Brand: brand}
	ctx := context.Background()
	_, err := r.Validate(ctx, templates.Target{Lang: "en"}, "page.zip_wait", templates.Draft{Body: `<div class="card">{{ folder.name }}</div>`})
	if err == nil {
		t.Fatal("a zip page without its progress bar was accepted")
	}
	p, err := r.Validate(ctx, templates.Target{Lang: "en"}, "page.pin", templates.Draft{Body: "<p>\n{% if form.error %}\n"})
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("syntax error: %v %+v", err, p)
	}
	_, err = r.Validate(ctx, templates.Target{Lang: "en"}, "mail.share", templates.Draft{Body: `{{ item.nmae }}`})
	if err == nil {
		t.Fatal("a misspelt variable was accepted")
	}
	p, err = r.Validate(ctx, templates.Target{Lang: "en"}, "mail.share", templates.Draft{Subject: "Hi {{ item.name }}", Body: `<p>{{ item.name }}</p><script>x()</script>`})
	if err != nil || p.Subject != "Hi Q3 report.pdf" || len(p.Removed) == 0 || strings.Contains(p.HTML, "<script") {
		t.Fatalf("%v %+v", err, p)
	}
}

// The folder page's two layouts (they were share.RenderFolderPage's): a tile
// grid with thumbnails and a play badge, or a list with sizes and dates; and
// the names are text whatever they hold.
func TestFolderPageGalleryAndList(t *testing.T) {
	r := &templates.Renderer{Brand: brand}
	data := func(gallery bool) map[string]any {
		return map[string]any{
			"folder": map[string]any{"name": "Tatil Fotoğrafları", "path": "", "up_url": "", "zip_url": "/s/tok?zip=1", "gallery": gallery, "counts": "1 klasör · 2 dosya"},
			"entries": []any{
				map[string]any{"name": "alt", "kind": "dir", "url": "/s/tok?dir=alt", "size": "", "date": "", "thumb": ""},
				map[string]any{"name": "a.jpg", "kind": "image", "url": "/s/tok/f/a.jpg", "size": "1.0 KB", "date": "01.01.2026 10:00", "thumb": "/s/tok/f/a.jpg?thumb=1"},
				map[string]any{"name": "clip.mp4", "kind": "video", "url": "/s/tok/f/clip.mp4", "size": "2.0 MB", "date": "", "thumb": ""},
				map[string]any{"name": "<img onerror=x>", "kind": "file", "url": "/s/tok/f/x", "size": "", "date": "", "thumb": ""},
			},
		}
	}
	g, err := r.Page(context.Background(), templates.Target{Lang: "tr"}, "page.folder", data(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="ggrid"`, `<img src="/s/tok/f/a.jpg?thumb=1"`, `class="gbadge"`, "Tatil Fotoğrafları", "ile paylaşıldı", `href="/s/tok?zip=1"`, "1 klasör · 2 dosya"} {
		if !strings.Contains(g, want) {
			t.Errorf("gallery lacks %q", want)
		}
	}
	if strings.Contains(g, `class="flist"`) {
		t.Error("gallery drew the list")
	}
	l, err := r.Page(context.Background(), templates.Target{Lang: "tr"}, "page.folder", data(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="flist"`, "1.0 KB", "01.01.2026 10:00"} {
		if !strings.Contains(l, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(l, `class="ggrid"`) || strings.Contains(l, "<img onerror=x>") || strings.Contains(g, "<img onerror=x>") {
		t.Error("list drew the grid, or a name was markup")
	}
}

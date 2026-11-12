package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/shortlink"
	"github.com/brf-tech/filex/backend/internal/templates"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The app route door's pages are templates (#324) ─────────────────────────
//
// page.error for the door's own answers, page.route for an app's page, in
// the address's templates, loading nothing from anywhere, drawn once per
// language, kind and tenant. A door with no registry answers every visit as a
// miss, which is what these tests need of it; an app's page is drawn through
// appPage directly.

// templateReads counts the renderer's template lookups: a page served from
// the door's cache makes none.
type templateReads struct {
	db.Store
	n atomic.Int64
}

func (s *templateReads) GetTemplate(ctx context.Context, tenantID int64, key string) (*model.Template, error) {
	s.n.Add(1)
	return s.Store.GetTemplate(ctx, tenantID, key)
}

type doorPages struct {
	door         *RouteDoor
	store        *templateReads
	rend         *templates.Renderer
	acme, globex int64
}

// newDoorPages is a multi-tenant door with two tenants, acme.test and
// globex.test, each branded with its name, a logo address and an inline
// theme.
func newDoorPages(t *testing.T) *doorPages {
	t.Helper()
	_, raw := dbtest.NewTestDB(t)
	store := &templateReads{Store: raw}
	ctx := context.Background()
	a, err := raw.CreateProvider(ctx, &model.Provider{Slug: "acme", Realm: "acme", Name: "Acme", Host: "acme.test", AuthType: model.AuthTypeLocal, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	g, err := raw.CreateProvider(ctx, &model.Provider{Slug: "globex", Realm: "globex", Name: "Globex", Host: "globex.test", AuthType: model.AuthTypeLocal, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[int64]string{0: "Install", a.ID: "AcmeBrand", g.ID: "GlobexBrand"}
	rend := &templates.Renderer{Store: store, MultiTenant: true}
	rend.SetBrand(func(_ context.Context, tg templates.Target) templates.Brand {
		return templates.Brand{
			Name: names[tg.TenantID], LogoURL: "https://cdn.example.com/logo.png",
			ThemeCSS: "<style>:root{--px-accent:#123456}</style>",
		}
	}, nil)
	d := NewRouteDoor(nil, store, tenanturl.Resolver{}, true)
	d.Templates = rend
	return &doorPages{door: d, store: store, rend: rend, acme: a.ID, globex: g.ID}
}

func (f *doorPages) put(t *testing.T, tenant int64, key, body string) {
	t.Helper()
	cur, _ := f.store.Store.GetTemplate(context.Background(), tenant, key)
	rev := int64(0)
	if cur != nil {
		rev = cur.Revision
	}
	if _, err := f.store.Store.PutTemplate(context.Background(), &model.Template{TenantID: tenant, Key: key, Body: body}, rev); err != nil {
		t.Fatal(err)
	}
	f.rend.Forget() // what the editor's save does
}

// visit is a miss on base, asked at host, in lang.
func (f *doorPages) visit(t *testing.T, method, host string, base shortlink.Base, lang string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+host+"/l/nope", nil)
	r.Header.Set("Accept-Language", lang)
	w := httptest.NewRecorder()
	f.door.Serve(w, r, shortlink.Visit{Base: base, Code: "nope"})
	return w
}

// ownBase is a base on filex's own address; hostBase one on a host of its
// own of tenant.
func ownBase() shortlink.Base { return shortlink.Base{ID: 1, Prefix: "l", App: "short", Route: "go"} }
func hostBase(tenant int64) shortlink.Base {
	return shortlink.Base{ID: 2, Host: "go.acme.test", TenantID: tenant, App: "short", Route: "go"}
}

// ⚠⚠ The page wears the ADDRESS's templates: a host of its own its tenant's,
// filex's own address the tenant of the host the visit arrived on - and one
// tenant's page is never served to another (the cache is per tenant).
func TestRouteDoorPages_TheAddresssTemplates(t *testing.T) {
	f := newDoorPages(t)
	f.put(t, f.acme, "page.error", `<div class="card"><h1>ACME-ONLY {{ error.key }} {{ error.code }}</h1></div>`)

	own := f.visit(t, http.MethodGet, "go.acme.test", hostBase(f.acme), "en")
	if own.Code != http.StatusNotFound || !strings.Contains(own.Body.String(), "ACME-ONLY route_miss 404") {
		t.Fatalf("a host of its own does not wear its tenant's page.error: %d\n%s", own.Code, own.Body.String())
	}
	other := f.visit(t, http.MethodGet, "globex.test", ownBase(), "en")
	if strings.Contains(other.Body.String(), "ACME-ONLY") {
		t.Fatalf("tenant A's page reached tenant B's address:\n%s", other.Body.String())
	}
	if !strings.Contains(other.Body.String(), "This link is not available") || !strings.Contains(other.Body.String(), "GlobexBrand") {
		t.Fatalf("filex's own address does not wear the host's tenant:\n%s", other.Body.String())
	}
	if got := f.visit(t, http.MethodGet, "acme.test", ownBase(), "en"); !strings.Contains(got.Body.String(), "ACME-ONLY") {
		t.Fatalf("filex's own address at tenant A's host does not wear A's template:\n%s", got.Body.String())
	}
	if got := f.visit(t, http.MethodGet, "unknown.test", ownBase(), "en"); strings.Contains(got.Body.String(), "ACME-ONLY") || !strings.Contains(got.Body.String(), "Install") {
		t.Fatalf("an unknown host does not wear the install's:\n%s", got.Body.String())
	}
}

// ⚠⚠ Drawn once per language, kind and tenant: the second visit renders
// nothing (no template is looked up), each language keeps its own page, and a
// template saved meanwhile is shown at once.
func TestRouteDoorPages_DrawnOnceAndShownAgainWhenSaved(t *testing.T) {
	f := newDoorPages(t)
	first := f.visit(t, http.MethodGet, "globex.test", ownBase(), "en")
	reads := f.store.n.Load()
	if reads == 0 {
		t.Fatal("the first visit looked no template up")
	}
	for i := 0; i < 5; i++ {
		again := f.visit(t, http.MethodGet, "globex.test", ownBase(), "en")
		if again.Body.String() != first.Body.String() {
			t.Fatal("the same miss is not the same page")
		}
	}
	if got := f.store.n.Load(); got != reads {
		t.Fatalf("visits after the first rendered the page again (%d template reads, want %d)", got, reads)
	}
	tr := f.visit(t, http.MethodGet, "globex.test", ownBase(), "tr")
	if !strings.Contains(tr.Body.String(), "Bu bağlantı kullanılamıyor") {
		t.Fatalf("the Turkish visitor got another language's page:\n%s", tr.Body.String())
	}

	f.put(t, f.globex, "page.error", `<div class="card"><h1>GLOBEX-EDIT</h1></div>`)
	if got := f.visit(t, http.MethodGet, "globex.test", ownBase(), "en"); !strings.Contains(got.Body.String(), "GLOBEX-EDIT") {
		t.Fatalf("a saved template waits for the cache to run out:\n%s", got.Body.String())
	}
}

// ⚠⚠ Self-contained: an app's host of its own serves nothing else of filex,
// so the door's pages load nothing - no logo address, no stylesheet link, no
// script - under a policy of default-src 'none'; the brand's name and its
// inline theme are still there.
func TestRouteDoorPages_LoadNothing(t *testing.T) {
	f := newDoorPages(t)
	check := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		body := w.Body.String()
		if csp := w.Header().Get("Content-Security-Policy"); csp != routePageCSP || !strings.HasPrefix(csp, "default-src 'none';") {
			t.Errorf("%s: CSP %q", name, csp)
		}
		for _, bad := range []string{"src=", "<link", "url(", "@import", "<script", "cdn.example.com"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: the page asks for something from elsewhere (%s):\n%s", name, bad, body)
			}
		}
		if !strings.Contains(body, "AcmeBrand") || !strings.Contains(body, "--px-accent:#123456") {
			t.Errorf("%s: the brand's name or its inline theme is missing:\n%s", name, body)
		}
	}
	check("miss", f.visit(t, http.MethodGet, "go.acme.test", hostBase(f.acme), "en"))

	locked := shortlink.ClientKey("192.0.2.1")
	for i := 0; i < shortlink.MissBurst; i++ {
		f.door.Misses.Miss(locked)
	}
	busy := f.visit(t, http.MethodGet, "go.acme.test", hostBase(f.acme), "en")
	if busy.Code != http.StatusTooManyRequests || !strings.Contains(busy.Body.String(), "Too many tries") {
		t.Fatalf("not the busy page: %d\n%s", busy.Code, busy.Body.String())
	}
	check("busy", busy)

	w := httptest.NewRecorder()
	f.door.ownPage(w, httptest.NewRequest(http.MethodGet, "http://go.acme.test/x", nil), hostBase(f.acme), http.StatusServiceUnavailable, "en", routeUnavailable)
	check("unavailable", w)

	w = httptest.NewRecorder()
	f.door.appPage(w, httptest.NewRequest(http.MethodGet, "http://go.acme.test/x+", nil), hostBase(f.acme), "en",
		&wire.RoutePage{Title: wire.Text{"en": "Where it goes"}, Link: "https://example.com/next"})
	check("app page", w)
}

// An app's page is page.route: its words in the visitor's language, its
// link a button when it is a web address, the tenant's edit applied, and two
// links' pages never the same page.
func TestRouteDoorPages_AnAppsPage(t *testing.T) {
	f := newDoorPages(t)
	draw := func(lang string, p *wire.RoutePage) string {
		t.Helper()
		w := httptest.NewRecorder()
		f.door.appPage(w, httptest.NewRequest(http.MethodGet, "http://go.acme.test/a+", nil), hostBase(f.acme), lang, p)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		return w.Body.String()
	}
	a := draw("en", &wire.RoutePage{
		Title: wire.Text{"en": "Where this short link goes", "tr": "Bu kısa bağlantı nereye gidiyor"},
		Text:  wire.Text{"en": "https://example.com/a"}, Link: "https://example.com/a",
	})
	for _, want := range []string{"<title>Where this short link goes</title>", "<h1>Where this short link goes</h1>",
		`href="https://example.com/a"`, `rel="noopener noreferrer nofollow"`, "Go to the address"} {
		if !strings.Contains(a, want) {
			t.Errorf("the page lacks %q:\n%s", want, a)
		}
	}
	b := draw("en", &wire.RoutePage{Title: wire.Text{"en": "Another link"}, Link: "javascript:alert(1)"})
	if strings.Contains(b, "Where this short link goes") || !strings.Contains(b, "Another link") {
		t.Fatalf("another link's page is the first one's:\n%s", b)
	}
	if strings.Contains(b, "javascript:") || strings.Contains(b, `class="btn"`) {
		t.Fatalf("a link that is not a web address became a button:\n%s", b)
	}
	if tr := draw("tr", &wire.RoutePage{Title: wire.Text{"en": "Where this short link goes", "tr": "Bu kısa bağlantı nereye gidiyor"}, Link: "https://example.com/a"}); !strings.Contains(tr, "Bu kısa bağlantı nereye gidiyor") || !strings.Contains(tr, "Adrese git") {
		t.Fatalf("not in the visitor's language:\n%s", tr)
	}
	f.put(t, f.acme, "page.route", `<div class="card"><h1>ACME-ROUTE {{ route.title }}</h1></div>`)
	if got := draw("en", &wire.RoutePage{Title: wire.Text{"en": "Where this short link goes"}, Link: "https://example.com/a"}); !strings.Contains(got, "ACME-ROUTE Where this short link goes") {
		t.Fatalf("the tenant's page.route is not used:\n%s", got)
	}
}

// A browser that takes gzip gets the page compressed once, the same page;
// one that does not (or refuses it) gets it as it is.
func TestRouteDoorPages_CompressedForABrowserThatTakesIt(t *testing.T) {
	f := newDoorPages(t)
	get := func(enc string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://go.acme.test/nope", nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		w := httptest.NewRecorder()
		f.door.Serve(w, r, shortlink.Visit{Base: hostBase(f.acme), Code: "nope"})
		return w
	}
	plain := get("")
	if plain.Header().Get("Content-Encoding") != "" || !strings.Contains(plain.Body.String(), "This link is not available") {
		t.Fatalf("a visitor without gzip: %q\n%s", plain.Header().Get("Content-Encoding"), plain.Body.String())
	}
	if plain.Header().Get("Content-Length") != strconv.Itoa(plain.Body.Len()) || plain.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("length %q for %d bytes, Vary %q", plain.Header().Get("Content-Length"), plain.Body.Len(), plain.Header().Get("Vary"))
	}
	z := get("gzip, deflate, br, zstd")
	if z.Header().Get("Content-Encoding") != "gzip" || z.Body.Len() >= plain.Body.Len() {
		t.Fatalf("a browser that takes gzip: %q, %d bytes (plain %d)", z.Header().Get("Content-Encoding"), z.Body.Len(), plain.Body.Len())
	}
	zr, err := gzip.NewReader(bytes.NewReader(z.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	unz, err := io.ReadAll(zr)
	if err != nil || string(unz) != plain.Body.String() {
		t.Fatalf("the compressed page is not the page: %v", err)
	}
	for _, enc := range []string{"gzip;q=0", "br", "identity"} {
		if got := get(enc); got.Header().Get("Content-Encoding") != "" {
			t.Errorf("Accept-Encoding %q got %q", enc, got.Header().Get("Content-Encoding"))
		}
	}
}

// HEAD is answered with the page's status and headers, and no page.
func TestRouteDoorPages_HeadDrawsNothing(t *testing.T) {
	f := newDoorPages(t)
	w := f.visit(t, http.MethodHead, "go.acme.test", hostBase(f.acme), "en")
	if w.Code != http.StatusNotFound || w.Body.Len() != 0 || w.Header().Get("Content-Security-Policy") != routePageCSP {
		t.Fatalf("HEAD: %d, %d bytes, CSP %q", w.Code, w.Body.Len(), w.Header().Get("Content-Security-Policy"))
	}
	if f.store.n.Load() != 0 {
		t.Fatal("HEAD rendered a page")
	}
}

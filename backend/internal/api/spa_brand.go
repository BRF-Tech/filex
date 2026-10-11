package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // a GIF logo decodes too
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // the same formats the branding page accepts

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

// The installed app wears the operator's brand (white label, 0.56).
//
// Reported 2026-10-09: fm.example.com, branded "BRF Teknoloji", installed on a
// phone as plain "filex" with filex's icon - the manifest was the build's,
// byte for byte, whatever the Branding page said. Three filex instances
// installed on one phone were three identical "filex" icons.
//
// The app shell's three branded documents are rewritten per request, from the
// same branding the login page and the public pages use (handlers.BrandingSource,
// host-scoped in multi-tenant mode):
//
//   - manifest.webmanifest - `name`, `short_name`, `theme_color` and `icons`.
//     ⚠⚠ `id`, `start_url` and `scope` are NEVER touched here: `id` is the
//     installed app's identity, and an install whose id changes is a second,
//     separate app on the phone. Only the base path moves them (spa_shell.go).
//   - index.html - `<title>`, the theme-color meta, the favicon and the
//     apple-touch-icon, in the FIRST paint. Not left to the client: Chrome's
//     WebAPK server and iOS "Add to Home Screen" read the documents as served,
//     with no session and no JavaScript.
//   - sw.js - the precache revisions of index.html and the manifest gain the
//     brand's fingerprint. Both documents are precached (vite-plugin-pwa), and
//     a precached copy is answered from the cache until its revision changes:
//     without this an installed app kept the brand it had the day its worker
//     installed until the next filex release.
//
// The icons are rendered here, in Go, from the logo the operator gave
// (brand-icons/<hash>-<size>.png, content-addressed, so Chrome sees a new
// address when the logo changes and updates the WebAPK's icon). A raster logo
// (PNG, JPEG, GIF, WebP data: URI) is resized; an SVG logo is offered as the
// `any` icon beside filex's PNGs, because there is no SVG rasteriser in the
// standard library or x/image and the binary takes no cgo or outside tool for
// one. A logo given as an http(s) address is fetched by the server and drawn
// the same way (#219): it was left out at first, and an instance branded with
// a logo URL installed under filex's icon. The address is one a tenant admin
// chose, so the fetch is the SSRF shape and goes through netguard's download
// client - a public address only, checked after DNS and on every redirect
// hop, no https to http downgrade, a picture type, a size cap and a timeout -
// in the background, never holding a page longer than a short first wait (see
// remoteLogo). A site-relative logo ("/…") names this server and is not
// fetched: the install keeps filex's icons under the brand's name and colour.
//
// Nothing here is a secret: the branding is public (GET /api/branding answers
// the login page), and the manifest and icons stay readable without a session
// - Chrome fetches them without one.

// brandIconDir is the directory the rendered icons are served under, beside
// the manifest that names them.
const brandIconDir = "brand-icons"

// brandIconRenderVersion is mixed into every icon hash: a change to how the
// icons are drawn gives them new addresses, which is what tells an installed
// app to fetch them again.
const brandIconRenderVersion = "wl1"

// brandIconSizes are the PNG sizes rendered: Chrome's install minimum (192)
// and its splash/WebAPK size (512).
var brandIconSizes = []int{192, 512}

// brandLogoMaxSide refuses a logo bigger than this on either side before it
// is decoded: a 256 KB PNG can still claim a 60000x60000 canvas.
const brandLogoMaxSide = 4096

// shellBrander rewrites the app shell's documents for the request's brand.
// A nil *shellBrander, or a brand with nothing set, leaves every document as
// it came (the root deployment is then byte for byte the build).
type shellBrander struct {
	resolve func(*http.Request) handlers.BrandingConfig
	// base is the deployment's base path ("" at the root); the index.html
	// links it writes are absolute and live under it.
	base  string
	icons *brandIconCache
}

func newShellBrander(src *handlers.BrandingSource, base string) *shellBrander {
	if src == nil {
		return nil
	}
	return &shellBrander{resolve: src.ForRequest, base: base, icons: newBrandIconCache()}
}

// shellBrand is the part of the branding the app shell carries.
type shellBrand struct {
	name   string
	accent string
	logo   *brandLogo // nil: no logo, or one that cannot be an icon
}

func (b shellBrand) empty() bool { return b.name == "" && b.accent == "" && b.logo == nil }

// fingerprint changes whenever anything the shell carries changes.
func (b shellBrand) fingerprint() string {
	h := sha256.New()
	h.Write([]byte(b.name + "\x00" + b.accent + "\x00"))
	if b.logo != nil {
		h.Write([]byte(b.logo.hash))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (s *shellBrander) brandFor(r *http.Request) shellBrand {
	if s == nil || s.resolve == nil {
		return shellBrand{}
	}
	return s.brandOf(s.resolve(r))
}

// brandOf is the part of a branding config the app shell carries.
func (s *shellBrander) brandOf(cfg handlers.BrandingConfig) shellBrand {
	if s == nil {
		return shellBrand{}
	}
	return shellBrand{
		name:   strings.TrimSpace(cfg.Name),
		accent: strings.TrimSpace(cfg.Accent),
		logo:   s.icons.logo(strings.TrimSpace(cfg.LogoURL)),
	}
}

// iconHref is an icon's address relative to the manifest (and to every
// directory the SPA is served from).
func (l *brandLogo) iconHref(size int, maskable bool) string {
	if l.svg != nil {
		return brandIconDir + "/" + l.hash + ".svg"
	}
	kind := ""
	if maskable {
		kind = "-maskable"
	}
	return brandIconDir + "/" + l.hash + kind + "-" + strconv.Itoa(size) + ".png"
}

var (
	titleRe      = regexp.MustCompile(`(?s)<title>.*?</title>`)
	themeColorRe = regexp.MustCompile(`<meta name="theme-color" content="[^"]*"`)
	appleIconRe  = regexp.MustCompile(`<link rel="apple-touch-icon" href="[^"]*"`)
	faviconRe    = regexp.MustCompile(`<link rel="icon"[^>]*>`)
	// swRevisionRe finds the precache entries of the two branded documents in
	// the generated worker: `{url:"index.html",revision:"<hex>"}`.
	swRevisionRe = regexp.MustCompile(`(url:"(?:index\.html|manifest\.webmanifest)",revision:")([0-9a-f]+)(")`)
)

// index returns index.html wearing the brand.
func (s *shellBrander) index(doc []byte, r *http.Request) []byte {
	b := s.brandFor(r)
	if b.empty() {
		return doc
	}
	out := string(doc)
	if b.name != "" {
		out = replaceFirst(titleRe, out, "<title>"+html.EscapeString(b.name)+"</title>")
	}
	if b.accent != "" {
		out = replaceFirst(themeColorRe, out, `<meta name="theme-color" content="`+b.accent+`"`)
	}
	if b.logo != nil {
		prefix := s.base + "/admin/"
		if b.logo.svg != nil {
			out = replaceFirst(faviconRe, out, `<link rel="icon" type="image/svg+xml" href="`+prefix+b.logo.iconHref(0, false)+`" />`)
		} else {
			out = replaceFirst(faviconRe, out, `<link rel="icon" type="image/png" href="`+prefix+b.logo.iconHref(192, false)+`" />`)
			// The OPAQUE one: iOS fills a transparent home-screen icon with
			// black, and the maskable rendering stands on a solid background.
			out = replaceFirst(appleIconRe, out, `<link rel="apple-touch-icon" href="`+prefix+b.logo.iconHref(192, true)+`"`)
		}
	}
	return []byte(out)
}

// manifest returns the web app manifest wearing the brand. `id`, `start_url`
// and `scope` are left exactly as they came.
func (s *shellBrander) manifest(doc []byte, r *http.Request) []byte {
	return brandedManifest(doc, s.brandFor(r))
}

// pushIcon is the PNG icon the installed app's manifest names for this
// branding, as an address relative to the manifest and the worker
// (`<base>/admin/`): what a notification shows (#219). Read FROM the
// manifest this branding is served, so a push, the page's own pop-up and the
// home screen cannot show three different pictures: the drawn logo's 192 px
// icon, or filex's own PNG where the logo is an SVG, missing or not drawable.
// "" when the manifest names no PNG.
func (s *shellBrander) pushIcon(built []byte, cfg handlers.BrandingConfig) string {
	return firstPNGIcon(brandedManifest(built, s.brandOf(cfg)))
}

// firstPNGIcon is a manifest's first PNG icon for `any` use (a missing
// purpose is `any`), as written there.
func firstPNGIcon(doc []byte) string {
	var m struct {
		Icons []struct {
			Src     string `json:"src"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if json.Unmarshal(doc, &m) != nil {
		return ""
	}
	for _, ic := range m.Icons {
		isPNG := ic.Type == "image/png" || (ic.Type == "" && strings.HasSuffix(strings.ToLower(ic.Src), ".png"))
		purpose := strings.Fields(ic.Purpose)
		anyUse := len(purpose) == 0
		for _, p := range purpose {
			anyUse = anyUse || p == "any"
		}
		if isPNG && anyUse && ic.Src != "" {
			return ic.Src
		}
	}
	return ""
}

// brandedManifest is a manifest wearing b. Nothing branded: doc as it came.
func brandedManifest(doc []byte, b shellBrand) []byte {
	if b.empty() {
		return doc
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return doc
	}
	if b.name != "" {
		m["name"] = b.name
		m["short_name"] = b.name
	}
	if b.accent != "" {
		// theme_color only: background_color is the splash screen, which
		// should match the page that follows it, not the accent.
		m["theme_color"] = b.accent
	}
	if b.logo != nil {
		m["icons"] = b.logo.manifestIcons(m["icons"])
	}
	out, err := json.Marshal(m)
	if err != nil {
		return doc
	}
	return out
}

// manifestIcons is the icon list for the logo. A raster logo replaces filex's
// icons outright; an SVG logo leads, and filex's PNGs stay behind it for the
// places that take no SVG (Chromium's notification decoder, iOS, WebAPK).
func (l *brandLogo) manifestIcons(built any) []any {
	if l.svg != nil {
		icons := []any{map[string]any{"src": l.iconHref(0, false), "sizes": "any", "type": "image/svg+xml", "purpose": "any"}}
		if list, ok := built.([]any); ok {
			for _, it := range list {
				if m, ok := it.(map[string]any); ok && m["type"] == "image/png" {
					icons = append(icons, m)
				}
			}
		}
		return icons
	}
	var icons []any
	for _, maskable := range []bool{false, true} {
		purpose := "any"
		if maskable {
			purpose = "maskable"
		}
		for _, n := range brandIconSizes {
			icons = append(icons, map[string]any{
				"src": l.iconHref(n, maskable), "sizes": strconv.Itoa(n) + "x" + strconv.Itoa(n), "type": "image/png", "purpose": purpose,
			})
		}
	}
	return icons
}

// serviceWorker returns the generated worker with the brand's fingerprint on
// the precache revisions of index.html and the manifest, so an installed app
// fetches them again when the brand changes. The worker's bytes change with
// it, which is what makes the browser install the new worker.
func (s *shellBrander) serviceWorker(doc []byte, r *http.Request) []byte {
	b := s.brandFor(r)
	if b.empty() {
		return doc
	}
	return swRevisionRe.ReplaceAll(doc, []byte("${1}${2}-"+b.fingerprint()+"${3}"))
}

// serveIcon answers brand-icons/<name>. Only the current logo's addresses
// exist: an address is its content, so an old one is a 404 rather than a new
// picture behind an address a cache was told never changes.
func (s *shellBrander) serveIcon(w http.ResponseWriter, r *http.Request, name string) {
	b := s.brandFor(r)
	if b.logo == nil {
		http.NotFound(w, r)
		return
	}
	data, ct := b.logo.file(name)
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ct == "image/svg+xml" {
		// The operator's SVG, on this origin: opened on its own, it runs
		// nothing.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	}
	_, _ = w.Write(data)
}

func replaceFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + repl + s[loc[1]:]
}

// ─────────────────── the icons ───────────────────

// brandLogo is one logo, decoded once, with its renderings made on first ask.
type brandLogo struct {
	hash string
	img  image.Image // a raster logo
	svg  []byte      // an SVG logo
	bg   color.NRGBA // the maskable icon's background

	mu   sync.Mutex
	pngs map[string][]byte
}

// brandIconCache keeps the decoded logos by their stored reference. A handful
// at most (one per tenant); dropped whole when it grows past that.
type brandIconCache struct {
	mu     sync.Mutex
	logos  map[string]*brandLogo  // data: URIs, decoded
	none   map[string]bool        // data: URIs that cannot be an icon
	remote map[string]*remoteLogo // http(s) addresses, fetched

	// fetch reads a logo address; the guarded download client in service,
	// a stub or a loopback-allowed client in tests.
	fetch func(ctx context.Context, rawURL string) (mime string, raw []byte, err error)
	// now is the clock the refresh times are read from.
	now func() time.Time
	// fetches counts the background fetches in flight (tests wait on it).
	fetches sync.WaitGroup
}

const brandIconCacheMax = 64

func newBrandIconCache() *brandIconCache {
	return &brandIconCache{
		logos:  map[string]*brandLogo{},
		none:   map[string]bool{},
		remote: map[string]*remoteLogo{},
		fetch:  fetchBrandLogo(netguard.Policy{}.DownloadClient(brandRemoteLogoTimeout)),
		now:    time.Now,
	}
}

// logo returns the decoded logo for a stored reference, or nil when there is
// none or it cannot become an icon (a site-relative path, an unreadable
// picture, an address that has not answered with one).
func (c *brandIconCache) logo(ref string) *brandLogo {
	if c == nil {
		return nil
	}
	switch {
	case strings.HasPrefix(ref, "data:image/"):
		return c.dataLogo(ref)
	case strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "http://"):
		return c.remoteLogo(ref)
	}
	return nil
}

func (c *brandIconCache) dataLogo(ref string) *brandLogo {
	c.mu.Lock()
	if l, ok := c.logos[ref]; ok {
		c.mu.Unlock()
		return l
	}
	if c.none[ref] {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	l := decodeBrandLogo(ref)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.logos)+len(c.none) >= brandIconCacheMax {
		c.logos = map[string]*brandLogo{}
		c.none = map[string]bool{}
	}
	if l == nil {
		c.none[ref] = true
	} else {
		c.logos[ref] = l
	}
	return l
}

// decodeBrandLogo reads a data:image/… URI.
func decodeBrandLogo(ref string) *brandLogo {
	comma := strings.IndexByte(ref, ',')
	if comma < 0 {
		return nil
	}
	meta, payload := ref[len("data:"):comma], ref[comma+1:]
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(meta, ";", 2)[0]))
	var raw []byte
	if strings.Contains(strings.ToLower(meta), ";base64") {
		payload = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, payload)
		var err error
		if raw, err = base64.StdEncoding.DecodeString(payload); err != nil {
			if raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "=")); err != nil {
				return nil
			}
		}
	} else {
		s, err := url.PathUnescape(payload)
		if err != nil {
			return nil
		}
		raw = []byte(s)
	}
	return decodeBrandLogoBytes(mime, raw)
}

// decodeBrandLogoBytes makes a logo of a picture's bytes, whichever way they
// came (a data: URI, an address). The hash is the content's: the same picture
// has the same icon addresses, a changed one new addresses.
func decodeBrandLogoBytes(mime string, raw []byte) *brandLogo {
	if len(raw) == 0 {
		return nil
	}
	sum := sha256.Sum256(append([]byte(brandIconRenderVersion+"\x00"), raw...))
	l := &brandLogo{hash: hex.EncodeToString(sum[:])[:16], pngs: map[string][]byte{}}

	if mime == "image/svg+xml" {
		if !bytes.Contains(bytes.ToLower(raw), []byte("<svg")) {
			return nil
		}
		l.svg = raw
		return l
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > brandLogoMaxSide || cfg.Height > brandLogoMaxSide {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	l.img = img
	l.bg = maskableBackground(img)
	return l
}

// ─────────────────── a logo given as an address ───────────────────

const (
	// brandRemoteLogoMaxBytes caps what a logo address may answer with. A
	// data: URI logo is capped at 256 KB of text when it is saved; an address
	// gets more room (a company's own logo file is often bigger), the same
	// 4096 px canvas limit still applies before decoding.
	brandRemoteLogoMaxBytes = 1 << 20
	// brandRemoteLogoTimeout bounds one fetch, body included.
	brandRemoteLogoTimeout = 10 * time.Second
	// brandRemoteLogoTTL is how long a fetched logo is used before the
	// address is read again (a new picture at the same address is a new icon
	// within the hour; Chrome checks an installed app's manifest about daily).
	brandRemoteLogoTTL = time.Hour
	// brandRemoteLogoRetry is how soon an address that did not answer with a
	// picture is tried again.
	brandRemoteLogoRetry = 10 * time.Minute
)

// brandRemoteLogoColdWait is how long a request waits for the FIRST fetch of
// an address before it is answered without the logo: the page a person opens
// right after setting a logo URL wears it, and a slow or dead address costs a
// page this much once, not every time. A variable for the tests only.
var brandRemoteLogoColdWait = 1500 * time.Millisecond

// brandLogoTypes are the answers a logo address may give: the types the
// branding page accepts as a data: URI.
var brandLogoTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/svg+xml": true,
}

// remoteLogo is one logo address: the last picture it answered with, and
// when it was last read.
type remoteLogo struct {
	logo    *brandLogo    // the last good picture; kept through a failed refresh
	checked time.Time     // when the last fetch finished; zero before the first
	failed  bool          // the last fetch did not give a picture
	done    chan struct{} // non-nil while a fetch runs, closed when it ends
}

// remoteLogo answers a logo address from the cache and, when the cached
// answer is missing or old, reads the address again in the background. Only
// the very first read is waited for, and only for brandRemoteLogoColdWait.
func (c *brandIconCache) remoteLogo(ref string) *brandLogo {
	c.mu.Lock()
	e := c.remote[ref]
	if e == nil {
		if len(c.remote) >= brandIconCacheMax {
			c.remote = map[string]*remoteLogo{}
		}
		e = &remoteLogo{}
		c.remote[ref] = e
	}
	if e.done == nil && c.stale(e) {
		e.done = make(chan struct{})
		c.fetches.Add(1)
		go c.refresh(ref, e, e.done)
	}
	logo, done, cold := e.logo, e.done, e.checked.IsZero()
	c.mu.Unlock()

	if logo != nil || !cold || done == nil {
		return logo
	}
	t := time.NewTimer(brandRemoteLogoColdWait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return e.logo
}

// stale says whether an entry is due to be read again. Caller holds c.mu.
func (c *brandIconCache) stale(e *remoteLogo) bool {
	if e.checked.IsZero() {
		return true
	}
	ttl := brandRemoteLogoTTL
	if e.failed {
		ttl = brandRemoteLogoRetry
	}
	return c.now().Sub(e.checked) >= ttl
}

// refresh reads a logo address once and records the outcome. A failure keeps
// the picture the address gave before: a hiccup at the logo's host must not
// turn every installed app back into filex's icon (and change the worker).
func (c *brandIconCache) refresh(ref string, e *remoteLogo, done chan struct{}) {
	defer c.fetches.Done()
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), brandRemoteLogoTimeout)
	defer cancel()

	var l *brandLogo
	mime, raw, err := c.fetch(ctx, ref)
	if err == nil {
		if l = decodeBrandLogoBytes(mime, raw); l == nil {
			err = errors.New("the answer is not a picture filex can draw an icon from")
		}
	}

	c.mu.Lock()
	// Said once per turn for the worse, not every retry.
	tell := err != nil && !e.failed
	e.checked = c.now()
	e.failed = l == nil
	if l != nil && (e.logo == nil || e.logo.hash != l.hash) {
		e.logo = l
	}
	e.done = nil
	c.mu.Unlock()

	if tell {
		slog.Warn("branding: the logo address did not give a picture; the installed app keeps the icon it had (filex's if none)",
			"url", redactURL(ref), "error", err)
	}
}

// redactURL drops a password from an address before it is logged.
func redactURL(ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return "(unparsable)"
	}
	return u.Redacted()
}

// errBrandLogo wraps every refusal of a logo address.
var errBrandLogo = errors.New("branding logo")

// fetchBrandLogo reads a logo address with client: a 200 answer of a picture
// type, at most brandRemoteLogoMaxBytes. The client is netguard's download
// client in service, so the dial, every redirect hop and the scheme of each
// hop are the guard's business, not this function's.
func fetchBrandLogo(client *http.Client) func(ctx context.Context, rawURL string) (string, []byte, error) {
	return func(ctx context.Context, rawURL string) (string, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errBrandLogo, err)
		}
		req.Header.Set("Accept", "image/png,image/webp,image/jpeg,image/gif,image/svg+xml;q=0.9")
		resp, err := client.Do(req)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errBrandLogo, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", nil, fmt.Errorf("%w: the address answered %d", errBrandLogo, resp.StatusCode)
		}
		mime := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
		if !brandLogoTypes[mime] {
			return "", nil, fmt.Errorf("%w: the address answered %q, not a picture", errBrandLogo, mime)
		}
		if resp.ContentLength > brandRemoteLogoMaxBytes {
			return "", nil, fmt.Errorf("%w: the picture is larger than %d bytes", errBrandLogo, brandRemoteLogoMaxBytes)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, brandRemoteLogoMaxBytes+1))
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errBrandLogo, err)
		}
		if len(raw) > brandRemoteLogoMaxBytes {
			return "", nil, fmt.Errorf("%w: the picture is larger than %d bytes", errBrandLogo, brandRemoteLogoMaxBytes)
		}
		return mime, raw, nil
	}
}

// maskableBackground is the colour a maskable icon stands on: the logo's own
// corner when that is opaque (the logo's background simply continues to the
// edge), white otherwise - logos are drawn for a light page far more often
// than for a dark one.
func maskableBackground(img image.Image) color.NRGBA {
	b := img.Bounds()
	c := color.NRGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.NRGBA)
	if c.A >= 250 {
		c.A = 255
		return c
	}
	return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
}

// file returns the bytes and type of one of this logo's icons by its name in
// brand-icons/, or nil for a name it does not have.
func (l *brandLogo) file(name string) ([]byte, string) {
	if l.svg != nil {
		if name == l.hash+".svg" {
			return l.svg, "image/svg+xml"
		}
		return nil, ""
	}
	for _, n := range brandIconSizes {
		for _, maskable := range []bool{false, true} {
			if brandIconDir+"/"+name != l.iconHref(n, maskable) {
				continue
			}
			l.mu.Lock()
			defer l.mu.Unlock()
			if data, ok := l.pngs[name]; ok {
				return data, "image/png"
			}
			data := renderBrandIcon(l.img, n, maskable, l.bg)
			if data == nil {
				return nil, ""
			}
			l.pngs[name] = data
			return data, "image/png"
		}
	}
	return nil, ""
}

// renderBrandIcon draws the logo on an n×n square. `any`: the logo as large
// as it fits, centred, on transparency. `maskable`: on a solid background,
// small enough that its corners stay inside the safe zone - the centred
// circle of 80% the launcher may cut the icon to - whatever shape the launcher
// cuts.
func renderBrandIcon(src image.Image, n int, maskable bool, bg color.NRGBA) []byte {
	sb := src.Bounds()
	w, h := float64(sb.Dx()), float64(sb.Dy())
	if w <= 0 || h <= 0 {
		return nil
	}
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	var scale float64
	if maskable {
		draw.Draw(dst, dst.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
		scale = 0.8 * float64(n) / math.Hypot(w, h)
	} else {
		scale = math.Min(float64(n)/w, float64(n)/h)
	}
	dw, dh := int(math.Round(w*scale)), int(math.Round(h*scale))
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	x0, y0 := (n-dw)/2, (n-dh)/2
	xdraw.CatmullRom.Scale(dst, image.Rect(x0, y0, x0+dw, y0+dh), src, sb, xdraw.Over, nil)
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, dst); err != nil {
		return nil
	}
	return buf.Bytes()
}

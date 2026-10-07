// Package originguard refuses a state-changing request that a browser sent
// from somewhere other than filex itself, on a person's behalf (cross-site
// request forgery).
//
// # What it protects
//
// A browser attaches filex's session cookie to every request it sends to
// filex, whoever wrote the page that sent it. SameSite=Lax keeps the cookie
// off a cross-SITE form post, but a page on a sibling host (any other
// `*.example.com` when filex is `files.example.com`) is the SAME site, and the
// cookie rides along. Before this guard such a page could upload, delete,
// share, mint an API key or install a plugin as whoever was signed in. A
// trusted proxy's header sign-in (proxy-header driver) and the sign-in form
// have the same exposure: the first is as ambient as a cookie, and a forged
// sign-in drops the visitor into somebody else's account.
//
// # How it decides
//
// A request is judged only when all of these hold:
//
//   - its method changes something (anything but GET, HEAD, OPTIONS, TRACE),
//     or it is a WebSocket upgrade;
//   - its path is not one whose credential travels in the request itself
//     (Config.Exempt: share and drop links, upload tickets, signed callbacks);
//   - it carries no credential a page has to attach on purpose: an
//     `Authorization: Bearer` header, an `X-Filex-Token` header, or the
//     `ticket` of a WebSocket upgrade. A browser cannot put those headers on
//     a request to another origin unless that origin's CORS answer lets it
//     (a preflight), so such a request comes from filex's own pages, from an
//     origin the CORS list trusts, or from no browser at all. API keys, the
//     desktop app and every embed that proxies with a key are untouched.
//
// Then the browser's own word decides, the order the Fetch Metadata spec and
// Go's net/http CrossOriginProtection use:
//
//  1. `Sec-Fetch-Site: same-origin` or `none` passes. Every current browser
//     sends this header, and no page can forge it.
//  2. Any other Sec-Fetch-Site (`same-site`, `cross-site`) passes only when
//     the request's Origin (or, without one, its Referer) is trusted:
//     Config.Self or an entry of Config.Trusted. `same-site` is NOT trusted
//     by itself: a sibling host is exactly the page this guard is for.
//  3. Without Sec-Fetch-Site (an older browser), the Origin (or Referer)
//     decides: filex's own host, or a trusted origin, passes; `null` and any
//     other origin are refused.
//  4. Neither header, nor a Referer: not a browser (curl, a script, a test, a
//     native client). It passes, as in net/http: every current browser sends
//     Origin on a state-changing request, and Sec-Fetch-Site on all of them.
//
// A refusal is a 403 `{"error":"cross_origin_refused","message":…}` in the
// visitor's language, a log line and, when the request carried a live session,
// an audit row naming the account (throttled, see record).
package originguard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// ErrorCode is the `error` of a refusal, for clients that match on it.
const ErrorCode = "cross_origin_refused"

// AuditAction is the audit row a refusal writes (see record).
const AuditAction = "auth.cross_origin_refused"

// Reasons a request is refused, as the log line and the audit row say them.
const (
	ReasonCrossSite     = "cross-site"     // Sec-Fetch-Site: cross-site, origin not trusted
	ReasonSameSite      = "same-site"      // Sec-Fetch-Site: same-site, origin not trusted
	ReasonForeignOrigin = "foreign-origin" // no Sec-Fetch-Site, Origin/Referer elsewhere
	ReasonNullOrigin    = "null-origin"    // no Sec-Fetch-Site, Origin: null
)

// tokenHeader is the API-key header auth.extractAPIToken reads.
const tokenHeader = "X-Filex-Token"

// bearerPrefix is the Authorization form the session and API-key drivers read
// (auth/drivers/local.BearerPrefix, auth.extractAPIToken). Case-sensitive, as
// there: a header those drivers do not take as a bearer is not an exemption.
const bearerPrefix = "Bearer "

// Store is what the guard needs from the database: where the audit row goes,
// and whose session a refused request carried. db.Store satisfies it.
type Store interface {
	InsertAuditEntry(ctx context.Context, e *model.AuditEntry) error
	GetSessionByToken(ctx context.Context, token string) (*model.Session, error)
}

// Config is the guard's whole input. Every field may be empty.
type Config struct {
	// Trusted are the other origins that may change things with a visitor's
	// session: the CORS list (FILEX_CORS_ALLOWED_ORIGINS), the one place an
	// operator names the pages that call filex from another origin. Entries
	// match the way the CORS layer (go-chi/cors) matches them: exactly, case
	// insensitively, or with one `*` standing for any run of characters
	// (`https://*.example.com`). A bare `*`, the default, trusts nobody here:
	// it lets any page READ what filex answers without credentials, which says
	// nothing about letting a page WRITE with somebody's session.
	Trusted []string
	// Untrusted are origins never trusted, whatever Trusted says: the origins
	// where filex has code it did not write run - the ONLYOFFICE editor's
	// frame origin, normally the document server's own (task #92), and the
	// app-interface origin. A CORS wildcard such as `https://*.example.com`
	// covers the document server's host too, and must not let the document
	// server's script write with a person's session.
	Untrusted []string
	// Self is filex's own address (FILEX_PUBLIC_URL), trusted like an entry of
	// Trusted. It is how the platform's own pages reach a tenant's host on a
	// multi-tenant install, and how a proxy that rewrites Host still matches.
	// Empty when the operator never set it.
	Self string
	// Exempt are path prefixes (with the base path already removed) whose
	// credential is part of the request: a share or drop link, an upload
	// ticket, a signature. Matched by whole segments: "/s" covers "/s/abc",
	// never "/s3".
	Exempt []string
	// SessionCookie is the session cookie's name, read only to name the
	// account in the audit row.
	SessionCookie string
	// Store writes the audit row. Nil: the log line only.
	Store Store
	// Now is the clock (tests). Nil: time.Now.
	Now func() time.Time
}

// Verdict is what Judge decided.
type Verdict struct {
	Refused bool
	// Reason is one of the Reason* constants when Refused.
	Reason string
	// Site is the Sec-Fetch-Site the request carried ("" for none).
	Site string
	// Source is the origin the request named (Origin, else Referer's origin),
	// lower case; "null" for an opaque one, "" for none.
	Source string
}

// Guard is a configured guard. Build it with New; it is safe for concurrent use.
type Guard struct {
	cfg       Config
	self      string
	exact     map[string]bool
	wild      []wildcard
	untrusted map[string]bool
	exempt    []string
	now       func() time.Time
	limit     limiter
}

// New builds a guard from cfg.
func New(cfg Config) *Guard {
	g := &Guard{cfg: cfg, exact: map[string]bool{}, untrusted: map[string]bool{}, now: cfg.Now}
	if g.now == nil {
		g.now = time.Now
	}
	if o, ok := canonicalOrigin(cfg.Self); ok {
		g.self = o
	}
	for _, raw := range cfg.Untrusted {
		if o, ok := canonicalOrigin(raw); ok {
			g.untrusted[o] = true
		}
	}
	for _, raw := range cfg.Trusted {
		o := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case o == "" || o == "*":
			continue
		case strings.Count(o, "*") == 1:
			i := strings.IndexByte(o, '*')
			g.wild = append(g.wild, wildcard{prefix: o[:i], suffix: o[i+1:]})
		default:
			g.exact[o] = true
			if c, ok := canonicalOrigin(o); ok {
				g.exact[c] = true
			}
		}
	}
	for _, p := range cfg.Exempt {
		p = strings.TrimRight(strings.TrimSpace(p), "/")
		if p != "" {
			g.exempt = append(g.exempt, p)
		}
	}
	g.limit.seen = map[string]time.Time{}
	return g
}

// Middleware refuses what Judge refuses and passes everything else on.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := g.Judge(r)
		if !v.Refused {
			next.ServeHTTP(w, r)
			return
		}
		g.record(r, v)
		lang := srvtext.Pick(srvtext.FromAcceptLanguage(r.Header.Get("Accept-Language")))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   ErrorCode,
			"message": srvtext.Text(lang, "server.origin.refused", nil),
		})
	})
}

// Judge decides one request (see the package comment for the rule).
func (g *Guard) Judge(r *http.Request) Verdict {
	ws := isWebSocketUpgrade(r)
	if !ws && safeMethod(r.Method) {
		return Verdict{}
	}
	if g.isExempt(r.URL.Path) || explicitCredential(r, ws) {
		return Verdict{}
	}
	site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	src := sourceOrigin(r)
	v := Verdict{Site: site, Source: src}
	switch site {
	case "same-origin", "none":
		return Verdict{}
	case "":
		switch {
		case src == "":
			return Verdict{}
		case src == "null":
			v.Refused, v.Reason = true, ReasonNullOrigin
		case sameHost(src, r.Host) || g.trusted(src):
			return Verdict{}
		default:
			v.Refused, v.Reason = true, ReasonForeignOrigin
		}
		return v
	default:
		if g.trusted(src) {
			return Verdict{}
		}
		v.Refused, v.Reason = true, ReasonCrossSite
		if site == "same-site" {
			v.Reason = ReasonSameSite
		}
		return v
	}
}

// trusted reports whether src is filex's own address or a Trusted entry, and
// not an Untrusted one.
func (g *Guard) trusted(src string) bool {
	if src == "" || src == "null" {
		return false
	}
	c, ok := canonicalOrigin(src)
	if g.untrusted[src] || (ok && g.untrusted[c]) {
		return false
	}
	if g.exact[src] {
		return true
	}
	if ok && (c == g.self || g.exact[c]) {
		return true
	}
	for _, w := range g.wild {
		if w.match(src) {
			return true
		}
	}
	return false
}

func (g *Guard) isExempt(path string) bool {
	for _, p := range g.exempt {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// record writes the log line and, for a request that carried a live session,
// the audit row.
//
// Throttled per account (or, without a session, per reason) to one row and one
// warning a minute: a page that loops on a refused request must not be able to
// fill the audit table or the log, and one row a minute is enough for an
// operator to see that somebody's browser is being used. A request with no
// session gets no row at all, because it has no account to have been used
// and anybody can send one with any Origin header, as often as they like.
func (g *Guard) record(r *http.Request, v Verdict) {
	var uid int64
	if g.cfg.Store != nil && g.cfg.SessionCookie != "" {
		if c, err := r.Cookie(g.cfg.SessionCookie); err == nil && c.Value != "" {
			if s, err := g.cfg.Store.GetSessionByToken(r.Context(), c.Value); err == nil && s != nil {
				uid = s.UserID
			}
		}
	}
	key := "anon|" + v.Reason
	if uid != 0 {
		key = "user|" + strconv.FormatInt(uid, 10) + "|" + v.Reason
	}
	attrs := []any{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("origin", v.Source),
		slog.String("sec_fetch_site", v.Site),
		slog.String("reason", v.Reason),
	}
	if !g.limit.allow(key, g.now()) {
		slog.Debug("refused a request sent from another origin", attrs...)
		return
	}
	slog.Warn("refused a request sent from another origin", attrs...)
	if uid == 0 {
		return
	}
	e := &model.AuditEntry{
		UserID: &uid,
		Action: AuditAction,
		Metadata: map[string]any{
			"method": r.Method, "path": r.URL.Path, "origin": v.Source,
			"sec_fetch_site": v.Site, "reason": v.Reason,
		},
		IP: clientip.FromRequest(r),
	}
	if err := g.cfg.Store.InsertAuditEntry(r.Context(), e); err != nil {
		slog.Warn("could not write the audit row for a refused cross-origin request", slog.String("err", err.Error()))
	}
}

// limiter remembers when a key last produced a row.
type limiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

const (
	recordEvery = time.Minute
	recordKeys  = 1024
)

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.seen[key]; ok && now.Sub(last) < recordEvery {
		return false
	}
	if len(l.seen) >= recordKeys {
		clear(l.seen)
	}
	l.seen[key] = now
	return true
}

// wildcard is one `prefix*suffix` entry, matched as go-chi/cors matches it.
type wildcard struct{ prefix, suffix string }

func (w wildcard) match(s string) bool {
	return len(s) >= len(w.prefix)+len(w.suffix) && strings.HasPrefix(s, w.prefix) && strings.HasSuffix(s, w.suffix)
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// isWebSocketUpgrade: a GET that opens a socket acts with the session for as
// long as the socket lives, so it is judged like a write.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func explicitCredential(r *http.Request, ws bool) bool {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearerPrefix) && strings.TrimSpace(h[len(bearerPrefix):]) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get(tokenHeader)) != "" {
		return true
	}
	return ws && r.URL.Query().Get("ticket") != ""
}

// sourceOrigin is the origin the request names: its Origin header, else the
// scheme and host of its Referer. Lower case; "" when it names none.
func sourceOrigin(r *http.Request) string {
	if o := strings.TrimSpace(r.Header.Get("Origin")); o != "" {
		return strings.ToLower(o)
	}
	if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return strings.ToLower(u.Scheme + "://" + u.Host)
		}
	}
	return ""
}

// Canonical is an origin as the guard compares it: scheme://host[:port],
// lower case, without a default port and without a path. false for anything
// that is not an http(s) origin. The CORS layer's refusal of the Untrusted
// origins (api.corsNever) compares the same way.
func Canonical(raw string) (string, bool) { return canonicalOrigin(raw) }

// canonicalOrigin is scheme://host[:port], lower case, without a default port
// and without a path. false for anything that is not an http(s) origin.
func canonicalOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.ToLower(strings.TrimSpace(raw)))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.Scheme + "://" + withoutDefaultPort(u.Scheme, u.Host), true
}

// sameHost reports whether origin src names the host the request was sent to.
// The request's scheme is not known behind a proxy, so only host and port are
// compared (as in net/http); a browser old enough to reach this branch sends
// no Sec-Fetch-Site, and HSTS is what covers its plain-HTTP twin.
func sameHost(src, reqHost string) bool {
	u, err := url.Parse(src)
	if err != nil || u.Host == "" || reqHost == "" {
		return false
	}
	return strings.EqualFold(withoutDefaultPort(u.Scheme, u.Host), withoutDefaultPort(u.Scheme, reqHost))
}

func withoutDefaultPort(scheme, host string) string {
	switch {
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		return strings.TrimSuffix(host, ":443")
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		return strings.TrimSuffix(host, ":80")
	}
	return host
}

package shortlink

import (
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"golang.org/x/net/idna"
)

// ── The addresses an app's public routes are served at ─────────────────────
//
// A base is a host and a path prefix. Two kinds:
//
//   - filex's own address (Host ""): the links live under a prefix of every
//     address filex answers on - `files.example.com/l/<code>`, and on a
//     multi-tenant instance each tenant's own address too. The prefix is
//     required and may not be a path filex itself uses (`s`, `d`, `api` ...).
//   - a host of its own (`go.example.com`, `l.example.com/x`): that host
//     answers its short links and /healthz, and NOTHING else - no sign-in
//     page, no share, no API - so pointing a name at filex for short links
//     opens nothing more than short links. The prefix may be empty.
//
// A base never changes once made (a printed link must keep working): it is
// added, switched off or removed.

// Base is one address short links are served at.
type Base struct {
	ID       int64
	Host     string // lower case, ASCII (xn--); "" = filex's own address
	Prefix   string // "l", "go/x"; "" only on a host of its own
	TenantID int64  // the tenant a host of its own belongs to; 0 = the platform
	Enabled  bool
	// App and Route are whose base this is: the app that answers it and the
	// manifest route it was given to.
	App   string
	Route string
}

// Errors a base is refused with. The API answers them as codes.
var (
	ErrHost         = errors.New("short_link_host_invalid")
	ErrPrefix       = errors.New("short_link_prefix_invalid")
	ErrPrefixInUse  = errors.New("short_link_prefix_reserved")
	ErrPrefixNeeded = errors.New("short_link_prefix_required")
)

var prefixSegRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// CheckPrefix normalises a prefix (no leading or trailing slash, lower case)
// and refuses one that is not 0-3 segments of `[a-z0-9-]`, an empty one on
// filex's own address, or one whose first segment is a path filex uses
// (reserved).
func CheckPrefix(prefix string, ownHost bool, reserved func(first string) bool) (string, error) {
	p := strings.ToLower(strings.Trim(strings.TrimSpace(prefix), "/"))
	if p == "" {
		if ownHost {
			return "", ErrPrefixNeeded
		}
		return "", nil
	}
	segs := strings.Split(p, "/")
	if len(segs) > 3 {
		return "", ErrPrefix
	}
	for _, s := range segs {
		if !prefixSegRe.MatchString(s) {
			return "", ErrPrefix
		}
	}
	if ownHost && reserved != nil && reserved(segs[0]) {
		return "", ErrPrefixInUse
	}
	return p, nil
}

// CheckHost spells a host the way it is stored and compared (lower case, no
// trailing dot, an internationalised name in its xn-- form) and refuses what
// cannot be one: an IP address, a port, a single label.
func CheckHost(host string) (string, error) {
	h := strings.Trim(strings.TrimSpace(host), ".")
	if h == "" || net.ParseIP(h) != nil || strings.ContainsAny(h, "/:@ ?#") {
		return "", ErrHost
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", ErrHost
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 || !strings.Contains(ascii, ".") {
		return "", ErrHost
	}
	return ascii, nil
}

// Routes is a snapshot of the enabled bases, ready to be asked per request.
type Routes struct {
	dedicated map[string][]Base
	own       []Base
}

// NewRoutes builds a snapshot. Disabled bases are left out.
func NewRoutes(bases []Base) *Routes {
	rt := &Routes{dedicated: map[string][]Base{}}
	for _, b := range bases {
		if !b.Enabled {
			continue
		}
		if b.Host == "" {
			rt.own = append(rt.own, b)
			continue
		}
		rt.dedicated[b.Host] = append(rt.dedicated[b.Host], b)
	}
	return rt
}

// Hosts are the hosts of their own, for the certificate question.
func (rt *Routes) Hosts() []string {
	if rt == nil {
		return nil
	}
	out := make([]string, 0, len(rt.dedicated))
	for h := range rt.dedicated {
		out = append(out, h)
	}
	return out
}

// Serves reports whether host is a short-link host of its own.
func (rt *Routes) Serves(host string) bool {
	if rt == nil {
		return false
	}
	_, ok := rt.dedicated[strings.ToLower(strings.TrimSuffix(host, "."))]
	return ok
}

// Visit is one request for a short link, as the path spelled it.
type Visit struct {
	Base Base
	// Code is the code's path segment as it arrived, still escaped.
	Code string
	// Preview: the code was followed by `+` - show where the link goes,
	// do not go there.
	Preview bool
}

// match finds the base a path belongs to: `/<prefix>/<code>` (or `/<code>`
// on a host of its own with no prefix), one segment of code, an optional `+`.
// ok is false when the path is not a short link's at all (the base's prefix
// is not there, or there is more than one segment after it).
func match(bases []Base, escapedPath string) (Visit, bool) {
	p := strings.TrimPrefix(escapedPath, "/")
	for _, b := range bases {
		rest := p
		if b.Prefix != "" {
			if !strings.HasPrefix(p, b.Prefix+"/") {
				continue
			}
			rest = p[len(b.Prefix)+1:]
		}
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		v := Visit{Base: b, Code: rest}
		// `+` ends the code only when it is the last character, escaped or
		// not (%2B): a symbol is never `+`.
		switch {
		case strings.HasSuffix(rest, "+"):
			v.Preview, v.Code = true, strings.TrimSuffix(rest, "+")
		case strings.HasSuffix(strings.ToUpper(rest), "%2B"):
			v.Preview, v.Code = true, rest[:len(rest)-3]
		}
		if v.Code == "" {
			continue
		}
		return v, true
	}
	return Visit{}, false
}

// requestHost is the bare lower-case host a request asked for.
func requestHost(r *http.Request) string {
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

// Snapshot holds the live Routes; a write replaces it whole.
type Snapshot struct{ v atomic.Pointer[Routes] }

// Load answers the current routes (never nil).
func (s *Snapshot) Load() *Routes {
	if rt := s.v.Load(); rt != nil {
		return rt
	}
	return &Routes{}
}

// Store replaces the routes.
func (s *Snapshot) Store(rt *Routes) { s.v.Store(rt) }

// HostSplit is the router's first word on a short-link host of its own:
// /healthz goes on (a probe), a short link is served, and everything else is
// a plain 404 - nothing of filex but its short links is reachable there.
// Every other host goes on untouched.
//
// ⚠ Above the base path: such a host serves its links at ITS root (or its
// own prefix), whatever FILEX_BASE_PATH says - like a dedicated S3 host.
func HostSplit(routes *Snapshot, serve func(http.ResponseWriter, *http.Request, Visit)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt := routes.Load()
			bases, ok := rt.dedicated[requestHost(r)]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			if v, ok := match(bases, r.URL.EscapedPath()); ok {
				serve(w, r, v)
				return
			}
			http.NotFound(w, r)
		})
	}
}

// OwnAddress serves short links under their prefix on filex's own address
// (any host HostSplit let through). Below the base path, so the path it
// reads is the one every route reads.
func OwnAddress(routes *Snapshot, serve func(http.ResponseWriter, *http.Request, Visit)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt := routes.Load()
			if len(rt.own) == 0 || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
				next.ServeHTTP(w, r)
				return
			}
			if v, ok := match(rt.own, r.URL.EscapedPath()); ok {
				serve(w, r, v)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Package proxyheader implements authentication via headers injected by a
// trusted upstream reverse proxy (nginx, Caddy, oauth2-proxy, Cloudflare
// Access, Authelia, …).
//
// The driver only honors headers when the request originates from an IP in
// the configured trusted_proxies CIDR set — otherwise any client could
// forge X-Auth-User and elevate to admin. trusted_proxies is REQUIRED;
// init fails if the list is empty.
//
// On a successful header read, the user is upserted into the local users
// table (when auto_provision=true) and returned directly from
// Authenticate — no session cookie is minted, because the upstream proxy
// is the source of truth for every request.
package proxyheader

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func init() {
	auth.Register("proxy-header", func() auth.Driver { return &Driver{} })
}

// Default header names — overridable in cfg.
const (
	defaultUserHeader  = "X-Auth-User"
	defaultEmailHeader = "X-Auth-Email"
	defaultNameHeader  = "X-Auth-Name"
	defaultRolesHeader = "X-Auth-Roles"
)

// Driver is the trusted-proxy header auth driver.
type Driver struct {
	store db.Store

	mu             sync.RWMutex
	headerUser     string
	headerEmail    string
	headerName     string
	headerRoles    string
	trustedProxies []*net.IPNet
	autoProvision  bool
	adminRole      string // role string in headerRoles that elevates to admin (default "admin")
}

// New constructs an empty driver — Init must be called.
func New(store db.Store) *Driver {
	return &Driver{store: store}
}

// Name implements auth.Driver.
func (d *Driver) Name() string { return "proxy-header" }

// Init validates configuration. trusted_proxies is REQUIRED — without it
// any client could spoof X-Auth-User and elevate themselves to admin.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if d.store == nil {
		return errors.New("proxyheader: nil store")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.headerUser = stringOr(cfg, "header_user", defaultUserHeader)
	d.headerEmail = stringOr(cfg, "header_email", defaultEmailHeader)
	d.headerName = stringOr(cfg, "header_name", defaultNameHeader)
	d.headerRoles = stringOr(cfg, "header_roles", defaultRolesHeader)
	d.adminRole = stringOr(cfg, "admin_role", model.RoleAdmin)
	d.autoProvision = boolOr(cfg, "auto_provision", true)

	raw := stringSlice(cfg, "trusted_proxies")
	if len(raw) == 0 {
		return errors.New("proxyheader: trusted_proxies is required (CIDR list); refusing to start with unrestricted header trust")
	}
	nets := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Bare IP -> /32 or /128.
		if !strings.Contains(s, "/") {
			ip := net.ParseIP(s)
			if ip == nil {
				return fmt.Errorf("proxyheader: invalid trusted_proxy entry %q", s)
			}
			if ip4 := ip.To4(); ip4 != nil {
				s = ip4.String() + "/32"
			} else {
				s = ip.String() + "/128"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return fmt.Errorf("proxyheader: parse CIDR %q: %w", s, err)
		}
		nets = append(nets, n)
	}
	if len(nets) == 0 {
		return errors.New("proxyheader: trusted_proxies parsed to empty set")
	}
	d.trustedProxies = nets
	return nil
}

// Capabilities implements auth.Driver. Header-proxy auth offers no
// in-band sign-in / logout — those are handled by the upstream proxy.
func (d *Driver) Capabilities() auth.Capabilities {
	return auth.Capabilities{
		SignIn:         false,
		Logout:         false,
		ChangePassword: false,
		Register:       false,
	}
}

// Authenticate inspects the request, validates the source IP against
// trusted_proxies, reads the configured headers, and resolves (or
// optionally provisions) a model.User.
//
// Returns auth.ErrUnauthorized when the source is untrusted or the
// identity header is empty — the caller (middleware) will then fall
// through to the next driver.
func (d *Driver) Authenticate(r *http.Request) (*model.User, error) {
	d.mu.RLock()
	headerUser := d.headerUser
	headerEmail := d.headerEmail
	headerName := d.headerName
	headerRoles := d.headerRoles
	autoProvision := d.autoProvision
	adminRole := d.adminRole
	nets := d.trustedProxies
	d.mu.RUnlock()

	if !sourceTrusted(r, nets) {
		return nil, auth.ErrUnauthorized
	}

	uid := strings.TrimSpace(r.Header.Get(headerUser))
	if uid == "" {
		return nil, auth.ErrUnauthorized
	}
	email := strings.ToLower(strings.TrimSpace(r.Header.Get(headerEmail)))
	if email == "" {
		// Fall back to the user identifier when it already looks like an email,
		// otherwise synthesize a stable local-only email.
		if strings.Contains(uid, "@") {
			email = strings.ToLower(uid)
		} else {
			email = strings.ToLower(uid) + "@proxy.local"
		}
	}
	_ = strings.TrimSpace(r.Header.Get(headerName)) // accepted but Users table has no name field today

	role := model.RoleUser
	if rawRoles := r.Header.Get(headerRoles); rawRoles != "" {
		for _, p := range strings.Split(rawRoles, ",") {
			if strings.EqualFold(strings.TrimSpace(p), adminRole) {
				role = model.RoleAdmin
				break
			}
		}
	}

	ctx := r.Context()
	user, err := d.store.GetUserByEmail(ctx, email)
	if err != nil {
		if !autoProvision {
			return nil, auth.ErrUnauthorized
		}
		user, err = d.store.CreateUser(ctx, email, "", role, "en", "UTC")
		if err != nil {
			return nil, fmt.Errorf("proxyheader: provision user: %w", err)
		}
	}
	_ = d.store.TouchLastLogin(ctx, user.ID)
	return user, nil
}

// sourceTrusted returns true when the request's RemoteAddr (after
// accounting for an upstream-set X-Forwarded-For optional override is
// NOT honored here — trust comes from the direct peer only) belongs to
// a configured CIDR.
//
// Honoring XFF would defeat the entire trust model: if the proxy injects
// XFF then the proxy is the trusted peer and that's what we check.
func sourceTrusted(r *http.Request, nets []*net.IPNet) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// stringOr extracts a string from cfg or returns def.
func stringOr(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return def
}

// boolOr extracts a bool from cfg or returns def.
func boolOr(cfg map[string]any, key string, def bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return def
}

// stringSlice accepts either []string or []any (yaml may decode lists as
// []any) under cfg[key] and returns a []string.
func stringSlice(cfg map[string]any, key string) []string {
	if v, ok := cfg[key].([]string); ok {
		return v
	}
	if v, ok := cfg[key].([]any); ok {
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

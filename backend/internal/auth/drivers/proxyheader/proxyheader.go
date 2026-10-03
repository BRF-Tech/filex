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
	// firstLogin is what happens to a person the proxy names who has no account
	// yet: auto_create (older name: auto_provision) and allowed_groups, judged
	// against the roles header's values (auth.ProvisionFirstLogin).
	firstLogin auth.FirstLoginPolicy
	adminRole  string // role string in headerRoles that elevates to admin (default "admin")
	// homing decides which tenant a just-in-time account lands in. Zero value
	// (MultiTenant=false) is the single-tenant install and does nothing.
	homing auth.TenantHoming
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
	return d.load(cfg)
}

// load reads a configuration into the driver — everything Init does except
// needing a store, so Probe tests a configuration the way it would run.
func (d *Driver) load(cfg map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.headerUser = stringOr(cfg, "header_user", defaultUserHeader)
	d.headerEmail = stringOr(cfg, "header_email", defaultEmailHeader)
	d.headerName = stringOr(cfg, "header_name", defaultNameHeader)
	d.headerRoles = stringOr(cfg, "header_roles", defaultRolesHeader)
	d.adminRole = stringOr(cfg, "admin_role", model.RoleAdmin)
	d.firstLogin = auth.FirstLoginPolicyFrom(cfg)
	d.homing.MultiTenant = boolOr(cfg, "multi_tenant", false)
	d.homing.Pin = stringOr(cfg, "provider", "")

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
	firstLogin := d.firstLogin
	adminRole := d.adminRole
	nets := d.trustedProxies
	homing := d.homing
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
	// The roles header doubles as the person's groups: allowed_groups is judged
	// against them, admin_role is looked for in them, and they are recorded
	// (below). Every line of it counts: fields of one name are one
	// comma-separated list (RFC 9110, section 5.3), however the proxy splits
	// them.
	//
	// ⚠ Absent and empty are two answers. No roles header at all is a proxy
	// that says nothing about groups - often one that sends none, or a request
	// its group lookup did not run for - so what the last request recorded
	// stays. A header that is there and empty is the proxy saying "no groups",
	// and the linked groups are left. (For allowed_groups both are "in no
	// group": a person with no account yet has nothing recorded to fall back
	// on.)
	lines, saysGroups := r.Header[http.CanonicalHeaderKey(headerRoles)]
	groups := auth.SplitList(strings.Join(lines, ","))
	for _, g := range groups {
		if strings.EqualFold(g, adminRole) {
			role = model.RoleAdmin
			break
		}
	}

	// ⚠ The Host the client asked for, stamped so ProvisionUser can home a new
	// account in that tenant. Unlike the LDAP driver this one runs INSIDE an
	// *http.Request, so the signal is always there.
	ctx := auth.WithRequestLoginHost(r.Context(), r)
	created := false
	user, err := d.store.GetUserByEmail(ctx, email)
	if err != nil {
		// ⚠⚠ NOT store.CreateUser directly — that homes the account in
		// `default`, which is seeded is_supertenant = 1 and therefore
		// confine-EXEMPT. On a multi-tenant install, header-trust
		// auto-provisioning was minting an account that could reach every
		// storage on the box for anybody the upstream proxy named.
		//
		// ⚠ The first-login rule sits in front: auto_create off, or
		// allowed_groups with no match, answers 401 - and the 401 carries the
		// reason code (auth.RefusalToTell, written by auth.Middleware), so the
		// sign-in page can say why. The proxy has already said who the person
		// is: there is no password here to confirm. The reason is also in the
		// log and the audit row.
		user, err = auth.ProvisionFirstLogin(ctx, d.store, auth.FirstLogin{
			Driver: "proxyheader", Identifier: uid, Email: email, Role: role,
			Groups: groups, Policy: firstLogin, Homing: homing,
		})
		if err != nil {
			if errors.Is(err, auth.ErrFirstLoginRefused) {
				return nil, auth.RefusalToTell(err)
			}
			return nil, fmt.Errorf("proxyheader: provision user: %w", err)
		}
		created = true
	}
	if saysGroups {
		// The header's groups, recorded and REPLACED - the proxy is the
		// authority on membership - through the one rule every provider shares
		// (auth.RecordSignInGroups): a new account starts with the role they
		// name, the filex groups of the account's own tenant linked to them are
		// joined and left, and the level a group's role sets is checked. With or
		// without allowed_groups, which only decides who gets an account.
		//
		// Only when the set changed: every request is a sign-in here, and the
		// same groups again would rewrite the same rows each time.
		if cur, lerr := d.store.ListUserSSOGroups(ctx, user.ID); created || lerr != nil || !sameGroups(cur, groups) {
			user = auth.RecordSignInGroups(ctx, d.store, "proxyheader", user, groups, created)
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

// stringSlice accepts []string, []any (yaml may decode lists as []any) or a
// comma-separated string (what the admin panel's form sends and the settings
// table stores) under cfg[key] and returns a []string.
func stringSlice(cfg map[string]any, key string) []string {
	if v, ok := cfg[key].([]string); ok {
		return v
	}
	if v, ok := cfg[key].(string); ok {
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
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

// sameGroups reports whether two group lists hold the same names (order and
// repeats aside).
func sameGroups(a, b []string) bool {
	set := func(l []string) map[string]struct{} {
		m := make(map[string]struct{}, len(l))
		for _, g := range l {
			m[g] = struct{}{}
		}
		return m
	}
	x, y := set(a), set(b)
	if len(x) != len(y) {
		return false
	}
	for g := range x {
		if _, ok := y[g]; !ok {
			return false
		}
	}
	return true
}

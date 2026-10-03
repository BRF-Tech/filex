// Package multioidc dispatches OIDC flows to per-tenant IdP realms
// (docs/MULTI-TENANCY.md §5,§7).
//
// In multi-tenant mode each provider row may carry its own OIDC config
// (issuer/client/secret — e.g. one Keycloak realm per tenant, each on its own
// host). TenantDriver builds a lazily initialised, cached oidc.Driver pinned
// to that provider (SetProviderID): it signs people in to that tenant only.
//
// ⚠ A sign-in is started and finished through authsetup's flows
// (oidcflow.go), which name the tenant it is for. The host dispatch below
// (resolve, falling back to the config-file driver) is what EndSessionURL
// uses; a callback without its flow is refused on a multi-tenant install,
// never answered by the fallback (docs/SSO.md, "Which account an SSO sign-in
// opens").
package multioidc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/auth"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Dispatcher implements auth.OIDCDriver over N per-tenant realms.
type Dispatcher struct {
	store    db.Store
	fallback auth.OIDCDriver // config-file driver (may be nil)

	mu    sync.Mutex
	cache map[int64]*entry
}

type entry struct {
	drv  *authoidc.Driver
	hash string // config fingerprint; re-init when the provider row changes
}

// New wraps the config-file OIDC driver (may be nil) with per-tenant dispatch.
func New(store db.Store, fallback auth.OIDCDriver) *Dispatcher {
	return &Dispatcher{store: store, fallback: fallback, cache: map[int64]*entry{}}
}

// StartFlow implements auth.OIDCDriver.
func (m *Dispatcher) StartFlow(w http.ResponseWriter, r *http.Request) error {
	drv, err := m.resolve(r)
	if err != nil {
		return err
	}
	return drv.StartFlow(w, r)
}

// HandleCallback implements auth.OIDCDriver.
func (m *Dispatcher) HandleCallback(w http.ResponseWriter, r *http.Request) (*model.User, string, error) {
	drv, err := m.resolve(r)
	if err != nil {
		return nil, "", err
	}
	return drv.HandleCallback(w, r)
}

// EndSessionURL implements auth.OIDCLogoutDriver on the tenant's own driver —
// the host resolves exactly as it did for sign-in. "" (sign-out stays local)
// when the host has no IdP, the IdP cannot be reached for discovery, or the
// driver does not do RP-initiated logout.
func (m *Dispatcher) EndSessionURL(r *http.Request, idToken, postLogoutRedirect string) string {
	drv, err := m.resolve(r)
	if err != nil {
		return ""
	}
	lo, ok := drv.(auth.OIDCLogoutDriver)
	if !ok {
		return ""
	}
	return lo.EndSessionURL(r, idToken, postLogoutRedirect)
}

// HasOwnOIDC reports whether a tenant's provider row carries an OIDC of its
// own: the tenant's own SSO (docs/TENANT-ADMIN.md), started for that tenant
// only.
func HasOwnOIDC(p *model.Provider) bool {
	return p != nil && p.AuthType == model.AuthTypeOIDC && p.OIDCIssuer != "" && p.OIDCClientID != ""
}

// TenantDriver is the driver of a tenant's own OIDC (its provider row), built
// once per configuration and cached; host is the address the flow runs on, for
// the default redirect URI.
func (m *Dispatcher) TenantDriver(ctx context.Context, p *model.Provider, host string) (auth.OIDCDriver, error) {
	if !HasOwnOIDC(p) {
		return nil, errors.New("oidc: this tenant has no identity provider of its own")
	}
	return m.driverFor(ctx, p, host)
}

// resolve maps the request host to a tenant OIDC driver, or the fallback.
func (m *Dispatcher) resolve(r *http.Request) (auth.OIDCDriver, error) {
	p, _ := m.store.GetProviderByHost(r.Context(), RequestHost(r))
	if !HasOwnOIDC(p) {
		if m.fallback == nil {
			return nil, errors.New("oidc: no identity provider for this host")
		}
		return m.fallback, nil
	}
	return m.driverFor(r.Context(), p, RequestHost(r))
}

// driverFor returns the cached driver for p, (re)initialising it when the
// provider row changed. Discovery runs once per provider per config version.
func (m *Dispatcher) driverFor(ctx context.Context, p *model.Provider, host string) (auth.OIDCDriver, error) {
	redirect := p.OIDCRedirectURL
	if redirect == "" {
		// Each tenant lives on its own host; default the redirect there. TLS is
		// assumed — multi-tenant hosts sit behind the reverse proxy that
		// terminates HTTPS (see docs/MULTI-TENANCY.md §13). Under a base path
		// (FILEX_BASE_PATH) every tenant host serves filex under it too.
		redirect = "https://" + host + basepath.From(ctx) + "/api/auth/oidc/callback"
	}
	hash := strings.Join([]string{p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret, redirect, p.RoleClaim, p.AdminGroup, fmt.Sprint(p.OIDCTrustEmail)}, "\x00")

	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.cache[p.ID]; ok && e.hash == hash {
		return e.drv, nil
	}
	drv := authoidc.New(m.store)
	if err := drv.Init(ctx, map[string]any{
		"issuer":        p.OIDCIssuer,
		"client_id":     p.OIDCClientID,
		"client_secret": p.OIDCClientSecret,
		"redirect_url":  redirect,
		"role_claim":    p.RoleClaim,
		"admin_group":   p.AdminGroup,
		"trust_email":   p.OIDCTrustEmail,
	}); err != nil {
		return nil, err
	}
	drv.SetProviderID(p.ID)
	m.cache[p.ID] = &entry{drv: drv, hash: hash}
	return drv, nil
}

// RequestHost extracts the bare hostname (no port) the client asked for.
// Behind the reverse proxy filex trusts the proxied Host header (the proxy is
// the only reachable path in the documented deployments — §13 trusted-host).
func RequestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

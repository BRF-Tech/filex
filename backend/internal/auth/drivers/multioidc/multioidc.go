// Package multioidc dispatches OIDC flows to per-tenant IdP realms
// (docs/MULTI-TENANCY.md §5,§7).
//
// In multi-tenant mode each provider row may carry its own OIDC config
// (issuer/client/secret — e.g. one Keycloak realm per tenant, each on its own
// host). The dispatcher resolves the request Host → provider → a lazily
// initialised, cached oidc.Driver pinned to that provider (SetProviderID), so
// the JIT upsert stamps users with the right tenant. Hosts that resolve to no
// provider — or to a provider without OIDC config — fall back to the
// config-file driver (the single-tenant/supertenant realm), so the flag being
// on never breaks the operator's own login.
//
// The OIDC callback arrives on the same host that started the flow (each
// tenant's redirect URL lives on its own host), so StartFlow and
// HandleCallback resolve to the same provider by construction.
package multioidc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/auth"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
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

// resolve maps the request host to a tenant OIDC driver, or the fallback.
func (m *Dispatcher) resolve(r *http.Request) (auth.OIDCDriver, error) {
	p, _ := m.store.GetProviderByHost(r.Context(), RequestHost(r))
	if p == nil || p.AuthType != model.AuthTypeOIDC || p.OIDCIssuer == "" || p.OIDCClientID == "" {
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
		// terminates HTTPS (see docs/MULTI-TENANCY.md §13).
		redirect = "https://" + host + "/api/auth/oidc/callback"
	}
	hash := strings.Join([]string{p.OIDCIssuer, p.OIDCClientID, p.OIDCClientSecret, redirect, p.RoleClaim, p.AdminGroup}, "\x00")

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

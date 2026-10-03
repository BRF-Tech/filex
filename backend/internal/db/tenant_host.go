package db

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ── The addresses a tenant answers on (docs/TENANT-ADMIN.md) ────────────────
//
// GetProviderByHost is the one question every reader of a request host asks
// (tenant resolution, the realm rule, the capabilities' tenant block, cookie
// domain, OIDC redirect, minted links). A tenant answers on three kinds of
// address, and each engine's GetProviderByHost asks them in this order:
//
//  1. its `host` (providers.host, the operator's);
//  2. an own domain of it that is ACTIVE (provider_domains, proven by a CNAME
//     to the tenant's platform subdomain);
//  3. its platform subdomain, `<realm>.<tenant domain>`, when the
//     installation has a tenant domain (FILEX_TENANT_DOMAIN).
//
// Only an enabled tenant answers, and never the platform's own tenant on 2 or
// 3: the platform is reached on its own address.

var tenantDomain atomic.Value // string

// SetTenantDomain sets the installation's tenant domain (FILEX_TENANT_DOMAIN):
// `tenants.files.example` gives every tenant the address
// `<realm>.tenants.files.example`. "" switches platform subdomains off. Set
// once at boot (server.New); tests set and reset it.
//
// ⚠ A process-wide value and not a Store field: the store is wrapped and
// handed around (identitystore, quotastore, tenantstore) long before anyone
// could thread a setting through every wrapper, and every one of them must
// answer the same.
func SetTenantDomain(d string) {
	tenantDomain.Store(strings.Trim(strings.ToLower(strings.TrimSpace(d)), "."))
}

// TenantDomain returns the installation's tenant domain, "" for none.
func TenantDomain() string {
	v, _ := tenantDomain.Load().(string)
	return v
}

// PlatformSubdomain is a tenant's address under the tenant domain, "" when
// there is none (no tenant domain, the platform's own tenant, no realm).
func PlatformSubdomain(p *model.Provider) string {
	td := TenantDomain()
	if td == "" || p == nil || p.IsSupertenant || p.Realm == "" {
		return ""
	}
	return p.Realm + "." + td
}

// RealmOfPlatformSubdomain returns the realm a host names when it is a
// platform subdomain (`acme.tenants.files.example` → `acme`), "" otherwise.
// Only one label sits in front of the tenant domain.
func RealmOfPlatformSubdomain(host string) string {
	td := TenantDomain()
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	if td == "" || !strings.HasSuffix(host, "."+td) {
		return ""
	}
	label := strings.TrimSuffix(host, "."+td)
	if label == "" || strings.Contains(label, ".") {
		return ""
	}
	return label
}

// extraHostStore is what ResolveExtraHost reads.
type extraHostStore interface {
	ProviderIDByActiveDomain(ctx context.Context, domain string) (int64, error)
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	GetProviderByRealm(ctx context.Context, realm string) (*model.Provider, error)
}

// ResolveExtraHost answers steps 2 and 3 above for a host no provider's
// `host` claims: the enabled tenant an active own domain or a platform
// subdomain names, or (nil, nil).
func ResolveExtraHost(ctx context.Context, s extraHostStore, host string) (*model.Provider, error) {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return nil, nil
	}
	if id, err := s.ProviderIDByActiveDomain(ctx, host); err != nil {
		return nil, err
	} else if id != 0 {
		p, err := s.GetProvider(ctx, id)
		if err == nil && p != nil && p.Enabled && !p.IsSupertenant {
			return p, nil
		}
	}
	if realm := RealmOfPlatformSubdomain(host); realm != "" {
		p, err := s.GetProviderByRealm(ctx, realm)
		if err != nil {
			return nil, err
		}
		if p != nil && p.Enabled && !p.IsSupertenant {
			return p, nil
		}
	}
	return nil, nil
}

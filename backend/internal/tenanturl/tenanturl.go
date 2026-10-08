// Package tenanturl answers one question for the whole server: which origin
// should an absolute URL that filex hands out be built on?
//
// ⚠ Every absolute URL filex mints for a person or a client — share links,
// file-request links, the wss:// realtime endpoint, upload-ticket URLs and
// every link inside an e-mail — MUST be built from a Resolver. Reaching for
// config.PublicURL directly is correct only in a single-tenant install; in a
// multi-tenant one it hands the OPERATOR's hostname to a customer. That is a
// dead link at best, and at worst (handlers.Grants.Invite, the mail carrying
// a brand-new account's temporary password) a login page on somebody else's
// domain.
//
// Two ways in, because not every URL is built while a browser waits:
//
//   - FromRequest(r)           the browser is right there, so use the host it
//     asked for — once that host is proven to be a
//     tenant's.
//   - ForStorage / ForProvider no *http.Request in scope (an async op, a queue
//     worker, an MCP/AI call). The tenant then comes
//     from the DATA — the node's storage, or the
//     provider id — never from a guess.
//
// # Forged Host headers
//
// A request host is never echoed back unvalidated. In multi-tenant mode the
// host is resolved with GetProviderByHost, which matches an ENABLED provider
// row on an exact host equality; the origin is then assembled from that ROW's
// Host column — a value an operator provisioned — and not from the request
// string. An unknown, disabled or absent host falls through to PublicURL,
// which is the rule handlers.Auth.redirectBase has always used. So a request
// carrying `Host: evil.example` mints the operator's configured URL; evil.example
// never appears in a link or an e-mail.
package tenanturl

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Store is the slice of db.Store this package needs. Narrow on purpose: it
// keeps the resolver trivially fakeable and documents that resolving an
// origin reads provider rows and nothing else.
type Store interface {
	GetProviderByHost(ctx context.Context, host string) (*model.Provider, error)
	GetProvider(ctx context.Context, id int64) (*model.Provider, error)
	GetProviderIDForStorage(ctx context.Context, storageID int64) (int64, bool, error)
}

// Resolver builds the origin ("https://host", no trailing slash) that absolute
// URLs should be assembled on.
//
// The zero value is usable and behaves as a single-tenant install with an
// empty PublicURL — i.e. it yields "", so callers that concatenate produce the
// relative "/s/<token>" they produced before this package existed.
type Resolver struct {
	// PublicURL is the install's configured base (FILEX_PUBLIC_URL). It is the
	// answer in single-tenant mode and the fallback in every other case.
	PublicURL string
	// MultiTenant mirrors config.MultiTenant. While false the request is never
	// consulted at all, so a single-tenant install cannot be talked into
	// minting a different origin, and call sites that have no request lose
	// nothing.
	MultiTenant bool
	// Store reads provider rows. nil disables tenant resolution (fallback only).
	Store Store
}

// New builds a resolver. publicURL is normalised once here so no caller has to
// remember to trim it.
func New(store Store, publicURL string, multiTenant bool) Resolver {
	return Resolver{
		PublicURL:   strings.TrimRight(strings.TrimSpace(publicURL), "/"),
		MultiTenant: multiTenant,
		Store:       store,
	}
}

// Fallback is the configured origin, trimmed. Exported because a caller that
// already knows it wants the install's own URL (an operator-facing screen)
// should say so rather than reading PublicURL raw.
func (rv Resolver) Fallback() string {
	return strings.TrimRight(strings.TrimSpace(rv.PublicURL), "/")
}

// FromRequest returns the origin for a URL minted while r is being served.
//
// Single-tenant: PublicURL, and r is not read — see the MultiTenant field.
// Multi-tenant: the request's Host when (and only when) it resolves to an
// enabled provider row; anything else falls back to PublicURL.
func (rv Resolver) FromRequest(r *http.Request) string {
	if !rv.MultiTenant || rv.Store == nil || r == nil {
		return rv.Fallback()
	}
	host := RequestHost(r)
	if host == "" {
		return rv.Fallback()
	}
	p, err := rv.Store.GetProviderByHost(r.Context(), host)
	if err != nil || p == nil {
		return rv.Fallback()
	}
	if a := rv.addressOf(r.Context(), p, host); a != "" {
		return rv.origin(a, rv.scheme(r))
	}
	return rv.Fallback()
}

// ForProvider returns a tenant's origin without a request — for the e-mails
// and async work that run after the browser has gone.
func (rv Resolver) ForProvider(ctx context.Context, providerID int64) string {
	if !rv.MultiTenant || rv.Store == nil || providerID == 0 {
		return rv.Fallback()
	}
	p, err := rv.Store.GetProvider(ctx, providerID)
	if err != nil || p == nil || !p.Enabled {
		return rv.Fallback()
	}
	if a := rv.addressOf(ctx, p, ""); a != "" {
		return rv.origin(a, rv.scheme(nil))
	}
	return rv.Fallback()
}

// domainLister is the store's list of a tenant's own domains
// (docs/TENANT-ADMIN.md). Optional: a store without it knows no own domains.
type domainLister interface {
	ListProviderDomains(ctx context.Context, providerID int64) ([]*model.ProviderDomain, error)
}

// addressOf picks the address a tenant's links are minted on, "" for none:
//
//  1. its `host`, the operator's, when set (as before own domains existed);
//  2. the address the request arrived on, when that is one of the tenant's
//     own: its platform subdomain or an ACTIVE own domain, compared with what
//     the data says, so the request's string is never echoed unproven;
//  3. its first active own domain;
//  4. its platform subdomain.
//
// The platform's own tenant has neither own domains nor a subdomain.
func (rv Resolver) addressOf(ctx context.Context, p *model.Provider, arrived string) string {
	if p.Host != "" {
		return p.Host
	}
	if p.IsSupertenant {
		return ""
	}
	sub := db.PlatformSubdomain(p)
	if arrived != "" && arrived == sub {
		return sub
	}
	first := ""
	if dl, ok := rv.Store.(domainLister); ok {
		if ds, err := dl.ListProviderDomains(ctx, p.ID); err == nil {
			for _, d := range ds {
				if d == nil || d.Status != model.DomainActive {
					continue
				}
				if arrived != "" && d.Domain == arrived {
					return d.Domain
				}
				if first == "" {
					first = d.Domain
				}
			}
		}
	}
	if first != "" {
		return first
	}
	return sub
}

// ForStorage returns the origin of the tenant a storage belongs to. A storage
// linked to no provider (the operator's own, or any storage in a single-tenant
// install) falls back to PublicURL.
func (rv Resolver) ForStorage(ctx context.Context, storageID int64) string {
	if !rv.MultiTenant || rv.Store == nil || storageID == 0 {
		return rv.Fallback()
	}
	id, ok, err := rv.Store.GetProviderIDForStorage(ctx, storageID)
	if err != nil || !ok {
		return rv.Fallback()
	}
	return rv.ForProvider(ctx, id)
}

// origin assembles the base. host comes from a provider row, never from the
// request, and any port the client dialled is dropped — a tenant is reached on
// the proxy's standard port.
//
// ⚠ Under a base path (FILEX_BASE_PATH) the tenant's origin carries it: the
// router serves every host under the same prefix, and config.Load guarantees
// PublicURL ends with it, so its path IS the base. Without it a tenant's share
// link pointed at https://tenant.example.com/s/… while filex answered at
// https://tenant.example.com/filex/s/….
func (rv Resolver) origin(host, scheme string) string {
	return scheme + "://" + strings.ToLower(strings.TrimRight(host, "/")) + rv.basePath()
}

// basePath is the path of PublicURL (the install's base path), "" at the
// root. A PublicURL that is not an absolute URL has no path to offer.
func (rv Resolver) basePath() string {
	u, err := url.Parse(rv.Fallback())
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimRight(u.EscapedPath(), "/")
}

// scheme picks http vs https for a tenant host. r may be nil (no request in
// scope).
//
//  1. the trusted proxy saying X-Forwarded-Proto: http wins — that is a
//     deliberate TLS-less setup;
//  2. else an install configured with an http:// PublicURL is TLS-less too
//     (dev, docker-compose without a proxy);
//  3. else https — multi-tenant hosts sit behind the TLS-terminating reverse
//     proxy (docs/MULTI-TENANCY.md §13).
//
// ⚠ "The trusted proxy" is a peer internal/clientip trusts, the same rule
// X-Forwarded-For follows. A link minted here goes into e-mails to other
// people (an invitation, a drop notice), so how it is spelled is the
// proxy's claim to make, never the sender's.
func (rv Resolver) scheme(r *http.Request) string {
	if r != nil && clientip.PeerTrusted(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
		return "http"
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rv.PublicURL)), "http://") {
		return "http"
	}
	return "https"
}

// RequestHost extracts the bare lowercase hostname (no port) the client asked
// for. Behind the reverse proxy filex trusts the proxied Host header — the
// proxy is the only reachable path in the documented deployments (§13).
func RequestHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

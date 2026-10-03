// Package tenantdomain is a tenant's own domains (docs/TENANT-ADMIN.md,
// "Addresses and own domains"): adding one, proving it, keeping it proven.
//
// The owner's decision (2026-10-01): simple. Every tenant has a platform
// subdomain, `<realm>.<tenant domain>` (FILEX_TENANT_DOMAIN). A tenant points
// its own domain at that subdomain with a CNAME, and the CNAME is the proof:
// no TXT record. A domain belongs to one tenant at a time. A periodic check
// suspends a domain whose CNAME stopped pointing at its tenant's subdomain
// (the row stays, the tenant's administrators are told, and it comes back when
// the record does). Nothing more: no takeover logic.
//
// ⚠ The proof reads the domain's CANONICAL name (net.Resolver.LookupCNAME:
// the name at the end of its CNAME chain) and wants the tenant's subdomain.
// That holds only when the platform's wildcard record (`*.<tenant domain>`)
// is address records (A/AAAA), not a CNAME itself: a canonical name that ran
// on to the platform's host would no longer name the tenant. Check says so
// when the subdomain is not its own canonical name.
package tenantdomain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/idna"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Why a domain is refused. The API answers them as the error code.
var (
	ErrInvalid  = errors.New("domain_invalid")
	ErrTaken    = errors.New("domain_taken")
	ErrReserved = errors.New("domain_reserved")
	// ErrNoTenantDomain: the installation gives tenants no subdomain
	// (FILEX_TENANT_DOMAIN), so there is nothing a CNAME could point at.
	ErrNoTenantDomain = errors.New("no_tenant_domain")
)

// Normalize spells a domain the way it is stored and compared: lower case,
// no trailing dot, an internationalised name in its ASCII (`xn--`) form. It
// refuses what cannot be a tenant's address: an IP address, a single label,
// a label that is not letters, digits and dashes, anything longer than DNS
// allows.
func Normalize(domain string) (string, error) {
	d := strings.Trim(strings.TrimSpace(domain), ".")
	if d == "" || net.ParseIP(d) != nil || strings.ContainsAny(d, "/:@ ") {
		return "", ErrInvalid
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil {
		return "", ErrInvalid
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 {
		return "", ErrInvalid
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", ErrInvalid
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", ErrInvalid
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", ErrInvalid
			}
		}
	}
	return ascii, nil
}

// Resolver is the DNS question the proof asks; a test swaps it.
type Resolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// Notifier tells a tenant's administrators that a domain stopped (or started
// again) routing. Nil = nobody is told.
type Notifier func(ctx context.Context, p *model.Provider, d *model.ProviderDomain, status string)

// Service adds, proves and keeps proving a tenant's own domains.
type Service struct {
	Store db.Store
	// Resolver answers the CNAME question; nil = net.DefaultResolver.
	Resolver Resolver
	// PlatformHosts are the platform's own addresses (FILEX_PUBLIC_URL's
	// host): never a tenant's domain.
	PlatformHosts []string
	Notify        Notifier
	// Now is the clock; nil = time.Now.
	Now func() time.Time
	// ACME is what filex's own ACME last did for each address
	// (FILEX_TLS_MODE=acme), for the screen; nil in proxy mode.
	ACME *ACMEStatus
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) resolver() Resolver {
	if s.Resolver != nil {
		return s.Resolver
	}
	return net.DefaultResolver
}

// Target is the name a tenant's domain must point at (its platform
// subdomain), or ErrNoTenantDomain.
func Target(p *model.Provider) (string, error) {
	t := db.PlatformSubdomain(p)
	if t == "" {
		return "", ErrNoTenantDomain
	}
	return t, nil
}

// Add records a domain for a tenant, pending until its CNAME is seen. It
// refuses a domain any tenant already has (in any state: one tenant at a
// time), a provider's `host`, the platform's own addresses and anything under
// the tenant domain (those are the platform's names).
func (s *Service) Add(ctx context.Context, p *model.Provider, domain string, by *int64) (*model.ProviderDomain, error) {
	if _, err := Target(p); err != nil {
		return nil, err
	}
	d, err := Normalize(domain)
	if err != nil {
		return nil, err
	}
	if err := s.reserved(ctx, d); err != nil {
		return nil, err
	}
	if got, err := s.Store.GetProviderDomain(ctx, d); err != nil {
		return nil, err
	} else if got != nil {
		return nil, ErrTaken
	}
	row, err := s.Store.CreateProviderDomain(ctx, &model.ProviderDomain{ProviderID: p.ID, Domain: d, CreatedBy: by})
	if err != nil {
		// Two adds racing: the unique index decided.
		if got, gerr := s.Store.GetProviderDomain(ctx, d); gerr == nil && got != nil {
			return nil, ErrTaken
		}
		return nil, err
	}
	return row, nil
}

func (s *Service) reserved(ctx context.Context, d string) error {
	td := db.TenantDomain()
	if td != "" && (d == td || strings.HasSuffix(d, "."+td)) {
		return ErrReserved
	}
	for _, h := range s.PlatformHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" && d == h {
			return ErrReserved
		}
	}
	ps, err := s.Store.ListProviders(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if strings.EqualFold(strings.TrimSpace(p.Host), d) {
			return ErrReserved
		}
	}
	return nil
}

// Finding is what one look at a domain's DNS found.
type Finding struct {
	// Pointed: the domain's canonical name is the tenant's subdomain.
	Pointed bool
	// Definite: the answer is one the DNS gave (a name, or "no such name"),
	// not a failure to ask (a timeout, a server failure). Only a definite
	// answer changes a domain's state.
	Definite bool
	// Found is the canonical name the domain has ("" when none).
	Found string
	// Why says what is wrong when it is not pointed, in English (a log, a
	// tool); Code and Params say the same for a screen to put in the
	// reader's language (model.DomainWhy*).
	Why    string
	Code   string
	Params map[string]string
}

// Look asks where a domain points.
func (s *Service) Look(ctx context.Context, p *model.Provider, domain string) (Finding, error) {
	target, err := Target(p)
	if err != nil {
		return Finding{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, lerr := s.resolver().LookupCNAME(cctx, domain)
	if lerr != nil {
		var dnsErr *net.DNSError
		if errors.As(lerr, &dnsErr) && dnsErr.IsNotFound {
			return Finding{Definite: true, Why: "no record: " + domain + " does not resolve",
				Code: model.DomainWhyNoRecord, Params: map[string]string{"domain": domain}}, nil
		}
		return Finding{Why: "the DNS could not be asked: " + lerr.Error(),
			Code: model.DomainWhyDNSFailed, Params: map[string]string{"detail": lerr.Error()}}, nil
	}
	got = strings.ToLower(strings.TrimSuffix(got, "."))
	if got == target {
		return Finding{Pointed: true, Definite: true, Found: got}, nil
	}
	f := Finding{Definite: true, Found: got}
	switch {
	case got == domain:
		f.Why = "no CNAME: " + domain + " has address records of its own"
		f.Code, f.Params = model.DomainWhyNoCNAME, map[string]string{"domain": domain}
	default:
		f.Why = "points at " + got + ", not " + target
		f.Code, f.Params = model.DomainWhyPointsElsewhere, map[string]string{"found": got, "target": target}
	}
	// The platform's wildcard must be address records: say so when the
	// subdomain is not its own canonical name.
	if sub, serr := s.resolver().LookupCNAME(cctx, target); serr == nil {
		if sub = strings.ToLower(strings.TrimSuffix(sub, ".")); sub != target {
			f.Why = fmt.Sprintf("%s is a CNAME to %s: the platform's record for *.%s must be address records (A/AAAA)", target, sub, db.TenantDomain())
			f.Code, f.Params = model.DomainWhyWildcardCNAME, map[string]string{"target": target, "found": sub, "tenant_domain": db.TenantDomain()}
		}
	}
	return f, nil
}

// Check looks at one domain now and records what it found: pending becomes
// active when the CNAME is there; active becomes suspended when a definite
// answer says it is gone; suspended becomes active again when it is back. A
// failure to ask changes nothing. It answers the row as it is now and whether
// its routing changed.
func (s *Service) Check(ctx context.Context, d *model.ProviderDomain) (*model.ProviderDomain, bool, error) {
	p, err := s.Store.GetProvider(ctx, d.ProviderID)
	if err != nil || p == nil {
		return d, false, fmt.Errorf("domain %s: its tenant: %w", d.Domain, err)
	}
	f, err := s.Look(ctx, p, d.Domain)
	if err != nil {
		return d, false, err
	}
	next := d.Status
	switch {
	case f.Pointed:
		next = model.DomainActive
	case f.Definite && d.Status == model.DomainActive:
		next = model.DomainSuspended
	}
	if !f.Definite && !f.Pointed {
		// Nothing decided: only the time and the reason are recorded.
		next = d.Status
	}
	why := model.DomainCheck{Text: f.Why, Code: f.Code, Params: f.Params}
	if err := s.Store.SetProviderDomainStatus(ctx, d.ID, next, why, s.now()); err != nil {
		return d, false, err
	}
	fresh, err := s.Store.GetProviderDomainByID(ctx, d.ID)
	if err != nil || fresh == nil {
		return d, false, err
	}
	changed := (d.Status == model.DomainActive) != (next == model.DomainActive)
	if changed && s.Notify != nil && (next == model.DomainSuspended || d.Status == model.DomainSuspended) {
		s.Notify(ctx, p, fresh, next)
	}
	return fresh, changed, nil
}

// Sweep checks every domain once (the periodic check).
func (s *Service) Sweep(ctx context.Context) (checked, changed int, err error) {
	all, err := s.Store.ListProviderDomains(ctx, 0)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range all {
		if ctx.Err() != nil {
			return checked, changed, ctx.Err()
		}
		_, ch, cerr := s.Check(ctx, d)
		if cerr != nil {
			continue
		}
		checked++
		if ch {
			changed++
		}
	}
	return checked, changed, nil
}

// Run sweeps every `every` until ctx ends (the first sweep after one period:
// a restart does not ask the DNS about every domain at once).
func (s *Service) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 6 * time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _, _ = s.Sweep(ctx)
		}
	}
}

package model

import "time"

// The states of a tenant's own domain (provider_domains.status, migration
// 00076, docs/TENANT-ADMIN.md).
const (
	// DomainPending: added, its CNAME not seen yet. It does not route.
	DomainPending = "pending"
	// DomainActive: its CNAME points at the tenant's platform subdomain. It
	// routes to the tenant and is certified.
	DomainActive = "active"
	// DomainSuspended: it was active and a later check found the CNAME gone.
	// It stops routing and stops being certified; the row stays, and the next
	// check that finds the CNAME back makes it active again.
	DomainSuspended = "suspended"
)

// Why a check did not find a domain pointed at its tenant
// (ProviderDomain.LastErrorCode).
const (
	// DomainWhyNoRecord: the name does not resolve at all. Params: domain.
	DomainWhyNoRecord = "no_record"
	// DomainWhyNoCNAME: the name has address records of its own, no CNAME.
	// Params: domain.
	DomainWhyNoCNAME = "no_cname"
	// DomainWhyPointsElsewhere: its CNAME leads somewhere else. Params:
	// found, target.
	DomainWhyPointsElsewhere = "points_elsewhere"
	// DomainWhyWildcardCNAME: the platform's own record for the tenant domain
	// is a CNAME, so nothing can be proven. Params: target, found,
	// tenant_domain.
	DomainWhyWildcardCNAME = "wildcard_cname"
	// DomainWhyDNSFailed: the DNS could not be asked (a timeout, a server
	// failure); nothing was decided. Params: detail.
	DomainWhyDNSFailed = "dns_failed"
)

// DomainCheck is what one check records about a domain besides its state.
type DomainCheck struct {
	// Text is the English sentence (ProviderDomain.LastError).
	Text   string
	Code   string
	Params map[string]string
}

// ProviderDomain is a tenant's own domain.
type ProviderDomain struct {
	ID         int64  `json:"id"`
	ProviderID int64  `json:"provider_id"`
	Domain     string `json:"domain"`
	Status     string `json:"status"`
	// LastError is what the last check found when it did not find the CNAME
	// ("points at other.example", "no CNAME record").
	LastError string `json:"last_error,omitempty"`
	// LastErrorCode and LastErrorParams say the same as LastError for a
	// screen to put in the reader's language: one of the DomainWhy* codes and
	// its names (domain, found, target, tenant_domain, detail). LastError
	// stays the English sentence, for a log, a tool and a code a screen does
	// not know.
	LastErrorCode   string            `json:"last_error_code,omitempty"`
	LastErrorParams map[string]string `json:"last_error_params,omitempty"`
	CheckedAt       *time.Time        `json:"checked_at,omitempty"`
	ActiveSince     *time.Time        `json:"active_since,omitempty"`
	// TLSCertPEM is a certificate the tenant brought (the chain, PEM); its
	// key is sealed in TLSKeySealed and never sent to a client.
	TLSCertPEM   string     `json:"-"`
	TLSKeySealed string     `json:"-"`
	TLSNotAfter  *time.Time `json:"tls_not_after,omitempty"`
	CreatedBy    *int64     `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// HasOwnCertificate reports whether the tenant brought a certificate for it.
func (d *ProviderDomain) HasOwnCertificate() bool {
	return d != nil && d.TLSCertPEM != "" && d.TLSKeySealed != ""
}

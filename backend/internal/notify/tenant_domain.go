package notify

import (
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A tenant's own domain stopped (or started again) routing
// (EventTenantDomainSuspended / EventTenantDomainRestored). The words are the
// server catalogue's, in every built-in language in meta, like
// LegacyAccountElsewhere: each reader's bell shows their own, and so is the
// reason (the check's code, ProviderDomain.LastErrorCode); the English
// sentence (LastError) is the fallback for a code the catalogue does not know.

// TenantDomainChanged is the notice for one administrator of the tenant
// (userID), about one domain. suspended false is the notice that it is back.
func TenantDomainChanged(userID int64, d *model.ProviderDomain, suspended bool) Event {
	// Whole keys, never assembled: the catalogue's test finds every key by
	// its literal.
	ev, titleKey, bodyKey := EventTenantDomainRestored, "server.tenant_domain.restored_title", "server.tenant_domain.restored_body"
	if suspended {
		ev, titleKey, bodyKey = EventTenantDomainSuspended, "server.tenant_domain.suspended_title", "server.tenant_domain.suspended_body"
	}
	domain := ""
	if d != nil {
		domain = d.Domain
	}
	meta := map[string]any{"domain": domain}
	if d != nil && d.LastErrorCode != "" {
		meta["reason_code"] = d.LastErrorCode
	}
	for _, lang := range srvtext.BuiltinLanguages() {
		vars := srvtext.Vars{"domain": domain, "reason": DomainReason(lang, d)}
		meta["title_"+lang] = srvtext.Text(lang, titleKey, vars)
		meta["body_"+lang] = srvtext.Text(lang, bodyKey, vars)
	}
	lang := srvtext.Pick()
	vars := srvtext.Vars{"domain": domain, "reason": DomainReason(lang, d)}
	meta["reason"] = DomainReason("en", d)
	sev := SeverityInfo
	if suspended {
		sev = SeverityWarning
	}
	uid := userID
	return Event{
		Event:    ev,
		Severity: sev,
		Title:    srvtext.Text(lang, titleKey, vars),
		Body:     srvtext.Text(lang, bodyKey, vars),
		Meta:     meta,
		UserID:   &uid,
	}
}

// DomainReason says in lang why a check did not find a domain pointed at its
// tenant: the catalogue's sentence for its code, with its names; the English
// sentence the check recorded for a code it does not know.
func DomainReason(lang string, d *model.ProviderDomain) string {
	if d == nil {
		return ""
	}
	key := ""
	switch d.LastErrorCode {
	case model.DomainWhyNoRecord:
		key = "server.tenant_domain.why_no_record"
	case model.DomainWhyNoCNAME:
		key = "server.tenant_domain.why_no_cname"
	case model.DomainWhyPointsElsewhere:
		key = "server.tenant_domain.why_points_elsewhere"
	case model.DomainWhyWildcardCNAME:
		key = "server.tenant_domain.why_wildcard_cname"
	case model.DomainWhyDNSFailed:
		key = "server.tenant_domain.why_dns_failed"
	}
	if key == "" {
		return d.LastError
	}
	vars := srvtext.Vars{}
	for k, v := range d.LastErrorParams {
		vars[k] = v
	}
	return srvtext.Text(lang, key, vars)
}

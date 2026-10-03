package windows

import (
	"strings"
	"unicode"

	"github.com/brf-tech/filex/backend/internal/identity"
)

// account is what a typed Windows login name turns into.
//
// The name a person types comes in four shapes, and LogonUserW wants each of
// them differently:
//
//	alex             a name with no domain: the machine's own accounts, or the
//	                 configured default domain
//	CORP\alex        a domain (NetBIOS) name and the account
//	.\alex           the machine's own accounts, said outright
//	alex@corp.test   a user principal name (UPN): LogonUserW takes it whole, with
//	                 no domain argument
//
// and filex's own address for the account (what e-mail it is known by, what
// the protocol logins resolve) must come out the same whichever way the person
// wrote it — the e-mail is the account's identity, and two spellings that made
// two accounts would be the outcome this design exists to prevent.
type account struct {
	// Name is the bare account name as typed (its case kept, for LogonUserW).
	Name string
	// LogonUser and LogonDomain are the two arguments of LogonUserW. For a UPN
	// LogonUser is the whole name@suffix and LogonDomain is "".
	LogonUser   string
	LogonDomain string
	// Domain is the domain part as typed or configured, "" when there is none
	// (the machine's own accounts); used to recognise service identities.
	Domain string
	// Email is the address the account has in filex, lower case.
	Email string
}

// maxNameLen bounds every part of a typed name: a Windows account name is at
// most 20 characters, a domain 15 (NetBIOS) or 255 (DNS); anything longer is a
// probe, not a person.
const maxNameLen = 256

// parseAccount turns what a person typed into an account, or ok=false when it
// cannot be a Windows login at all.
//
// domain is the operator's default domain ("" = none), realm the tenant realm
// the sign-in is for on a multi-tenant install ("" = the platform's own tenant,
// or no tenants), and token the installation's e-mail token (already
// normalised; "" means the default).
//
// ⚠ Whatever the shape, Email is what identity.DeriveEmail would say for the
// bare name, the domain and the realm — or the UPN itself. A machine account
// is `alex@<token>` for the platform's own tenant and `alex@<realm>.<token>` in
// a tenant's realm, so two tenants' `alex` are two accounts.
func parseAccount(typed, domain, realm, token string) (account, bool) {
	typed = strings.TrimSpace(typed)
	if typed == "" || len(typed) > maxNameLen || hasControl(typed) {
		return account{}, false
	}
	if token = strings.ToLower(strings.TrimSpace(token)); token == "" {
		token = identity.DefaultEmailToken
	}
	domain = strings.TrimSpace(domain)

	// DOMAIN\name
	if i := strings.IndexByte(typed, '\\'); i >= 0 {
		dom, name := strings.TrimSpace(typed[:i]), strings.TrimSpace(typed[i+1:])
		if dom == "" || name == "" || strings.ContainsAny(name, `\@`) || strings.ContainsAny(dom, `\@`) {
			return account{}, false
		}
		if dom == "." {
			return account{Name: name, LogonUser: name, LogonDomain: ".", Domain: ".",
				Email: identity.DeriveEmail(name, "", realm, token)}, true
		}
		return account{Name: name, LogonUser: name, LogonDomain: dom, Domain: dom,
			Email: identity.DeriveEmail(name, dom, "", token)}, true
	}

	// name@suffix
	if i := strings.LastIndexByte(typed, '@'); i >= 0 {
		name, suffix := strings.TrimSpace(typed[:i]), strings.TrimSpace(typed[i+1:])
		if name == "" || suffix == "" || strings.ContainsAny(name, `@\`) || strings.ContainsAny(suffix, `@\`) {
			return account{}, false
		}
		low := strings.ToLower(suffix)
		if _, ok := identity.LoginNameOf(typed, realm, token); ok {
			// `alex@local` (`alex@acme.local` in realm acme) is the address this
			// provider itself gave a machine account: it comes back through the
			// protocol logins as the typed name. It is the machine's account,
			// not a domain called "local" — recognised by the derivation every
			// provider shares (identity.LoginNameOf).
			return account{Name: name, LogonUser: name, LogonDomain: ".", Domain: ".",
				Email: identity.DeriveEmail(name, "", realm, token)}, true
		}
		switch {
		case realm != "" && low == token:
			// The platform's own machine address typed in a tenant's realm:
			// not this sign-in's to take apart, and not a domain either.
			return account{}, false
		case !strings.Contains(low, "."):
			// A one-label suffix is a NetBIOS domain (`alex@corp` is what
			// CORP\alex becomes), and LogonUserW resolves it as one.
			return account{Name: name, LogonUser: name, LogonDomain: suffix, Domain: suffix,
				Email: strings.ToLower(name) + "@" + low}, true
		}
		return account{Name: name, LogonUser: name + "@" + suffix, Domain: suffix,
			Email: strings.ToLower(name) + "@" + low}, true
	}

	// A bare name: the operator's default domain, else this machine.
	switch {
	case domain == "":
		return account{Name: typed, LogonUser: typed, LogonDomain: ".", Domain: ".",
			Email: identity.DeriveEmail(typed, "", realm, token)}, true
	case strings.Contains(domain, "."):
		return account{Name: typed, LogonUser: typed + "@" + domain, Domain: domain,
			Email: identity.DeriveEmail(typed, domain, "", token)}, true
	}
	return account{Name: typed, LogonUser: typed, LogonDomain: domain, Domain: domain,
		Email: identity.DeriveEmail(typed, domain, "", token)}, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// serviceDomains are the "domains" Windows gives its own identities: a name
// under one of them is never a person.
var serviceDomains = map[string]bool{
	"nt authority": true, "nt service": true, "nt virtual machine": true,
	"window manager": true, "font driver host": true, "iis apppool": true,
	"builtin": true, "nt task": true,
}

// isServiceName reports, by NAME, whether a typed account is a Windows service
// or machine identity: anything under NT AUTHORITY\, NT SERVICE\ and the
// like, or a machine account (a name ending in `$`).
//
// It is the Windows-only half of the forbidden-account rule; the names every
// system shares (Administrator, Guest, root, DefaultAccount…) are
// identity.ForbiddenAccount's. What only Windows can say — the built-in
// Administrator under a LOCALISED or renamed name — is the SID check that runs
// after the sign-in (sidIsSystem).
func isServiceName(a account) bool {
	if serviceDomains[strings.ToLower(strings.TrimSpace(a.Domain))] {
		return true
	}
	return strings.HasSuffix(strings.TrimSpace(a.Name), "$")
}

// sidIsSystem reports, from the SID of a signed-in account, whether it is a
// system, service or built-in account rather than a person.
//
// Only two families are people: S-1-5-21-… (a local or domain account) with a
// RID of 1000 or more (below that are Administrator 500, Guest 501, krbtgt
// 502, DefaultAccount 503, WDAGUtilityAccount 504 and the well-known groups),
// and S-1-12-1-… (a Microsoft Entra / Azure AD account, which has no RID).
// Everything else — S-1-5-18 SYSTEM, -19, -20, S-1-5-80-… services, -82 IIS
// pools, -90 window manager, -96 font driver — is refused.
//
// ⚠ An unparsable SID is a system account: not knowing is a refusal.
func sidIsSystem(sid string) bool {
	sid = strings.ToUpper(strings.TrimSpace(sid))
	parts := strings.Split(sid, "-")
	if len(parts) < 4 || parts[0] != "S" || parts[1] != "1" {
		return true
	}
	switch {
	case parts[2] == "12" && len(parts) >= 5 && parts[3] == "1":
		return false
	case parts[2] == "5" && parts[3] == "21" && len(parts) >= 6:
		rid := parts[len(parts)-1]
		if rid == "" || len(rid) > 10 {
			return true
		}
		n := 0
		for _, r := range rid {
			if r < '0' || r > '9' {
				return true
			}
			n = n*10 + int(r-'0')
		}
		return n < 1000
	}
	return true
}

package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ── The first-login rule's account-name helpers ─────────────────────────────
//
// A sign-in provider that has never seen a person before either opens an
// account for them or refuses (internal/auth.ProvisionFirstLogin). Two pieces
// of that decision are about NAMES rather than policy, and every provider that
// takes an operating-system account (the OS sign-in providers) needs the same
// answers, so they live here once:
//
//   - which accounts may never be given a filex login (CheckOSLogin);
//   - what e-mail address an account gets when the provider knows only a login
//     name (DeriveEmail).

// Why a login name is refused. They are the operator's words in the audit row
// and the log, never the person's: a person is told only that the sign-in did
// not work.
const (
	// RefuseInvalidName: the OS name cannot be a filex username (too short,
	// starts with a digit, a character a protocol login cannot carry, or one of
	// the names identity reserves).
	RefuseInvalidName = "invalid_name"
	// RefuseForbidden: a system or service account — root, Administrator,
	// daemons, the accounts an OS creates for its own services.
	RefuseForbidden = "forbidden_account"
)

// forbiddenNames are accounts no filex login is ever created for, on any
// operating system. This is deliberately a NAME list: it is the part every
// provider can apply before it has looked anything up. What only the OS can
// say — a Linux account with UID below 1000, a nologin or false shell, a
// Windows service SID — is asked through SystemAccountChecker.
//
// It is a superset of `reserved` (which guards names users may CLAIM); this one
// guards names an OS may PRESENT. Both include root and Administrator.
var forbiddenNames = map[string]bool{
	// Windows built-ins and service identities.
	"administrator": true, "guest": true, "defaultaccount": true,
	"wdagutilityaccount": true, "krbtgt": true, "system": true,
	"localservice": true, "networkservice": true, "trustedinstaller": true,
	"local.service": true, "network.service": true,
	// Unix root and the classic service accounts.
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"games": true, "man": true, "lp": true, "mail": true, "news": true,
	"uucp": true, "proxy": true, "www-data": true, "backup": true,
	"list": true, "irc": true, "gnats": true, "nobody": true, "nogroup": true,
	"sshd": true, "messagebus": true, "syslog": true, "polkitd": true,
	"postgres": true, "mysql": true, "redis": true, "nginx": true,
	"apache": true, "docker": true, "filex": true,
}

// forbiddenPrefixes catch families of service accounts: systemd's
// (`systemd-network`), macOS's underscore accounts (`_www`).
var forbiddenPrefixes = []string{"systemd-", "_"}

// ForbiddenAccount reports whether a login name belongs to a system or service
// account by name. The name is compared folded (case, surrounding space).
func ForbiddenAccount(name string) bool {
	n := Normalize(name)
	if n == "" {
		return false
	}
	if forbiddenNames[n] || reserved[n] {
		return true
	}
	for _, p := range forbiddenPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// SystemAccountChecker is what an OS sign-in provider implements to say what
// only the operating system knows about an account: a Linux UID below 1000, a
// nologin/false shell, a Windows service SID.
//
// ⚠ It is asked AFTER the name checks, and a checker that cannot answer (the
// account database is unreadable) must return an error: CheckOSLogin refuses on
// error. The other way round — letting an account through because nobody could
// say it was a service account — is how root would get a filex login.
type SystemAccountChecker interface {
	IsSystemAccount(ctx context.Context, name string) (bool, error)
}

// CheckOSLogin turns the name an operating system reported into the filex
// username it will be, or names why it cannot be one.
//
//   - the name is folded to lower case (a Windows `Alex` is the filex `alex`);
//   - it must satisfy Check — at least MinLen characters, no leading digit, the
//     protocol-safe character set, not one of the reserved names — because the
//     username is what SFTP, FTPS and S3 see;
//   - system and service accounts are refused in every case, by name
//     (ForbiddenAccount) and then by what the OS says (checker, may be nil).
//
// The refusal is one of RefuseInvalidName / RefuseForbidden; on success it is
// "". Nothing here ever repairs a name (no transliteration, no truncation): a
// login that quietly became another name would sign one person into another's
// account.
func CheckOSLogin(ctx context.Context, raw string, checker SystemAccountChecker) (name, refusal string) {
	n := Normalize(raw)
	// Forbidden first: `root` and `Administrator` are named as what they are
	// in the audit row, not as "too short" or "reserved".
	if ForbiddenAccount(n) {
		return "", RefuseForbidden
	}
	if p := Check(n); p != nil {
		return "", RefuseInvalidName
	}
	if checker != nil {
		sys, err := checker.IsSystemAccount(ctx, n)
		if err != nil || sys {
			return "", RefuseForbidden
		}
	}
	return n, ""
}

// DefaultEmailToken is what follows the `@` of an account that has a login name
// and no e-mail domain: `alex` becomes `alex@local`.
const DefaultEmailToken = "local"

// ErrBadEmailToken is returned for an e-mail token that cannot follow an `@`.
var ErrBadEmailToken = errors.New("identity: the e-mail token must be letters, digits, dots and dashes")

// EmailToken normalises the operator's e-mail token (FILEX_OS_LOGIN_EMAIL_TOKEN):
// "" is DefaultEmailToken, a leading `@` is dropped, the rest is folded to lower
// case and must be DNS-label-like.
//
// ⚠⚠ The token is chosen ONCE, at installation, by the administrator — from the
// environment, never from the admin panel. It is part of every account's e-mail
// address, and an address is an account's identity: change the token later and
// `alex` signs in as `alex@evim` while the `alex@local` account (with its files,
// shares and quota) sits beside it. Two accounts for one person is the outcome
// this design exists to prevent, so nothing in filex edits it live.
func EmailToken(raw string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@")))
	if t == "" {
		return DefaultEmailToken, nil
	}
	if !dnsLike(t) {
		return "", fmt.Errorf("%w: %q", ErrBadEmailToken, raw)
	}
	return t, nil
}

func dnsLike(s string) bool {
	if s == "" || s[0] == '.' || s[0] == '-' || s[len(s)-1] == '.' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return !strings.Contains(s, "..")
}

// DeriveEmail is the e-mail address an account gets when a provider knows a
// login name and, possibly, a domain:
//
//   - a name that already contains `@` is an address: returned folded, as it is;
//   - with a domain (the machine's or directory's own, "corp.example"): the
//     person's address, `alex@corp.example` — the same in every realm;
//   - with no domain: `alex@<token>` for the platform's own tenant (and every
//     single-tenant install), `alex@<realm>.<token>` for a tenant's realm
//     (`alex@acme.local`) — token from EmailToken, `local` unless the
//     administrator chose another at installation.
//
// ⚠ The realm is what lets two tenants each have an `alex` on a multi-tenant
// install: without it both were `alex@local`, and e-mail addresses are unique
// across the platform, so the second one could never get an account.
//
// realm is the tenant's realm ("" = the platform's own tenant, or no tenants
// at all); token is used as given when non-empty (call EmailToken first to
// validate an operator's value); "" means DefaultEmailToken.
func DeriveEmail(name, domain, realm, token string) string {
	n := Normalize(name)
	if n == "" || strings.Contains(n, "@") {
		return n
	}
	d := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(domain), "@")))
	if d == "" {
		d = TokenDomain(realm, token)
	}
	return n + "@" + d
}

// TokenDomain is what follows the `@` of an address DeriveEmail makes up for a
// login name with no domain: the token, or `<realm>.<token>` in a tenant's
// realm.
func TokenDomain(realm, token string) string {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "" {
		t = DefaultEmailToken
	}
	r := strings.ToLower(strings.TrimSpace(realm))
	if r == "" {
		return t
	}
	return r + "." + t
}

// LoginNameOf reads DeriveEmail backwards for a name that came with no domain:
// the login name an address `<name>@<token>` (or `<name>@<realm>.<token>` in
// that realm) was made from. ok is false for any other address (an address of
// another domain is a real mailbox, not one filex made up — and so is another
// realm's) and for a bare name. realm and token are read as DeriveEmail reads
// them.
//
// ⚠ It exists for the file protocols. They ask a provider with the account's
// e-mail — the only name SFTP's username field and the account share — so a
// provider that looks people up by login name (an LDAP filter on `uid`) is
// handed back `alex@acme.local` and has to ask its directory about `alex`.
func LoginNameOf(email, realm, token string) (string, bool) {
	e := Normalize(email)
	i := strings.LastIndexByte(e, '@')
	if i <= 0 || e[i+1:] != TokenDomain(realm, token) {
		return "", false
	}
	name := e[:i]
	if strings.Contains(name, "@") {
		return "", false
	}
	return name, true
}

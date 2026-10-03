package tenant

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ── A tenant's realm ─────────────────────────────────────────────────────────
//
// On a multi-tenant install two tenants can each have a person called `alex`.
// The realm is the tenant's sign-in name, and it is what tells the two apart
// where nothing else can: the sign-in form's Realm field, and `realm/name` in
// the user name of a protocol that carries no address (SFTP). It is also part
// of the e-mail address an account gets when its sign-in provider knows only a
// login name (`alex@acme.local`, identity.DeriveEmail).
//
// ⚠⚠ A realm is chosen once, when the tenant is created, and never changes —
// not even when the slug does. It is written into account addresses and into
// every client configuration people saved (`acme/alex` in an SFTP bookmark);
// renaming it would strand both. The API refuses a change and the store never
// writes one (UpdateProvider leaves the column alone).
//
// The platform's own tenant (the supertenant) has no realm: an empty realm is
// how a person signs in to it.

// RealmMaxLen bounds a realm: a DNS label's length, because a realm is also
// the first label of an address domain (`alex@acme.local`).
const RealmMaxLen = 63

// Why a realm is refused. The API answers them as `realm_<kind>`.
var (
	ErrRealmEmpty    = errors.New("tenant: the realm is empty")
	ErrRealmInvalid  = errors.New("tenant: a realm is lower-case letters, digits and dashes, at most 63 characters, and starts and ends with a letter or a digit")
	ErrRealmReserved = errors.New("tenant: this realm is reserved")
)

// reservedRealms may not be given to a tenant: names a person would read as
// "the platform itself" or as a protocol, and the default e-mail token.
var reservedRealms = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true,
	"filex": true, "default": true, "main": true, "platform": true,
	"local": true, "localhost": true, "api": true, "www": true, "auth": true,
	"login": true, "dav": true, "webdav": true, "ftp": true, "ftps": true,
	"sftp": true, "ssh": true, "s3": true, "nfs": true, "smb": true,
}

// NormalizeRealm folds a realm the way it is stored and compared: trimmed and
// lower-cased. It does not validate.
func NormalizeRealm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// CheckRealm reports why a realm cannot be given to a new tenant, nil when it
// can. The realm is normalised first.
//
// ⚠ The character set is narrower than a slug's has ever been checked to be,
// on purpose: `/` separates the realm in `realm/name`, `@` and `.` would make
// the derived address ambiguous (`alex@a.b.local`), and `\` is the Windows
// domain separator a login name may already carry.
func CheckRealm(s string) error {
	r := NormalizeRealm(s)
	if r == "" {
		return ErrRealmEmpty
	}
	if len(r) > RealmMaxLen || r[0] == '-' || r[len(r)-1] == '-' {
		return ErrRealmInvalid
	}
	for _, c := range r {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return ErrRealmInvalid
		}
	}
	if reservedRealms[r] {
		return ErrRealmReserved
	}
	return nil
}

// IsReservedRealm reports whether a realm is one no tenant may be given.
func IsReservedRealm(s string) bool { return reservedRealms[NormalizeRealm(s)] }

// latinFold spells the letters a decomposition does not take apart (they are
// letters of their own, not a base letter with a mark) the way a person
// writing the name in plain ASCII would.
var latinFold = map[rune]string{
	'ı': "i", 'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'đ': "d", 'ð': "d",
	'ł': "l", 'þ': "th", 'ħ': "h", 'ŋ': "n",
}

// SuggestRealm turns a slug (or a tenant's name) into the realm the tenant
// screen offers while a tenant is being created: lower case, letters with
// marks written without them (`Müşteri` → `musteri`), every run of anything
// that is not a letter or a digit one dash, no dash at either end, at most
// RealmMaxLen characters. "" when nothing realm-shaped is left.
//
// ⚠ A SUGGESTION, shown before the tenant exists and edited by whoever creates
// it: the realm is immutable once given, so nothing here is applied without a
// person seeing it. The API still validates what it is sent (CheckRealm), and
// a suggestion may be reserved or taken; the caller asks for that
// (RealmCandidates).
func SuggestRealm(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(strings.ToLower(strings.TrimSpace(s))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if f, ok := latinFold[r]; ok {
			b.WriteString(f)
			dash = false
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > RealmMaxLen {
		out = strings.TrimRight(out[:RealmMaxLen], "-")
	}
	return out
}

// RealmCandidates lists the realms to try, in order, for a suggestion that
// may already be taken or reserved: the suggestion itself, then `-2` … `-n`
// on it (shortened so the result still fits RealmMaxLen). Every candidate
// passes CheckRealm except, possibly, the first.
func RealmCandidates(base string, n int) []string {
	base = SuggestRealm(base)
	if base == "" {
		return nil
	}
	out := []string{base}
	for i := 2; i <= n; i++ {
		suffix := "-" + strconv.Itoa(i)
		stem := base
		if len(stem)+len(suffix) > RealmMaxLen {
			stem = strings.TrimRight(stem[:RealmMaxLen-len(suffix)], "-")
		}
		out = append(out, stem+suffix)
	}
	return out
}

// SplitLogin reads `realm/name` — the way a protocol user name names a realm
// when the protocol carries no address. realm is normalised; named says a
// realm part was written at all.
//
//	acme/alex          realm "acme", name "alex"
//	acme/alex@x.com    realm "acme", name "alex@x.com" (split at the FIRST `/`)
//	/alex              realm "" named: the platform's own tenant, said outright
//	alex               no realm part
//
// A `/` that comes after an `@` or a `\` is part of the name, not a realm
// separator.
func SplitLogin(s string) (realm, name string, named bool) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, '/')
	if i < 0 || strings.ContainsAny(s[:i], `@\`) {
		return "", s, false
	}
	return NormalizeRealm(s[:i]), strings.TrimSpace(s[i+1:]), true
}

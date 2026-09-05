// Package identity owns the rules for account login names, and the single
// door that turns a typed identifier into a user.
//
// # Why this package exists
//
// filex accounts have always been addressed by e-mail. The protocols filex is
// growing do not carry an e-mail comfortably: `sftp://ada@example.com@host` needs
// escaping, rclone and WinSCP config files split on `@`, and FTP's USER command
// with an `@` in it confuses proxying clients. So accounts get a second, short
// identifier — and every surface accepts EITHER (the owner's words, translated
// from Turkish: "wherever we can, let's look at both the e-mail and the
// username — think of it like a dual-side login").
//
// "Every surface" is the load-bearing half. A login rule that lives in one
// handler is a login rule that is subtly different in the next one, and the
// difference is invisible until a user reports that the same credentials work
// in the browser and fail over SFTP. So resolution lives here, once, and the
// surfaces call Resolve.
//
// # The disambiguation rule
//
// An identifier containing `@` is an e-mail; anything else is a username. The
// lookup NEVER tries both.
//
// That is a security rule, not a tidiness one. If resolution fell back from one
// to the other, a username could be chosen to collide with the local part of
// someone else's e-mail (or vice versa), and which account answered would
// depend on row order and on which query happened to run first. Usernames are
// therefore forbidden from containing `@` (Validate rejects it), which makes
// the two namespaces provably disjoint and the rule total: every identifier has
// exactly one interpretation.
package identity

import (
	"fmt"
	"strings"
	"unicode"
)

// Bounds for a username. 32 is also the width of the MySQL column, so the
// validator and the schema cannot disagree.
const (
	MinLen = 3
	MaxLen = 32
)

// reserved names may not be claimed by a user. They are names an operator or a
// protocol is likely to want to mean something else — an SFTP login of `root`
// or an S3 key labelled `admin` invites exactly the confusion this package
// exists to prevent. Kept small on purpose: a long list is a support burden.
var reserved = map[string]bool{
	"admin": true, "administrator": true, "root": true, "filex": true,
	"api": true, "www": true, "ftp": true, "sftp": true, "webdav": true,
	"dav": true, "s3": true, "nfs": true, "smb": true, "system": true,
	"support": true, "help": true, "null": true, "undefined": true,
	"anonymous": true, "guest": true, "nobody": true,
}

// LooksLikeEmail reports whether an identifier should be resolved as an e-mail.
// The test is deliberately the crudest one that is total: usernames cannot
// contain `@` (Validate enforces it), so the presence of one is decisive.
// A stricter parse would only add ways for a valid address to be misread.
func LooksLikeEmail(identifier string) bool {
	return strings.Contains(identifier, "@")
}

// Normalize folds an identifier to its canonical form for lookup: trimmed and
// lowercased. Both namespaces are case-insensitive — a user who types `Ada`
// into FileZilla means `ada`, and an e-mail address is already stored lower.
func Normalize(identifier string) string {
	return strings.ToLower(strings.TrimSpace(identifier))
}

// Validate reports whether a username is acceptable, returning an error whose
// message is meant to be shown to the person who typed it.
//
// The character set is ASCII on purpose, and this is the one place in filex
// where that is the right answer: a username travels through SSH USER records,
// FTP command lines, S3 key labels and shell config files, all of which mangle
// non-ASCII differently. The DISPLAY name is where a person's name belongs, and
// it has always accepted the full alphabet. The error text below is the user's
// language's job at the API boundary; the sentinel is what callers match on.
func Validate(username string) error {
	if username == "" {
		return fmt.Errorf("%w: empty", ErrInvalidUsername)
	}
	if strings.Contains(username, "@") {
		// Stated separately from the character-set error because it is the one
		// mistake a user is most likely to make (typing their e-mail), and a
		// generic "invalid character" would not tell them what to do instead.
		return fmt.Errorf("%w: contains @, which belongs to an e-mail address", ErrInvalidUsername)
	}
	if len(username) < MinLen {
		return fmt.Errorf("%w: shorter than %d characters", ErrInvalidUsername, MinLen)
	}
	if len(username) > MaxLen {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidUsername, MaxLen)
	}
	if username != strings.ToLower(username) {
		return fmt.Errorf("%w: must be lowercase", ErrInvalidUsername)
	}
	if unicode.IsDigit(rune(username[0])) {
		// A leading digit makes a username ambiguous with a numeric user id in
		// the places that accept either (admin URLs, CLI arguments).
		return fmt.Errorf("%w: must not start with a digit", ErrInvalidUsername)
	}
	for _, r := range username {
		if !isUsernameRune(r) {
			return fmt.Errorf("%w: %q is not allowed (use a-z, 0-9, dot, dash, underscore)", ErrInvalidUsername, r)
		}
	}
	if reserved[username] {
		return fmt.Errorf("%w: %q is reserved", ErrReservedUsername, username)
	}
	return nil
}

func isUsernameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	case r == '.', r == '-', r == '_':
		return true
	}
	return false
}

// translit maps the letters a person's name actually contains to the ASCII a
// protocol login can carry. Dropping them instead would turn "gözlük" into
// "gzlk", and replacing them with separators would give "g.zl.k" — both are
// names their owner would not recognise as theirs.
//
// This is not the project's ASCII-folding rule leaking in: display names,
// labels and every user-visible string keep the real spelling. A username is a
// wire identifier that passes through SSH USER records, FTP command lines and
// shell config files, which mangle non-ASCII in three different ways.
var translit = map[rune]string{
	'ı': "i", 'İ': "i", 'ş': "s", 'Ş': "s", 'ğ': "g", 'Ğ': "g",
	'ü': "u", 'Ü': "u", 'ö': "o", 'Ö': "o", 'ç': "c", 'Ç': "c",
	'ä': "a", 'Ä': "a", 'ß': "ss", 'é': "e", 'É': "e", 'è': "e",
	'á': "a", 'Á': "a", 'í': "i", 'ó': "o", 'ú': "u", 'ñ': "n", 'Ñ': "n",
	'å': "a", 'ø': "o", 'æ': "ae",
}

// Suggest derives a candidate username from an e-mail address. It is
// deterministic: the same address always yields the same candidate, so a
// backfill that is interrupted and re-run does not rename anybody.
//
// Uniqueness is NOT this function's job — Suggest can and will return a name
// that is already taken, including a reserved one. Claiming is EnsureUsername's
// job, because only it can see the database. That split is deliberate: Suggest
// never invents an unrelated name to dodge a collision.
func Suggest(email string) string {
	local, domain := splitEmail(Normalize(email))

	// Everything after a plus is a mail tag (ada+filex@…), not part of the
	// person's name.
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}

	out := clean(local)
	if len(out) > MaxLen {
		out = strings.Trim(out[:MaxLen], "._-")
	}
	if out != "" && unicode.IsDigit(rune(out[0])) {
		// A leading digit would fail Validate; keep the name, prefix it.
		out = "u" + out
	}
	if len(out) < MinLen {
		// A very short local part ("bs@example.com") deserves a working account, not
		// an error — and not a padding character that means nothing either. The
		// domain label is the one piece of related, deterministic material at
		// hand, so "b@example.com" becomes "b.brf" rather than "b.u".
		if label := clean(firstLabel(domain)); label != "" {
			out = strings.Trim(out+"."+label, "._-")
		}
		for len(out) < MinLen {
			out += "x"
		}
	}
	return out
}

// clean folds a raw string to the username character set, transliterating what
// it can and collapsing everything else into single dots.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case isUsernameRune(r):
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			if t, ok := translit[r]; ok {
				b.WriteString(t)
				continue
			}
			b.WriteByte('.')
		}
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	return strings.Trim(out, "._-")
}

func splitEmail(email string) (local, domain string) {
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		return email[:i], email[i+1:]
	}
	return email, ""
}

// firstLabel returns the leading DNS label of a domain ("example.com" → "brf").
func firstLabel(domain string) string {
	if i := strings.IndexByte(domain, '.'); i >= 0 {
		return domain[:i]
	}
	return domain
}

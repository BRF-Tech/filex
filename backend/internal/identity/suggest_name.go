package identity

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// SuggestName derives a display name from an e-mail address, for a form that
// fills one in while the address is typed: "jane.doe@corp.com" → "Jane Doe".
//
// The local part is split at dots, underscores, hyphens and pluses, and each
// word gets a capital first letter in the reader's language (`lang`, a BCP 47
// tag; "" is the root rules) - "ismail" is "İsmail" to a Turkish reader. What
// follows a plus is a mail tag (ada+filex@…), not part of the name, the same
// rule Suggest applies to the username. The rest of each word is kept as it
// was typed.
//
// ⚠ Here and not in the browser (filex #211, audit B4): the admin form used
// to make both suggestions itself, and its username rule turned "gözlük" into
// "g.zl.k" where Suggest - the rule an SSO account's first sign-in gets -
// gives "gozluk". One address, two usernames.
func SuggestName(email, lang string) string {
	local, _ := splitEmail(strings.TrimSpace(email))
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	upper := cases.Upper(language.Make(lang))
	words := strings.FieldsFunc(local, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '+'
	})
	for i, w := range words {
		r, size := utf8.DecodeRuneInString(w)
		if r == utf8.RuneError {
			continue
		}
		words[i] = upper.String(w[:size]) + w[size:]
	}
	return strings.Join(words, " ")
}

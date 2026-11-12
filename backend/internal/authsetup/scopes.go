package authsetup

import "strings"

// ExtraScopes reads a list of extra OpenID Connect scopes the way both places
// that take one write it: the Scopes box of a provider added on the Identity
// providers page, and FILEX_OIDC_EXTRA_SCOPES / auth.oidc.extra_scopes for the
// provider the environment defines (GitHub #137). Scopes are separated by
// spaces or commas. The driver always asks for openid, profile and email, so
// those are left out, and so is a scope named twice; the order is kept.
//
// ⚠ One reader for both: an IdP that sends groups only for an extra scope
// (VoidAuth, Authentik, Keycloak's "groups" client scope) must get the same
// request whether the provider was set up in the panel or in the environment.
func ExtraScopes(s string) []string {
	seen := map[string]bool{"openid": true, "profile": true, "email": true}
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

package apitoken

import (
	"errors"
	"fmt"
	"strings"
)

// ── Issuing a token: the ONE rule every door applies ───────────────────
//
// ⚠⚠ Owner's decision (v0.43.0): a token's scope list is always EXPLICIT.
// Until this version an empty list meant "every scope" — and "every" included
// `admin`: a token minted on the admin screen with nothing ticked read
// /api/ai/admin/users and /api/ai/admin/storages (release-candidate sweep,
// 2026-09-21; the form even said "If none are selected, all scopes are
// granted"). The self-service door had quietly patched its own copy of the
// problem by filling a default; the admin door had not. Now:
//
//   - no door mints a token without at least one verb scope (ParseIssued is
//     that rule, and every door calls it — the admin screen, self-service,
//     the desktop sign-in);
//   - `admin` is never implied: it is granted only when it is in the list;
//   - the driver reads an empty list as NOTHING (model.APIToken.HasScope),
//     so a row that somehow has one fails closed instead of open;
//   - rows that were empty before this version were rewritten by migration
//     00054 to the explicit full list, admin included — the access they had,
//     written down.

// ErrScopesRequired is an issuance request that names no verb scope.
var ErrScopesRequired = errors.New("at least one scope is required (read, write, delete, mcp or admin)")

// UnknownScopeError names a scope this server does not issue.
type UnknownScopeError struct{ Scope string }

func (e *UnknownScopeError) Error() string {
	return fmt.Sprintf("unknown scope %q (valid: %s)", e.Scope, strings.Join(ValidScopes, ", "))
}

// ParseIssued validates a requested scope list for a NEW token: every entry
// is a known verb or a well-formed `root:` confinement, duplicates collapse,
// and at least one VERB is present — a root on its own grants no verb, so it
// is refused like an empty list rather than minted as a token that can do
// nothing. It returns the verbs (in the order ValidScopes lists them) and
// the roots (in the order given).
func ParseIssued(raw string) (verbs, roots []string, err error) {
	seen := map[string]bool{}
	gotVerb := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if !IsValidScope(p) {
			return nil, nil, &UnknownScopeError{Scope: p}
		}
		if strings.HasPrefix(p, ScopeRootPrefix) {
			roots = append(roots, p)
			continue
		}
		gotVerb[p] = true
	}
	for _, v := range ValidScopes {
		if gotVerb[v] {
			verbs = append(verbs, v)
		}
	}
	if len(verbs) == 0 {
		return nil, nil, ErrScopesRequired
	}
	return verbs, roots, nil
}

// JoinScopes is the stored form of a parsed list: the verbs, then the roots.
func JoinScopes(verbs, roots []string) string {
	return strings.Join(append(append([]string(nil), verbs...), roots...), ",")
}

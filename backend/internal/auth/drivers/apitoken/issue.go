package apitoken

import (
	"errors"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/tokenperm"
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
//
// A permission with a level (package tokenperm, `comments:rw`) is not a verb:
// it does not count towards "at least one", and a list that does not name one
// holds its default - which is why ParseIssued writes a level only when it is
// not the default (tokenperm.Canonical), and why no migration rewrites the
// rows from before a permission existed.

// ErrScopesRequired is an issuance request that names no verb scope.
var ErrScopesRequired = errors.New("at least one scope is required (read, write, delete, mcp or admin)")

// UnknownScopeError names a scope this server does not issue.
type UnknownScopeError struct{ Scope string }

func (e *UnknownScopeError) Error() string {
	valid := append([]string(nil), ValidScopes...)
	valid = append(valid, ScopeRootPrefix+"<storage>://<path>")
	for _, d := range tokenperm.All() {
		for _, l := range d.Levels {
			valid = append(valid, d.Key+":"+string(l))
		}
	}
	return fmt.Sprintf("unknown scope %q (valid: %s)", e.Scope, strings.Join(valid, ", "))
}

// ConflictingLevelsError is a list that names one permission at two levels
// (`comments:read,comments:rw`): which one was meant is not for the server
// to guess.
type ConflictingLevelsError struct{ Key string }

func (e *ConflictingLevelsError) Error() string {
	return fmt.Sprintf("permission %q is named at two levels; name it once", e.Key)
}

// ParseIssued validates a requested scope list for a NEW token: every entry
// is a known verb, a well-formed `root:` confinement or a permission at one of
// its levels, duplicates collapse, and at least one VERB is present — a root
// or a permission on its own grants no verb, so it is refused like an empty
// list rather than minted as a token that can do nothing. It returns the verbs
// (in the order ValidScopes lists them), the roots (in the order given) and
// the permissions in their stored form (tokenperm.Canonical: catalogue order,
// a level that is the default left out).
func ParseIssued(raw string) (verbs, roots, perms []string, err error) {
	seen := map[string]bool{}
	gotVerb := map[string]bool{}
	levels := map[string]tokenperm.Level{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if !IsValidScope(p) {
			return nil, nil, nil, &UnknownScopeError{Scope: p}
		}
		if strings.HasPrefix(p, ScopeRootPrefix) {
			roots = append(roots, p)
			continue
		}
		if key, level, isPerm, _ := tokenperm.ParseEntry(p); isPerm {
			if prev, ok := levels[key]; ok && prev != level {
				return nil, nil, nil, &ConflictingLevelsError{Key: key}
			}
			levels[key] = level
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
		return nil, nil, nil, ErrScopesRequired
	}
	return verbs, roots, tokenperm.Canonical(levels), nil
}

// JoinScopes is the stored form of a parsed list: the verbs, then the roots,
// then the permissions.
func JoinScopes(verbs, roots, perms []string) string {
	out := append(append([]string(nil), verbs...), roots...)
	return strings.Join(append(out, perms...), ",")
}

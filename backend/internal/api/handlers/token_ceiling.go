package handlers

import (
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── a credential minted by a token is never wider than that token ────────
//
// The self-service credential surfaces (/api/tokens, S3 access keys, SSH keys,
// NFS exports — the RequirePersonalCaller group in routes.go) cap what they
// mint by the OWNER's role and grants. A caller authenticated with a narrower
// personal token — read-only, confined to one folder by `root:`, or expiring —
// could therefore mint a wider credential for its owner than it holds itself:
// a read-only token posting to /api/tokens got a read,write,delete one, and an
// S3 key minted without naming a parent carried the owner's full access.
//
// It was the same hole the review of PR #35 found (and closed) at
// /api/auth/desktop/complete, still open on these four doors, and #35 made it
// reachable from one more place: a paired desktop now holds a PERSON's token,
// which these routes let in (they refuse only app tokens).
//
// The rule: a token caller may mint only what it could do itself. A browser
// session — the person — is unaffected, and so is a token that already holds
// everything its owner could put into a file credential (the full desktop
// token): it cannot pass on more than it has.

// tokenCeiling is the API token a request authenticates with, parsed.
type tokenCeiling struct {
	tok   *model.APIToken
	verbs map[string]bool
	root  *confine.Root
}

// ceilingOf returns the calling token's ceiling, or nil for a browser session.
func ceilingOf(r *http.Request) *tokenCeiling {
	tok := auth.TokenFrom(r.Context())
	if tok == nil {
		return nil
	}
	c := &tokenCeiling{tok: tok, verbs: map[string]bool{}}
	for _, s := range strings.Split(tok.Scopes, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, apitoken.ScopeRootPrefix) {
			if root, ok := confine.ParseRoot(strings.TrimSpace(strings.TrimPrefix(s, apitoken.ScopeRootPrefix))); ok && c.root == nil {
				c.root = &root
			}
			continue
		}
		c.verbs[s] = true
	}
	return c
}

// narrowerThanOwner reports whether the token can do less with files than its
// owner can: it is confined to a folder, it expires, or it lacks a file verb
// the owner holds (a viewer holds only `read`).
func (c *tokenCeiling) narrowerThanOwner(u *model.User) bool {
	if c.root != nil || c.tok.ExpiresAt != nil {
		return true
	}
	need := []string{apitoken.ScopeRead, apitoken.ScopeWrite, apitoken.ScopeDelete}
	if u.IsViewer() {
		need = []string{apitoken.ScopeRead}
	}
	for _, v := range need {
		if !c.verbs[v] {
			return true
		}
	}
	return false
}

// refuseWider answers a mint the calling token may not make.
func refuseWider(w http.ResponseWriter, why string) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":  "this token cannot create a credential wider than itself: " + why + ". Sign in in a browser to create it, or use a token that holds it",
		"reason": "token_ceiling",
	})
}

// allowsToken checks a token the caller asked /api/tokens for (its scopes in
// the canonical form cappedScopes returns). It returns "" when allowed, else
// why not.
func (c *tokenCeiling) allowsToken(scopes string) string {
	verbs, roots, err := apitoken.ParseIssued(scopes)
	if err != nil {
		return err.Error()
	}
	for _, v := range verbs {
		if !c.verbs[v] {
			return "it does not hold the `" + v + "` scope"
		}
	}
	if c.root == nil {
		return ""
	}
	if len(roots) == 0 {
		return "it is confined to " + c.root.Adapter + "://" + c.root.Rel + ", so what it creates must be confined there too"
	}
	for _, rs := range roots {
		root, ok := confine.ParseRoot(strings.TrimSpace(strings.TrimPrefix(rs, apitoken.ScopeRootPrefix)))
		if !ok || !c.root.Within(root.Adapter, root.Rel) {
			return "it is confined to " + c.root.Adapter + "://" + c.root.Rel
		}
	}
	return ""
}

package apitoken

import (
	"errors"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The one issuance rule (owner's decision, v0.43.0): no door mints a token
// without an explicit list naming at least one verb, and admin is granted
// only when it is named.
func TestParseIssued_AnEmptyListIsRefused(t *testing.T) {
	for _, raw := range []string{"", " ", ",", " , ", "root:main://docs"} {
		if _, _, err := ParseIssued(raw); !errors.Is(err, ErrScopesRequired) {
			t.Errorf("%q: want ErrScopesRequired, got %v", raw, err)
		}
	}
}

func TestParseIssued_AdminIsNeverImplied(t *testing.T) {
	verbs, roots, err := ParseIssued(" mcp, read , read,root:main://docs ")
	if err != nil {
		t.Fatal(err)
	}
	if got := JoinScopes(verbs, roots); got != "read,mcp,root:main://docs" {
		t.Fatalf("canonical form %q", got)
	}
	tok := &model.APIToken{Scopes: JoinScopes(verbs, roots)}
	if tok.HasScope(ScopeAdmin) {
		t.Fatal("admin was granted without being named")
	}
	verbs, _, err = ParseIssued("admin")
	if err != nil || len(verbs) != 1 || verbs[0] != ScopeAdmin {
		t.Fatalf("admin named on purpose: %v %v", verbs, err)
	}
}

func TestParseIssued_UnknownIsNamed(t *testing.T) {
	_, _, err := ParseIssued("read,bogus")
	var u *UnknownScopeError
	if !errors.As(err, &u) || u.Scope != "bogus" {
		t.Fatalf("want UnknownScopeError(bogus), got %v", err)
	}
}

// The driver fails CLOSED: a row whose list is empty grants nothing — not
// "everything", which is what it granted until v0.43.0.
func TestHasScope_AnEmptyListGrantsNothing(t *testing.T) {
	for _, scopes := range []string{"", "  "} {
		tok := &model.APIToken{Scopes: scopes}
		for _, s := range ValidScopes {
			if tok.HasScope(s) {
				t.Errorf("%q grants %s", scopes, s)
			}
		}
	}
	var nilTok *model.APIToken
	if nilTok.HasScope(ScopeRead) {
		t.Error("a nil token grants read")
	}
}

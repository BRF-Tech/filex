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
	for _, raw := range []string{"", " ", ",", " , ", "root:main://docs", "comments:rw", "comments:rw,root:main://docs"} {
		if _, _, _, err := ParseIssued(raw); !errors.Is(err, ErrScopesRequired) {
			t.Errorf("%q: want ErrScopesRequired, got %v", raw, err)
		}
	}
}

func TestParseIssued_AdminIsNeverImplied(t *testing.T) {
	verbs, roots, perms, err := ParseIssued(" mcp, read , read,root:main://docs ")
	if err != nil {
		t.Fatal(err)
	}
	if got := JoinScopes(verbs, roots, perms); got != "read,mcp,root:main://docs" {
		t.Fatalf("canonical form %q", got)
	}
	tok := &model.APIToken{Scopes: JoinScopes(verbs, roots, perms)}
	if tok.HasScope(ScopeAdmin) {
		t.Fatal("admin was granted without being named")
	}
	verbs, _, _, err = ParseIssued("admin")
	if err != nil || len(verbs) != 1 || verbs[0] != ScopeAdmin {
		t.Fatalf("admin named on purpose: %v %v", verbs, err)
	}
}

func TestParseIssued_UnknownIsNamed(t *testing.T) {
	_, _, _, err := ParseIssued("read,bogus")
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

// A permission with a level (task #157) rides in the same list: its level is
// written only when it is not the default, a level the permission does not
// have is refused by name, and two levels of one permission are refused.
func TestParseIssued_APermissionAtALevel(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"read,write,mcp", "read,write,mcp"},
		{"read,comments:read", "read"},
		{"comments:rw,read", "read,comments:rw"},
		{"read,comments:write", "read,comments:rw"},
		{"read,comments:rw,comments:write,root:main://docs", "read,root:main://docs,comments:rw"},
	}
	for _, c := range cases {
		verbs, roots, perms, err := ParseIssued(c.raw)
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		if got := JoinScopes(verbs, roots, perms); got != c.want {
			t.Errorf("%q: stored as %q, want %q", c.raw, got, c.want)
		}
	}

	_, _, _, err := ParseIssued("read,comments:admin")
	var u *UnknownScopeError
	if !errors.As(err, &u) || u.Scope != "comments:admin" {
		t.Errorf("a level comments does not have: want UnknownScopeError(comments:admin), got %v", err)
	}
	_, _, _, err = ParseIssued("read,comments:read,comments:rw")
	var c *ConflictingLevelsError
	if !errors.As(err, &c) || c.Key != "comments" {
		t.Errorf("two levels of one permission: want ConflictingLevelsError(comments), got %v", err)
	}
	for _, ok := range []string{"comments:read", "comments:rw", "comments:write"} {
		if !IsValidScope(ok) {
			t.Errorf("%s is a valid scope", ok)
		}
	}
	for _, bad := range []string{"comments", "comments:", "comments:none", "comment:rw"} {
		if IsValidScope(bad) {
			t.Errorf("%s is not a valid scope", bad)
		}
	}
}

package oidc

import "testing"

func TestClaimContains(t *testing.T) {
	nested := map[string]any{
		"realm_access": map[string]any{"roles": []any{"user", "filex-admin"}},
	}
	if !claimContains(nested, "realm_access.roles", "filex-admin") {
		t.Fatal("dotted path array should match")
	}
	if claimContains(nested, "realm_access.roles", "nope") {
		t.Fatal("should not match absent role")
	}
	flat := map[string]any{"groups": "admins"}
	if !claimContains(flat, "groups", "admins") {
		t.Fatal("flat string claim should match")
	}
	if claimContains(nested, "missing.path", "x") {
		t.Fatal("missing path should not panic/match")
	}
}

func TestParseJWTClaims(t *testing.T) {
	// header.payload.sig — payload = {"realm_access":{"roles":["filex-admin"]}}
	tokenPayload := "eyJyZWFsbV9hY2Nlc3MiOnsicm9sZXMiOlsiZmlsZXgtYWRtaW4iXX19"
	tok := "aaa." + tokenPayload + ".bbb"
	c := parseJWTClaims(tok)
	if c == nil {
		t.Fatal("should parse JWT payload")
	}
	if !claimContains(c, "realm_access.roles", "filex-admin") {
		t.Fatal("parsed claims should contain the role")
	}
	if parseJWTClaims("not-a-jwt") != nil {
		t.Fatal("opaque token should return nil")
	}
}

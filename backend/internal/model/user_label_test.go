package model

import "testing"

// ⚠ One rule for naming a person (QA, 2026-09-21: "admin2" in the Owner
// column, "admin@local" in notifications, a full name on signatures). The
// browser's twin is packages/core/src/lib/personName.ts; the cases below are
// the same cases web/tests/lib/personName.test.ts holds it to.
func TestPersonLabel(t *testing.T) {
	cases := []struct{ display, username, email, want string }{
		{"Ayşe Yılmaz", "ayse", "ayse@example.com", "Ayşe Yılmaz"},
		{"", "ayse", "ayse@example.com", "ayse"},
		{"  ", "  ayse ", "ayse@example.com", "ayse"},
		{"", "", "ayse@example.com", "ayse@example.com"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := PersonLabel(c.display, c.username, c.email); got != c.want {
			t.Errorf("PersonLabel(%q, %q, %q) = %q, want %q", c.display, c.username, c.email, got, c.want)
		}
	}
	var nobody *User
	if nobody.Label() != "" {
		t.Error("a nil user has no label")
	}
	u := &User{DisplayName: "", Username: "admin2", Email: "admin@local"}
	if u.Label() != "admin2" {
		t.Errorf("Label() = %q", u.Label())
	}
}

package identity

import (
	"errors"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestValidateAcceptsAndRejects(t *testing.T) {
	valid := []string{"ada", "grace", "g.hopper", "a_b-c", "user2", "abc"}
	for _, u := range valid {
		if err := Validate(u); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", u, err)
		}
	}

	// Each rejection is a rule someone will otherwise reintroduce.
	rejects := map[string]error{
		"":                            ErrInvalidUsername, // empty
		"ab":                          ErrInvalidUsername, // shorter than MinLen
		strings.Repeat("a", MaxLen+1): ErrInvalidUsername, // longer than MaxLen
		"Ada":                         ErrInvalidUsername, // not lowercase
		"2ada":                        ErrInvalidUsername, // leading digit
		"two words":                   ErrInvalidUsername, // space
		"ada/x":                       ErrInvalidUsername, // path separator
		"çığır":                       ErrInvalidUsername, // non-ASCII
		"admin":                       ErrReservedUsername,
		"root":                        ErrReservedUsername,
		"sftp":                        ErrReservedUsername,
	}
	for u, want := range rejects {
		err := Validate(u)
		if err == nil {
			t.Errorf("Validate(%q) = nil, want %v", u, want)
			continue
		}
		if !errors.Is(err, want) {
			t.Errorf("Validate(%q) = %v, want %v", u, err, want)
		}
	}
}

// An `@` in a username would make the two namespaces overlap, and Resolve's
// "never try both" rule depends on them being disjoint. This is the test that
// keeps that guarantee true.
func TestValidateRejectsAtSignSoNamespacesStayDisjoint(t *testing.T) {
	err := Validate("ada@example.com")
	if !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("Validate(e-mail as username) = %v, want ErrInvalidUsername", err)
	}
	if !strings.Contains(err.Error(), "@") {
		t.Errorf("error should name the offending character so the user knows what to do: %v", err)
	}
}

func TestSuggest(t *testing.T) {
	cases := []struct{ email, want string }{
		{"ada@example.com", "ada"},
		{"ADA@EXAMPLE.COM", "ada"},        // case folded
		{"  ada@example.com  ", "ada"},    // trimmed
		{"ada+filex@example.com", "ada"},       // mail tag dropped
		{"g.hopper@example.com", "g.hopper"},   // dots kept
		{"gözlük@example.com", "gozluk"},       // transliterated, not mangled
		{"şişe.ürün@example.com", "sise.urun"}, // ditto
		{"2fast@example.com", "u2fast"},        // leading digit prefixed
		// ⚠ The domain is part of the EXPECTATION here, so this case must use a
		// domain nothing rewrites — the public-export transform maps the
		// project's own host to example.com and turned "b.brf" into "b.example".
		{"b@short.test", "b.short"}, // too short: domain label added
		{"admin@example.com", "admin"},   // reserved is Suggest's problem to hand on, not to dodge
		{"a..b@example.com", "a.b"},      // separators collapsed
		{".ada.@example.com", "ada"},     // separators trimmed
		{strings.Repeat("x", 40) + "@example.com", strings.Repeat("x", MaxLen)},
	}
	for _, c := range cases {
		if got := Suggest(c.email); got != c.want {
			t.Errorf("Suggest(%q) = %q, want %q", c.email, got, c.want)
		}
	}
}

// A backfill that renames people on its second run is worse than one that
// never ran, so determinism is a contract, not an implementation detail.
func TestSuggestIsDeterministic(t *testing.T) {
	for _, email := range []string{"ada@example.com", "çığır@example.com", "b@short.test"} {
		first := Suggest(email)
		for i := 0; i < 5; i++ {
			if got := Suggest(email); got != first {
				t.Fatalf("Suggest(%q) is not deterministic: %q then %q", email, first, got)
			}
		}
	}
}

// Everything Suggest produces must be something Validate accepts, or the
// backfill would derive names it then refuses to claim.
func TestSuggestOutputSatisfiesValidateExceptWhenReserved(t *testing.T) {
	emails := []string{
		"ada@example.com", "b@example.com", "2fast@example.com", "çığır@example.com",
		"şişe.ürün@example.com", "a..b@example.com", ".x.@example.com",
		strings.Repeat("x", 40) + "@example.com", "ada+tag@example.com",
	}
	for _, e := range emails {
		got := Suggest(e)
		if err := Validate(got); err != nil && !errors.Is(err, ErrReservedUsername) {
			t.Errorf("Suggest(%q) = %q, which Validate rejects: %v", e, got, err)
		}
	}
}

func TestLooksLikeEmail(t *testing.T) {
	if !LooksLikeEmail("ada@example.com") {
		t.Error("an address with @ must be read as an e-mail")
	}
	if LooksLikeEmail("ada") {
		t.Error("a bare name must be read as a username")
	}
}

func TestNames(t *testing.T) {
	u := &model.User{Email: "ada@example.com", Username: "ada"}
	for _, id := range []string{"ada@example.com", "ADA@EXAMPLE.COM", "ada", "ADA", " ada "} {
		if !Names(u, id) {
			t.Errorf("Names(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "grace", "grace@example.com"} {
		if Names(u, id) {
			t.Errorf("Names(%q) = true, want false", id)
		}
	}

	// An account with no username yet must not be matched by an empty one.
	unnamed := &model.User{Email: "x@example.com"}
	if Names(unnamed, "") {
		t.Error("an empty identifier must never name an account")
	}
}

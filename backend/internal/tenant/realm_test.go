package tenant

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckRealm(t *testing.T) {
	ok := []string{"acme", "Acme", " beta-co ", "a", "x9", "9x", strings.Repeat("a", RealmMaxLen)}
	for _, r := range ok {
		if err := CheckRealm(r); err != nil {
			t.Errorf("%q refused: %v", r, err)
		}
	}
	bad := map[string]error{
		"":                                 ErrRealmEmpty,
		"   ":                              ErrRealmEmpty,
		"acme/x":                           ErrRealmInvalid,
		"a@b":                              ErrRealmInvalid,
		`corp\x`:                           ErrRealmInvalid,
		"acme.co":                          ErrRealmInvalid,
		"a_b":                              ErrRealmInvalid,
		"-acme":                            ErrRealmInvalid,
		"acme-":                            ErrRealmInvalid,
		"açme":                             ErrRealmInvalid,
		strings.Repeat("a", RealmMaxLen+1): ErrRealmInvalid,
		"admin":                            ErrRealmReserved,
		"LOCAL":                            ErrRealmReserved,
		"default":                          ErrRealmReserved,
	}
	for r, want := range bad {
		if err := CheckRealm(r); !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", r, err, want)
		}
	}
}

func TestSplitLogin(t *testing.T) {
	cases := []struct {
		in, realm, name string
		named           bool
	}{
		{"acme/alex", "acme", "alex", true},
		{" ACME/Alex ", "acme", "Alex", true},
		{"acme/alex@x.com", "acme", "alex@x.com", true},
		{"acme/a/b@x.com", "acme", "a/b@x.com", true},
		{"/alex", "", "alex", true},
		{"alex", "", "alex", false},
		{"alex@x.com", "", "alex@x.com", false},
		{"a@b/c", "", "a@b/c", false},
		{`corp\x/y`, "", `corp\x/y`, false},
		{"", "", "", false},
	}
	for _, c := range cases {
		realm, name, named := SplitLogin(c.in)
		if realm != c.realm || name != c.name || named != c.named {
			t.Errorf("SplitLogin(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, realm, name, named, c.realm, c.name, c.named)
		}
	}
}

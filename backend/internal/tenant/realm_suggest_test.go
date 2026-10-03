package tenant

import (
	"strings"
	"testing"
)

// The tenant screen suggests a realm from the slug while a tenant is being
// created (the realm is immutable afterwards, so a person sees it first).
func TestSuggestRealm(t *testing.T) {
	cases := map[string]string{
		"acme":                   "acme",
		"Acme":                   "acme",
		"  Acme Corp  ":          "acme-corp",
		"acme_corp":              "acme-corp",
		"acme--corp":             "acme-corp",
		"Müşteri Hizmetleri":     "musteri-hizmetleri",
		"IŞIK":                   "isik",
		"İstanbul Şubesi":        "istanbul-subesi",
		"çağrı":                  "cagri",
		"Straße":                 "strasse",
		"Øresund":                "oresund",
		"acme.example.com":       "acme-example-com",
		"-acme-":                 "acme",
		"___":                    "",
		"":                       "",
		"a/b@c":                  "a-b-c",
		"x9":                     "x9",
		strings.Repeat("ab", 40): strings.Repeat("ab", 31) + "a",
	}
	for in, want := range cases {
		if got := SuggestRealm(in); got != want {
			t.Errorf("SuggestRealm(%q) = %q, want %q", in, got, want)
		}
	}
	// Every non-empty suggestion is realm-shaped (it may still be reserved).
	for in := range cases {
		got := SuggestRealm(in)
		if got == "" {
			continue
		}
		if err := CheckRealm(got); err != nil && !IsReservedRealm(got) {
			t.Errorf("SuggestRealm(%q) = %q is not a realm: %v", in, got, err)
		}
	}
}

func TestSuggestRealm_CutAtTheLimitLeavesNoTrailingDash(t *testing.T) {
	in := strings.Repeat("a", RealmMaxLen-1) + " b"
	got := SuggestRealm(in)
	if strings.HasSuffix(got, "-") || len(got) > RealmMaxLen {
		t.Fatalf("SuggestRealm cut badly: %q (%d)", got, len(got))
	}
}

func TestRealmCandidates(t *testing.T) {
	got := RealmCandidates("Acme Corp", 3)
	want := []string{"acme-corp", "acme-corp-2", "acme-corp-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("RealmCandidates = %v, want %v", got, want)
	}
	if RealmCandidates("___", 3) != nil {
		t.Fatal("nothing realm-shaped: no candidates")
	}
	long := RealmCandidates(strings.Repeat("a", RealmMaxLen), 12)
	for _, c := range long[1:] {
		if err := CheckRealm(c); err != nil {
			t.Errorf("candidate %q: %v", c, err)
		}
	}
	if long[11] != strings.Repeat("a", RealmMaxLen-3)+"-12" {
		t.Errorf("long candidate = %q", long[11])
	}
	// A reserved suggestion is followed by ones that are not.
	res := RealmCandidates("admin", 2)
	if !IsReservedRealm(res[0]) || CheckRealm(res[1]) != nil {
		t.Errorf("reserved candidates = %v", res)
	}
}

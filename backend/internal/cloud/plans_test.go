package cloud

import (
	"strings"
	"testing"
)

// Plan-catalog parsing (FILEX_CLOUD_PLANS): empty input falls back to the
// built-in free skeleton; malformed input errors so /api/cloud/status can
// surface it.

func TestParsePlans_EmptyFallsBackToDefault(t *testing.T) {
	plans, err := ParsePlans("")
	if err != nil {
		t.Fatalf("empty input must not error: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != "free" {
		t.Fatalf("expected the single default free plan, got %+v", plans)
	}
	if plans[0].Limits.StorageBytes == 0 {
		t.Fatalf("default plan should carry a storage limit")
	}
}

func TestParsePlans_Valid(t *testing.T) {
	raw := `[
		{"id":"free","name":"Free","limits":{"storage_bytes":1073741824,"max_users":3}},
		{"id":"pro","name":"Pro","price_monthly":"9.90 EUR","stripe_price_id":"price_123","limits":{"storage_bytes":107374182400,"max_users":25}}
	]`
	plans, err := ParsePlans(raw)
	if err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[1].ID != "pro" || plans[1].StripePriceID != "price_123" || plans[1].Limits.MaxUsers != 25 {
		t.Fatalf("pro plan mis-parsed: %+v", plans[1])
	}
}

func TestParsePlans_Errors(t *testing.T) {
	cases := map[string]string{
		"bad json":     `{not json`,
		"empty list":   `[]`,
		"missing id":   `[{"name":"NoID"}]`,
		"duplicate id": `[{"id":"a","name":"A"},{"id":"a","name":"A2"}]`,
	}
	for name, raw := range cases {
		if _, err := ParsePlans(raw); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// New() must keep booting on a broken catalog: defaults active + the parse
// error remembered for /api/cloud/status.
func TestNew_BadPlansFallsBackAndRemembersError(t *testing.T) {
	svc := New(nil, nil, `{broken`, "")
	if svc.PlansErr() == "" {
		t.Fatalf("expected a remembered plans error")
	}
	if len(svc.Plans()) != 1 || svc.Plans()[0].ID != "free" {
		t.Fatalf("expected default fallback catalog, got %+v", svc.Plans())
	}
	if !strings.Contains(svc.PlansErr(), "FILEX_CLOUD_PLANS") {
		t.Fatalf("plans error should name the env var: %q", svc.PlansErr())
	}
}

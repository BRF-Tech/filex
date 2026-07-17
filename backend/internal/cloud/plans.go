// Package cloud is the self-serve cloud/SaaS PREPARATION skeleton
// (v0.7 "Kimlik" E3 — docs/CLOUD.md).
//
// Everything here is master-gated by FILEX_CLOUD (config.CloudConfig.Enabled,
// default OFF). While the flag is off the package is never constructed:
// api.BuildRouter skips the /api/cloud route block entirely, capabilities
// carry no cloud field, and the migration-00021 provider columns (plan /
// limits_json / billing_ref) stay NULL. Turning the flag on only makes sense
// together with FILEX_MULTI_TENANT=1 — a signed-up "tenant" IS a provider row
// from the v0.1.61 native multi-tenancy foundation, provisioned through the
// exact same db.Store.CreateProvider primitive the /api/admin/providers
// lifecycle API uses (no parallel provisioning path exists).
package cloud

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PlanLimits are the resource ceilings a plan grants a tenant. The zero value
// means "unlimited" for that dimension. Stamped as a JSON snapshot into
// providers.limits_json at signup so later catalog edits don't silently
// change an existing tenant's entitlement.
type PlanLimits struct {
	// StorageBytes caps the tenant's total storage. 0 = unlimited.
	StorageBytes int64 `json:"storage_bytes,omitempty"`
	// MaxUsers caps the tenant's user count. 0 = unlimited.
	MaxUsers int `json:"max_users,omitempty"`
}

// Plan is one entry of the config-driven catalog (FILEX_CLOUD_PLANS).
type Plan struct {
	// ID is the stable plan key stored in providers.plan (e.g. "free", "pro").
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// PriceMonthly is a display string (e.g. "9.90 EUR"); "" = free.
	PriceMonthly string `json:"price_monthly,omitempty"`
	// StripePriceID links the plan to a Stripe Price for checkout sessions.
	// Empty on free plans / while billing is not wired.
	StripePriceID string `json:"stripe_price_id,omitempty"`
	// Limits are the plan's resource ceilings.
	Limits PlanLimits `json:"limits"`
}

// DefaultPlans is the built-in catalog used when FILEX_CLOUD_PLANS is empty:
// a single free skeleton plan so the signup flow is exercisable without any
// configuration.
func DefaultPlans() []Plan {
	return []Plan{{
		ID:     "free",
		Name:   "Free",
		Limits: PlanLimits{StorageBytes: 1 << 30, MaxUsers: 3}, // 1 GiB / 3 users
	}}
}

// ParsePlans decodes the FILEX_CLOUD_PLANS JSON array. An empty input returns
// DefaultPlans(). Errors (bad JSON, missing/duplicate ids) are returned so
// the caller can surface them; callers that must keep booting fall back to
// DefaultPlans() and report the error via /api/cloud/status.
func ParsePlans(raw string) ([]Plan, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultPlans(), nil
	}
	var plans []Plan
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		return nil, fmt.Errorf("cloud: FILEX_CLOUD_PLANS: %w", err)
	}
	if len(plans) == 0 {
		return nil, fmt.Errorf("cloud: FILEX_CLOUD_PLANS: empty plan list")
	}
	seen := map[string]bool{}
	for i, p := range plans {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			return nil, fmt.Errorf("cloud: FILEX_CLOUD_PLANS: plan #%d has no id", i)
		}
		if seen[id] {
			return nil, fmt.Errorf("cloud: FILEX_CLOUD_PLANS: duplicate plan id %q", id)
		}
		seen[id] = true
	}
	return plans, nil
}

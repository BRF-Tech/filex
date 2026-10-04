package e2epolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// SettingE2EPolicyCarried marks that a single-tenant install's policy (the
// e2e.policy setting) has been carried to the supertenant's row once, when
// the install first started multi-tenant (CarryInstancePolicy). Its value is
// when that happened. While it is there nothing is carried again.
const SettingE2EPolicyCarried = "e2e.policy.carried"

// CarryInstancePolicy keeps a single-tenant install's choice when it becomes
// a multi-tenant one (operator decision 2026-10-03).
//
// On a single-tenant install the policy is the e2e.policy setting. Once
// FILEX_MULTI_TENANT is on, everybody with a tenant is judged by their
// tenant's row (providers.e2e_policy) instead, and the supertenant's row
// starts where migration 00080 put it: permitted. Read as it is, an install
// that had switched encryption off would find it silently on again for the
// platform's own people. So at the first multi-tenant start the setting is
// copied to the supertenant's row - only when that row still holds the
// migration's default and the setting says something else - logged, and
// written to the audit log. The setting itself is left alone (a return to
// single-tenant reads it again), and SettingE2EPolicyCarried is written, so
// a later start changes nothing, whatever an administrator has chosen since.
//
// It answers whether it changed the supertenant's row. Nothing to carry - a
// single-tenant install, no setting, no supertenant, a setting nobody can
// read as a policy - is no change and no error.
func CarryInstancePolicy(ctx context.Context, store db.Store, multiTenant bool) (bool, error) {
	if !multiTenant || store == nil {
		return false, nil
	}
	if _, err := store.GetSetting(ctx, SettingE2EPolicyCarried); err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("e2epolicy: %s: %w", SettingE2EPolicyCarried, err)
	}
	sup, err := store.GetSupertenant(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("e2epolicy: the supertenant: %w", err)
	}
	if sup == nil {
		// No supertenant yet: nothing to carry to, and nothing is marked, so
		// the start that has one carries it.
		return false, nil
	}
	raw, err := store.GetSetting(ctx, model.SettingE2EPolicy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("e2epolicy: %s: %w", model.SettingE2EPolicy, err)
	}
	carried := false
	if policy := strings.TrimSpace(raw); model.ValidE2EPolicy(policy) && policy != model.E2EPolicyPermitted {
		cur, err := store.GetProviderE2E(ctx, sup.ID)
		if err != nil {
			return false, fmt.Errorf("e2epolicy: the supertenant's policy: %w", err)
		}
		if knownPolicy(cur.Policy) == model.E2EPolicyPermitted {
			if err := store.SetProviderE2EPolicy(ctx, sup.ID, policy); err != nil {
				return false, fmt.Errorf("e2epolicy: carry the policy: %w", err)
			}
			carried = true
			slog.Info("e2e policy: this install's policy now binds the platform's own tenant",
				slog.String("policy", policy), slog.Int64("tenant_id", sup.ID),
				slog.String("from", model.SettingE2EPolicy))
			Audit(ctx, store, AuditRow{
				Action: AuditActionPolicyUpdate, TargetType: AuditTargetPolicy,
				TargetID: strconv.FormatInt(sup.ID, 10), TargetName: sup.Name,
				Meta: map[string]any{"before": model.E2EPolicyPermitted, "after": policy, "carried_from": model.SettingE2EPolicy},
			})
		}
	}
	if err := store.UpsertSetting(ctx, SettingE2EPolicyCarried, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return carried, fmt.Errorf("e2epolicy: %s: %w", SettingE2EPolicyCarried, err)
	}
	return carried, nil
}

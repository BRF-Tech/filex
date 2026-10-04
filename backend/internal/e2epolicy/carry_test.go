package e2epolicy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// supertenantOf is the install's supertenant, which a fresh database has.
func supertenantOf(t *testing.T, store db.Store) *model.Provider {
	t.Helper()
	sup, err := store.GetSupertenant(context.Background())
	require.NoError(t, err)
	require.NotNil(t, sup, "a fresh database has a supertenant")
	return sup
}

func superPolicy(t *testing.T, store db.Store, id int64) string {
	t.Helper()
	pe, err := store.GetProviderE2E(context.Background(), id)
	require.NoError(t, err)
	return pe.Policy
}

// A single-tenant install that switched encryption off and then starts
// multi-tenant keeps it off for the platform's own people: the setting is
// carried to the supertenant's row at the first multi-tenant start, once.
// After that nothing is carried again, whatever an administrator chooses
// (operator decision 2026-10-03).
func TestCarryInstancePolicy_OnceAtTheFirstMultiTenantStart(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	sup := supertenantOf(t, store)
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))

	carried, err := e2epolicy.CarryInstancePolicy(ctx, store, false)
	require.NoError(t, err)
	assert.False(t, carried, "a single-tenant start reads the setting itself")
	assert.Equal(t, model.E2EPolicyPermitted, superPolicy(t, store, sup.ID))

	carried, err = e2epolicy.CarryInstancePolicy(ctx, store, true)
	require.NoError(t, err)
	assert.True(t, carried)
	assert.Equal(t, model.E2EPolicyOff, superPolicy(t, store, sup.ID), "the platform's own tenant keeps the install's choice")
	rows := e2eAuditActions(t, store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1, "the carry is in the audit log")
	assert.Equal(t, model.SettingE2EPolicy, rows[0].Metadata["carried_from"])

	// An administrator chooses again; the next start leaves it.
	require.NoError(t, store.SetProviderE2EPolicy(ctx, sup.ID, model.E2EPolicyPermitted))
	carried, err = e2epolicy.CarryInstancePolicy(ctx, store, true)
	require.NoError(t, err)
	assert.False(t, carried, "carried once, never again")
	assert.Equal(t, model.E2EPolicyPermitted, superPolicy(t, store, sup.ID))
	raw, err := store.GetSetting(ctx, model.SettingE2EPolicy)
	require.NoError(t, err)
	assert.Equal(t, model.E2EPolicyOff, raw, "the setting itself is left alone")
	assert.Len(t, e2eAuditActions(t, store, e2epolicy.AuditActionPolicyUpdate), 1)
}

// A choice made on the supertenant's row already is never overwritten, and
// nothing to carry - no setting, or the default - changes nothing; both are
// marked, so a later setting is not carried either.
func TestCarryInstancePolicy_NeverOverAChoiceAndNothingFromNothing(t *testing.T) {
	ctx := context.Background()

	_, store := testutil.NewTestDB(t)
	sup := supertenantOf(t, store)
	require.NoError(t, store.SetProviderE2EPolicy(ctx, sup.ID, model.E2EPolicyApproval))
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyOff))
	carried, err := e2epolicy.CarryInstancePolicy(ctx, store, true)
	require.NoError(t, err)
	assert.False(t, carried)
	assert.Equal(t, model.E2EPolicyApproval, superPolicy(t, store, sup.ID), "a choice made there stands")

	_, store = testutil.NewTestDB(t)
	sup = supertenantOf(t, store)
	carried, err = e2epolicy.CarryInstancePolicy(ctx, store, true)
	require.NoError(t, err)
	assert.False(t, carried, "no setting, nothing to carry")
	require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyAdmins))
	carried, err = e2epolicy.CarryInstancePolicy(ctx, store, true)
	require.NoError(t, err)
	assert.False(t, carried, "the first multi-tenant start is past")
	assert.Equal(t, model.E2EPolicyPermitted, superPolicy(t, store, sup.ID))
}

func e2eAuditActions(t *testing.T, store db.Store, action string) []*model.AuditEntry {
	t.Helper()
	rows, _, err := store.ListAuditFiltered(context.Background(), nil, action, nil, nil, 100, 0)
	require.NoError(t, err)
	out := make([]*model.AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Entry)
	}
	return out
}

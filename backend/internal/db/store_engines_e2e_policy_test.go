package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestProviderE2EOnEveryEngine walks the two tenant columns of migration 00080
// on every engine. The default provider was written by 00014, long before the
// columns existed: that it reads "allowed, permitted" is the upgrade promise
// (nobody's access changes). A tenant made by CreateProvider — whose INSERT
// does not name the columns — starts there too. The columns are written only
// through their own accessor: the provider CRUD leaves them alone.
func TestProviderE2EOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			def, err := store.GetProviderBySlug(ctx, model.DefaultProviderSlug)
			require.NoError(t, err)
			require.NotNil(t, def)
			got, err := store.GetProviderE2E(ctx, def.ID)
			require.NoError(t, err)
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got,
				"a provider older than the columns reads as filex always behaved")

			alpha, err := store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
			require.NoError(t, err)
			got, err = store.GetProviderE2E(ctx, alpha.ID)
			require.NoError(t, err)
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got, "a new tenant starts there too")

			want := model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}
			require.NoError(t, store.SetProviderE2E(ctx, alpha.ID, want))
			got, err = store.GetProviderE2E(ctx, alpha.ID)
			require.NoError(t, err)
			require.Equal(t, want, got)

			got, err = store.GetProviderE2E(ctx, def.ID)
			require.NoError(t, err)
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, got, "another tenant is untouched")

			// The provider CRUD does not know the columns, so an edit of the
			// tenant's name cannot switch its encryption back on.
			alpha.Name = "Alpha A.Ş."
			require.NoError(t, store.UpdateProvider(ctx, alpha))
			got, err = store.GetProviderE2E(ctx, alpha.ID)
			require.NoError(t, err)
			require.Equal(t, want, got)

			require.Error(t, store.SetProviderE2E(ctx, alpha.ID, model.ProviderE2E{Allowed: true, Policy: "everyone"}),
				"a policy that is not one of the four is refused by the store itself")
			got, err = store.GetProviderE2E(ctx, alpha.ID)
			require.NoError(t, err)
			require.Equal(t, want, got, "the refused write changed nothing")

			_, err = store.GetProviderE2E(ctx, alpha.ID+1000)
			require.True(t, errors.Is(err, sql.ErrNoRows), "no such provider: sql.ErrNoRows, got %v", err)
		})
	}
}

// TestProviderE2EColumnByColumnOnEveryEngine: the tenant's policy and the
// platform's ceiling are written one column at a time, so writing one never
// carries a stale copy of the other back. With several filex instances on one
// database a lock inside one process cannot stop that; a statement that names
// only its own column does. Neither setter reads RowsAffected: MySQL counts
// changed rows, so writing the value already stored is 0 there, not an error.
func TestProviderE2EColumnByColumnOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			alpha, err := store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
			require.NoError(t, err)
			beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", AuthType: model.AuthTypeLocal, Enabled: true})
			require.NoError(t, err)
			read := func(id int64) model.ProviderE2E {
				t.Helper()
				got, err := store.GetProviderE2E(ctx, id)
				require.NoError(t, err)
				return got
			}

			// The operator switches the ceiling off; the tenant's policy
			// written after it, the same value again, leaves it off.
			require.NoError(t, store.SetProviderE2EAllowed(ctx, alpha.ID, false))
			require.NoError(t, store.SetProviderE2EPolicy(ctx, alpha.ID, model.E2EPolicyPermitted))
			require.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}, read(alpha.ID),
				"a policy write switched the ceiling back on")
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, read(beta.ID),
				"another tenant's ceiling moved")

			// The tenant chooses approval; the operator's ceiling written after
			// it leaves the policy alone.
			require.NoError(t, store.SetProviderE2EPolicy(ctx, alpha.ID, model.E2EPolicyApproval))
			require.NoError(t, store.SetProviderE2EAllowed(ctx, alpha.ID, true))
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}, read(alpha.ID),
				"a ceiling write put the policy back")

			// The value already stored, written again, is no error (MySQL: 0
			// rows changed).
			require.NoError(t, store.SetProviderE2EPolicy(ctx, alpha.ID, model.E2EPolicyApproval))
			require.NoError(t, store.SetProviderE2EAllowed(ctx, alpha.ID, true))

			// A policy that is not one of the four is refused by the store
			// itself, and changes nothing.
			require.Error(t, store.SetProviderE2EPolicy(ctx, alpha.ID, "everyone"))
			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}, read(alpha.ID),
				"the refused write changed something")

			require.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyPermitted}, read(beta.ID),
				"another tenant's policy moved")

			// A policy nobody can choose, written into the column by hand,
			// does not stop the operator's switch: the ceiling is written
			// alone, and the policy beside it is neither refused nor rewritten.
			_, err = sqlDB.ExecContext(ctx, fmt.Sprintf(`UPDATE providers SET e2e_policy='everyone' WHERE id=%d`, beta.ID))
			require.NoError(t, err)
			require.NoError(t, store.SetProviderE2EAllowed(ctx, beta.ID, false))
			require.Equal(t, model.ProviderE2E{Allowed: false, Policy: "everyone"}, read(beta.ID))

			// An unknown tenant is no error and creates nothing, like
			// SetProviderE2E: the handlers look the tenant up first.
			require.NoError(t, store.SetProviderE2EPolicy(ctx, beta.ID+1000, model.E2EPolicyOff))
			require.NoError(t, store.SetProviderE2EAllowed(ctx, beta.ID+1000, false))
			_, err = store.GetProviderE2E(ctx, beta.ID+1000)
			require.True(t, errors.Is(err, sql.ErrNoRows), "no such provider: sql.ErrNoRows, got %v", err)
		})
	}
}

// TestE2ERequestsOnEveryEngine walks the e2e_requests table (migration 00080)
// on every engine: a request is recorded, read back, listed by tenant, person
// and state, approved exactly once (a second administrator's decision is
// refused by the store itself), found as an approval only for that person,
// storage, exact path and kind and only before it expires, used exactly once,
// and removed with its person and its tenant. Written ONCE for every engine
// (db.E2ERequestSQL), so it is measured once for every engine.
func TestE2ERequestsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "depo", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			admin, err := store.CreateUser(ctx, "admin@example.com", "hash", model.RoleAdmin, "tr", "UTC")
			require.NoError(t, err)
			ada, err := store.CreateUser(ctx, "ada@example.com", "hash", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			bob, err := store.CreateUser(ctx, "bob@example.com", "hash", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			alpha, err := store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
			require.NoError(t, err)

			now := time.Now().UTC().Truncate(time.Second)
			week := now.Add(7 * 24 * time.Hour)
			const folder = "Muhasebe/Bordrolar"

			in := &model.E2ERequest{
				ProviderID: &alpha.ID, UserID: ada.ID, Requester: "Ada Lovelace", StorageID: st.ID,
				Path: folder, Kind: model.E2ERequestFolder, Reason: "Maaş bordroları — yalnız İK görmeli", ExpiresAt: week,
			}
			r, err := store.CreateE2ERequest(ctx, in)
			require.NoError(t, err)
			require.NotZero(t, r.ID)
			require.Equal(t, model.E2ERequestPending, r.Status, "a new request is pending")
			require.NotNil(t, r.ProviderID)
			require.Equal(t, alpha.ID, *r.ProviderID)
			require.Equal(t, ada.ID, r.UserID)
			require.Equal(t, "Ada Lovelace", r.Requester)
			require.Equal(t, folder, r.Path)
			require.Equal(t, model.E2ERequestFolder, r.Kind)
			require.Equal(t, in.Reason, r.Reason, "the reason is kept byte for byte (Turkish text too)")
			require.Nil(t, r.DecidedBy)
			require.Nil(t, r.DecidedAt)
			require.Nil(t, r.UsedAt)
			require.WithinDuration(t, week, r.ExpiresAt, time.Second, "the expiry reads back as the instant it was")
			require.False(t, r.CreatedAt.IsZero())

			// A single-tenant request names no tenant; the storage root is "".
			solo, err := store.CreateE2ERequest(ctx, &model.E2ERequest{
				UserID: bob.ID, Requester: "bob", StorageID: st.ID, Path: "", Kind: model.E2ERequestFile, ExpiresAt: week,
			})
			require.NoError(t, err)
			require.Nil(t, solo.ProviderID)
			require.Equal(t, "", solo.Path)

			for name, bad := range map[string]*model.E2ERequest{
				"no kind":      {UserID: ada.ID, StorageID: st.ID, ExpiresAt: week},
				"unknown kind": {UserID: ada.ID, StorageID: st.ID, Kind: "vault", ExpiresAt: week},
				"no expiry":    {UserID: ada.ID, StorageID: st.ID, Kind: model.E2ERequestFile},
				"no person":    {StorageID: st.ID, Kind: model.E2ERequestFile, ExpiresAt: week},
			} {
				_, err := store.CreateE2ERequest(ctx, bad)
				require.Error(t, err, name)
			}

			got, err := store.GetE2ERequest(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, folder, got.Path)
			_, err = store.GetE2ERequest(ctx, r.ID+1000)
			require.True(t, errors.Is(err, sql.ErrNoRows), "no such request: sql.ErrNoRows, got %v", err)

			all, err := store.ListE2ERequests(ctx, model.E2ERequestFilter{})
			require.NoError(t, err)
			require.Len(t, all, 2)
			require.Equal(t, solo.ID, all[0].ID, "newest first")
			inAlpha, err := store.ListE2ERequests(ctx, model.E2ERequestFilter{ProviderID: &alpha.ID, Status: model.E2ERequestPending})
			require.NoError(t, err)
			require.Len(t, inAlpha, 1)
			require.Equal(t, r.ID, inAlpha[0].ID)
			bobs, err := store.ListE2ERequests(ctx, model.E2ERequestFilter{UserID: &bob.ID})
			require.NoError(t, err)
			require.Len(t, bobs, 1)
			require.Equal(t, solo.ID, bobs[0].ID)
			capped, err := store.ListE2ERequests(ctx, model.E2ERequestFilter{Limit: 1})
			require.NoError(t, err)
			require.Len(t, capped, 1)

			_, err = store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder, model.E2ERequestFolder, now)
			require.True(t, errors.Is(err, sql.ErrNoRows), "a pending request is no approval, got %v", err)

			// Decided once: the first decision wins, the second is refused.
			decided := now
			r.Status, r.DecidedBy, r.Decider, r.DecidedAt = model.E2ERequestApproved, &admin.ID, "admin@example.com", &decided
			r.ExpiresAt = now.Add(7 * 24 * time.Hour)
			ok, err := store.UpdateE2ERequest(ctx, r, model.E2ERequestPending)
			require.NoError(t, err)
			require.True(t, ok)
			late := *r
			late.Status, late.DecisionNote = model.E2ERequestRejected, "too late"
			ok, err = store.UpdateE2ERequest(ctx, &late, model.E2ERequestPending)
			require.NoError(t, err)
			require.False(t, ok, "a request that is no longer pending is not decided again")

			ap, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder, model.E2ERequestFolder, now)
			require.NoError(t, err)
			require.Equal(t, r.ID, ap.ID)
			require.Equal(t, model.E2ERequestApproved, ap.Status)
			require.NotNil(t, ap.DecidedBy)
			require.Equal(t, admin.ID, *ap.DecidedBy)
			require.NotNil(t, ap.DecidedAt)
			require.WithinDuration(t, decided, *ap.DecidedAt, time.Second)
			require.Empty(t, ap.DecisionNote, "the refused rejection wrote nothing")

			for name, find := range map[string]func() error{
				"another person": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, bob.ID, st.ID, folder, model.E2ERequestFolder, now)
					return err
				},
				"another path": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, "Muhasebe", model.E2ERequestFolder, now)
					return err
				},
				"the path in other letters": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, "muhasebe/bordrolar", model.E2ERequestFolder, now)
					return err
				},
				"the path with a trailing space": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder+" ", model.E2ERequestFolder, now)
					return err
				},
				"another kind": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder, model.E2ERequestFile, now)
					return err
				},
				"after it expired": func() error {
					_, err := store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder, model.E2ERequestFolder, now.Add(8*24*time.Hour))
					return err
				},
			} {
				require.True(t, errors.Is(find(), sql.ErrNoRows), name)
			}

			// Used once: the first write that spends it wins.
			used := now.Add(time.Minute)
			ap.Status, ap.UsedAt = model.E2ERequestUsed, &used
			ok, err = store.UpdateE2ERequest(ctx, ap, model.E2ERequestApproved)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.UpdateE2ERequest(ctx, ap, model.E2ERequestApproved)
			require.NoError(t, err)
			require.False(t, ok, "an approval is good for one encryption")
			_, err = store.FindApprovedE2ERequest(ctx, ada.ID, st.ID, folder, model.E2ERequestFolder, now)
			require.True(t, errors.Is(err, sql.ErrNoRows), "a used approval is found no more, got %v", err)
			got, err = store.GetE2ERequest(ctx, r.ID)
			require.NoError(t, err)
			require.Equal(t, model.E2ERequestUsed, got.Status)
			require.NotNil(t, got.UsedAt)
			require.WithinDuration(t, used, *got.UsedAt, time.Second)

			// The decider's account going only forgets who decided; the
			// requester's takes the request with it; so does the tenant's.
			require.NoError(t, store.DeleteUser(ctx, admin.ID))
			got, err = store.GetE2ERequest(ctx, r.ID)
			require.NoError(t, err)
			require.Nil(t, got.DecidedBy)
			require.Equal(t, "admin@example.com", got.Decider, "the name shown when it was decided stays")
			require.NoError(t, store.DeleteUser(ctx, ada.ID))
			_, err = store.GetE2ERequest(ctx, r.ID)
			require.True(t, errors.Is(err, sql.ErrNoRows), "a request goes with its person, got %v", err)

			tenantOnly, err := store.CreateE2ERequest(ctx, &model.E2ERequest{
				ProviderID: &alpha.ID, UserID: bob.ID, StorageID: st.ID, Path: "Arşiv", Kind: model.E2ERequestFolder, ExpiresAt: week,
			})
			require.NoError(t, err)
			require.NoError(t, store.DeleteProvider(ctx, alpha.ID))
			_, err = store.GetE2ERequest(ctx, tenantOnly.ID)
			require.True(t, errors.Is(err, sql.ErrNoRows), "a request goes with its tenant, got %v", err)
			_, err = store.GetE2ERequest(ctx, solo.ID)
			require.NoError(t, err, "a single-tenant request belongs to no tenant")
		})
	}
}

// TestE2ERequestsDueOnEveryEngine: ExpiresBefore keeps what is due — an
// expiry at or before the bound, the bound itself included — oldest expiry
// first, a tie by id, with the other conditions and the limit still applied;
// without it the list stays newest first. The sweep (e2epolicy ExpireDue)
// reads a batch at a time through it, so a backlog of any size is reached: a
// list of the newest 500 never read an older row that was due.
func TestE2ERequestsDueOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "depo", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			ada, err := store.CreateUser(ctx, "ada@example.com", "hash", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			bob, err := store.CreateUser(ctx, "bob@example.com", "hash", model.RoleUser, "tr", "UTC")
			require.NoError(t, err)
			alpha, err := store.CreateProvider(ctx, &model.Provider{Slug: "alpha", Name: "Alpha", AuthType: model.AuthTypeLocal, Enabled: true})
			require.NoError(t, err)

			now := time.Now().UTC().Truncate(time.Second)
			add := func(tenant *int64, u *model.User, path string, expires time.Duration) int64 {
				t.Helper()
				r, err := store.CreateE2ERequest(ctx, &model.E2ERequest{
					ProviderID: tenant, UserID: u.ID, StorageID: st.ID, Path: path,
					Kind: model.E2ERequestFolder, ExpiresAt: now.Add(expires),
				})
				require.NoError(t, err)
				return r.ID
			}
			list := func(f model.E2ERequestFilter) []int64 {
				t.Helper()
				rows, err := store.ListE2ERequests(ctx, f)
				require.NoError(t, err)
				out := []int64{}
				for _, r := range rows {
					out = append(out, r.ID)
				}
				return out
			}

			later := add(&alpha.ID, ada, "Sonra", time.Hour)        // not due
			hourAgo := add(&alpha.ID, ada, "Saat", -time.Hour)      // due
			threeAgo := add(&alpha.ID, ada, "Uc", -3*time.Hour)     // due, and longer
			atBound := add(&alpha.ID, ada, "Sinir", 0)              // due: the bound is included
			bobs := add(&alpha.ID, bob, "Bob", -2*time.Hour)        // due, another person's
			tie := add(&alpha.ID, ada, "Esit", -3*time.Hour)        // due as long as threeAgo
			solo := add(nil, ada, "Tek", -4*time.Hour)              // due, no tenant's
			approved := add(&alpha.ID, ada, "Onayli", -5*time.Hour) // due, approved below
			ap, err := store.GetE2ERequest(ctx, approved)
			require.NoError(t, err)
			ap.Status = model.E2ERequestApproved
			ok, err := store.UpdateE2ERequest(ctx, ap, model.E2ERequestPending)
			require.NoError(t, err)
			require.True(t, ok)

			require.Equal(t, []int64{solo, threeAgo, tie, bobs, hourAgo, atBound},
				list(model.E2ERequestFilter{Status: model.E2ERequestPending, ExpiresBefore: now}),
				"what is due, the longest overdue first and a tie by id; the bound is included, what is not due is not")
			require.Equal(t, []int64{threeAgo, tie, hourAgo, atBound},
				list(model.E2ERequestFilter{ProviderID: &alpha.ID, UserID: &ada.ID, Status: model.E2ERequestPending, ExpiresBefore: now}),
				"the tenant, the person and the state still narrow it")
			require.Equal(t, []int64{threeAgo, tie},
				list(model.E2ERequestFilter{ProviderID: &alpha.ID, UserID: &ada.ID, Status: model.E2ERequestPending, ExpiresBefore: now, Limit: 2}),
				"the limit keeps the longest overdue")
			require.Equal(t, []int64{approved},
				list(model.E2ERequestFilter{Status: model.E2ERequestApproved, ExpiresBefore: now}))
			require.Equal(t, []int64{approved, solo, threeAgo, tie},
				list(model.E2ERequestFilter{ExpiresBefore: now.Add(-3 * time.Hour)}),
				"an earlier bound, included too, in every state")
			require.Empty(t, list(model.E2ERequestFilter{ExpiresBefore: now.Add(-6 * time.Hour)}))

			require.Equal(t, []int64{approved, solo, tie, atBound, threeAgo, hourAgo, later},
				list(model.E2ERequestFilter{UserID: &ada.ID}),
				"without it: newest first, due or not")
		})
	}
}

// TestSettingKeysAreComparedExactlyOnEveryEngine: a settings key is compared
// byte for byte on every engine. On MySQL the column was utf8mb4_0900_ai_ci
// (00001), which folds case AND accents, and 00041 left it there: `E2E.POLICY`
// or `e2é.policy` read and wrote the row of `e2e.policy`. The settings API
// guards that one key by comparing it exactly (handlers/settings.go), so on
// MySQL a spelling of it walked past the guard — the session it asks for, the
// validator, the audit row — and still changed who may encrypt. SQLite and
// PostgreSQL never did; 00080 makes MySQL's column binary too.
func TestSettingKeysAreComparedExactlyOnEveryEngine(t *testing.T) {
	// Case, accent, and the Turkish capital dotted İ (an accent of I).
	spellings := []string{"E2E.POLICY", "e2é.policy", "e2e.polİcy"}
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			// Another spelling is another key: writing it reaches nothing the
			// exact key reads.
			for _, key := range spellings {
				require.NoError(t, store.UpsertSetting(ctx, key, model.E2EPolicyOff))
				_, err := store.GetSetting(ctx, model.SettingE2EPolicy)
				require.True(t, errors.Is(err, sql.ErrNoRows),
					"writing %q made %q readable: got %v", key, model.SettingE2EPolicy, err)
			}

			// The exact key, written after them, is a row of its own and
			// leaves theirs alone.
			require.NoError(t, store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
			got, err := store.GetSetting(ctx, model.SettingE2EPolicy)
			require.NoError(t, err)
			require.Equal(t, model.E2EPolicyApproval, got)
			all, err := store.ListSettings(ctx)
			require.NoError(t, err)
			for _, key := range spellings {
				got, err := store.GetSetting(ctx, key)
				require.NoError(t, err, key)
				require.Equal(t, model.E2EPolicyOff, got, "writing %q changed %q", model.SettingE2EPolicy, key)
				require.Contains(t, all, key, "one row for each spelling")
			}
			require.Contains(t, all, model.SettingE2EPolicy)
		})
	}
}

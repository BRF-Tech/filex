package wasmplugin

// Tenant isolation of the two host functions that reach the directory:
// users_lookup and notify_send's to_user_id (outbound.go → directoryScope,
// mayAddress).
//
// The store narrows the directory only when the context carries a tenant
// scope, and three kinds of call carry no person of their own (a public page,
// the hourly wake-up, a scheduled job); each must still stay inside a tenant.
//
// No wasm module is needed: the host functions are called directly with the
// scope each kind of call gets, on the tenant-scoped store the server wires
// (tenantstore) and the user scope it wires (auth.ScopeForUser).

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

type tenantWorld struct {
	reg      *Registry
	raw      db.Store
	plugin   *Installed
	notified *fakeNotify
	storA    *model.Storage // linked to tenant A only
	userA    *model.User
	userA2   *model.User
	userB    *model.User
}

// newTenantWorld is two tenants, one storage each, two people in A and one in
// B, and an installed app holding users:lookup and notify:send. multi=false
// wires no user scope: the single-tenant instance.
func newTenantWorld(t *testing.T, multi bool) *tenantWorld {
	t.Helper()
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	reg, err := New(Options{Store: tenantstore.New(raw), Dir: filepath.Join(t.TempDir(), "app-plugins"),
		SecretKey: "0123456789abcdef0123456789abcdef"})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })
	if multi {
		reg.SetUserScope(func(ctx context.Context, u *model.User) context.Context {
			return tenant.WithScope(ctx, auth.ScopeForUser(ctx, raw, u))
		})
	}
	nf := &fakeNotify{}
	reg.SetNotify(nf)

	pa, err := raw.CreateProvider(ctx, &model.Provider{Slug: "ta", Name: "TA", Enabled: true})
	require.NoError(t, err)
	pb, err := raw.CreateProvider(ctx, &model.Provider{Slug: "tb", Name: "TB", Enabled: true})
	require.NoError(t, err)
	stA, err := raw.CreateStorage(ctx, &model.Storage{Name: "sa", Driver: "local", MountPath: "/sa", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"."}`)})
	require.NoError(t, err)
	stB, err := raw.CreateStorage(ctx, &model.Storage{Name: "sb", Driver: "local", MountPath: "/sb", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"."}`)})
	require.NoError(t, err)
	require.NoError(t, raw.LinkProviderStorage(ctx, pa.ID, stA.ID))
	require.NoError(t, raw.LinkProviderStorage(ctx, pb.ID, stB.ID))

	mk := func(email string, provider int64) *model.User {
		u, err := raw.CreateUser(ctx, email, "", model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		require.NoError(t, raw.SetUserProvider(ctx, u.ID, provider, ""))
		got, err := raw.GetUser(ctx, u.ID)
		require.NoError(t, err)
		return got
	}
	w := &tenantWorld{reg: reg, raw: raw, notified: nf, storA: stA,
		userA: mk("ann@ta.test", pa.ID), userA2: mk("amy@ta.test", pa.ID), userB: mk("bea@tb.test", pb.ID)}

	perms := []Permission{PermUsersLookup, PermNotifySend}
	w.plugin = &Installed{
		Row:      &model.AppPlugin{ID: 1, Name: "probe"},
		Manifest: &Manifest{Manifest: wire.Manifest{Name: "probe", Label: wire.Text{"en": "Probe"}}},
		Grants:   NewGrants(perms), Perms: perms, logs: &logRing{},
	}
	return w
}

// scope builds the scope one kind of call gets (see jobs.go runJob,
// public.go PageEvent, schedule.go Tick).
func (w *tenantWorld) scope(t *testing.T, storageID int64, actor *model.User, page *model.Share) *Scope {
	t.Helper()
	s, err := newScope(w.plugin, w.reg, "", storageID, nil, actor, "en", actor != nil)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	s.page = page
	return s
}

func (w *tenantWorld) lookup(t *testing.T, s *Scope, q string) []string {
	t.Helper()
	out, err := hfUsersLookup(context.Background(), s, json.RawMessage(`{"q":"`+q+`"}`))
	require.NoError(t, err)
	rows := out.(map[string]any)["users"].([]UserRow)
	emails := make([]string, 0, len(rows))
	for _, r := range rows {
		emails = append(emails, r.Email)
	}
	return emails
}

func (w *tenantWorld) notify(s *Scope, to int64) error {
	in, _ := json.Marshal(map[string]any{"title": map[string]string{"en": "hello"}, "to_user_id": to})
	_, err := hfNotifySend(context.Background(), s, in)
	return err
}

func TestTenant_UsersLookup_ReadsOnlyTheTenantOfThePersonTheCallSpeaksFor(t *testing.T) {
	w := newTenantWorld(t, true)

	// A person's job: their tenant, never the other one.
	job := w.lookup(t, w.scope(t, w.storA.ID, w.userA, nil), "test")
	assert.ElementsMatch(t, []string{"ann@ta.test", "amy@ta.test"}, job)

	// A public page: the visitor has no account; the link's creator does.
	creator := w.userA.ID
	page := w.lookup(t, w.scope(t, w.storA.ID, nil, &model.Share{ID: 9, CreatedBy: &creator}), "test")
	assert.ElementsMatch(t, []string{"ann@ta.test", "amy@ta.test"}, page, "a page reads its creator's tenant")

	// The wake-up (no storage, no actor) and a scheduled job (the empty
	// actor runJob hands a SYSTEM job) speak for nobody and read nobody.
	assert.Empty(t, w.lookup(t, w.scope(t, 0, nil, nil), "test"), "the wake-up must not list any tenant")
	assert.Empty(t, w.lookup(t, w.scope(t, w.storA.ID, &model.User{}, nil), "test"), "a scheduled job must not list any tenant")
}

func TestTenant_UsersLookup_SingleTenantIsUnchanged(t *testing.T) {
	w := newTenantWorld(t, false)
	got := w.lookup(t, w.scope(t, 0, nil, nil), "test")
	assert.Subset(t, got, []string{"ann@ta.test", "amy@ta.test", "bea@tb.test"})
}

func TestTenant_NotifySend_ReachesOnlyPeopleWhoseTenantReachesTheStorage(t *testing.T) {
	w := newTenantWorld(t, true)
	onA := w.scope(t, w.storA.ID, w.userA, nil)

	require.NoError(t, w.notify(onA, w.userA2.ID), "a colleague in the same tenant is reachable")
	require.Len(t, w.notified.events, 1)

	err := w.notify(onA, w.userB.ID)
	require.Error(t, err, "a person of another tenant is not addressable")
	assert.Equal(t, wire.ErrNotFound, asHostError(err).Code, "and the answer is the one a missing id gets")
	assert.Len(t, w.notified.events, 1, "nothing was delivered")

	// A scheduled job on A's storage: the storage decides, not the (absent) person.
	sys := w.scope(t, w.storA.ID, &model.User{}, nil)
	require.NoError(t, w.notify(sys, w.userA.ID))
	require.Error(t, w.notify(sys, w.userB.ID))

	// A call about no storage and with nobody behind it addresses nobody.
	require.Error(t, w.notify(w.scope(t, 0, nil, nil), w.userA.ID))

	// …and with a person behind it, only that person's tenant.
	creator := w.userA.ID
	pageNoStorage := w.scope(t, 0, nil, &model.Share{ID: 9, CreatedBy: &creator})
	require.NoError(t, w.notify(pageNoStorage, w.userA2.ID))
	require.Error(t, w.notify(pageNoStorage, w.userB.ID))
}

func TestTenant_NotifySend_SingleTenantIsUnchanged(t *testing.T) {
	w := newTenantWorld(t, false)
	require.NoError(t, w.notify(w.scope(t, w.storA.ID, w.userA, nil), w.userB.ID))
	require.NoError(t, w.notify(w.scope(t, 0, nil, nil), w.userB.ID))
}

package dav

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"net/http/httptest"

	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// tenantHarness is newHarness in multi-tenant shape: the handler sees the
// tenant-scoped store (as the server wires it) and MultiTenant is on.
type tenantHarness struct {
	*harness
	raw db.Store
}

func newTenantHarness(t *testing.T) *tenantHarness {
	t.Helper()
	_, raw := dbtest.NewTestDB(t)
	scoped := tenantstore.New(raw)

	drivers := map[int64]storage.Driver{}
	var mu sync.Mutex
	resolver := func(id int64) (storage.Driver, error) {
		mu.Lock()
		defer mu.Unlock()
		if d, ok := drivers[id]; ok {
			return d, nil
		}
		st, err := raw.GetStorage(context.Background(), id)
		if err != nil {
			return nil, err
		}
		d, err := storage.Get(st.Driver)
		if err != nil {
			return nil, err
		}
		cfg := map[string]any{}
		if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
			return nil, err
		}
		if err := d.Init(context.Background(), cfg); err != nil {
			return nil, err
		}
		drivers[id] = d
		return d, nil
	}

	h := NewHandler(Config{
		Enabled:     true,
		Store:       scoped,
		Resolver:    resolver,
		ACL:         acl.New(raw),
		MultiTenant: true,
	})
	mux := http.NewServeMux()
	mux.Handle(Prefix+"/", h)
	mux.Handle(Prefix, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	inner := &harness{store: raw, h: h, srv: srv, resolver: resolver}
	return &tenantHarness{harness: inner, raw: raw}
}

// addTenant creates a provider, an enabled storage linked to it, and an
// admin-role user belonging to it — one olivov-style tenant.
func (ha *tenantHarness) addTenant(t *testing.T, slug, storageName, email string, supertenant bool) (*model.Provider, *model.Storage, string) {
	t.Helper()
	ctx := context.Background()

	// Enabled matters: auth.LoginAllowed refuses a disabled provider, so a
	// harness that forgets it gets 401 everywhere and every scoping
	// assertion passes for the wrong reason.
	p, err := ha.raw.CreateProvider(ctx, &model.Provider{
		Slug: slug, Name: slug, AuthType: "local", IsSupertenant: supertenant, Enabled: true,
	})
	require.NoError(t, err)

	st := ha.addStorage(t, storageName, false, false)
	require.NoError(t, ha.raw.LinkProviderStorage(ctx, p.ID, st.ID))

	password := "TenantPass!1"
	hash, err := local.HashPassword(password)
	require.NoError(t, err)
	// role=admin on purpose: olivov's tenant admins hold filex role admin,
	// which is what made this reachable.
	u, err := ha.raw.CreateUser(ctx, email, hash, model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, ha.raw.SetUserProvider(ctx, u.ID, p.ID, ""))

	return p, st, password
}

// TestDAV_TenantScoping — a tenant admin (role=admin, provider not the
// supertenant) must see and reach only their own provider's storages.
//
// Before the fix /dav resolved storages globally: olivov's diyetlif admin
// mounted https://files.diyetlif.com.tr/dav/ in Finder and got all ten
// tenants' storages listed, read-write, with DELETE hard-deleting rather
// than going to trash (H4, 2026-08-05).
func TestDAV_TenantScoping(t *testing.T) {
	ha := newTenantHarness(t)
	_, ownSt, pass := ha.addTenant(t, "diyetlif", "Dosyalar", "berk@diyetlif.test", false)
	_, otherSt, _ := ha.addTenant(t, "arasboya", "ArasboyaDosyalar", "admin@arasboya.test", false)

	const me = "berk@diyetlif.test"

	t.Run("root lists only own storage", func(t *testing.T) {
		resp := ha.req(t, "PROPFIND", "/dav/", me, pass, "", map[string]string{"Depth": "1"})
		require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
		body := bodyString(t, resp)
		require.Contains(t, body, ownSt.Name)
		require.NotContains(t, body, otherSt.Name,
			"another tenant's storage must not appear in the /dav root")
	})

	t.Run("foreign storage is not readable", func(t *testing.T) {
		resp := ha.req(t, "PROPFIND", "/dav/"+otherSt.Name+"/", me, pass, "", map[string]string{"Depth": "1"})
		require.Equal(t, http.StatusNotFound, resp.StatusCode,
			"a path outside the caller's provider must 404, not enumerate")
	})

	t.Run("foreign storage is not writable", func(t *testing.T) {
		resp := ha.req(t, http.MethodPut, "/dav/"+otherSt.Name+"/pwned.txt", me, pass, "x", nil)
		require.NotEqual(t, http.StatusCreated, resp.StatusCode,
			"writing into another tenant's storage must be refused")
		require.GreaterOrEqual(t, resp.StatusCode, 400)
	})

	t.Run("foreign storage is not deletable", func(t *testing.T) {
		resp := ha.req(t, http.MethodDelete, "/dav/"+otherSt.Name+"/", me, pass, "", nil)
		require.GreaterOrEqual(t, resp.StatusCode, 400,
			"DELETE on another tenant's storage is permanent — it must be refused")
	})

	t.Run("own storage still works", func(t *testing.T) {
		resp := ha.req(t, http.MethodPut, "/dav/"+ownSt.Name+"/mine.txt", me, pass, "hello", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	})

	// COPY resolves its Destination separately from the request path, in the
	// pre-gate as well as the FileSystem. Both have to apply the tenant gate,
	// or a caller could stream their own file into someone else's storage.
	t.Run("cannot copy out to a foreign storage", func(t *testing.T) {
		resp := ha.req(t, "COPY", "/dav/"+ownSt.Name+"/mine.txt", me, pass, "", map[string]string{
			"Destination": "/dav/" + otherSt.Name + "/leaked.txt",
		})
		require.GreaterOrEqual(t, resp.StatusCode, 400,
			"a copy whose destination is another tenant's storage must be refused")
	})
}

// TestDAV_SupertenantSeesAll — "admin sees everything" stays true for an
// admin of the supertenant provider, which is the platform operator.
func TestDAV_SupertenantSeesAll(t *testing.T) {
	ha := newTenantHarness(t)
	_, opsSt, pass := ha.addTenant(t, "olivov", "OlivovDosyalar", "ops@olivov.test", true)
	_, tenantSt, _ := ha.addTenant(t, "diyetlif", "Dosyalar", "berk@diyetlif.test", false)

	resp := ha.req(t, "PROPFIND", "/dav/", "ops@olivov.test", pass, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	body := bodyString(t, resp)
	require.Contains(t, body, opsSt.Name)
	require.Contains(t, body, tenantSt.Name,
		"the supertenant is confine-exempt and must still see every storage")
}

// TestDAV_UnresolvableProviderSeesNothing — multi-tenant mode fails closed:
// an authenticated user whose provider_id doesn't resolve gets no storages
// rather than falling through to "unscoped, see everything".
//
// provider_id is not FK-enforced, so a deleted or mis-migrated tenant leaves
// exactly this dangling state.
func TestDAV_UnresolvableProviderSeesNothing(t *testing.T) {
	ha := newTenantHarness(t)
	_, st, _ := ha.addTenant(t, "diyetlif", "Dosyalar", "berk@diyetlif.test", false)

	ctx := context.Background()
	password := "Orphan!1"
	hash, err := local.HashPassword(password)
	require.NoError(t, err)
	u, err := ha.raw.CreateUser(ctx, "orphan@nowhere.test", hash, model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, ha.raw.SetUserProvider(ctx, u.ID, 99999, ""))

	resp := ha.req(t, "PROPFIND", "/dav/", "orphan@nowhere.test", password, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.False(t, strings.Contains(bodyString(t, resp), st.Name),
		"a user whose provider doesn't resolve must see no storages")
}

// TestDAV_NewUsersLandInTheSupertenant documents a sharp edge this work
// uncovered rather than a behaviour to rely on: store.CreateUser defaults
// provider_id to 1, and provider 1 (`default`) is a SUPERTENANT. A user
// created without an explicit provider is therefore confine-exempt and sees
// every tenant's storages over /dav.
//
// That is what makes POST /api/admin/users ignoring provider_id a security
// problem and not just an inconvenience (olivov G1).
func TestDAV_NewUsersLandInTheSupertenant(t *testing.T) {
	ha := newTenantHarness(t)
	_, st, _ := ha.addTenant(t, "diyetlif", "Dosyalar", "berk@diyetlif.test", false)

	ctx := context.Background()
	password := "Fresh!1"
	hash, err := local.HashPassword(password)
	require.NoError(t, err)
	u, err := ha.raw.CreateUser(ctx, "fresh@nowhere.test", hash, model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)

	p, err := ha.raw.GetProvider(ctx, *u.ProviderID)
	require.NoError(t, err)
	require.True(t, p.IsSupertenant,
		"a user created with no provider lands in the supertenant — see G1")

	resp := ha.req(t, "PROPFIND", "/dav/", "fresh@nowhere.test", password, "", map[string]string{"Depth": "1"})
	require.Equal(t, http.StatusMultiStatus, resp.StatusCode)
	require.Contains(t, bodyString(t, resp), st.Name,
		"...and consequently sees every tenant's storages")
}

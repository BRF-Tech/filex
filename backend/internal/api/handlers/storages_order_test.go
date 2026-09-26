package handlers_test

// PUT /api/admin/storages/order — the admin decides the order the storages
// are listed in (issue #57). The body is the WHOLE order as the admin sees it,
// first = top: the storages it names are placed 1..n, every other storage the
// caller may administer is un-placed, and the storages of another tenant are
// neither named nor touched.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func orderStorage(t *testing.T, store db.Store, name string) *model.Storage {
	t.Helper()
	cfg, _ := json.Marshal(map[string]string{"root": t.TempDir()})
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, ConfigJSON: cfg,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	return st
}

// adminOrder reads GET /api/admin/storages: names in the order served, and the
// raw sort_order each row carries (nil = JSON null).
func adminOrder(t *testing.T, client *http.Client, url string) ([]string, map[string]*int64) {
	t.Helper()
	st, raw := doReq(t, client, http.MethodGet, url+"/api/admin/storages", nil)
	require.Equal(t, http.StatusOK, st, "admin list: %s", raw)
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &rows))
	names := make([]string, 0, len(rows))
	orders := map[string]*int64{}
	for _, row := range rows {
		var name string
		require.NoError(t, json.Unmarshal(row["name"], &name))
		so, ok := row["sort_order"]
		require.True(t, ok, "%s: the admin list must carry sort_order, even when it is null", name)
		var v *int64
		require.NoError(t, json.Unmarshal(so, &v))
		names = append(names, name)
		orders[name] = v
	}
	return names, orders
}

func putOrder(t *testing.T, client *http.Client, url string, body any) (int, map[string]any) {
	t.Helper()
	st, raw := doReq(t, client, http.MethodPut, url+"/api/admin/storages/order", body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return st, out
}

func i64(v int64) *int64 { return &v }

func TestStorageOrder_TheAdminsOrderIsTheOrderEverybodySees(t *testing.T) {
	srv, admin, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, admin, email, pw)

	a := orderStorage(t, store, "arsiv")
	b := orderStorage(t, store, "belgeler")
	c := orderStorage(t, store, "calisma")

	names, orders := adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"arsiv", "belgeler", "calisma"}, names, "nothing placed: creation order")
	require.Equal(t, map[string]*int64{"arsiv": nil, "belgeler": nil, "calisma": nil}, orders)

	st, out := putOrder(t, admin, srv.URL, map[string]any{"ids": []int64{c.ID, a.ID}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	assert.Equal(t, true, out["ok"])
	assert.Equal(t, []any{float64(c.ID), float64(a.ID)}, out["ids"])

	names, orders = adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"calisma", "arsiv", "belgeler"}, names)
	require.Equal(t, map[string]*int64{"calisma": i64(1), "arsiv": i64(2), "belgeler": nil}, orders)

	// A person who cannot read the admin list sees the same order in the
	// explorer root, with the positions in storage_info.
	createUser(t, srv.URL, admin, "order-user@test.local", "UserPass1!", model.RoleUser)
	user := freshClient(t)
	testutil.LoginAs(t, srv, user, "order-user@test.local", "UserPass1!")
	sc, raw := doReq(t, user, http.MethodGet, srv.URL+"/api/files/manager?action=index&path=", nil)
	require.Equal(t, http.StatusOK, sc, "%s", raw)
	var root struct {
		Storages    []string `json:"storages"`
		StorageInfo []struct {
			Name      string `json:"name"`
			SortOrder *int64 `json:"sort_order"`
		} `json:"storage_info"`
	}
	require.NoError(t, json.Unmarshal(raw, &root))
	require.Equal(t, []string{"calisma", "arsiv", "belgeler"}, root.Storages,
		"the explorer lists the storages in the admin's order, not by name or id")
	got := map[string]*int64{}
	for _, i := range root.StorageInfo {
		got[i.Name] = i.SortOrder
	}
	require.Equal(t, map[string]*int64{"calisma": i64(1), "arsiv": i64(2), "belgeler": nil}, got)

	// Editing a storage (the form sends the whole row back) keeps its place.
	sc, raw = doReq(t, admin, http.MethodPatch, fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, a.ID),
		map[string]any{"name": "arsiv-2", "sort_order": nil})
	require.Equal(t, http.StatusOK, sc, "%s", raw)
	var edited model.Storage
	require.NoError(t, json.Unmarshal(raw, &edited))
	require.NotNil(t, edited.SortOrder, "the edit answered as if the position had been cleared")
	require.Equal(t, int64(2), *edited.SortOrder)
	names, orders = adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"calisma", "arsiv-2", "belgeler"}, names)
	require.Equal(t, i64(2), orders["arsiv-2"])

	// The list is the WHOLE order: a storage it leaves out is un-placed.
	st, out = putOrder(t, admin, srv.URL, map[string]any{"ids": []int64{b.ID}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	names, orders = adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"belgeler", "arsiv-2", "calisma"}, names)
	require.Equal(t, map[string]*int64{"belgeler": i64(1), "arsiv-2": nil, "calisma": nil}, orders)

	// An empty list is "back to the default order".
	st, out = putOrder(t, admin, srv.URL, map[string]any{"ids": []int64{}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	assert.Equal(t, []any{}, out["ids"])
	names, orders = adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"arsiv-2", "belgeler", "calisma"}, names)
	require.Equal(t, map[string]*int64{"arsiv-2": nil, "belgeler": nil, "calisma": nil}, orders)
}

func TestStorageOrder_RefusesWhatIsNotAnOrder(t *testing.T) {
	srv, admin, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, admin, email, pw)

	a := orderStorage(t, store, "arsiv")
	b := orderStorage(t, store, "belgeler")
	require.NoError(t, store.SetStorageOrder(context.Background(), []int64{b.ID, a.ID}, nil))

	sendRaw := func(body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/admin/storages/order", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := admin.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		msg, _ := out["error"].(string)
		return resp.StatusCode, msg
	}

	cases := []struct {
		name, body, want string
	}{
		{"not json", `{"ids": [`, "bad json"},
		{"no ids", `{}`, "ids required"},
		{"null ids", `{"ids": null}`, "ids required"},
		{"ids not numbers", `{"ids": ["arsiv"]}`, "bad json"},
		{"duplicate", fmt.Sprintf(`{"ids": [%d, %d, %d]}`, a.ID, b.ID, a.ID), fmt.Sprintf("duplicate storage id %d", a.ID)},
		{"unknown", fmt.Sprintf(`{"ids": [%d, 999999]}`, a.ID), "unknown storage id 999999"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, msg := sendRaw(c.body)
			require.Equal(t, http.StatusBadRequest, st)
			require.Equal(t, c.want, msg)
			// A refused request changes nothing.
			names, _ := adminOrder(t, admin, srv.URL)
			require.Equal(t, []string{"belgeler", "arsiv"}, names)
		})
	}
}

func TestStorageOrder_OnlyAnAdminOrders(t *testing.T) {
	srv, admin, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, admin, email, pw)
	a := orderStorage(t, store, "arsiv")
	b := orderStorage(t, store, "belgeler")

	createUser(t, srv.URL, admin, "order-plain@test.local", "UserPass1!", model.RoleUser)
	user := freshClient(t)
	testutil.LoginAs(t, srv, user, "order-plain@test.local", "UserPass1!")

	st, _ := putOrder(t, user, srv.URL, map[string]any{"ids": []int64{b.ID, a.ID}})
	require.Equal(t, http.StatusForbidden, st)
	st, _ = putOrder(t, freshClient(t), srv.URL, map[string]any{"ids": []int64{b.ID, a.ID}})
	require.Equal(t, http.StatusUnauthorized, st)

	names, _ := adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"arsiv", "belgeler"}, names, "a refused caller changed the order")

	// The same request from the admin goes through: the 403 above was the
	// admin gate, not a route that does not exist.
	st, out := putOrder(t, admin, srv.URL, map[string]any{"ids": []int64{b.ID, a.ID}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	names, _ = adminOrder(t, admin, srv.URL)
	require.Equal(t, []string{"belgeler", "arsiv"}, names)
}

// A tenant admin orders their own storages and nothing else: another
// tenant's storage is refused in the words an id that never existed gets
// (tenantown.go), and the other tenant's order is left exactly as it was.
func TestStorageOrder_ATenantOrdersOnlyItsOwn(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	ctx := context.Background()

	minePID, email, pw := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	theirsPID, _, _ := seedTenant(t, store, "arasboya", "admin@arasboya.test", false)
	m1 := seedStorageFor(t, store, minePID, "diyetlif-1")
	m2 := seedStorageFor(t, store, minePID, "diyetlif-2")
	t1 := seedStorageFor(t, store, theirsPID, "arasboya-1")
	t2 := seedStorageFor(t, store, theirsPID, "arasboya-2")
	require.NoError(t, store.SetStorageOrder(ctx, []int64{t2.ID, t1.ID}, nil))

	testutil.LoginAs(t, srv, client, email, pw)

	theirOrder := func() (*int64, *int64) {
		t.Helper()
		return mustSortOrder(t, store, t1.ID), mustSortOrder(t, store, t2.ID)
	}

	// Naming the other tenant's storage: the same 400 and the same words as
	// an id nobody ever created.
	st, foreign := putOrder(t, client, srv.URL, map[string]any{"ids": []int64{m2.ID, t1.ID}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, fmt.Sprintf("unknown storage id %d", t1.ID), foreign["error"])
	st, missing := putOrder(t, client, srv.URL, map[string]any{"ids": []int64{m2.ID, 999999}})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, "unknown storage id 999999", missing["error"])
	require.Nil(t, mustSortOrder(t, store, m2.ID), "a refused order was partly written")

	// Their own: placed. The other tenant's positions do not move.
	st, out := putOrder(t, client, srv.URL, map[string]any{"ids": []int64{m2.ID, m1.ID}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	require.Equal(t, int64(1), *mustSortOrder(t, store, m2.ID))
	require.Equal(t, int64(2), *mustSortOrder(t, store, m1.ID))
	o1, o2 := theirOrder()
	require.Equal(t, i64(2), o1)
	require.Equal(t, i64(1), o2)

	// The tenant admin's list is their own storages, in their order.
	names, _ := adminOrder(t, client, srv.URL)
	require.Equal(t, []string{"diyetlif-2", "diyetlif-1"}, names)

	// Resetting clears their own positions — and only theirs.
	st, out = putOrder(t, client, srv.URL, map[string]any{"ids": []int64{}})
	require.Equal(t, http.StatusOK, st, "%v", out)
	require.Nil(t, mustSortOrder(t, store, m1.ID))
	require.Nil(t, mustSortOrder(t, store, m2.ID))
	o1, o2 = theirOrder()
	require.Equal(t, i64(2), o1, "a tenant's reset cleared another tenant's order")
	require.Equal(t, i64(1), o2, "a tenant's reset cleared another tenant's order")
}

func mustSortOrder(t *testing.T, store db.Store, id int64) *int64 {
	t.Helper()
	st, err := store.GetStorage(context.Background(), id)
	require.NoError(t, err)
	return st.SortOrder
}

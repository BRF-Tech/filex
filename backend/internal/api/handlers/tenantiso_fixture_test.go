package handlers_test

// A REAL two-tenant instance, over real HTTP, for the cross-tenant isolation
// suites (`*_tenant_iso_test.go`).
//
// Why a bespoke fixture instead of `testutil.NewTestServerCfg`: three of the
// holes under test only exist once the dependency that owns them is wired.
// `Ops` nil answers "ops queue unavailable" (503), a nil search `Index` sends
// the manager's search down the SQL-LIKE branch that was already scoped, and a
// nil `Trash` service answers nothing at all. A harness that cannot reach the
// code reports a safety it never measured, so this fixture wires the ops
// service (which needs the raw *sql.DB the shared helper does not hand back), a
// real Bleve index, the trash service and the notify service.
//
// It also wraps the handlers' store in `tenantstore` exactly as
// `internal/server.New` does. `api.BuildRouter` does NOT wrap — the production
// bootstrap does — so a fixture that skipped it would measure a router whose
// storage listings are unconfined and would attribute the resulting leak to the
// handler rather than to its own missing wrapper.
//
// The two tenants are `alpha` and `bravo`, each with one local storage of the
// same name, one plain (role=user) member, and files on disk. `super` is a
// third provider flagged supertenant — the platform operator, who must keep
// reaching everything.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// mtFix is one running instance plus logged-in clients for both tenants and
// the platform operator.
type mtFix struct {
	URL   string
	Store db.Store // UNWRAPPED: seeding must not be confined
	SQL   *sql.DB
	Ops   *ops.Service
	Index *search.Index
	Notif notify.Service

	ProvA, ProvB int64
	StA, StB     *model.Storage
	RootA, RootB string
	UserA, UserB int64

	// StA2 is a SECOND storage for tenant alpha, and it is load-bearing rather
	// than decorative: the manager's toolbar search only enters its
	// cross-storage branch when the caller can see more than one storage
	// (`crossStorage := rel == "" && len(storageNames) > 1`, manager.go). With
	// one storage per tenant the branch is never taken and a fixture would
	// report the leak as absent.
	StA2   *model.Storage
	RootA2 string

	// A and B are plain (role=user) members of alpha and bravo; Super is an
	// admin of the supertenant provider. AdminA is an ADMIN of alpha — some
	// holes are only reachable from an admin-flavoured surface, and the point
	// of the tenant boundary is that being an admin of your own tenant buys
	// you nothing in somebody else's.
	A, B, Super, AdminA *http.Client
}

const mtUserPass = "VictimPass!1"

// newMTFix stands the instance up. `multiTenant=false` builds the SAME
// topology with the feature switched off, which is how the single-tenant
// assertions are written: identical requests, opposite expectation, and they
// pass on `main` too because with no scope attached every predicate is inert.
func newMTFix(t *testing.T, multiTenant bool) *mtFix {
	t.Helper()
	ctx := context.Background()

	sqlDB, raw := testutil.NewTestDB(t)
	// Mirror internal/server.New's stack: quota accounting, then identity,
	// then (for the handlers only) the tenant-scoped storage listings.
	accounting := quotastore.New(raw)
	var store db.Store = identitystore.New(accounting)

	rootA, rootB := t.TempDir(), t.TempDir()
	drvA, drvB := &local.Driver{}, &local.Driver{}
	require.NoError(t, drvA.Init(ctx, map[string]any{"root": rootA}))
	require.NoError(t, drvB.Init(ctx, map[string]any{"root": rootB}))

	mkStorage := func(name, root string) *model.Storage {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
			ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`),
		})
		require.NoError(t, err)
		return st
	}
	rootA2 := t.TempDir()
	drvA2 := &local.Driver{}
	require.NoError(t, drvA2.Init(ctx, map[string]any{"root": rootA2}))

	stA, stB := mkStorage("alpha", rootA), mkStorage("bravo", rootB)
	stA2 := mkStorage("alpha-arsiv", rootA2)

	resolver := func(id int64) (storage.Driver, error) {
		switch id {
		case stA.ID:
			return drvA, nil
		case stA2.ID:
			return drvA2, nil
		case stB.ID:
			return drvB, nil
		}
		return nil, fmt.Errorf("unknown storage id %d", id)
	}

	provA, adminAEmail, adminAPass := seedTenant(t, store, "alpha", "admin@alpha.test", false)
	provB, _, _ := seedTenant(t, store, "bravo", "admin@bravo.test", false)
	_, superEmail, superPass := seedTenant(t, store, "platform", "super@platform.test", true)
	require.NoError(t, store.LinkProviderStorage(ctx, provA, stA.ID))
	require.NoError(t, store.LinkProviderStorage(ctx, provA, stA2.ID))
	require.NoError(t, store.LinkProviderStorage(ctx, provB, stB.ID))

	userA := seedUserIn(t, store, provA, "member@alpha.test")
	userB := seedUserIn(t, store, provB, "member@bravo.test")

	// ⚠ With the mode OFF, nobody may stay homed in a customer provider:
	// `auth.LoginAllowed` refuses exactly that shape ("mode off + tenants exist
	// ⇒ maintenance lockout", internal/auth/login_policy.go:50), so a
	// single-tenant fixture that kept the memberships would answer 403 at the
	// login and never reach the code it claims to measure — a harness reporting
	// a safety it never measured. Everyone moves to the platform provider,
	// which is what a single-tenant install actually looks like.
	if !multiTenant {
		super, err := store.GetSupertenant(ctx)
		require.NoError(t, err)
		require.NotNil(t, super)
		for _, email := range []string{"member@alpha.test", "member@bravo.test", adminAEmail} {
			u, err := store.GetUserByEmail(ctx, email)
			require.NoError(t, err)
			require.NoError(t, store.SetUserProvider(ctx, u.ID, super.ID, ""))
		}
	}

	// Ops queue — the same service the server bootstraps, so a submitted op is
	// really executed against the driver and the red proof can assert on the
	// bytes rather than on a 202.
	opsSvc := ops.New(sqlDB, resolver)
	require.NoError(t, opsSvc.Migrate(ctx))
	opsSvc.SetSync(handlers.NewManager(store, resolver))

	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}
	cfg.MultiTenant = multiTenant

	notifSvc := notify.New(store, notify.Config{})
	t.Cleanup(notifSvc.Stop)

	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg: cfg,
		// ⚠ The SCOPED store, as internal/server.New hands it to the router.
		Store:           tenantstore.New(store),
		Quota:           accounting.Quota(),
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		StorageResolver: resolver,
		Ops:             opsSvc,
		Trash:           trash.New(store, resolver, accounting.Quota()),
		Index:           idx,
		Notify:          notifSvc,
		LocalAuth:       localDrv,
	}))
	t.Cleanup(srv.Close)

	f := &mtFix{
		URL: srv.URL, Store: store, SQL: sqlDB, Ops: opsSvc, Index: idx, Notif: notifSvc,
		ProvA: provA, ProvB: provB, StA: stA, StB: stB, RootA: rootA, RootB: rootB,
		StA2: stA2, RootA2: rootA2,
		UserA: userA, UserB: userB,
	}
	f.A = mtLogin(t, srv, "member@alpha.test", mtUserPass)
	f.B = mtLogin(t, srv, "member@bravo.test", mtUserPass)
	f.AdminA = mtLogin(t, srv, adminAEmail, adminAPass)
	f.Super = mtLogin(t, srv, superEmail, superPass)
	return f
}

func mtLogin(t *testing.T, srv *httptest.Server, email, pw string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, c, email, pw)
	return c
}

// seedFile writes a real file into a storage's root AND mirrors it as a node
// row + search document, which is what makes it reachable by every one of the
// surfaces under test (bytes, listing, stat, search, trash).
func (f *mtFix) seedFile(t *testing.T, st *model.Storage, root, rel, content string) *model.Node {
	t.Helper()
	ctx := context.Background()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))

	p := "/" + strings.TrimLeft(rel, "/")
	n, err := f.Store.CreateNode(ctx, &model.Node{
		StorageID: st.ID,
		Name:      filepath.Base(rel),
		Path:      p,
		PathHash:  pathkey.Hash(st.ID, p),
		Type:      model.NodeTypeFile,
		Mime:      "text/plain",
		Size:      int64(len(content)),
		Etag:      "e-" + rel,
	})
	require.NoError(t, err)
	require.NoError(t, f.Index.IndexNode(ctx, n))
	return n
}

// get is a bare authenticated GET returning (status, body).
func mtGet(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return resp.StatusCode, string(buf)
}

// tag labels a node. Tags live on node_meta and are SHARED across users by
// design, which is exactly what makes the toolbar's `tag:` branch a second
// entrance into the cross-storage listing.
func (f *mtFix) tag(t *testing.T, nodeID int64, tags ...string) {
	t.Helper()
	require.NoError(t, f.Store.SetNodeTags(context.Background(), nodeID, tags))
}

package handlers_test

// Cross-tenant crossings on the /api/files routes that take a raw id.
//
// The admin half of this audit lives in admin_ownership_tenant_test.go. This
// is the USER half, and it is worse in one specific way: /api/admin at least
// requires an admin account, whereas everything below is reachable by an
// ordinary logged-in user of any tenant.
//
// ⚠ The thing that makes these holes survive a reading of the code is that
// each one already HAS a permission check, so the eye stops there. None of
// those checks is a tenant boundary:
//
//   - `aclAllowID` consults storages.rbac_enabled, which defaults FALSE, and
//     with it off acl.Set.Effective returns roleBase(role) for EVERY path —
//     Editor for a plain `user`, Owner for any admin. So an ACL gate in front
//     of a client-supplied node id refuses nobody.
//   - `user.IsAdmin()` is an admin of SOME tenant, not of the row's tenant.
//   - tenantstore confines exactly three list queries (ListStorages,
//     ListEnabledStorages, ListUsers). GetNode / GetStorageByName /
//     GetShareByID are pass-throughs, so any handler that reaches a row by id
//     rather than by listing has no confinement at all.
//
// That last point is also the tell for which body shape is safe: the
// {"path": …} form of /api/files/share resolves through the CONFINED
// ListEnabledStorages and always was safe, while the {"node_id": …} form of
// the same handler was not. Two shapes, one handler, different security
// properties.
//
// ⚠ Refusals are 404, not 403 — a foreign id must be indistinguishable from
// one that never existed, or the endpoint is an enumeration oracle. Same
// reasoning as tenantown.go's header.
//
// Every case is paired with a single-tenant assertion
// (TestFilesCross_SingleTenantUnaffected). With multi-tenancy off no scope is
// attached at all and every one of these requests must keep working exactly
// as before; that test passes on `main` too, which is the honest form of the
// proof.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// ── harness ───────────────────────────────────────────────────────────────

// xtSide is one tenant with everything the /api/files id-taking routes can
// name: a provider, an admin, a plain user, a storage backed by a REAL local
// directory, a file node with bytes on disk, a version of it, a folder node,
// a share and a comment.
type xtSide struct {
	provider   int64
	adminEmail string
	adminPass  string
	userEmail  string
	userPass   string
	userID     int64
	storage    *model.Storage
	root       string
	node       *model.Node
	dir        *model.Node
	version    int64
	share      int64
	comment    int64
}

// xtFixture is the two-tenant instance plus the attacker's session.
type xtFixture struct {
	srv    *httptest.Server
	client *http.Client
	store  db.Store
	mine   *xtSide
	theirs *xtSide
}

// xtUserPass mirrors seedUserIn's hard-coded password (users_tenant_gate_test.go).
const xtUserPass = "VictimPass!1"

// xtEscrowPub is a fixed RSA-2048 public key (SPKI, base64) so the escrow
// endpoints are actually ENABLED in the harness. With d.E2EEscrow nil they
// answer 404 "escrow is not enabled on this installation" before looking at
// the path at all — which reads exactly like a refusal and is not one. Same
// trap the admin matrix hit with nil Trash/Versions services.
var xtEscrowPub string

// newXTServer builds the router with the dependencies these routes need:
//
//   - a StorageResolver that returns a REAL local driver, so a version
//     restore actually rewrites bytes and the damage can be measured on disk
//     rather than inferred from a status code;
//   - versioning.Service, without which /versions answers "service not
//     initialised" before it ever looks at node_id;
//   - onlyoffice.Service with a document-server URL + JWT secret, without
//     which /onlyoffice/config answers 503 before it ever looks at node_id;
//   - an escrow key, without which /e2e/escrow/* answers 404 for everyone.
func newXTServer(t *testing.T, multiTenant bool) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()
	return testutil.NewTestServerWith(t,
		func(c *config.Config) { c.MultiTenant = multiTenant },
		func(d *api.Deps) {
			store := d.Store
			d.StorageResolver = func(id int64) (storage.Driver, error) {
				st, err := store.GetStorage(context.Background(), id)
				if err != nil || st == nil {
					return nil, fmt.Errorf("unknown storage %d", id)
				}
				var cfg map[string]any
				if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
					return nil, err
				}
				drv := &local.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			d.Versions = versioning.New(d.Store, d.StorageResolver)
			d.OnlyOffice = onlyoffice.New(d.Store, d.StorageResolver,
				"http://ds.test", "onlyoffice-test-secret", "http://test.local", time.Hour)
			if xtEscrowPub == "" {
				pub, _, err := e2e.GenerateEscrowKeyPair(2048)
				require.NoError(t, err)
				xtEscrowPub = pub
			}
			k, err := e2e.ParseEscrowPublicKey(xtEscrowPub)
			require.NoError(t, err)
			d.E2EEscrow = k
		})
}

// seedXTSide creates one whole tenant. `slug` names it; every row it owns is
// prefixed with it so a leak is legible in the failure message.
func seedXTSide(t *testing.T, store db.Store, slug string) *xtSide {
	t.Helper()
	ctx := context.Background()

	pid, adminEmail, adminPass := seedTenant(t, store, slug, "admin@"+slug+".test", false)
	s := &xtSide{provider: pid, adminEmail: adminEmail, adminPass: adminPass}

	// A real directory, because a version restore has to be measurable on
	// disk. seedStorageFor's /tmp/<name> root is fine for rows but nothing
	// can be read or written through it.
	s.root = t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": s.root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name:          slug + "-store",
		Driver:        "local",
		MountPath:     "/" + slug,
		ConfigJSON:    cfg,
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, pid, st.ID))
	s.storage = st

	s.userEmail = "user@" + slug + ".test"
	s.userPass = xtUserPass
	s.userID = seedUserIn(t, store, pid, s.userEmail)

	// The victim file: live bytes on disk plus a recorded earlier version.
	live := slug + "-secret.txt"
	require.NoError(t, os.WriteFile(filepath.Join(s.root, live), []byte("LIVE-"+slug), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(s.root, ".versions"), 0o755))
	oldKey := ".versions/" + live + ".v1"
	require.NoError(t, os.WriteFile(filepath.Join(s.root, oldKey), []byte("ROLLED-BACK-"+slug), 0o644))

	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: live, Path: live, Type: model.NodeTypeFile,
		Size: int64(len("LIVE-" + slug)), Etag: "live", Mime: "text/plain",
		PathHash: mutTestPathHash(st.ID, live), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	s.node = n

	v, err := store.CreateNodeVersion(ctx, &model.NodeVersion{
		NodeID: n.ID, VersionN: 1, Size: int64(len("ROLLED-BACK-" + slug)),
		Etag: "v1", StorageKey: oldKey,
	})
	require.NoError(t, err)
	s.version = v.ID

	// A folder, for the drop-link crossing.
	dirName := slug + "-inbox"
	require.NoError(t, os.MkdirAll(filepath.Join(s.root, dirName), 0o755))
	dn, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: dirName, Path: dirName, Type: model.NodeTypeDirectory,
		PathHash: mutTestPathHash(st.ID, dirName), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	s.dir = dn

	sh, err := store.CreateShare(ctx, &model.Share{
		NodeID: n.ID, Token: slug + "-existing-token", CreatedBy: &s.userID,
		Kind: model.ShareKindDownload,
	})
	require.NoError(t, err)
	s.share = sh.ID

	c, err := store.CreateNodeComment(ctx, &model.NodeComment{
		NodeID: n.ID, UserID: s.userID, Body: slug + " internal thread text",
	})
	require.NoError(t, err)
	s.comment = c.ID

	return s
}

// newXTFixture is the standard two-tenant instance with the attacker logged
// in. `asAdmin` selects whether the attacker is their tenant's admin or a
// plain user — several of these routes are reachable by both, and the ones
// gated on `user.IsAdmin()` are reachable by the admin of ANY tenant.
func newXTFixture(t *testing.T, asAdmin bool) *xtFixture {
	t.Helper()
	srv, client, store := newXTServer(t, true)
	f := &xtFixture{srv: srv, client: client, store: store}
	f.mine = seedXTSide(t, store, "diyetlif")
	f.theirs = seedXTSide(t, store, "arasboya")
	if asAdmin {
		testutil.LoginAs(t, srv, client, f.mine.adminEmail, f.mine.adminPass)
	} else {
		testutil.LoginAs(t, srv, client, f.mine.userEmail, f.mine.userPass)
	}
	return f
}

func (f *xtFixture) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, f.client, method, f.srv.URL+path, body)
}

func xtItoa(i int64) string { return strconv.FormatInt(i, 10) }

// ── 1. POST /api/files/share {node_id} — mint a link over a foreign node ───

// TestFilesCross_ShareCreateByNodeID.
//
// Red proof on the unfixed build: 200, body carried a working
// `share.url`/`token` over the other tenant's file.
//
// The {"path": …} shape of this same handler was always safe because it
// resolves through the confined ListEnabledStorages — which is exactly why
// the node_id shape went unnoticed.
func TestFilesCross_ShareCreateByNodeID(t *testing.T) {
	f := newXTFixture(t, false)

	status, body := f.do(t, http.MethodPost, "/api/files/share", map[string]any{
		"node_id": f.theirs.node.ID,
	})
	require.Equal(t, http.StatusNotFound, status,
		"minting a public link over another tenant's node must 404: %v", body)

	// And nothing was created — a 404 that still wrote the row is not a fix.
	rows, err := f.store.ListSharesByNode(context.Background(), f.theirs.node.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the tenant's own pre-existing share may remain")
	require.Equal(t, f.theirs.share, rows[0].ID)
}

// TestFilesCross_DropLinkIntoForeignFolder — the WRITE half of the same hole.
//
// kind:"drop" mints a public UPLOAD endpoint. The only extra check on that
// path was "the node is a directory", so the create hole was not merely a
// disclosure: it handed out a link that deposits files into another tenant's
// storage.
func TestFilesCross_DropLinkIntoForeignFolder(t *testing.T) {
	f := newXTFixture(t, false)

	status, body := f.do(t, http.MethodPost, "/api/files/share", map[string]any{
		"node_id": f.theirs.dir.ID,
		"kind":    string(model.ShareKindDrop),
	})
	require.Equal(t, http.StatusNotFound, status,
		"a drop link into another tenant's folder must 404: %v", body)

	rows, err := f.store.ListSharesByNode(context.Background(), f.theirs.dir.ID)
	require.NoError(t, err)
	require.Empty(t, rows, "no upload link may exist over the other tenant's folder")
}

// ── 2. GET /api/files/share?node_id= — hand over existing tokens ───────────

// TestFilesCross_ShareListByNodeID.
//
// Red proof on the unfixed build: 200 with the other tenant's live share
// listed, `url` containing the redeemable token.
//
// ⚠ The refusal here is the handler's own empty-list answer rather than a
// 404 body: HandleList already answers `{"shares":[]}` for a path that
// resolves to nothing, so an empty list IS this endpoint's "never existed".
// Returning a 404 instead would make the confined case distinguishable from
// the unresolvable one, which is the oracle we are closing.
//
// ⚠ The attacker here has to be an ADMIN, and that is a measurement, not a
// convenience. The first version of this test logged in as a plain user and
// came back with an empty list on the UNFIXED build, which looks exactly like
// safety: the per-row filter at the bottom of HandleList drops links the
// caller did not create, so a plain user of another tenant sees nothing.
// `!user.IsAdmin()` is what turns that filter off, and in multi-tenant mode
// "an admin" is the admin of every tenant — so the disclosure is real and it
// is the admin who gets it. A harness that measured the wrong role would have
// reported this endpoint clean.
func TestFilesCross_ShareListByNodeID(t *testing.T) {
	f := newXTFixture(t, true)

	status, body := f.do(t, http.MethodGet,
		"/api/files/share?node_id="+xtItoa(f.theirs.node.ID), nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	shares, _ := body["shares"].([]any)
	require.Empty(t, shares,
		"another tenant's share tokens must not be listed: %v", body)
}

// ── 3. DELETE /api/files/share/{id} — the admin bypass ────────────────────

// TestFilesCross_ShareDeleteAdminBypass.
//
// The non-admin half of this check was right all along (a plain user only
// manages links they created). The admin half was `!user.IsAdmin() && …`,
// and under multi-tenancy "an admin" is the admin of every tenant.
//
// Red proof on the unfixed build: 200 {"ok":true}, and the other tenant's
// share came back with expires_at set — revoked.
func TestFilesCross_ShareDeleteAdminBypass(t *testing.T) {
	f := newXTFixture(t, true) // attacker is an admin — of their OWN tenant

	status, body := f.do(t, http.MethodDelete,
		"/api/files/share/"+xtItoa(f.theirs.share), nil)
	require.Equal(t, http.StatusNotFound, status,
		"a foreign share id must 404: %v", body)

	sh, err := f.store.GetShareByID(context.Background(), f.theirs.share)
	require.NoError(t, err)
	require.Nil(t, sh.ExpiresAt, "the other tenant's share must not have been revoked")
}

// ── 4. /api/files/versions — history, snapshot, and a destructive restore ──

// TestFilesCross_VersionsList — foreign edit history.
//
// Red proof on the unfixed build: 200 with the other tenant's version rows,
// including their storage keys under `.versions/`.
func TestFilesCross_VersionsList(t *testing.T) {
	f := newXTFixture(t, false)

	status, body := f.do(t, http.MethodGet,
		"/api/files/versions?node_id="+xtItoa(f.theirs.node.ID), nil)
	require.Equal(t, http.StatusNotFound, status,
		"another tenant's version history must 404: %v", body)
}

// TestFilesCross_VersionsSnapshot — forced quota burn on a foreign storage.
//
// Red proof on the unfixed build: 200 {"ok":true,"version":{…}}, a new row in
// the other tenant's node_versions and a new object under their `.versions/`.
func TestFilesCross_VersionsSnapshot(t *testing.T) {
	f := newXTFixture(t, false)
	ctx := context.Background()

	before, err := f.store.ListNodeVersions(ctx, f.theirs.node.ID)
	require.NoError(t, err)

	status, body := f.do(t, http.MethodPost, "/api/files/versions/snapshot", map[string]any{
		"node_id": f.theirs.node.ID,
	})
	require.Equal(t, http.StatusNotFound, status,
		"snapshotting another tenant's file must 404: %v", body)

	after, err := f.store.ListNodeVersions(ctx, f.theirs.node.ID)
	require.NoError(t, err)
	require.Len(t, after, len(before),
		"no version row may be created in the other tenant's storage")
}

// TestFilesCross_VersionsRestore — the destructive one.
//
// Restore had NO authorization of any kind: the request struct has no ACL
// field and versions.go contained no `acl.` reference at all. versioning's
// Restore checks only that the version belongs to the node before it
// overwrites the live bytes. So any logged-in account could roll back any
// file on the instance.
//
// Red proof on the unfixed build: 200 {"ok":true}, and the file on the other
// tenant's disk had been overwritten with the old version's contents.
//
// ⚠ This test measures the BYTES, not the status. A refusal that still wrote
// would be the worst possible outcome here.
func TestFilesCross_VersionsRestore(t *testing.T) {
	f := newXTFixture(t, false)
	livePath := filepath.Join(f.theirs.root, f.theirs.node.Path)

	status, body := f.do(t, http.MethodPost, "/api/files/versions/restore", map[string]any{
		"node_id": f.theirs.node.ID, "version_id": f.theirs.version,
	})
	require.Equal(t, http.StatusNotFound, status,
		"restoring another tenant's file must 404: %v", body)

	live, err := os.ReadFile(livePath)
	require.NoError(t, err)
	require.Equal(t, "LIVE-arasboya", string(live),
		"the other tenant's live bytes must be untouched")
}

// ── 5. /api/files/comments — read the thread, delete the thread ───────────

// TestFilesCross_CommentsList — thread text plus author_name.
//
// Red proof on the unfixed build: 200 with the other tenant's comment body
// and the author's display name.
func TestFilesCross_CommentsList(t *testing.T) {
	f := newXTFixture(t, false)

	status, body := f.do(t, http.MethodGet,
		"/api/files/comments?node_id="+xtItoa(f.theirs.node.ID), nil)
	require.Equal(t, http.StatusNotFound, status,
		"another tenant's comment thread must 404: %v", body)
}

// TestFilesCross_CommentsCreate — writing into a foreign thread.
func TestFilesCross_CommentsCreate(t *testing.T) {
	f := newXTFixture(t, false)
	ctx := context.Background()

	status, body := f.do(t, http.MethodPost, "/api/files/comments", map[string]any{
		"node_id": f.theirs.node.ID, "body": "injected from another tenant",
	})
	require.Equal(t, http.StatusNotFound, status,
		"posting into another tenant's thread must 404: %v", body)

	list, err := f.store.ListNodeComments(ctx, f.theirs.node.ID)
	require.NoError(t, err)
	require.Len(t, list, 1, "no foreign comment may have landed")
}

// TestFilesCross_CommentsDelete — the tenant-admin bypass.
//
// comments.CanDelete is author-or-admin, and "admin" is again the admin of
// any tenant, so a tenant admin could delete any comment on the instance.
//
// Red proof on the unfixed build: 200 {"ok":true} and the row soft-deleted.
func TestFilesCross_CommentsDelete(t *testing.T) {
	f := newXTFixture(t, true)
	ctx := context.Background()

	status, body := f.do(t, http.MethodDelete,
		"/api/files/comments/"+xtItoa(f.theirs.comment), nil)
	require.Equal(t, http.StatusNotFound, status,
		"a foreign comment id must 404: %v", body)

	list, err := f.store.ListNodeComments(ctx, f.theirs.node.ID)
	require.NoError(t, err)
	require.Len(t, list, 1, "the other tenant's comment must still be live")
}

// ── 6. /api/files/onlyoffice/config — bytes, via a signed fetch URL ───────

// TestFilesCross_OnlyOfficeConfigByNodeID.
//
// The config response carries a credential-free HMAC-signed URL redeemed at
// the PUBLIC /api/files/onlyoffice/fetch, so this is not a metadata leak —
// it is the file's bytes for every extension OnlyOffice opens.
//
// ⚠ docs/MULTI-TENANCY.md argued this endpoint was safe because "storage is
// derived server-side from the node". That is true and it is not a defence:
// the NODE ID is client-supplied, so deriving the storage from it derives
// nothing about the caller.
//
// Red proof on the unfixed build: 200 with documentServerUrl + a signed
// config whose document.url pointed at the other tenant's file.
func TestFilesCross_OnlyOfficeConfigByNodeID(t *testing.T) {
	f := newXTFixture(t, false)

	t.Run("POST body node_id", func(t *testing.T) {
		status, body := f.do(t, http.MethodPost, "/api/files/onlyoffice/config", map[string]any{
			"node_id": f.theirs.node.ID, "mode": "view",
		})
		require.Equal(t, http.StatusNotFound, status,
			"an office config over another tenant's node must 404: %v", body)
	})

	t.Run("GET query id", func(t *testing.T) {
		status, body := f.do(t, http.MethodGet,
			"/api/files/onlyoffice/config?id="+xtItoa(f.theirs.node.ID)+"&mode=view", nil)
		require.Equal(t, http.StatusNotFound, status,
			"the GET shape must refuse the same crossing: %v", body)
	})
}

// ── 7. /api/files/e2e/escrow/challenge — the storage-name oracle ──────────

// TestFilesCross_EscrowChallengeOracle.
//
// No bytes and no key material: this is an oracle, not a disclosure. It
// distinguishes 200 (encrypted folder here) / 400 "not an encrypted folder"
// (storage real, folder not encrypted) / 400 "unknown adapter" (no such
// storage), which confirms other tenants' storage NAMES and locates their
// E2EE folders. resolveDir used the unconfined GetStorageByName and never
// called CanAccessStorage; the aclAllowID(…LevelViewer) gate in front of it
// is not a tenant boundary.
//
// Red proof on the unfixed build: the foreign storage name answered 400
// "not an encrypted folder" while a name that does not exist answered 400
// "unknown adapter: …" — two distinguishable answers, which is the oracle.
func TestFilesCross_EscrowChallengeOracle(t *testing.T) {
	f := newXTFixture(t, false)

	real := f.theirs.storage.Name
	fake := "no-such-storage-at-all"

	statusReal, bodyReal := f.do(t, http.MethodPost, "/api/files/e2e/escrow/challenge",
		map[string]any{"path": real + "://" + f.theirs.dir.Path})
	statusFake, bodyFake := f.do(t, http.MethodPost, "/api/files/e2e/escrow/challenge",
		map[string]any{"path": fake + "://whatever"})

	require.Equal(t, statusFake, statusReal,
		"a foreign storage name must answer exactly like one that does not exist")
	// The error echoes back the adapter the CALLER named, so the two bodies
	// differ in that one substring and must be identical everywhere else —
	// otherwise the answer still says "this name is real, that one is not".
	require.Equal(t, "unknown adapter: "+fake, bodyFake["error"], "%v", bodyFake)
	require.Equal(t, "unknown adapter: "+real, bodyReal["error"],
		"a foreign storage name must answer with the same template as one that "+
			"does not exist, or the endpoint still confirms the name: %v", bodyReal)
}

// TestFilesCross_EscrowUsedNeedsAChallengeFirst — the NOT-a-hole case.
//
// POST /e2e/escrow/used shares resolveDir with the challenge endpoint, so it
// is now confined too, but it never needed to be: the first thing it does is
// `challenges.take(req.ID)`, and that id is 128 random bits minted by the
// challenge endpoint. Without a challenge there is no id, and redeeming one
// also requires decrypting an RSA-OAEP nonce with the escrow PRIVATE key,
// compared in constant time. Measured here: a foreign path plus a fabricated
// id answers 400 "unknown or expired challenge" — the same answer it gives
// anybody, on the fixed build and on the unfixed one.
//
// So the cryptography really is the gate on this half, and closing the
// challenge endpoint closes the pair. Recorded as a test rather than as a
// sentence in a report because "we decided this one was fine" is the kind of
// claim that should be re-runnable.
func TestFilesCross_EscrowUsedNeedsAChallengeFirst(t *testing.T) {
	f := newXTFixture(t, false)

	status, body := f.do(t, http.MethodPost, "/api/files/e2e/escrow/used", map[string]any{
		"path":  f.theirs.storage.Name + "://" + f.theirs.dir.Path,
		"id":    "fabricated-challenge-id",
		"nonce": "AAAA",
	})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	require.Equal(t, "unknown or expired challenge", body["error"],
		"the challenge id is the first gate, before the path is even looked at: %v", body)
}

// ── single-tenant control ─────────────────────────────────────────────────

// TestFilesCross_SingleTenantUnaffected is the other half of every proof
// above: with multi-tenancy OFF no scope is attached, nothing here confines,
// and each of those requests must keep working exactly as it did.
//
// It passes on `main` too — that is the point. A tenant check that also
// breaks the single-tenant install would pass the crossing tests and still be
// a regression for every user filex actually has.
func TestFilesCross_SingleTenantUnaffected(t *testing.T) {
	srv, client, store := newXTServer(t, false)
	// One "tenant" worth of rows, but multi-tenant mode is off, so no scope is
	// attached and the provider link is inert.
	side := seedXTSide(t, store, "solo")
	// ⚠ The caller must be a PROVIDER-LESS admin, and that is not cosmetic:
	// auth.LoginAllowed refuses a user homed in a non-supertenant provider
	// while multi-tenant mode is off (the "mode turned off while tenants
	// exist" maintenance lockout). Logging in as side.adminEmail here answers
	// 403 at /api/auth/login, and every assertion below would then be
	// measuring the login screen instead of the handlers.
	adminEmail, adminPass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, adminEmail, adminPass)

	do := func(method, path string, body any) (int, map[string]any) {
		return doJSON(t, client, method, srv.URL+path, body)
	}
	ctx := context.Background()

	t.Run("share create by node_id still works", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/share", map[string]any{
			"node_id": side.node.ID,
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("drop link on a folder still works", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/share", map[string]any{
			"node_id": side.dir.ID, "kind": string(model.ShareKindDrop),
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("share list by node_id still lists", func(t *testing.T) {
		status, body := do(http.MethodGet,
			"/api/files/share?node_id="+xtItoa(side.node.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		shares, _ := body["shares"].([]any)
		require.NotEmpty(t, shares, "the admin must still see the node's links: %v", body)
	})

	t.Run("share delete still works", func(t *testing.T) {
		status, body := do(http.MethodDelete,
			"/api/files/share/"+xtItoa(side.share), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("versions list still works", func(t *testing.T) {
		status, body := do(http.MethodGet,
			"/api/files/versions?node_id="+xtItoa(side.node.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("versions snapshot still works", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/versions/snapshot", map[string]any{
			"node_id": side.node.ID,
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("versions restore still rewrites the bytes", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/versions/restore", map[string]any{
			"node_id": side.node.ID, "version_id": side.version,
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
		live, err := os.ReadFile(filepath.Join(side.root, side.node.Path))
		require.NoError(t, err)
		require.Equal(t, "ROLLED-BACK-solo", string(live),
			"the single-tenant restore must still do the restore")
	})

	t.Run("comments list still works", func(t *testing.T) {
		status, body := do(http.MethodGet,
			"/api/files/comments?node_id="+xtItoa(side.node.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("comments create still works", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/comments", map[string]any{
			"node_id": side.node.ID, "body": "hello from the single-tenant install",
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
	})

	t.Run("comments delete still works for an admin", func(t *testing.T) {
		status, body := do(http.MethodDelete,
			"/api/files/comments/"+xtItoa(side.comment), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		list, err := store.ListNodeComments(ctx, side.node.ID)
		require.NoError(t, err)
		require.Len(t, list, 1, "only the comment the admin deleted may be gone")
	})

	t.Run("onlyoffice config still builds", func(t *testing.T) {
		status, body := do(http.MethodPost, "/api/files/onlyoffice/config", map[string]any{
			"node_id": side.node.ID, "mode": "view",
		})
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.NotEmpty(t, body["documentServerUrl"], "%v", body)
	})

	t.Run("escrow challenge still reaches the storage", func(t *testing.T) {
		// Not an encrypted folder, so 400 — but the 400 that says the folder
		// is not encrypted, which means resolveDir got there. The refusal
		// this fix adds would answer "unknown adapter" instead.
		status, body := do(http.MethodPost, "/api/files/e2e/escrow/challenge",
			map[string]any{"path": side.storage.Name + "://" + side.dir.Path})
		require.Equal(t, http.StatusBadRequest, status, "%v", body)
		require.Equal(t, "not an encrypted folder", body["error"],
			"the single-tenant install must still resolve its own storage: %v", body)
	})
}

// ── supertenant control ───────────────────────────────────────────────────

// TestFilesCross_SupertenantReachesEverything — the platform operator's
// account is confine-exempt by design (tenant.Scope.CanAccessStorage returns
// true for a supertenant), and must keep being able to work on a tenant's
// rows. This is the case a check written against `tenant.FromContext` alone,
// rather than confinedScope, gets wrong.
func TestFilesCross_SupertenantReachesEverything(t *testing.T) {
	srv, client, store := newXTServer(t, true)
	_, superEmail, superPass := seedTenant(t, store, "platform", "ops@platform.test", true)
	side := seedXTSide(t, store, "arasboya")
	testutil.LoginAs(t, srv, client, superEmail, superPass)

	do := func(method, path string, body any) (int, map[string]any) {
		return doJSON(t, client, method, srv.URL+path, body)
	}

	status, body := do(http.MethodGet,
		"/api/files/versions?node_id="+xtItoa(side.node.ID), nil)
	require.Equal(t, http.StatusOK, status, "supertenant must still read history: %v", body)

	status, body = do(http.MethodGet,
		"/api/files/comments?node_id="+xtItoa(side.node.ID), nil)
	require.Equal(t, http.StatusOK, status, "supertenant must still read comments: %v", body)

	status, body = do(http.MethodPost, "/api/files/share", map[string]any{
		"node_id": side.node.ID,
	})
	require.Equal(t, http.StatusOK, status, "supertenant must still mint a link: %v", body)

	status, body = do(http.MethodPost, "/api/files/onlyoffice/config", map[string]any{
		"node_id": side.node.ID, "mode": "view",
	})
	require.Equal(t, http.StatusOK, status, "supertenant must still open the editor: %v", body)
}

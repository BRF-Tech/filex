package handlers_test

// GET /api/files/thumb/{id} used to serve a rendered preview of any file on
// the instance to anybody who could count.
//
// ⚠ The three things that made it survive a reading of the code:
//
//   - the struct comment says "Public-but-signed … to prevent enumeration", so
//     the eye stops there. `checkSig` returned true when `sig` was ABSENT;
//   - `manager.go` emitted `thumb_url` with no signature, so no deployment was
//     ever on the signed path;
//   - nothing in the codebase ever wrote `settings.thumb_signing_key`, so even
//     a caller who supplied a signature was waved through by the `key == ""`
//     branch. Measured: `?sig=deadbeef` answered 200 with the JPEG.
//
// It is NOT a tenancy bug. Node ids are dense integers and a single-tenant
// install leaked exactly as much — which is why the single-tenant assertions
// below matter as much as the cross-tenant ones.
//
// ⚠⚠ The fix cannot simply be "require auth". Thumbnails are rendered by
// `<img src>`, which carries no Authorization header, and the session cookie is
// SameSite=Lax so it is not sent by an <img> inside a third-party embed either.
// Hence two proofs: a signature the listing stamps onto the URL it hands out,
// or an authenticated caller who clears the node's tenancy, confinement and
// ACL. Every case below names which of the two it is exercising.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// ── harness ───────────────────────────────────────────────────────────────

// thumbFixture is one instance with a real local storage, a catalogued file
// and a rendered thumbnail on disk — the state the endpoint actually serves
// from.
type thumbFixture struct {
	srv      *httptest.Server
	client   *http.Client
	store    db.Store
	cacheDir string
	storage  *model.Storage
	root     string
	node     *model.Node
	jpeg     []byte
}

// newThumbServer builds the router the way production does, with a REAL local
// driver so the listing endpoint can be exercised end to end (that is the
// assertion that proves an embed still renders).
func newThumbServer(t *testing.T, multiTenant bool) (*httptest.Server, *http.Client, db.Store, string) {
	t.Helper()
	cacheDir := t.TempDir()
	srv, client, store := testutil.NewTestServerWith(t,
		func(c *config.Config) {
			c.MultiTenant = multiTenant
			c.Thumbs.CacheDir = cacheDir
		},
		func(d *api.Deps) {
			st := d.Store
			d.StorageResolver = func(id int64) (storage.Driver, error) {
				row, err := st.GetStorage(context.Background(), id)
				if err != nil || row == nil {
					return nil, fmt.Errorf("unknown storage %d", id)
				}
				var cfg map[string]any
				if err := json.Unmarshal(row.ConfigJSON, &cfg); err != nil {
					return nil, err
				}
				drv := &local.Driver{}
				if err := drv.Init(context.Background(), cfg); err != nil {
					return nil, err
				}
				return drv, nil
			}
			// Without a pipeline the handler cannot resolve a cache path and
			// every case below would 500 instead of measuring authorization.
			d.Thumbs = thumb.New(d.Store, cacheDir, thumb.Capabilities{Image: true})
		})
	return srv, client, store, cacheDir
}

// seedThumbNode creates a storage + file + a "ready" thumbnail whose JPEG
// really exists in the cache dir.
func seedThumbNode(t *testing.T, store db.Store, cacheDir, name string) (*model.Storage, string, *model.Node, []byte) {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: name + "-store", Driver: "local", MountPath: "/" + name,
		ConfigJSON: cfg, SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	rel := name + "-invoice.png"
	require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte("PNGDATA-"+name), 0o644))
	n, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: rel, Path: rel, Type: model.NodeTypeFile,
		Size: 12, Etag: "e", Mime: "image/png",
		PathHash: mutTestPathHash(st.ID, rel), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	// The rendered preview: a row that says "ready" plus real bytes in the
	// cache. `JFIF` is enough of a JPEG for a leak to be legible in a diff.
	jpeg := []byte("\xff\xd8\xff\xe0JFIF-rendered-preview-of-" + name)
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, strconv.FormatInt(n.ID, 10)+".jpg"), jpeg, 0o644))
	now := time.Now()
	require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{
		NodeID: n.ID, State: "ready", StorageKey: rel, Width: 240, Height: 240, GeneratedAt: &now,
	}))
	return st, root, n, jpeg
}

func newThumbFixture(t *testing.T, multiTenant bool) *thumbFixture {
	t.Helper()
	srv, client, store, cacheDir := newThumbServer(t, multiTenant)
	f := &thumbFixture{srv: srv, client: client, store: store, cacheDir: cacheDir}
	f.storage, f.root, f.node, f.jpeg = seedThumbNode(t, store, cacheDir, "solo")
	return f
}

// getRaw performs a request with NO cookie jar and no credentials at all —
// the anonymous internet, and also the shape of a cross-site `<img>` fetch.
func getRaw(t *testing.T, url string, hdr map[string]string) (int, []byte, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// A bare client: no jar, so nothing this test did earlier can leak in.
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body, resp.Header.Get("Content-Type")
}

func thumbPath(id int64) string { return "/api/files/thumb/" + strconv.FormatInt(id, 10) }

// ── 1. the leak itself ────────────────────────────────────────────────────

// TestThumb_AnonymousIsRefused.
//
// Red proof on the unfixed build: 200, `Content-Type: image/jpeg`, and the
// full rendered body — on the same server where an anonymous
// /api/files/quota/me correctly answers 401.
func TestThumb_AnonymousIsRefused(t *testing.T) {
	f := newThumbFixture(t, false)

	// The control, so the failure cannot be "this harness has no auth at all".
	status, _, _ := getRaw(t, f.srv.URL+"/api/files/quota/me", nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"control endpoint must refuse an anonymous caller")

	status, body, ctype := getRaw(t, f.srv.URL+thumbPath(f.node.ID), nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"an anonymous thumbnail fetch must be refused, got %d %q", status, string(body))
	require.NotContains(t, ctype, "image/jpeg")
	require.NotContains(t, string(body), "rendered-preview")
}

// TestThumb_UnsignedSignatureIsNotAWaiver — the `key == ""` branch.
//
// Red proof on the unfixed build: `?sig=deadbeef` answered 200 with the JPEG,
// because nothing ever wrote settings.thumb_signing_key and an empty key was
// read as "signatures are not configured, let everything through".
func TestThumb_UnsignedSignatureIsNotAWaiver(t *testing.T) {
	f := newThumbFixture(t, false)

	for _, q := range []string{"?sig=deadbeef", "?sig=", "?exp=99999999999&sig=deadbeef", "?exp=0&sig="} {
		status, body, _ := getRaw(t, f.srv.URL+thumbPath(f.node.ID)+q, nil)
		require.Equal(t, http.StatusUnauthorized, status,
			"%q must not be accepted as proof, got %d %q", q, status, string(body))
	}
}

// ── 2. the four consumers ─────────────────────────────────────────────────

// TestThumb_SignedURLRendersWithNoCredentials is the EMBED case: an `<img>`
// inside a third-party page sends no Authorization header, and no cookie
// either (SameSite=Lax). The URL the server itself emitted has to be enough.
//
// ⚠ It asserts on the URL the LISTING produced, not on one the test minted:
// a fix where the endpoint accepts a signature nobody stamps would pass the
// second and blank every embed in production.
func TestThumb_SignedURLRendersWithNoCredentials(t *testing.T) {
	f := newThumbFixture(t, false)
	email, pass := testutil.SeedAdmin(t, f.store)
	testutil.LoginAs(t, f.srv, f.client, email, pass)

	status, body := doJSON(t, f.client, http.MethodGet,
		f.srv.URL+"/api/files/manager?action=index&path="+f.storage.Name+"://", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)

	files, _ := body["files"].([]any)
	require.NotEmpty(t, files, "the listing must return the seeded file: %v", body)
	var thumbURL string
	for _, raw := range files {
		row, _ := raw.(map[string]any)
		if u, _ := row["thumb_url"].(string); u != "" {
			thumbURL = u
			break
		}
	}
	require.NotEmpty(t, thumbURL, "the listing must emit a thumb_url: %v", body)
	require.Contains(t, thumbURL, "sig=",
		"the listing must STAMP the URL — an unsigned one blanks every embedded <img>")
	require.Contains(t, thumbURL, "exp=", "a stamp with no expiry is a permanent bearer capability")

	status, raw, ctype := getRaw(t, f.srv.URL+thumbURL, nil)
	require.Equal(t, http.StatusOK, status,
		"the stamped URL must render with no credentials at all: %d %q", status, string(raw))
	require.Contains(t, ctype, "image/jpeg")
	require.Equal(t, f.jpeg, raw)
}

// TestThumb_AuthenticatedConsumersRender covers the other three: the admin
// SPA (session cookie), the desktop app (bearer token) and an embedded
// explorer whose host proxy injects a root-confined token. All three fetch
// through useThumbs — `fetch()` with credentials — so an unsigned URL is fine
// for them, and that is the path this asserts.
func TestThumb_AuthenticatedConsumersRender(t *testing.T) {
	f := newThumbFixture(t, false)
	adminID, email := testutil.SeedAdminUser(t, f.store)
	testutil.LoginAs(t, f.srv, f.client, email, "TestAdminPass!1")

	t.Run("admin SPA — session cookie", func(t *testing.T) {
		resp, err := f.client.Get(f.srv.URL + thumbPath(f.node.ID))
		require.NoError(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		require.Equal(t, f.jpeg, body)
	})

	t.Run("desktop app — bearer token", func(t *testing.T) {
		tok := testutil.NewAPIToken(t, f.store, adminID, "")
		status, body, ctype := getRaw(t, f.srv.URL+thumbPath(f.node.ID),
			map[string]string{"Authorization": "Bearer " + tok})
		require.Equal(t, http.StatusOK, status, string(body))
		require.Contains(t, ctype, "image/jpeg")
		require.Equal(t, f.jpeg, body)
	})

	t.Run("embedded explorer — root-confined token, inside its root", func(t *testing.T) {
		tok := testutil.NewAPIToken(t, f.store, adminID,
			"root:"+f.storage.Name+"://")
		status, body, _ := getRaw(t, f.srv.URL+thumbPath(f.node.ID),
			map[string]string{"Authorization": "Bearer " + tok})
		require.Equal(t, http.StatusOK, status, string(body))
		require.Equal(t, f.jpeg, body)
	})
}

// TestThumb_ConfinedTokenCannotLeaveItsRoot — the node id is not a path, so
// confine.Middleware's rewriting cannot reach this route and the handler has
// to filter by node itself. Without that, a host app's embed token could walk
// the id range out of its own subtree.
func TestThumb_ConfinedTokenCannotLeaveItsRoot(t *testing.T) {
	f := newThumbFixture(t, false)
	adminID, _ := testutil.SeedAdminUser(t, f.store)

	tok := testutil.NewAPIToken(t, f.store, adminID,
		"root:"+f.storage.Name+"://somewhere-else")
	status, body, _ := getRaw(t, f.srv.URL+thumbPath(f.node.ID),
		map[string]string{"Authorization": "Bearer " + tok})
	require.Equal(t, http.StatusNotFound, status,
		"a confined token must not preview a node outside its root: %d %q", status, string(body))
	require.NotContains(t, string(body), "rendered-preview")
}

// ── 3. tenancy ────────────────────────────────────────────────────────────

// TestThumb_CrossTenantIsRefused — an authenticated tenant user naming another
// tenant's node id.
//
// ⚠ 404, not 403: a foreign id must be indistinguishable from one that never
// existed, or the endpoint is an enumeration oracle across tenants.
func TestThumb_CrossTenantIsRefused(t *testing.T) {
	srv, client, store, cacheDir := newThumbServer(t, true)

	minePID, _, _ := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	theirsPID, _, _ := seedTenant(t, store, "arasboya", "admin@arasboya.test", false)

	myStorage, _, _, _ := seedThumbNode(t, store, cacheDir, "diyetlif")
	require.NoError(t, store.LinkProviderStorage(context.Background(), minePID, myStorage.ID))
	theirStorage, _, theirNode, theirJPEG := seedThumbNode(t, store, cacheDir, "arasboya")
	require.NoError(t, store.LinkProviderStorage(context.Background(), theirsPID, theirStorage.ID))

	seedUserIn(t, store, minePID, "user@diyetlif.test")
	testutil.LoginAs(t, srv, client, "user@diyetlif.test", xtUserPass)

	resp, err := client.Get(srv.URL + thumbPath(theirNode.ID))
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusNotFound, resp.StatusCode,
		"another tenant's preview must 404: %d %q", resp.StatusCode, string(body))
	require.NotEqual(t, theirJPEG, body)
}

// ── 4. single-tenant regression ───────────────────────────────────────────

// TestThumb_SingleTenantUnaffected is the honest half of the proof: it passes
// on `main` too. Nothing here may make an ordinary admin or user worse off.
func TestThumb_SingleTenantUnaffected(t *testing.T) {
	f := newThumbFixture(t, false)
	email, pass := testutil.SeedAdmin(t, f.store)

	t.Run("an admin still sees the preview", func(t *testing.T) {
		testutil.LoginAs(t, f.srv, f.client, email, pass)
		resp, err := f.client.Get(f.srv.URL + thumbPath(f.node.ID))
		require.NoError(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		require.Equal(t, f.jpeg, body)
	})

	t.Run("a plain user still sees the preview", func(t *testing.T) {
		testutil.SeedRegularUser(t, f.store, "plain@test.local", "PlainPass!1")
		jar, jerr := cookiejar.New(nil)
		require.NoError(t, jerr)
		srv2, client2 := f.srv, &http.Client{Jar: jar}
		testutil.LoginAs(t, srv2, client2, "plain@test.local", "PlainPass!1")
		resp, err := client2.Get(srv2.URL + thumbPath(f.node.ID))
		require.NoError(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		require.Equal(t, f.jpeg, body)
	})

	t.Run("a missing thumbnail is still a 404, not a 500", func(t *testing.T) {
		testutil.LoginAs(t, f.srv, f.client, email, pass)
		resp, err := f.client.Get(f.srv.URL + thumbPath(999999))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("a bad id is still a 400", func(t *testing.T) {
		testutil.LoginAs(t, f.srv, f.client, email, pass)
		resp, err := f.client.Get(f.srv.URL + "/api/files/thumb/not-a-number")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

// TestThumb_ExpiredStampIsRefused — a stamp is a capability with a clock on
// it, and a stamp with no clock would be a permanent one.
func TestThumb_ExpiredStampIsRefused(t *testing.T) {
	f := newThumbFixture(t, false)

	// A signer over the same store mints what the server will verify.
	live := thumb.NewSigner(f.store, time.Hour).Query(f.node.ID)
	require.Contains(t, live, "sig=")

	status, _, _ := getRaw(t, f.srv.URL+thumbPath(f.node.ID)+"?"+live, nil)
	require.Equal(t, http.StatusOK, status, "a live stamp must be accepted")

	// Same signature, expiry rewritten to the past: the HMAC covers the
	// expiry, so this is both "expired" and "tampered" and must fail either
	// way.
	past := strings.Replace(live, "exp="+expOf(t, live), "exp=1", 1)
	status, _, _ = getRaw(t, f.srv.URL+thumbPath(f.node.ID)+"?"+past, nil)
	require.Equal(t, http.StatusUnauthorized, status, "an expired stamp must be refused")

	// A live stamp for a DIFFERENT node must not open this one.
	other := thumb.NewSigner(f.store, time.Hour).Query(f.node.ID + 1)
	status, _, _ = getRaw(t, f.srv.URL+thumbPath(f.node.ID)+"?"+other, nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"a stamp is bound to one node id, not to the endpoint")
}

func expOf(t *testing.T, q string) string {
	t.Helper()
	for _, part := range strings.Split(q, "&") {
		if strings.HasPrefix(part, "exp=") {
			return strings.TrimPrefix(part, "exp=")
		}
	}
	t.Fatalf("no exp in %q", q)
	return ""
}

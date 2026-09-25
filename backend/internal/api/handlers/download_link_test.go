package handlers_test

// One-file download links (#71): the same mint/redeem pair as a selection
// archive (archive_download_test.go), asked for with `"mode":"file"`.
//
// Why they exist: the browser's drag-out (`DownloadURL` on the dataTransfer)
// fetches a URL with its own download stack, which carries no Authorization
// header. The admin SPA signs its calls with a bearer, so until this link it
// could not offer that drag at all. The link is minted by the signed-in caller
// and redeemed by the browser with no credential.
//
// What a link has to be, and where each property is pinned:
//   - the file's own bytes, not an archive           → _ServesTheFileItself
//   - good once, gone after                          → _ServesTheFileItself
//   - short-lived; lapsed = 410                      → _ExpiredIs410, _LifeIsCapped
//   - one FILE: a folder and a list are refused      → _FolderIsRefused, _ExactlyOnePath
//   - the caller's own reach at mint                 → _ACLIsTheOwners (mint half)
//   - the owner's reach AGAIN at redeem              → _ACLIsTheOwners (revoke half)
//   - a disabled owner / revoked token kills it      → _DisabledOwner, _RevokedToken
//   - another tenant's host cannot redeem it         → _AnotherTenantsHost
//   - the storage leaving the tenant kills it        → _StorageLeftTheTenant
//   - a `root:` token cannot reach past its root     → _ConfinedTokenStaysInRoot
//   - the redeem lands in the audit log              → _ACLIsTheOwners, _ServesTheFileItself

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// mintLink asks for a one-file link with a token.
func mintLink(t *testing.T, client *http.Client, base, tok string, body map[string]any) (int, map[string]any) {
	t.Helper()
	if _, ok := body["mode"]; !ok {
		body["mode"] = "file"
	}
	resp := aiReq(t, client, "POST", base+"/api/files/archive/download", tok, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// redeem fetches a link with NO credentials at all — which is how the browser's
// download stack arrives — optionally on a named host.
func redeem(t *testing.T, base, url, host string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+url, nil)
	require.NoError(t, err)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, raw
}

// auditRows returns the rows of one action, newest first.
func auditRows(t *testing.T, store db.Store, action string) []*model.AuditEntry {
	t.Helper()
	all, err := store.ListAuditRecent(context.Background(), 200)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, e := range all {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

func linkURL(t *testing.T, info map[string]any) string {
	t.Helper()
	u, _ := info["url"].(string)
	require.True(t, strings.HasPrefix(u, "/z/"), "url = %q (%v)", u, info)
	return u
}

func TestDownloadLink_ServesTheFileItself(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://docs/Sözleşme.txt": "RAW BYTES"})

	code, info := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://docs/Sözleşme.txt"}})
	require.Equal(t, http.StatusOK, code, "mint: %v", info)
	assert.Equal(t, "file", info["mode"], "the answer says which kind of link it minted — an older server would have minted a zip")
	assert.Equal(t, "Sözleşme.txt", info["name"])
	assert.Equal(t, float64(1), info["files"])
	assert.Equal(t, float64(len("RAW BYTES")), info["bytes"])
	ttl, _ := info["ttl_seconds"].(float64)
	assert.True(t, ttl > 0 && ttl <= 60, "a drag link lives a minute at most, got %v", info["ttl_seconds"])

	url := linkURL(t, info)
	resp, raw := redeem(t, srv.URL, url, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "redeem: %s", raw)
	assert.Equal(t, "RAW BYTES", string(raw), "the file's own bytes — not an archive of it")
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "attachment")
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "filename*=UTF-8''S%C3%B6zle%C5%9Fme.txt")
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), "a one-time link's body is not for any cache")

	second, _ := redeem(t, srv.URL, url, "")
	assert.Equal(t, http.StatusNotFound, second.StatusCode, "a used link must not serve the file again")

	rows := auditRows(t, store, "file.download_link")
	require.Len(t, rows, 1, "the redeem is audited once")
	require.NotNil(t, rows[0].UserID, "the row names the account the link acted for")
	assert.Equal(t, "main://docs/Sözleşme.txt", rows[0].Metadata["target_name"])
	assert.NotContains(t, strings.Join([]string{rows[0].TargetID, rows[0].IP}, " "), strings.TrimPrefix(url, "/z/"),
		"the link itself never reaches the audit table")
	for k, v := range rows[0].Metadata {
		assert.NotContains(t, fmt.Sprint(v), strings.TrimPrefix(url, "/z/"), "the link itself never reaches the audit table (%s)", k)
	}
}

// The archive mint is unchanged, and now says what it minted.
func TestDownloadLink_ArchiveModeIsTheDefault(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://z.txt": "Z"})
	code, info := mintArchive(t, client, srv.URL, tok, []string{"main://z.txt"})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "zip", info["mode"])
	resp, raw := fetchArchive(t, srv.URL, info["url"].(string))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Z", openZip(t, raw)["z.txt"])
}

func TestDownloadLink_UnknownModeIsRefused(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://m.txt": "M"})
	code, _ := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://m.txt"}, "mode": "tar"})
	assert.Equal(t, http.StatusBadRequest, code)
}

// A folder cannot ride a single-file download — refused at the mint, with a
// code the client can read, rather than minted and failing at the drop.
func TestDownloadLink_FolderIsRefused(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://Klasör/iç.txt": "I"})
	code, info := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://Klasör"}})
	assert.Equal(t, http.StatusConflict, code, "%v", info)
	assert.Equal(t, "IS_FOLDER", info["code"])
}

func TestDownloadLink_ExactlyOnePath(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://a.txt": "A", "main://b.txt": "B"})
	code, _ := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://a.txt", "main://b.txt"}})
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{}})
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestDownloadLink_MissingFileIs404(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	code, _ := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://yok.txt"}})
	assert.Equal(t, http.StatusNotFound, code)
}

func TestDownloadLink_ExpiredIs410(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://late.txt": "L"})
	code, info := mintLink(t, client, srv.URL, tok, map[string]any{
		"paths": []string{"main://late.txt"}, "expires_in_seconds": 1,
	})
	require.Equal(t, http.StatusOK, code, "%v", info)
	assert.Equal(t, float64(1), info["ttl_seconds"], "a caller may ask for a SHORTER life")
	time.Sleep(1100 * time.Millisecond)
	resp, _ := redeem(t, srv.URL, linkURL(t, info), "")
	assert.Equal(t, http.StatusGone, resp.StatusCode, "a lapsed link is 410 — not the same answer as one that never existed")
}

func TestDownloadLink_LifeIsCapped(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://cap.txt": "C"})
	code, info := mintLink(t, client, srv.URL, tok, map[string]any{
		"paths": []string{"main://cap.txt"}, "expires_in_seconds": 86400,
	})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(60), info["ttl_seconds"], "a caller may not ask for a LONGER one")
}

// ── the owner's reach: at mint, and again at redeem ─────────────────────────

// rbacDrive is one RBAC-enabled storage over a real directory, two plain
// users, and a file only the first may read.
type rbacDrive struct {
	base         string
	store        db.Store
	st           *model.Storage
	owner, other *http.Client
	ownerID      int64
	grantID      int64
}

func newRBACDrive(t *testing.T) *rbacDrive {
	t.Helper()
	srv, _, store := newXTServer(t, false)
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Hukuk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Hukuk", "sözleşme.txt"), []byte("GİZLİ"), 0o644))
	cfg, _ := json.Marshal(map[string]any{"root": root})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "ekip", Driver: "local", MountPath: "/ekip", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true, RBACEnabled: true,
	})
	require.NoError(t, err)
	owner := seedSharedUser(t, store, "sahip@test.local", "SahipPass1!")
	seedSharedUser(t, store, "baska@test.local", "BaskaPass1!")
	g, err := store.CreateFileGrant(ctx, &model.FileGrant{
		StorageID: st.ID, PathPrefix: "Hukuk/sözleşme.txt", UserID: owner.ID, Level: model.GrantViewer,
	})
	require.NoError(t, err)

	d := &rbacDrive{base: srv.URL, store: store, st: st, ownerID: owner.ID, grantID: g.ID}
	d.owner = freshClient(t)
	testutil.LoginAs(t, srv, d.owner, "sahip@test.local", "SahipPass1!")
	d.other = freshClient(t)
	testutil.LoginAs(t, srv, d.other, "baska@test.local", "BaskaPass1!")
	return d
}

func (d *rbacDrive) mint(t *testing.T, as *http.Client) (int, map[string]any) {
	t.Helper()
	code, raw := doReq(t, as, http.MethodPost, d.base+"/api/files/archive/download",
		map[string]any{"paths": []string{"ekip://Hukuk/sözleşme.txt"}, "mode": "file"})
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return code, out
}

func TestDownloadLink_ACLIsTheOwners(t *testing.T) {
	d := newRBACDrive(t)

	// Another user cannot mint a link to a file they cannot read.
	code, info := d.mint(t, d.other)
	assert.Equal(t, http.StatusForbidden, code, "a user without a grant must not get a link: %v", info)

	// The owner can, and the link carries the owner's reach NOW, not then:
	// revoke the grant between the mint and the drop and the drop is refused.
	code, info = d.mint(t, d.owner)
	require.Equal(t, http.StatusOK, code, "%v", info)
	url := linkURL(t, info)
	require.NoError(t, d.store.DeleteFileGrant(context.Background(), d.grantID))
	resp, raw := redeem(t, d.base, url, "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "the grant is gone; the link must not outlive it: %s", raw)
	assert.NotContains(t, string(raw), "GİZLİ")

	refused := auditRows(t, d.store, "file.download_link_refused")
	require.Len(t, refused, 1, "a refused redeem is audited too")
	require.NotNil(t, refused[0].UserID)
	assert.Equal(t, d.ownerID, *refused[0].UserID)
	assert.Equal(t, "acl", refused[0].Metadata["reason"])

	// Grant back, mint again, and the same person's drop arrives.
	_, err := d.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: d.st.ID, PathPrefix: "Hukuk/sözleşme.txt", UserID: d.ownerID, Level: model.GrantViewer,
	})
	require.NoError(t, err)
	code, info = d.mint(t, d.owner)
	require.Equal(t, http.StatusOK, code, "%v", info)
	resp, raw = redeem(t, d.base, linkURL(t, info), "")
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s", raw)
	assert.Equal(t, "GİZLİ", string(raw))
	served := auditRows(t, d.store, "file.download_link")
	require.Len(t, served, 1)
	assert.Equal(t, d.ownerID, *served[0].UserID, "the download is attributed to the account that minted it")
}

func TestDownloadLink_DisabledOwner(t *testing.T) {
	d := newRBACDrive(t)
	code, info := d.mint(t, d.owner)
	require.Equal(t, http.StatusOK, code, "%v", info)
	require.NoError(t, d.store.SetUserEnabled(context.Background(), d.ownerID, false))
	resp, raw := redeem(t, d.base, linkURL(t, info), "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a disabled account's links die with it: %s", raw)
}

func TestDownloadLink_RevokedToken(t *testing.T) {
	srv, client, store, _ := aiFixture(t)
	u, err := store.CreateUser(context.Background(), "tokenli@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	tok := issueToken(t, store, u.ID, fullScopes, nil)
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://t.txt": "T"})
	code, info := mintLink(t, client, srv.URL, tok, map[string]any{"paths": []string{"main://t.txt"}})
	require.Equal(t, http.StatusOK, code, "%v", info)

	toks, err := store.ListAPITokensByUser(context.Background(), u.ID)
	require.NoError(t, err)
	require.Len(t, toks, 1)
	require.NoError(t, store.DeleteAPIToken(context.Background(), toks[0].ID))

	resp, _ := redeem(t, srv.URL, linkURL(t, info), "")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a link minted with a token dies when the token is revoked")
}

// A `root:` token reaches one folder. Its mint must not name a file outside it
// — in EITHER mode: the archive mint had the same gap, because confine rewrites
// `path`, `source` and `items` in a body but not `paths`.
func TestDownloadLink_ConfinedTokenStaysInRoot(t *testing.T) {
	srv, client, store, adminTok := aiFixture(t)
	seedFiles(t, client, srv.URL, adminTok, map[string]string{
		"main://tenant/in.txt":      "IN",
		"main://outside/secret.txt": "SECRET",
	})
	u, err := store.CreateUser(context.Background(), "kutu@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	conf := issueToken(t, store, u.ID, "read,write,delete,root:main://tenant", nil)

	code, info := mintLink(t, client, srv.URL, conf, map[string]any{"paths": []string{"main://tenant/in.txt"}})
	require.Equal(t, http.StatusOK, code, "inside the root: %v", info)

	for _, mode := range []string{"file", "zip"} {
		code, info = mintLink(t, client, srv.URL, conf, map[string]any{
			"paths": []string{"main://outside/secret.txt"}, "mode": mode,
		})
		assert.Equal(t, http.StatusForbidden, code, "mode %s, outside the root: %v", mode, info)
	}
}

// ── tenants ─────────────────────────────────────────────────────────────────

func TestDownloadLink_AnotherTenantsHost(t *testing.T) {
	f := newXTFixture(t, false)
	ctx := context.Background()
	for _, side := range []*xtSide{f.mine, f.theirs} {
		p, err := f.store.GetProvider(ctx, side.provider)
		require.NoError(t, err)
		p.Host = side.storage.Name + ".test"
		require.NoError(t, f.store.UpdateProvider(ctx, p))
	}
	path := f.mine.storage.Name + "://" + f.mine.node.Path

	// Minted on my tenant's host…
	code, raw := doReqHost(t, f.client, http.MethodPost, f.srv.URL+"/api/files/archive/download",
		f.mine.storage.Name+".test", map[string]any{"paths": []string{path}, "mode": "file"})
	require.Equal(t, http.StatusOK, code, "%s", raw)
	info := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &info))
	url := linkURL(t, info)

	// …is refused on the other tenant's host.
	resp, body := redeem(t, f.srv.URL, url, f.theirs.storage.Name+".test")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s", body)
	assert.NotContains(t, string(body), "LIVE-")

	// A fresh link on the right host arrives.
	code, raw = doReqHost(t, f.client, http.MethodPost, f.srv.URL+"/api/files/archive/download",
		f.mine.storage.Name+".test", map[string]any{"paths": []string{path}, "mode": "file"})
	require.Equal(t, http.StatusOK, code, "%s", raw)
	require.NoError(t, json.Unmarshal(raw, &info))
	resp, body = redeem(t, f.srv.URL, linkURL(t, info), f.mine.storage.Name+".test")
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s", body)
	assert.Equal(t, "LIVE-"+strings.TrimSuffix(f.mine.storage.Name, "-store"), string(body))
}

func TestDownloadLink_OtherTenantsFileCannotBeMinted(t *testing.T) {
	f := newXTFixture(t, false)
	code, _ := f.do(t, http.MethodPost, "/api/files/archive/download", map[string]any{
		"paths": []string{f.theirs.storage.Name + "://" + f.theirs.node.Path}, "mode": "file",
	})
	assert.GreaterOrEqual(t, code, 400, "another tenant's file must not mint")
	assert.Less(t, code, 500)
}

func TestDownloadLink_StorageLeftTheTenant(t *testing.T) {
	f := newXTFixture(t, false)
	code, info := f.do(t, http.MethodPost, "/api/files/archive/download", map[string]any{
		"paths": []string{f.mine.storage.Name + "://" + f.mine.node.Path}, "mode": "file",
	})
	require.Equal(t, http.StatusOK, code, "%v", info)
	require.NoError(t, f.store.UnlinkProviderStorage(context.Background(), f.mine.provider, f.mine.storage.ID))
	resp, body := redeem(t, f.srv.URL, linkURL(t, info), "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "the storage is no longer this tenant's: %s", body)
}

// doReqHost is doReq with the Host header set — how a multi-tenant install
// tells its tenants apart.
func doReqHost(t *testing.T, client *http.Client, method, url, host string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, url, strings.NewReader(string(b)))
	require.NoError(t, err)
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

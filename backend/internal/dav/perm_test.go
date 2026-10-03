package dav

// Per-user permissions (internal/perm) over WebDAV: access.webdav at every
// request's authentication, and each method on the action it performs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const (
	permUser = "perm@test.local"
	permPass = "PermPass!1"
)

// permHarness is a storage holding report.txt and a plain user denied ps.
func permHarness(t *testing.T, ps ...perm.Perm) (*harness, string) {
	t.Helper()
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	root := cfg["path"]
	require.NoError(t, os.WriteFile(filepath.Join(root, "report.txt"), []byte("q3 numbers"), 0o644))

	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)
	u, err := ha.store.GetUserByEmail(context.Background(), permUser)
	require.NoError(t, err)
	m := map[string]string{}
	for _, p := range ps {
		m[string(p)] = model.PermDeny
	}
	require.NoError(t, ha.store.SetUserPermissionOverrides(context.Background(), u.ID, m, nil))
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
	return ha, root
}

func TestPerm_AccessWebDAVGatesEveryRequest(t *testing.T) {
	ha, _ := permHarness(t, perm.AccessWebDAV)
	resp := ha.req(t, "PROPFIND", "/dav/depo/", permUser, permPass, "", map[string]string{"Depth": "1"})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "an account without access.webdav was let in")
}

func TestPerm_WebDAVMethods(t *testing.T) {
	cases := []struct {
		denied        perm.Perm
		method, path  string
		body          string
		hdr           map[string]string
		stillThere    bool   // report.txt must survive
		unchanged     bool   // report.txt keeps its bytes
		neighbour     string // "METHOD path" that must still succeed
		neighbourBody string
		neighbourHdr  map[string]string
	}{
		{denied: perm.FilesDownload, method: http.MethodGet, path: "/dav/depo/report.txt", neighbour: "PROPFIND /dav/depo/report.txt"},
		{denied: perm.FilesDelete, method: http.MethodDelete, path: "/dav/depo/report.txt", stillThere: true, neighbour: "MKCOL /dav/depo/newdir"},
		{denied: perm.FilesCreate, method: http.MethodPut, path: "/dav/depo/new.txt", body: "x", neighbour: "PUT /dav/depo/report.txt", neighbourBody: "revised"},
		{denied: perm.FilesModify, method: http.MethodPut, path: "/dav/depo/report.txt", body: "tampered", unchanged: true, neighbour: "PUT /dav/depo/fresh.txt", neighbourBody: "new"},
		{denied: perm.FilesRename, method: "MOVE", path: "/dav/depo/report.txt", hdr: map[string]string{"Destination": "/dav/depo/moved.txt"}, stillThere: true, neighbour: "DELETE /dav/depo/report.txt"},
		{denied: perm.FilesMove, method: "MOVE", path: "/dav/depo/report.txt", hdr: map[string]string{"Destination": "/dav/depo/sub/report.txt"}, stillThere: true, neighbour: "MOVE /dav/depo/report.txt", neighbourHdr: map[string]string{"Destination": "/dav/depo/renamed.txt"}},
		{denied: perm.FilesCreate, method: "MKCOL", path: "/dav/depo/nope", neighbour: "GET /dav/depo/report.txt"},
	}
	for _, c := range cases {
		t.Run(string(c.denied)+"/"+c.method, func(t *testing.T) {
			ha, root := permHarness(t, c.denied)
			resp := ha.req(t, c.method, c.path, permUser, permPass, c.body, c.hdr)
			body := bodyString(t, resp)
			require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s %s without %s: %s", c.method, c.path, c.denied, body)
			assert.Contains(t, body, string(c.denied), "the refusal names the permission")

			if c.stillThere || c.unchanged {
				got, err := os.ReadFile(filepath.Join(root, "report.txt"))
				require.NoError(t, err, "report.txt is gone")
				if c.unchanged {
					assert.Equal(t, "q3 numbers", string(got))
				}
			}

			method, path := splitNeighbour(c.neighbour)
			hdr := map[string]string{}
			for k, v := range c.neighbourHdr {
				hdr[k] = v
			}
			if method == "PROPFIND" {
				hdr["Depth"] = "0"
			}
			n := ha.req(t, method, path, permUser, permPass, c.neighbourBody, hdr)
			assert.Less(t, n.StatusCode, 300, "denying %s also stopped %s: %s", c.denied, c.neighbour, bodyString(t, n))
		})
	}
}

func splitNeighbour(s string) (string, string) {
	for i := range s {
		if s[i] == ' ' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// Who may encrypt (internal/e2epolicy) over WebDAV. With the policy off, a
// request that would CREATE an encrypted folder's key file or a `.fxe` — a
// PUT, the LOCK that makes an empty file, a COPY onto a new name — is refused
// with a 403 by the pre-gate (a refused OpenFile would reach the client as a
// 404), and nothing lands. Rewriting a key file that is there — a password
// change — and an ordinary file still go through.
func TestPerm_WebDAVEncryptionPolicy(t *testing.T) {
	ha, root := permHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Kasa"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Kasa", ".filex-e2e.json"), []byte(`{"v":2}`), 0o644))
	require.NoError(t, ha.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))

	for _, c := range []struct {
		what, method, path, body, landed string
		hdr                              map[string]string
	}{
		{what: "PUT of a key file", method: http.MethodPut, path: "/dav/depo/.filex-e2e.json", body: "x", landed: ".filex-e2e.json"},
		{what: "PUT of a .fxe", method: http.MethodPut, path: "/dav/depo/yeni.fxe", body: "x", landed: "yeni.fxe"},
		// A real lock request: let through, x/net/webdav would create the
		// empty file this row looks for.
		{what: "LOCK that creates a .fxe", method: "LOCK", path: "/dav/depo/kilit.fxe", body: lockInfo, landed: "kilit.fxe", hdr: lockHeaders},
		{what: "COPY onto a new .fxe", method: "COPY", path: "/dav/depo/report.txt", body: "x", landed: "kopya.fxe",
			hdr: map[string]string{"Destination": "/dav/depo/kopya.fxe"}},
	} {
		resp := ha.req(t, c.method, c.path, permUser, permPass, c.body, c.hdr)
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s: %s", c.what, body)
		assert.Contains(t, body, "encrypted", "%s: the refusal says why", c.what)
		assert.NoFileExists(t, filepath.Join(root, c.landed), "%s: it landed", c.what)
	}

	resp := ha.req(t, http.MethodPut, "/dav/depo/Kasa/.filex-e2e.json", permUser, permPass, `{"v":2,"rewritten":true}`, nil)
	require.Less(t, resp.StatusCode, 300, "rewriting a key file that is there: %s", bodyString(t, resp))
	got, err := os.ReadFile(filepath.Join(root, "Kasa", ".filex-e2e.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"v":2,"rewritten":true}`, string(got))
	resp = ha.req(t, http.MethodPut, "/dav/depo/notlar.txt", permUser, permPass, "plain", nil)
	assert.Less(t, resp.StatusCode, 300, "an ordinary file: %s", bodyString(t, resp))
}

// A folder named like an encryption is not the file (the Task 6 review's
// probe). MKCOL makes one freely — a folder is exempt — and a COPY of an
// ordinary file onto it then looked like an overwrite: x/net/webdav moves the
// folder to the trash and creates the file in its place, which is creating
// the key file or the `.fxe`. A PUT onto it is the same create (an object
// store keeps the file beside the folder). With the policy off both are the
// rule's 403 — on this local storage the driver would refuse the PUT too, but
// later and in its own words — and no file is there afterwards.
func TestPerm_WebDAVAFolderWithTheNameIsNotTheFile(t *testing.T) {
	ha, root := permHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Acik"), 0o755))
	require.NoError(t, ha.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))

	for _, name := range []string{".filex-e2e.json", "x.fxe"} {
		target := "/dav/depo/Acik/" + name
		resp := ha.req(t, "MKCOL", target, permUser, permPass, "", nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "a folder named %s is exempt: %s", name, bodyString(t, resp))

		for _, c := range []struct {
			what, method, path string
			hdr                map[string]string
		}{
			{what: "PUT onto the folder " + name, method: http.MethodPut, path: target},
			{what: "COPY onto the folder " + name, method: "COPY", path: "/dav/depo/report.txt", hdr: map[string]string{"Destination": target}},
		} {
			resp := ha.req(t, c.method, c.path, permUser, permPass, "x", c.hdr)
			body := bodyString(t, resp)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s: %s", c.what, body)
			assert.Contains(t, body, "encrypted", "%s: the refusal is the rule's", c.what)
			if fi, err := os.Stat(filepath.Join(root, "Acik", name)); err == nil {
				assert.True(t, fi.IsDir(), "%s: a file named %s landed", c.what, name)
			}
		}
	}
}

// logged captures what slog writes for the rest of the test — from any
// goroutine, since the handler logs on the server's. SetDefault also points
// the log package at the new handler, and putting the old logger back does not
// undo that: the package's writer and flags are restored by hand.
func logged(t *testing.T) func() string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	prev, out, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(lockedWriter{mu: &mu, w: &buf}, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(out)
		log.SetFlags(flags)
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// A rule that cannot be decided is the server's failure, not a refusal. With
// the policy unreadable (the store fails), a PUT of a `.fxe` answers 500 — not
// the 403 a client reads as "not allowed", and an operator as a policy doing
// its job — nothing is written, and the log says what failed. An ordinary
// name never asks, so it still goes through.
func TestPerm_WebDAVUndecidedEncryptionIsAServerFailure(t *testing.T) {
	logs := logged(t)
	ha := newHarnessStore(t, func(s db.Store) db.Store {
		return dbtest.SettingFails(s, model.SettingE2EPolicy, errors.New("database is locked"))
	})
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)

	resp := ha.req(t, http.MethodPut, "/dav/depo/yeni.fxe", permUser, permPass, "x", nil)
	body := bodyString(t, resp)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, body)
	assert.Contains(t, body, "could not check the encryption policy")
	assert.NoFileExists(t, filepath.Join(cfg["path"], "yeni.fxe"))
	assert.Contains(t, logs(), "e2e policy: could not decide a create")
	assert.Contains(t, logs(), "database is locked")

	resp = ha.req(t, http.MethodPut, "/dav/depo/notlar.txt", permUser, permPass, "plain", nil)
	assert.Less(t, resp.StatusCode, 300, "an ordinary file: %s", bodyString(t, resp))
}

// A handler built without the router's rule still asks one: NewHandler builds
// it from its store. Nil used to mean "not wired, allow", so losing the one
// line in routes.go that hands the rule over switched it off for WebDAV under
// every policy, and nothing said so.
func TestPerm_WebDAVAsksTheRuleWhenNobodyWiredIt(t *testing.T) {
	ha := newHarnessWith(t, func(s db.Store) db.Store { return s }, func(c *Config) { c.E2EPolicy = nil })
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)
	require.NoError(t, ha.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))

	resp := ha.req(t, http.MethodPut, "/dav/depo/yeni.fxe", permUser, permPass, "x", nil)
	body := bodyString(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, body)
	assert.Contains(t, body, "encrypted")
	assert.NoFileExists(t, filepath.Join(cfg["path"], "yeni.fxe"))
}

// The rule a handler builds for itself when nobody wired one records an
// approval it spends as the router's does: one e2e_request.use row, naming
// the approval and the folder it opened — here one folder inside the folder
// approved. No door spends an approval without that row.
func TestPerm_WebDAVAnApprovalSpentByAnUnwiredRuleIsAudited(t *testing.T) {
	ha := newHarnessWith(t, func(s db.Store) db.Store { return s }, func(c *Config) { c.E2EPolicy = nil })
	ctx := context.Background()
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	require.NoError(t, os.MkdirAll(filepath.Join(cfg["path"], "Acik", "Alt"), 0o755))
	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)
	u, err := ha.store.GetUserByEmail(ctx, permUser)
	require.NoError(t, err)
	require.NoError(t, ha.store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	r := dbtest.ApproveE2E(t, ha.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)

	resp := ha.req(t, http.MethodPut, "/dav/depo/Acik/Alt/.filex-e2e.json", permUser, permPass, `{"v":2}`, nil)
	require.Less(t, resp.StatusCode, 300, "the approved key file: %s", bodyString(t, resp))
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, ha.store, r.ID), "the approval was not spent")

	rows, _, err := ha.store.ListAuditFiltered(ctx, nil, e2epolicy.AuditActionRequestUse, nil, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the spent approval has no audit row")
	assert.Equal(t, strconv.FormatInt(r.ID, 10), rows[0].Entry.TargetID)
	require.NotNil(t, rows[0].Entry.UserID)
	assert.Equal(t, u.ID, *rows[0].Entry.UserID, "the person who spent it")
	assert.Equal(t, "depo://Acik", rows[0].Entry.Metadata["target_name"], "the approval")
	assert.Equal(t, "depo://Acik/Alt", rows[0].Entry.Metadata["encrypted"], "the folder it opened")
}

// lockInfo is a real exclusive write-lock request, and lockHeaders what a
// client sends with it.
const lockInfo = `<?xml version="1.0" encoding="utf-8"?>
<D:lockinfo xmlns:D="DAV:">
  <D:lockscope><D:exclusive/></D:lockscope>
  <D:locktype><D:write/></D:locktype>
  <D:owner>dav-test</D:owner>
</D:lockinfo>`

var lockHeaders = map[string]string{"Timeout": "Second-60", "Depth": "0"}

// Who may encrypt lets through what it should, and spends an approval once.
// Under the default policy (permitted) a plain member creates a `.fxe`: an
// upgrade changes nobody's access. Under the approval policy one approval
// for a folder lets exactly one key file be made there: the request turns
// used, the next key file — one folder down, which the same approval would
// have covered — is refused, and rewriting the key file that is there is not
// asked at all (were it asked, nothing is left to spend). A LOCK that creates
// a `.fxe` spends its approval once: the PUT after it, with the lock's token,
// is an overwrite.
func TestPerm_WebDAVEncryptionPolicyLetsThePermittedThrough(t *testing.T) {
	ha, root := permHarness(t)
	ctx := context.Background()
	u, err := ha.store.GetUserByEmail(ctx, permUser)
	require.NoError(t, err)
	st, err := ha.store.GetStorageByName(ctx, "depo")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Acik", "Alt"), 0o755))

	resp := ha.req(t, http.MethodPut, "/dav/depo/Acik/izinli.fxe", permUser, permPass, "x", nil)
	require.Less(t, resp.StatusCode, 300, "a .fxe under the default policy: %s", bodyString(t, resp))
	assert.FileExists(t, filepath.Join(root, "Acik", "izinli.fxe"))

	require.NoError(t, ha.store.UpsertSetting(ctx, model.SettingE2EPolicy, model.E2EPolicyApproval))
	r := dbtest.ApproveE2E(t, ha.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)
	resp = ha.req(t, http.MethodPut, "/dav/depo/Acik/.filex-e2e.json", permUser, permPass, `{"v":2}`, nil)
	require.Less(t, resp.StatusCode, 300, "the approved key file: %s", bodyString(t, resp))
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, ha.store, r.ID), "the approval was not spent")
	resp = ha.req(t, http.MethodPut, "/dav/depo/Acik/Alt/.filex-e2e.json", permUser, permPass, `{"v":2}`, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "a second key file on one approval: %s", bodyString(t, resp))
	assert.NoFileExists(t, filepath.Join(root, "Acik", "Alt", ".filex-e2e.json"))
	resp = ha.req(t, http.MethodPut, "/dav/depo/Acik/.filex-e2e.json", permUser, permPass, `{"v":2,"rewritten":true}`, nil)
	require.Less(t, resp.StatusCode, 300, "rewriting the key file that is there: %s", bodyString(t, resp))
	got, err := os.ReadFile(filepath.Join(root, "Acik", ".filex-e2e.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"v":2,"rewritten":true}`, string(got))

	r = dbtest.ApproveE2E(t, ha.store, u.ID, st.ID, "Acik", model.E2ERequestFile)
	resp = ha.req(t, "LOCK", "/dav/depo/Acik/kilitli.fxe", permUser, permPass, lockInfo, lockHeaders)
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, resp.StatusCode, "a LOCK that creates a .fxe: %s", bodyString(t, resp))
	token := resp.Header.Get("Lock-Token")
	require.NotEmpty(t, token)
	assert.Equal(t, model.E2ERequestUsed, dbtest.E2EStatus(t, ha.store, r.ID), "the LOCK did not spend the approval")
	resp = ha.req(t, http.MethodPut, "/dav/depo/Acik/kilitli.fxe", permUser, permPass, "cipher", map[string]string{"If": "(" + token + ")"})
	require.Less(t, resp.StatusCode, 300, "the PUT after the LOCK was asked again: %s", bodyString(t, resp))
	got, err = os.ReadFile(filepath.Join(root, "Acik", "kilitli.fxe"))
	require.NoError(t, err)
	assert.Equal(t, "cipher", string(got))
}

// layRenames writes into root what the MOVE tests need: plain files, a
// `.fxe` and an encrypted folder made before the policy was switched off, a
// folder to give a key file's name, and an empty folder for a key file to be
// moved into.
func layRenames(t *testing.T, root string) {
	t.Helper()
	for rel, body := range map[string]string{
		"Acik/izinli.bin": "plain", "Acik/notlar.txt": "plain", "Acik/rapor.bin": "plain", "Acik/m.json": "{}",
		"Acik/a.fxe": "cipher", "Acik/c.fxe": "cipher", "Acik/Dosyalar/not.txt": "plain", "Acik/ek.bin": "plain",
		"Acik/yedek.bin": "plain", "Acik/eski.fxe": "cipher", "Kasa/.filex-e2e.json": `{"v":2}`,
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Klasör"), 0o755))
}

// move asks for a MOVE of from to to on the storage depo, as permUser;
// overwrite sends Overwrite: T, without which x/net/webdav refuses a
// destination that is there (412).
func (ha *harness) move(t *testing.T, from, to string, overwrite bool) *http.Response {
	t.Helper()
	hdr := map[string]string{"Destination": (&url.URL{Path: "/dav/depo/" + to}).EscapedPath()}
	if overwrite {
		hdr["Overwrite"] = "T"
	}
	return ha.req(t, "MOVE", "/dav/depo/"+from, permUser, permPass, "", hdr)
}

// Who may encrypt at a MOVE (operator decision 2026-09-30). Giving a plain
// file a key file's or a `.fxe`'s name encrypts as surely as creating one, so
// a MOVE that does it is asked as a PUT of the file there would be. Under the
// default policy a plain member's goes through; with the policy off the
// pre-gate refuses it with a 403, and nothing moves. So does a MOVE of a
// `.fxe` onto a key file's name, and of a key file into another folder
// (operator decision 2026-10-03): each encrypts a folder nobody was asked
// about. A `.fxe` that stays a `.fxe` is free, a folder with any name is not a
// key file, a file moved into an encrypted folder keeps its plain name, and a
// MOVE onto a `.fxe` that is there (Overwrite: T) replaces it unasked, as any
// overwrite is.
func TestPerm_WebDAVMoveOntoAnEncryptionName(t *testing.T) {
	ha, root := permHarness(t)
	layRenames(t, root)
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	resp := ha.move(t, "Acik/izinli.bin", "Acik/izinli.fxe", false)
	require.Less(t, resp.StatusCode, 300, "a .fxe by MOVE under the default policy: %s", bodyString(t, resp))
	require.NoError(t, ha.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))

	for _, c := range []struct{ from, to string }{
		{"Acik/rapor.bin", "Acik/rapor.bin.fxe"}, {"Acik/m.json", "Klasör/.filex-e2e.json"},
		{"Acik/c.fxe", "Acik/.filex-e2e.json"}, {"Kasa/.filex-e2e.json", "Klasör/.filex-e2e.json"},
	} {
		resp := ha.move(t, c.from, c.to, false)
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s → %s: %s", c.from, c.to, body)
		assert.Contains(t, body, "encrypted", "%s → %s: the refusal says why", c.from, c.to)
		assert.FileExists(t, at(c.from), "%s moved", c.from)
		assert.NoFileExists(t, at(c.to), "%s landed", c.to)
	}
	for _, c := range []struct {
		from, to  string
		overwrite bool
	}{
		{"Acik/notlar.txt", "Acik/notlar-2.txt", false}, {"Acik/a.fxe", "Acik/b.fxe", false},
		{"Acik/ek.bin", "Kasa/ek.bin", false}, {"Acik/Dosyalar", "Acik/.filex-e2e.json", false},
		{"Acik/yedek.bin", "Acik/eski.fxe", true},
	} {
		resp := ha.move(t, c.from, c.to, c.overwrite)
		assert.Less(t, resp.StatusCode, 300, "%s → %s: %s", c.from, c.to, bodyString(t, resp))
		_, err := os.Stat(at(c.to))
		assert.NoError(t, err, "%s did not arrive", c.to)
	}
	assert.DirExists(t, at("Acik/.filex-e2e.json"))
	got, err := os.ReadFile(at("Acik/eski.fxe"))
	require.NoError(t, err)
	assert.Equal(t, "plain", string(got), "eski.fxe was not replaced")
}

// A rule that cannot be decided is the server's failure at a MOVE too: 500,
// not the 403 of a refusal, nothing moves, and the log says what failed.
func TestPerm_WebDAVUndecidedMoveIsAServerFailure(t *testing.T) {
	logs := logged(t)
	ha := newHarnessStore(t, func(s db.Store) db.Store {
		return dbtest.SettingFails(s, model.SettingE2EPolicy, errors.New("database is locked"))
	})
	st := ha.addStorage(t, "depo", false, false)
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	require.NoError(t, os.WriteFile(filepath.Join(cfg["path"], "rapor.bin"), []byte("plain"), 0o644))
	dbtest.SeedRegularUser(t, ha.store, permUser, permPass)

	resp := ha.move(t, "rapor.bin", "rapor.bin.fxe", false)
	body := bodyString(t, resp)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, body)
	assert.Contains(t, body, "could not check the encryption policy")
	assert.FileExists(t, filepath.Join(cfg["path"], "rapor.bin"))
	assert.NoFileExists(t, filepath.Join(cfg["path"], "rapor.bin.fxe"))
	assert.Contains(t, logs(), "e2e policy: could not decide a create")
}

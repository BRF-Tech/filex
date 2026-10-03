package handlers_test

// The ops queue inside ONE tenant (or a single-tenant install): whose rows a
// person is shown.
//
// A queue row carries its sources and its destination — file and folder paths.
// The listing was scoped to the storages the caller's tenant reaches and to
// nothing finer, so every member of a tenant read every other member's rows,
// whatever folders their grants cover: on a production install (2026-09-26)
// the queue held a colleague's move into a folder of client records, and the
// listing handed it to every member of the tenant. `GET /ops/{id}` answered the
// full row for any id in the tenant, and the ids are sequential.

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queuedOp reads the id of the op a submit answered.
func queuedOp(t *testing.T, body map[string]any) string {
	t.Helper()
	op, ok := body["op"].(map[string]any)
	require.True(t, ok, "no op in %v", body)
	return strconv.FormatInt(int64(op["id"].(float64)), 10)
}

// secondMember is another plain (role=user) member of alpha — or, with the
// mode off, of the one provider everybody lives in.
func secondMember(t *testing.T, f *mtFix) *http.Client {
	t.Helper()
	provider := f.ProvA
	if !f.MultiTenant {
		super, err := f.Store.GetSupertenant(context.Background())
		require.NoError(t, err)
		provider = super.ID
	}
	seedUserIn(t, f.Store, provider, "second@alpha.test")
	return mtLogin(t, f.Srv, "alpha", "second@alpha.test", mtUserPass)
}

func TestOpsList_AMemberSeesOnlyTheOpsTheyQueued(t *testing.T) {
	for _, multi := range []bool{true, false} {
		name := "single-tenant"
		if multi {
			name = "multi-tenant"
		}
		t.Run(name, func(t *testing.T) {
			f := newMTFix(t, multi)
			second := secondMember(t, f)
			f.seedFile(t, f.StA, f.RootA, "danisan-kayitlari.txt", "the first member's file")
			f.seedFile(t, f.StA, f.RootA, "ortak.txt", "the second member's file")

			status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
				"source": []string{"alpha://danisan-kayitlari.txt"},
			})
			require.Equal(t, http.StatusAccepted, status, "%v", body)
			firstOp := queuedOp(t, body)

			status, body = doJSON(t, second, http.MethodPost, f.URL+"/api/files/copy", map[string]any{
				"source": []string{"alpha://ortak.txt"}, "target": "alpha://",
			})
			require.Equal(t, http.StatusAccepted, status, "%v", body)
			secondOp := queuedOp(t, body)

			// The second member's tray: their own row, nobody else's.
			status, raw := mtGet(t, second, f.URL+"/api/files/ops")
			require.Equal(t, http.StatusOK, status)
			require.NotContains(t, raw, "danisan-kayitlari.txt", "another member's row must not be in the listing")
			require.Contains(t, raw, "ortak.txt", "a member keeps their own row")

			// Nor by id, nor by cancelling it — and in the words a missing id
			// gets, so walking the sequential ids counts nothing.
			status, raw = mtGet(t, second, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusNotFound, status, raw)
			status, body = doJSON(t, second, http.MethodPost, f.URL+"/api/files/ops/"+firstOp+"/cancel", nil)
			require.Equal(t, http.StatusNotFound, status, "%v", body)

			status, raw = mtGet(t, second, f.URL+"/api/files/ops/"+secondOp)
			require.Equal(t, http.StatusOK, status, raw)
			status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusOK, status, "the person who queued it still follows it: %s", raw)

			// An administrator keeps the whole view of what they administer.
			status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops")
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, raw, "danisan-kayitlari.txt")
			require.Contains(t, raw, "ortak.txt")
			status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops/"+firstOp)
			require.Equal(t, http.StatusOK, status, raw)
		})
	}
}

// A row that names nobody — queued before actor_id existed, or by something
// that is not a person (a scheduled app job) — is nobody's to follow but an
// administrator's. It used to be everybody's, cancellable by anyone who saw it.
func TestOpsList_ARowNobodyQueuedIsAnAdministratorsOnly(t *testing.T) {
	f := newMTFix(t, true)
	res, err := f.SQL.ExecContext(context.Background(),
		`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
		 VALUES ('delete', ?, ?, '["sistem-isi.txt"]', '', 1, 'pending')`, f.StA.ID, f.StA.ID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	row := strconv.FormatInt(id, 10)

	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, raw, "sistem-isi.txt")
	status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+row)
	require.Equal(t, http.StatusNotFound, status, raw)
	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/ops/"+row+"/cancel", nil)
	require.Equal(t, http.StatusNotFound, status, "%v", body)

	status, raw = mtGet(t, f.AdminA, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, raw, "sistem-isi.txt")
	status, body = doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/ops/"+row+"/cancel", nil)
	require.Equal(t, http.StatusOK, status, "an administrator can still stop it: %v", body)
}

// "An administrator" is the credential, not the account behind it: a token
// minted on an administrator's account that is read-scoped or confined to a
// folder, or the administrator's own session narrowed by X-Filex-Root, is
// shown the operations it queued — nobody else's, by list or by id.
//
// RED PROOF (before, 2026-09-26): opsViewer asked `u.IsAdmin()` — the account
// — so each of the three listed the member's queued delete, with its path,
// and read it by id.
func TestOpsList_ANarrowCredentialOfAnAdministratorSeesOnlyItsOwn(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	f.seedFile(t, f.StA, f.RootA, "danisan-kayitlari.txt", "the member's file")
	status, body := doJSON(t, f.A, http.MethodPost, f.URL+"/api/files/delete", map[string]any{
		"source": []string{"alpha://danisan-kayitlari.txt"},
	})
	require.Equal(t, http.StatusAccepted, status, "%v", body)
	memberOp := queuedOp(t, body)

	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	get := func(t *testing.T, client *http.Client, url, token, root string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("X-Filex-Token", token)
		}
		if root != "" {
			req.Header.Set("X-Filex-Root", root)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	narrow := map[string]struct {
		client *http.Client
		token  string
		root   string
	}{
		"a read-scoped token":          {http.DefaultClient, issueToken(t, f.Store, admin.ID, "read", nil), ""},
		"a root-confined admin token":  {http.DefaultClient, issueToken(t, f.Store, admin.ID, "read,write,delete,admin,root:alpha://", nil), ""},
		"a session narrowed by header": {f.AdminA, "", "alpha://"},
	}
	for what, c := range narrow {
		status, raw := get(t, c.client, f.URL+"/api/files/ops", c.token, c.root)
		require.Equal(t, http.StatusOK, status, "%s: %s", what, raw)
		assert.NotContains(t, raw, "danisan-kayitlari.txt", "%s was shown another person's operation", what)
		status, raw = get(t, c.client, f.URL+"/api/files/ops/"+memberOp, c.token, c.root)
		assert.Equal(t, http.StatusNotFound, status, "%s read another person's operation by id: %s", what, raw)
	}

	// The administrator's full credential keeps the whole view.
	status, raw := get(t, http.DefaultClient, f.URL+"/api/files/ops", issueToken(t, f.Store, admin.ID, fullScopes, nil), "")
	require.Equal(t, http.StatusOK, status, raw)
	assert.Contains(t, raw, "danisan-kayitlari.txt")
	status, raw = get(t, f.AdminA, f.URL+"/api/files/ops", "", "")
	require.Equal(t, http.StatusOK, status, raw)
	assert.Contains(t, raw, "danisan-kayitlari.txt")
}

// Cancelling a job that cannot be stopped once it runs says so. It is not
// finished — the result is still on its way.
//
// RED PROOF (PR #61, 2026-09-26): a running rename answered 409 "already
// finished".
func TestOpsCancel_ARunningRenameSaysItCannotBeStopped(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	insert := func(status string) string {
		res, err := f.SQL.ExecContext(ctx,
			`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
			 VALUES ('rename', ?, ?, '["Leon"]', 'Leo', 1, ?)`, f.StA.ID, f.StA.ID, status)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return strconv.FormatInt(id, 10)
	}

	status, body := doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/ops/"+insert("running")+"/cancel", nil)
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "NOT_CANCELLABLE", body["code"], "%v", body)
	assert.NotContains(t, body["error"], "finished", "a running job was said to be finished")

	status, body = doJSON(t, f.AdminA, http.MethodPost, f.URL+"/api/files/ops/"+insert("ok")+"/cancel", nil)
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "FINISHED", body["code"], "%v", body)
}
